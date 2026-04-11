package main

import (
	"log"

	"campusassistant-api/internal/config"
	"campusassistant-api/internal/domain"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	dsn := cfg.DatabaseURL
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to open connection: %v", err)
	}

	log.Println("Dropping all tables to clean state...")

	// Drop tables in dependency order (Children first, Roots last)
	tables := []interface{}{
		&domain.Verification{},
		&domain.AuditLog{},
		&domain.Notification{},
		&domain.Attachment{},
		&domain.Resource{},
		&domain.Transport{},
		&domain.Student{},
		&domain.Teacher{},
		&domain.Staff{},
		&domain.Batch{},
		&domain.Semester{},
		&domain.Hall{},
		&domain.Session{},
		&domain.User{},
		&domain.Department{},
		&domain.University{},
	}

	for _, table := range tables {
		if err := db.Migrator().DropTable(table); err != nil {
			log.Printf("Warning: Failed to drop table %T: %v", table, err)
		}
	}

	log.Println("Database reset completed successfully.")
}
