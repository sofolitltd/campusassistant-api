package domain

import (
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Notice is a department-scoped announcement shown on the student notice board.
type Notice struct {
	Base
	Uploader      string         `gorm:"size:255;not null" json:"uploader"`
	Message       string         `gorm:"type:text;not null" json:"message"`
	ImageURLs     pq.StringArray `gorm:"type:text[];default:'{}'" json:"image_urls"`
	UniversityID  uuid.UUID      `gorm:"type:uuid;index;not null" json:"university_id"`
	DepartmentID  uuid.UUID      `gorm:"type:uuid;index;not null" json:"department_id"`
	LikesCount    int            `gorm:"default:0" json:"likes_count"`
	CommentsCount int            `gorm:"default:0" json:"comments_count"`
	ViewsCount    int            `gorm:"default:0" json:"views_count"`
}

// NoticeLike records that a user has liked a notice.
type NoticeLike struct {
	NoticeID uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID   uuid.UUID `gorm:"type:uuid;primaryKey"`
}

// NoticeRead records that a user has opened/read a notice, used to count
// unique views exactly once per user rather than once per open.
type NoticeRead struct {
	NoticeID uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID   uuid.UUID `gorm:"type:uuid;primaryKey"`
}

// NoticeComment is a flat (non-threaded) comment on a notice.
type NoticeComment struct {
	Base
	Content  string    `gorm:"type:text;not null" json:"content"`
	NoticeID uuid.UUID `gorm:"type:uuid;index;not null" json:"notice_id"`
	AuthorID uuid.UUID `gorm:"type:uuid;not null" json:"author_id"`
	Author   *User     `gorm:"foreignKey:AuthorID" json:"author,omitempty"`
}
