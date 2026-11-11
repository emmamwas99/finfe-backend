package main

import (
	"context"
	"log"
	"os"

	"finfe/internal/config"
	"finfe/internal/infrastructure/database"
)

func main() {
	direction := "up"
	if len(os.Args) > 1 {
		direction = os.Args[1]
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := database.Open(context.Background(), cfg.DatabaseURL, cfg.DatabaseMaxConns)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(pool, direction); err != nil {
		log.Fatal(err)
	}
	log.Printf("migrations %s complete", direction)
}
