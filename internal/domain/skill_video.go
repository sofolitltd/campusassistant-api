package domain

import "github.com/google/uuid"

// SkillVideo is one YouTube video within a Skill, added manually by an
// admin (no YouTube API key involved — the admin panel auto-fills
// Title/ThumbnailURL via the free, keyless oEmbed endpoint).
type SkillVideo struct {
	Base
	SkillID      uuid.UUID `gorm:"type:uuid;not null;index" json:"skill_id"`
	YoutubeURL   string    `gorm:"size:500;not null" json:"youtube_url"`
	Title        string    `gorm:"size:255;not null" json:"title"`
	ThumbnailURL string    `gorm:"size:500" json:"thumbnail_url"`
	// Duration is free text (e.g. "12:34"), optionally typed by the admin —
	// there's no key-free way to fetch real duration. UI hides it when empty.
	Duration string `gorm:"size:20" json:"duration"`
	Index    int    `gorm:"default:0" json:"index"` // manual ordering within the skill
}
