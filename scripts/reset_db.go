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

	// Drop tables in dependency order
	// Depends on entities:
	// Student -> Batch -> Session/Dept -> University
	// Staff -> University
	// Teacher -> Department
	// Note -> Department

	tables := []interface{}{
		&domain.Verification{},
		&domain.Student{},
		&domain.Teacher{},
		&domain.Staff{},
		&domain.Batch{},
		&domain.Session{},
		&domain.Book{},
		&domain.Question{},
		&domain.Note{},
		&domain.Syllabus{},
		&domain.Transport{},
		&domain.User{},
		&domain.Department{}, // FK from above (Teacher, Note, etc)
		&domain.University{}, // FK from above (Dept, Session)
	}

	if err := db.Migrator().DropTable(tables...); err != nil {
		log.Fatalf("Failed to drop tables: %v", err)
	}

	log.Println("Tables dropped successfully.")
}
