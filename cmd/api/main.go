package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"campusassistant-api/internal/config"
	httpDelivery "campusassistant-api/internal/delivery/http"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/pkg/logger"
)

func main() {
	// 1. Load Configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 2. Initialize Logger
	logger.InitLogger(cfg.Environment)
	logger.Infof("Starting Campus Assistant API in %s mode", cfg.Environment)

	// 3. Database Connection
	db, err := postgres.NewConnection(cfg)
	if err != nil {
		logger.Fatalf("Database connection failed: %v", err)
	}

	// 4. Run Migrations (Conditional)
	// Only run migrations in development or if explicitly enabled
	if cfg.Environment == "development" || cfg.DBAutoMigrate {
		logger.Infof("Migrations enabled (Mode: %s, AutoMigrate: %v). Running...", cfg.Environment, cfg.DBAutoMigrate)
		if err := postgres.RunMigrations(db); err != nil {
			logger.Fatalf("Migration failed: %v", err)
		}
	} else {
		logger.Infof("Migrations skipped for production (Set DB_AUTO_MIGRATE=true to enable)")
	}

	// 5. Setup Router
	r := httpDelivery.NewRouter(cfg, db)

	// 5. Background Workers
	subRepo := postgres.NewSubscriptionRepository(db)
	chatRepo := postgres.NewChatRepository(db)
	go func() {
		subTicker := time.NewTicker(1 * time.Hour)
		cleanupTicker := time.NewTicker(24 * time.Hour)
		defer subTicker.Stop()
		defer cleanupTicker.Stop()
		for {
			select {
			case <-subTicker.C:
				count, err := subRepo.ExpireSubscriptions(context.Background())
				if err != nil {
					log.Printf("Background Worker Error: Failed to expire subscriptions: %v", err)
				} else if count > 0 {
					log.Printf("Background Worker: Expired %d user subscriptions automatically.", count)
				}
			case <-cleanupTicker.C:
				cutoff := time.Now().Add(-90 * 24 * time.Hour) // 90 days
				count, err := chatRepo.CleanupDeletedMessages(context.Background(), cutoff)
				if err != nil {
					log.Printf("Background Worker Error: Failed to cleanup deleted messages: %v", err)
				} else if count > 0 {
					log.Printf("Background Worker: Cleaned up %d deleted message records.", count)
				}
			}
		}
	}()

	// 6. Start Server
	serverAddr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Server listening on %s", serverAddr)
	if err := r.Run(serverAddr); err != nil {
		logger.Fatalf("Server failed to start: %v", err)
	}
}
