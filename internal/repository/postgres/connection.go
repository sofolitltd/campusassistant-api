package postgres

import (
	"fmt"
	"log"
	"time"

	"campusassistant-api/internal/config"
	"campusassistant-api/internal/domain"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func NewConnection(cfg *config.Config) (*gorm.DB, error) {
	dsn := cfg.DatabaseURL
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}

	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	}

	if cfg.Environment == "production" {
		gormConfig.Logger = logger.Default.LogMode(logger.Error)
	}

	db, err := gorm.Open(postgres.Open(dsn), gormConfig)
	if err != nil {
		return nil, err
	}

	// AutoMigrate all models
	err = db.AutoMigrate(
		&domain.University{},
		&domain.Department{},
		&domain.Session{},
		&domain.Batch{},
		&domain.User{},
		&domain.Student{},
		&domain.Teacher{},
		&domain.Staff{},
		&domain.Verification{},
		&domain.Book{},
		&domain.Question{},
		&domain.Note{},
		&domain.Syllabus{},
		&domain.Transport{},
	)
	if err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Connection Pool settings
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	log.Println("Connected to PostgreSQL successfully")
	return db, nil
}
