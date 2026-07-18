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
		Logger:                 logger.Default.LogMode(logger.Warn),
		PrepareStmt:            false,
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
	
	// Fix for subscription_plans.duration_days NOT NULL constraint error
	// If the column exists, update null values to a default (e.g., 30) before GORM tries to make it NOT NULL
	if db.Migrator().HasTable(&domain.SubscriptionPlan{}) {
		if db.Migrator().HasColumn(&domain.SubscriptionPlan{}, "duration_days") {
			db.Model(&domain.SubscriptionPlan{}).Where("duration_days IS NULL").Update("duration_days", 30)
		}
	}

	// Clean up legacy columns for Transport
	if db.Migrator().HasTable("transports") {
		for _, col := range []string{"route_name", "bus_number", "schedule", "driver_contact"} {
			if db.Migrator().HasColumn("transports", col) {
				db.Migrator().DropColumn("transports", col)
			}
		}
		// If "title" column does not exist yet, it will be added by AutoMigrate.
		// If there are existing rows, they will have NULL in "title", causing constraint failure.
		// To prevent this, we delete all rows from transports if "title" column is missing.
		if !db.Migrator().HasColumn("transports", "title") {
			log.Println("[MIGRATION] Dropping legacy rows from transports to avoid NOT NULL constraint errors")
			db.Exec("DELETE FROM transports")
		}
	}

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
		&domain.SubscriptionTarget{},
		&domain.UserSubscription{},
		&domain.Routine{},
		&domain.Organization{},
		&domain.Alumni{},
		&domain.Bookmark{},
		&domain.CourseCategory{},
		&domain.CoursePrefix{},
		&domain.Level{},
		&domain.Course{},
		&domain.Banner{},
		&domain.BannerTarget{},
		&domain.Chapter{},
		&domain.EmergencyContact{},
		&domain.ContactTarget{},
		&domain.CommunityPost{},
		&domain.CommunityComment{},
		&domain.CommunityPostLike{},
		&domain.CommunityCommentLike{},
		&domain.Club{},
		&domain.Conversation{},
		&domain.ConversationParticipant{},
		&domain.Message{},
		&domain.UserDeletedMessage{},
	)
	if err != nil {
		return fmt.Errorf("AutoMigrate failed: %w", err)
	}

	// Drop legacy columns from earlier schema iterations
	if db.Migrator().HasColumn(&domain.Message{}, "deleted_by_user") {
		db.Migrator().DropColumn(&domain.Message{}, "deleted_by_user")
	}

	// Cleanup legacy tables
	legacyTables := []string{"notes", "books", "questions", "syllabuses", "emergency_contacts"}
	for _, table := range legacyTables {
		if db.Migrator().HasTable(table) {
			db.Migrator().DropTable(table)
		}
	}

	// Drop legacy columns from modernized tables to prevent NOT NULL constraint errors
	if db.Migrator().HasColumn("contacts", "scope") {
		db.Migrator().DropColumn("contacts", "scope")
	}
	if db.Migrator().HasColumn("contacts", "university_id") {
		db.Migrator().DropColumn("contacts", "university_id")
	}
	if db.Migrator().HasColumn("contacts", "department_id") {
		db.Migrator().DropColumn("contacts", "department_id")
	}

	// Backfill organizational IDs on existing community posts (new columns
	// start NULL). Derive from the author's student record so old posts show
	// in the correct filtered feeds.
	if db.Migrator().HasColumn(&domain.CommunityPost{}, "university_id") {
		if err := db.Exec(`
			UPDATE community_posts cp
			SET university_id = s.university_id,
			    department_id = s.department_id,
			    batch_id      = s.batch_id
			FROM students s
			WHERE cp.author_id = s.user_id
			  AND cp.university_id IS NULL
		`).Error; err != nil {
			log.Printf("[MIGRATION] community_posts backfill warning: %v", err)
		}
	}

	log.Println("[MIGRATION] Database migrations completed successfully")
	return nil
}

