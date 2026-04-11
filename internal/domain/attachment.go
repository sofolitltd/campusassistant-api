package domain

import "github.com/google/uuid"

type Attachment struct {
	Base
	FileName    string    `json:"file_name"`
	FileURL     string    `json:"file_url"`
	FileType    string    `json:"file_type"`
	FileSize    int64     `json:"file_size"`
	ReferenceID uuid.UUID `gorm:"type:uuid;index" json:"reference_id,omitempty"` // Optional link to another entity
}
