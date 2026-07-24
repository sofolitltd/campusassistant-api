package domain

import "github.com/google/uuid"

type Attachment struct {
	Base
	FileName    string    `json:"file_name"`
	FileURL     string    `json:"file_url"`
	FileType    string    `json:"file_type"`
	FileSize    int64     `json:"file_size"`
	ReferenceID uuid.UUID `gorm:"type:uuid;index" json:"reference_id,omitempty"` // Optional link to another entity

	// StorageKey is the raw object path in R2 (e.g. "merchant-verification/2026/02/uuid.jpg"),
	// always populated. FileURL is only populated when Visibility is "public"
	// — for "private" attachments, the URL must be resolved on demand via a
	// short-lived presigned URL (see UploadHandler.MyGetUploadURL/AdminGetAttachmentURL),
	// never cached or returned directly.
	StorageKey string `json:"-"`
	// Visibility is "public" (default, resolvable via FileURL forever) or
	// "private" (sensitive documents — NID/Student ID proofs — requiring
	// ownership or admin access to view, and only via a short-lived URL).
	Visibility string `gorm:"size:20;default:'public'" json:"visibility"`
	// UploaderID is the JWT user who uploaded this file, set only for
	// uploads made through the authenticated /my/upload path. Legacy/admin
	// uploads via /upload have no uploader identity.
	UploaderID *uuid.UUID `gorm:"type:uuid;index" json:"uploader_id,omitempty"`
}
