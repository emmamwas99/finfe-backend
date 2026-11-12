package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"finfe/internal/config"
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
		log.Error("mongodb", "error", err)
		os.Exit(1)
	}
	defer db.Close(context.Background())
	if err := db.EnsureIndexes(ctx); err != nil {
		log.Error("mongodb indexes", "error", err)
		os.Exit(1)
	}
	log.Info("MongoDB indexes ready", "database", cfg.MongoDBDatabase)
}
