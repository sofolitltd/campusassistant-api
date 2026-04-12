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
		Logger:                 logger.Default.LogMode(logger.Info),
		PrepareStmt:            true,
		SkipDefaultTransaction: true,
	}

	if cfg.Environment == "production" {
		gormConfig.Logger = logger.Default.LogMode(logger.Error)
	}

	db, err := gorm.Open(postgres.Open(dsn), gormConfig)
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Lower connection pool for serverless environments (Vercel/Neon)
	if cfg.Environment == "production" {
		sqlDB.SetMaxIdleConns(2)
		sqlDB.SetMaxOpenConns(10)
	} else {
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetMaxOpenConns(100)
	}
	sqlDB.SetConnMaxLifetime(time.Hour)

	log.Println("Database connection established")
	return db, nil
}

func RunMigrations(db *gorm.DB) error {
	log.Println("[MIGRATION] Starting database migrations...")
	
	// AutoMigrate all models
	err := db.AutoMigrate(
		&domain.University{},
		&domain.Department{},
		&domain.Session{},
		&domain.Batch{},
		&domain.User{},
		&domain.Student{},
		&domain.Teacher{},
		&domain.Staff{},
		&domain.CR{},
		&domain.Verification{},
		&domain.Resource{},
		&domain.Transport{},
		&domain.Attachment{},
		&domain.Hall{},
		&domain.AuditLog{},
		&domain.Notification{},
		&domain.SubscriptionPlan{},
		&domain.ProFeature{},
		&domain.UserSubscription{},
		&domain.Routine{},
		&domain.Alumni{},
		&domain.Bookmark{},
		&domain.CourseCategory{},
		&domain.CoursePrefix{},
		&domain.Semester{},
		&domain.Course{},
		&domain.Banner{},
		&domain.BannerTarget{},
		&domain.Chapter{},
		&domain.EmergencyContact{},
	)
	if err != nil {
		return fmt.Errorf("AutoMigrate failed: %w", err)
	}

	// Cleanup legacy tables
	legacyTables := []string{"notes", "books", "questions", "syllabuses", "emergency_contacts"}
	for _, table := range legacyTables {
		if db.Migrator().HasTable(table) {
			db.Migrator().DropTable(table)
		}
	}

	log.Println("[MIGRATION] Database migrations completed successfully")
	return nil
}

