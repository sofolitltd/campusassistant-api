package domain

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Batch represents a specific batch of students (e.g., "CSE - 25th Batch").
type Batch struct {
	Base
	Name       string `gorm:"size:100;not null" json:"name"`                     // e.g. "Batch 10"
	Slug       string `gorm:"size:100;not null;index" json:"slug"`               // e.g. "batch-10"
	IsStudying bool   `gorm:"default:true" json:"is_studying"`                   // "study" field in firestore
	Program    string `gorm:"size:20;not null;default:'honours'" json:"program"` // BatchProgram*; a Masters intake is its own batch
	// ImageURL is the batch's cover photo (16:9 WebP, see BatchUploadFolder).
	// Empty = the app draws a placeholder with the batch number.
	ImageURL     string      `gorm:"size:500" json:"image_url"`
	DepartmentID uuid.UUID   `gorm:"type:uuid;not null;index" json:"department_id"`
	Department   *Department `json:"department,omitempty"`
	UniversityID uuid.UUID   `gorm:"type:uuid;not null;index" json:"university_id"`
	Sessions     []Session   `gorm:"many2many:batch_sessions;" json:"sessions,omitempty"`
	// SessionIDs is write-only input from the admin form: when present it
	// replaces the batch's sessions (see AfterSave). Absent = leave them.
	SessionIDs *[]uuid.UUID `gorm:"-" json:"session_ids,omitempty"`
	Levels     []Level      `gorm:"many2many:level_batches;" json:"levels,omitempty"`
	Students   []Student    `gorm:"foreignKey:BatchID" json:"students,omitempty"`
}

// Batch programs. Honors and Masters of the same department are separate
// batches; a student moves between them via POST /batches/:id/promote, which
// keeps the earlier program as a StudentEnrollment row.
const (
	BatchProgramHonours = "honours"
	BatchProgramMasters = "masters"
)

// BatchUploadFolder holds batch cover photos. The admin crops them to 16:9
// and compresses them to WebP under ~150KB; the upload handler rejects
// anything over 300KB so an uncompressed photo can't slip through.
const BatchUploadFolder = "batches"

func IsValidBatchProgram(p string) bool {
	return p == BatchProgramHonours || p == BatchProgramMasters
}

// BeforeSave keeps Program valid: older clients (and full-struct PUTs from
// forms that predate the field) send it empty, which means Honors.
func (b *Batch) BeforeSave(tx *gorm.DB) error {
	if b.Program == "" {
		b.Program = BatchProgramHonours
	}
	if !IsValidBatchProgram(b.Program) {
		return fmt.Errorf("invalid batch program %q", b.Program)
	}
	return nil
}

// AfterSave applies SessionIDs. Without this the admin's session picker was
// silently dropped — the generic handler only knows struct fields. Writes the
// join table directly: Association().Replace re-saves the batch, which would
// re-enter this hook.
func (b *Batch) AfterSave(tx *gorm.DB) error {
	if b.SessionIDs == nil {
		return nil
	}
	ids := *b.SessionIDs
	b.SessionIDs = nil
	db := tx.Session(&gorm.Session{NewDB: true})

	sessions := []Session{}
	if len(ids) > 0 {
		if err := db.Where("id IN ?", ids).Order("sort_key DESC").Find(&sessions).Error; err != nil {
			return err
		}
		if len(sessions) != len(ids) {
			return fmt.Errorf("unknown session in session_ids")
		}
	}
	if err := db.Exec("DELETE FROM batch_sessions WHERE batch_id = ?", b.ID).Error; err != nil {
		return err
	}
	for _, s := range sessions {
		if err := db.Exec("INSERT INTO batch_sessions (batch_id, session_id) VALUES (?, ?)", b.ID, s.ID).Error; err != nil {
			return err
		}
	}
	b.Sessions = sessions
	return nil
}
