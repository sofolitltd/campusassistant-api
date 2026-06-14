//go:build ignore

package main

import (
	"log"

	"campusassistant-api/internal/config"
	"campusassistant-api/internal/repository/postgres"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	db, err := postgres.NewConnection(cfg)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}

	log.Println("Running database migrations...")
	if err := postgres.RunMigrations(db); err != nil {
		log.Fatalf("Migrations failed: %v", err)
	}

	log.Println("Database migrations completed successfully!")
}
