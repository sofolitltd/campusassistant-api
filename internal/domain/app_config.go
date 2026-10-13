package domain

import "time"

// AppConfig is a singleton row (fixed ID 1) the mobile app reads on launch to
// decide whether the installed build must be force-updated before use.
// MinSupportedVersionCode is an Android versionCode floor: bump it only when
// shipping a release that must be mandatory, leave it alone otherwise.
type AppConfig struct {
	ID                      uint      `gorm:"primaryKey" json:"id"`
	MinSupportedVersionCode int       `gorm:"default:0" json:"min_supported_version_code"`
	UpdateMessage           string    `json:"update_message"`
	PlayStoreURL            string    `json:"play_store_url"`
	UpdatedAt               time.Time `json:"updated_at"`
}
