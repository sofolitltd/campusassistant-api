package domain

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AnalyticsEvent struct {
	ID           uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID       uuid.UUID  `gorm:"type:uuid;not null;index:idx_analytics_user_event_time,priority:1" json:"user_id"`
	EventType    string     `gorm:"size:50;not null;index:idx_analytics_type_time,priority:1;index:idx_analytics_user_event_time,priority:2" json:"event_type"`
	EntityType   string     `gorm:"size:30;index:idx_analytics_entity,priority:1" json:"entity_type,omitempty"`
	EntityID     *uuid.UUID `gorm:"type:uuid;index:idx_analytics_entity,priority:2" json:"entity_id,omitempty"`
	AttemptID    *uuid.UUID `gorm:"type:uuid;index" json:"attempt_id,omitempty"`
	Category     string     `gorm:"size:32;index" json:"category,omitempty"`
	ResultCount  *int       `json:"result_count,omitempty"`
	Platform     string     `gorm:"size:12" json:"platform"`
	UniversityID *uuid.UUID `gorm:"type:uuid;index" json:"university_id,omitempty"`
	DepartmentID *uuid.UUID `gorm:"type:uuid;index" json:"department_id,omitempty"`
	DedupeKey    *string    `gorm:"size:100;uniqueIndex" json:"-"`
	EventAt      time.Time  `gorm:"not null;index:idx_analytics_type_time,priority:2;index:idx_analytics_user_event_time,priority:3" json:"event_at"`
}

func (e *AnalyticsEvent) BeforeCreate(_ *gorm.DB) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	if e.EventAt.IsZero() {
		e.EventAt = time.Now().UTC()
	}
	return nil
}
