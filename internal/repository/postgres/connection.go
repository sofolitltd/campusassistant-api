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
		Logger:                                   logger.Default.LogMode(logger.Warn),
		PrepareStmt:                              false,
		SkipDefaultTransaction:                   true,
		DisableForeignKeyConstraintWhenMigrating: true,
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

	// Pool sized for a small VPS (1 CPU core, 2GB RAM) running Postgres alongside the app.
	if cfg.Environment == "production" {
		sqlDB.SetMaxIdleConns(3)
		sqlDB.SetMaxOpenConns(15)
	} else {
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetMaxOpenConns(100)
	}
	sqlDB.SetConnMaxLifetime(time.Hour)

	log.Println("Database connection established")
	return db, nil
}

// once runs fn exactly one time across the life of the database: it checks
// the schema_migrations ledger for key, skips if already recorded, and
// records it after fn succeeds. This replaces re-running (and re-logging)
// every ad-hoc legacy cleanup step's HasTable/HasColumn checks on every boot
// — once a step has applied, later runs skip it outright.
func once(db *gorm.DB, key string, fn func() error) {
	var count int64
	db.Model(&domain.SchemaMigration{}).Where("key = ?", key).Count(&count)
	if count > 0 {
		return
	}
	log.Printf("[MIGRATION] Running one-time step: %s", key)
	if err := fn(); err != nil {
		log.Printf("[MIGRATION] one-time step %q failed, will retry next run: %v", key, err)
		return
	}
	if err := db.Create(&domain.SchemaMigration{Key: key, AppliedAt: time.Now()}).Error; err != nil {
		log.Printf("[MIGRATION] failed to record one-time step %q as applied: %v", key, err)
	}
}

func RunMigrations(db *gorm.DB) error {
	log.Println("[MIGRATION] Starting database migrations...")

	// Ledger table for the `once` helper below — must exist before anything
	// else checks it.
	if err := db.AutoMigrate(&domain.SchemaMigration{}); err != nil {
		return fmt.Errorf("failed to create schema_migrations ledger: %w", err)
	}

	// Fix for subscription_plans.duration_days NOT NULL constraint error
	// If the column exists, update null values to a default (e.g., 30) before GORM tries to make it NOT NULL
	once(db, "backfill_subscription_plans_duration_days", func() error {
		if db.Migrator().HasTable(&domain.SubscriptionPlan{}) && db.Migrator().HasColumn(&domain.SubscriptionPlan{}, "duration_days") {
			db.Model(&domain.SubscriptionPlan{}).Where("duration_days IS NULL").Update("duration_days", 30)
		}
		return nil
	})

	// Clean up legacy columns for Transport
	once(db, "cleanup_transports_legacy_columns", func() error {
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
		return nil
	})

	// Notifications moved from one-row-per-recipient to a content row + per-user
	// recipient row. Drop the old shape so AutoMigrate recreates it cleanly —
	// existing rows are disposable, there's no external system depending on them.
	once(db, "drop_legacy_notifications_table", func() error {
		if db.Migrator().HasTable(&domain.Notification{}) && db.Migrator().HasColumn(&domain.Notification{}, "user_id") {
			log.Println("[MIGRATION] Dropping legacy per-recipient notifications table")
			db.Migrator().DropTable("notifications")
		}
		return nil
	})

	// FCMToken moved off User onto a dedicated UserDevice table (multi-device
	// support) — it was never actually read/written anywhere, safe to drop.
	once(db, "drop_users_fcm_token", func() error {
		if db.Migrator().HasTable(&domain.User{}) && db.Migrator().HasColumn(&domain.User{}, "fcm_token") {
			log.Println("[MIGRATION] Dropping legacy users.fcm_token column")
			db.Migrator().DropColumn(&domain.User{}, "fcm_token")
		}
		return nil
	})

	// AutoMigrate all models
	err := db.AutoMigrate(
		&domain.AppConfig{},
		&domain.University{},
		&domain.Faculty{},
		&domain.Department{},
		&domain.Skill{},
		&domain.SkillTarget{},
		&domain.SkillVideo{},
		&domain.Session{},
		&domain.Batch{},
		&domain.User{},
		&domain.Admin{},
		&domain.PasswordReset{},
		&domain.Student{},
		&domain.Teacher{},
		&domain.Staff{},
		&domain.CR{},
		&domain.Verification{},
		&domain.Resource{},
		&domain.ResourceRating{},
		&domain.Transport{},
		&domain.Attachment{},
		&domain.Hall{},
		&domain.AuditLog{},
		&domain.SystemLog{},
		&domain.Notification{},
		&domain.NotificationRecipient{},
		&domain.UserDevice{},
		&domain.SubscriptionPlan{},
		&domain.SubscriptionTarget{},
		&domain.UserSubscription{},
		&domain.AccessGrant{},
		&domain.BkashTransaction{},
		&domain.Journal{},
		&domain.LedgerEntry{},
		&domain.Invoice{},
		&domain.InvoiceLine{},
		&domain.InvoiceCounter{},
		&domain.MerchantPayout{},
		&domain.Refund{},
		&domain.PlanEntitlement{},
		&domain.UsageCounter{},
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
		&domain.ClubFollow{},
		&domain.ClubEvent{},
		&domain.ClubMember{},
		&domain.ClubManager{},
		&domain.ClubPost{},
		&domain.Association{},
		&domain.AssociationFollow{},
		&domain.AssociationEvent{},
		&domain.AssociationMember{},
		&domain.AssociationManager{},
		&domain.AssociationPost{},
		&domain.Conversation{},
		&domain.ConversationParticipant{},
		&domain.Message{},
		&domain.UserDeletedMessage{},
		&domain.Notice{},
		&domain.ClassNotice{},
		&domain.ClassSlot{},
		&domain.ClassSlotStatus{},
		&domain.Contributor{},
		&domain.NoticeLike{},
		&domain.NoticeRead{},
		&domain.NoticeComment{},
		&domain.Merchant{},
		&domain.Product{},
		&domain.ProductTarget{},
		&domain.MarketplaceCategory{},
		&domain.Address{},
		&domain.Order{},
		&domain.OrderItem{},
		&domain.OrderTransaction{},
		&domain.OrderEvent{},
		&domain.ProductReview{},
		&domain.WishlistItem{},
		&domain.CommissionPolicy{},
		&domain.LostFoundCategory{},
		&domain.LostFoundItem{},
		&domain.LostFoundItemTarget{},
		&domain.LostFoundClaim{},
		&domain.LostFoundReport{},
		&domain.CareerCircularCategory{},
		&domain.CareerCircular{},
		&domain.CareerCircularTarget{},
		&domain.CareerJob{},
		&domain.CareerReminder{},
		&domain.NotificationMute{},
		&domain.CouponCode{},
		&domain.Feedback{},
		&domain.StudentVerificationRequest{},
		&domain.UserReward{},
		&domain.RewardTransaction{},
	)
	if err != nil {
		return fmt.Errorf("AutoMigrate failed: %w", err)
	}

	// Drop legacy columns from earlier schema iterations
	once(db, "drop_message_deleted_by_user", func() error {
		if db.Migrator().HasColumn(&domain.Message{}, "deleted_by_user") {
			db.Migrator().DropColumn(&domain.Message{}, "deleted_by_user")
		}
		return nil
	})

	// Cleanup legacy tables
	once(db, "drop_legacy_tables", func() error {
		legacyTables := []string{"notes", "books", "questions", "syllabuses", "emergency_contacts"}
		for _, table := range legacyTables {
			if db.Migrator().HasTable(table) {
				db.Migrator().DropTable(table)
			}
		}
		return nil
	})

	// Drop legacy columns from modernized tables to prevent NOT NULL constraint errors
	once(db, "drop_contacts_legacy_columns", func() error {
		for _, col := range []string{"scope", "university_id", "department_id"} {
			if db.Migrator().HasColumn("contacts", col) {
				db.Migrator().DropColumn("contacts", col)
			}
		}
		return nil
	})

	// Drop legacy uploader columns from resources (migrated to created_by_id FK)
	once(db, "drop_resources_uploader_columns", func() error {
		if db.Migrator().HasTable("resources") {
			for _, col := range []string{"uploader_id", "uploader_uid", "uploader_name"} {
				if db.Migrator().HasColumn("resources", col) {
					db.Migrator().DropColumn("resources", col)
				}
			}
		}
		return nil
	})

	// Backfill organizational IDs on existing community posts (new columns
	// start NULL). Derive from the author's student record so old posts show
	// in the correct filtered feeds.
	once(db, "backfill_community_posts_org_ids", func() error {
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
		return nil
	})

	// Composite primary keys only index lookups by their *first* column, and
	// GORM never indexes the second one. These are the second columns we
	// actually filter by: "resources/chapters/courses/levels for my batch" and
	// "my conversations" / "posts I liked". Names follow GORM's idx_<table>_<col>
	// so a later `index` tag on the same column won't create a duplicate.
	once(db, "index_composite_pk_second_columns", func() error {
		for _, stmt := range []string{
			`CREATE INDEX IF NOT EXISTS idx_resource_batches_batch_id ON resource_batches (batch_id)`,
			`CREATE INDEX IF NOT EXISTS idx_chapter_batches_batch_id ON chapter_batches (batch_id)`,
			`CREATE INDEX IF NOT EXISTS idx_course_batches_batch_id ON course_batches (batch_id)`,
			`CREATE INDEX IF NOT EXISTS idx_level_batches_batch_id ON level_batches (batch_id)`,
			`CREATE INDEX IF NOT EXISTS idx_conversation_participants_user_id ON conversation_participants (user_id)`,
			`CREATE INDEX IF NOT EXISTS idx_community_post_likes_user_id ON community_post_likes (user_id)`,
		} {
			if err := db.Exec(stmt).Error; err != nil {
				return err
			}
		}
		return nil
	})

	log.Println("[MIGRATION] Database migrations completed successfully")
	return nil
}
