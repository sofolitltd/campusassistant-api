package domain

import "time"

// SchemaMigration records that a one-off legacy cleanup step in
// RunMigrations has already run, so it is skipped on later boots instead of
// re-checking (and re-logging) every time the process starts.
type SchemaMigration struct {
	Key       string `gorm:"primaryKey"`
	AppliedAt time.Time
}
