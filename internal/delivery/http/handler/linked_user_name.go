package handler

import (
	"log"
	"strings"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// syncLinkedUserName copies an admin's name correction on a claimed
// student/teacher/staff record onto the linked user account. The app shows
// the account's name everywhere, and verified users can't change it
// themselves (see UpdateMyUser), so without this an admin fix never shows up.
//
// Only an actual name change is copied: older claimed records can hold the
// admin-typed name while the account holds the one the user entered, and an
// unrelated edit (phone, hall, ...) mustn't overwrite the account's name.
func syncLinkedUserName(c *gin.Context, db *gorm.DB, userID *uuid.UUID, oldName, newName string) {
	newName = strings.Join(strings.Fields(newName), " ")
	if userID == nil || newName == "" || newName == strings.Join(strings.Fields(oldName), " ") {
		return
	}
	first, last, _ := strings.Cut(newName, " ")
	err := db.WithContext(c.Request.Context()).
		Model(&domain.User{}).
		Where("id = ?", *userID).
		Updates(map[string]interface{}{"first_name": first, "last_name": last}).Error
	if err != nil {
		log.Printf("[profile name sync] failed to update user %s: %v", *userID, err)
	}
}
