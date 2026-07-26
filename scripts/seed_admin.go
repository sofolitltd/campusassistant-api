//go:build ignore

package main

import (
	"log"

	"campusassistant-api/internal/config"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/pkg/auth"
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

	// Create admins table if not exists
	if err := db.AutoMigrate(&domain.Admin{}); err != nil {
		log.Fatalf("Failed to create admins table: %v", err)
	}
	log.Println("admins table ready")

	// Remove from users table if exists
	result := db.Unscoped().Where("email = ?", "sofolitltd@gmail.com").Delete(&domain.User{})
	if result.Error != nil {
		log.Printf("Warning: failed to delete from users: %v", result.Error)
	} else if result.RowsAffected > 0 {
		log.Println("Removed sofolitltd@gmail.com from users table")
	} else {
		log.Println("No matching user found in users table")
	}

	// Create admin in admins table
	hashedPassword, err := auth.HashPassword("Pro1122@@")
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	admin := domain.Admin{
		Email:        "sofolitltd@gmail.com",
		PasswordHash: hashedPassword,
		Name:         "Sofoli Limited",
		Role:         "super_admin",
		IsActive:     true,
	}

	repo := postgres.NewAdminRepository(db)
	existing, _ := repo.FindByEmail(admin.Email)
	if existing != nil {
		existing.PasswordHash = hashedPassword
		existing.Name = admin.Name
		existing.IsActive = true
		if err := db.Save(existing).Error; err != nil {
			log.Fatalf("Failed to update admin: %v", err)
		}
		log.Printf("Admin updated: %s (role: %s)", existing.Email, existing.Role)
	} else {
		if err := repo.Create(&admin); err != nil {
			log.Fatalf("Failed to create admin: %v", err)
		}
		log.Printf("Admin created: %s (role: %s)", admin.Email, admin.Role)
	}
}
