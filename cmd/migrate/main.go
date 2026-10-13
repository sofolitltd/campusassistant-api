// Command migrate runs database migrations on demand. The API server
// (cmd/api) no longer migrates on every boot — run this explicitly after
// pulling changes that add/modify a domain model, then start the API
// normally. See docs/MIGRATIONS.md for the full workflow.
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

	if err := postgres.RunMigrations(db); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	log.Println("Migrations completed successfully.")
}
