package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"finfe/internal/application/recurring"
	"finfe/internal/config"
	"finfe/internal/delivery/httpapi"
	"finfe/internal/infrastructure/email"
	"finfe/internal/infrastructure/mongodb"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := mongodb.Open(ctx, cfg.MongoDBURI, cfg.MongoDBDatabase, cfg.MongoDBMaxPool)
	if err != nil {
		log.Error("database", "error", err)
		os.Exit(1)
	}
	defer db.Close(context.Background())
	if err := db.EnsureIndexes(ctx); err != nil {
		log.Error("mongodb indexes", "error", err)
		os.Exit(1)
	}
	var mail email.Mailer = &email.Log{Logger: log}
	if cfg.MailDriver == "smtp" {
		mail = email.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.MailFrom}
	}
	api := httpapi.NewMongo(cfg, db, mail, log)
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if err := recurring.RunMongo(ctx, db, time.Now()); err != nil {
				log.Error("recurring job", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Router(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		log.Info("api listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		log.Error("shutdown", "error", err)
	}
}
