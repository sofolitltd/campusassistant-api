package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type FeedbackCategory string

const (
	FeedbackBug        FeedbackCategory = "bug"
	FeedbackFeature    FeedbackCategory = "feature"
	FeedbackSuggestion FeedbackCategory = "suggestion"
	FeedbackComplaint  FeedbackCategory = "complaint"
	FeedbackGeneral    FeedbackCategory = "general"
)

type FeedbackStatus string

const (
	FeedbackPending  FeedbackStatus = "pending"
	FeedbackReviewed FeedbackStatus = "reviewed"
	FeedbackResolved FeedbackStatus = "resolved"
)

type Feedback struct {
	Base
	UserID        uuid.UUID        `gorm:"type:uuid;not null;index" json:"user_id"`
	User          *User            `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Category      FeedbackCategory `gorm:"type:varchar(20);not null;index;default:'general'" json:"category"`
	Subject       string           `gorm:"size:200;not null" json:"subject"`
	Message       string           `gorm:"type:text;not null" json:"message"`
	AttachmentURL string           `gorm:"size:500" json:"attachment_url,omitempty"`
	Status        FeedbackStatus   `gorm:"type:varchar(20);default:'pending';index" json:"status"`
	AdminReply    string           `gorm:"type:text" json:"admin_reply,omitempty"`
	RepliedAt     *time.Time       `json:"replied_at,omitempty"`
	RepliedByID   *uuid.UUID       `gorm:"type:uuid" json:"replied_by_id,omitempty"`
}

type FeedbackRepository interface {
	Repository[Feedback]
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]Feedback, error)
}
