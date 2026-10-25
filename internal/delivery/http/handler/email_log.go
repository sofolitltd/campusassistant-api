package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// recordEmailLog persists one email_logs row per send attempt. Failure to
// log must never fail the mail flow — the send already happened — so a
// broken audit trail degrades to a server-log line, same spirit as the noop
// mailer's degradation.
//
// senderUUID is the acting admin's user_id from (Admin)JWT context; studentID
// is set for per-student mail and nil where it doesn't apply.
func recordEmailLog(db *gorm.DB, to, subject string, kind domain.EmailKind, status domain.EmailStatus, failure string, senderID, studentID *uuid.UUID) {
	row := domain.EmailLog{
		To:        to,
		Subject:   subject,
		Kind:      kind,
		Status:    status,
		Failure:   failure,
		StudentID: studentID,
	}
	row.CreatedByID = deref(senderID)
	if status == domain.EmailStatusFailed {
		logger.Errorf("email log: send to %s failed: %s", to, failure)
	}
	if err := db.Create(&row).Error; err != nil {
		logger.Errorf("email log: failed to record send to %s: %v", to, err)
	}
}

func deref(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
