package main

import (
	"fmt"
	"log"

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

	// 4. Setup Router
	r := httpDelivery.NewRouter(cfg, db)

	// 5. Start Server
	serverAddr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Server listening on %s", serverAddr)
	if err := r.Run(serverAddr); err != nil {
		logger.Fatalf("Server failed to start: %v", err)
	}
}
