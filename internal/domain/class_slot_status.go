package domain

import (
	"time"

	"github.com/google/uuid"
)

// ClassSlotStatus overrides a single occurrence of a ClassSlot: the class
// was cancelled, or moved to a different time/room. No row for a given
// (slot, date) means the class is held as scheduled. CRs set these as
// changes come up; admins can too.
type ClassSlotStatus struct {
	Base
	ClassSlotID  uuid.UUID `gorm:"type:uuid;not null;index:idx_class_slot_status_slot_date,unique" json:"class_slot_id"`
	Date         time.Time `gorm:"type:date;not null;index:idx_class_slot_status_slot_date,unique" json:"date"`
	Status       string    `gorm:"size:20;not null" json:"status"` // "cancelled" | "rescheduled"
	NewStartTime string    `gorm:"size:5" json:"new_start_time,omitempty"`
	NewEndTime   string    `gorm:"size:5" json:"new_end_time,omitempty"`
	NewRoom      string    `gorm:"size:100" json:"new_room,omitempty"`
	Note         string    `gorm:"size:255" json:"note,omitempty"`
}
