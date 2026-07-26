//go:build ignore

package main

import (
	"log"

	"campusassistant-api/internal/config"
	"campusassistant-api/internal/domain"
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

	// Auto-migrate to add price column if it doesn't exist
	if err := db.AutoMigrate(&domain.UserSubscription{}); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	log.Println("Migration complete (added price column if missing)")

	// Backfill price and plan title for existing subscriptions
	result := db.Model(&domain.UserSubscription{}).
		Where("price = 0 OR price IS NULL OR plan = ''").
		Update("price", db.Raw("COALESCE((SELECT price FROM subscription_plans WHERE subscription_plans.id = user_subscriptions.plan_id), 0)"))
	if result.Error != nil {
		log.Fatalf("Backfill price failed: %v", result.Error)
	}
	log.Printf("Backfilled price for %d subscriptions", result.RowsAffected)

	result2 := db.Model(&domain.UserSubscription{}).
		Where("plan = '' OR plan IS NULL").
		Update("plan", db.Raw("COALESCE((SELECT title FROM subscription_plans WHERE subscription_plans.id = user_subscriptions.plan_id), 'Unknown')"))
	if result2.Error != nil {
		log.Fatalf("Backfill plan title failed: %v", result2.Error)
	}
	log.Printf("Backfilled plan title for %d subscriptions", result2.RowsAffected)

	log.Println("Backfill complete!")
}
