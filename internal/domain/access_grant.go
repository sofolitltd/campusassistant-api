package domain

import (
	"time"

	"github.com/google/uuid"
)

// Access grant scopes — who a grant applies to.
const (
	GrantScopeAll        = "all"
	GrantScopeUniversity = "university"
	GrantScopeDepartment = "department"
	GrantScopeBatch      = "batch"
	GrantScopeUser       = "user"
)

// AccessGrant is an admin-issued, time-boxed perk for a slice of users —
// a new campus launch, a department partnership, a batch's exam season, an
// ambassador. Unlike a subscription it isn't bought and doesn't make the
// user Pro; it only switches individual perks on.
type AccessGrant struct {
	Base
	Label     string     `gorm:"size:120;not null" json:"label"`
	ScopeType string     `gorm:"size:20;not null;index:idx_access_grant_scope,priority:1" json:"scope_type"`
	ScopeID   *uuid.UUID `gorm:"type:uuid;index:idx_access_grant_scope,priority:2" json:"scope_id,omitempty"`

	// Perks. AdFree hides every ad; CoinFree makes resource downloads cost
	// no coins (and so skips the out-of-coins ad too).
	AdFree   bool `gorm:"not null;default:false" json:"ad_free"`
	CoinFree bool `gorm:"not null;default:false" json:"coin_free"`

	StartsAt time.Time  `gorm:"not null" json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at,omitempty"` // nil = no end

	// NewUserDays > 0 limits the grant to accounts younger than that many
	// days, each getting it until created_at + NewUserDays (capped by EndsAt)
	// — e.g. "every new user, first 7 days ad-free".
	NewUserDays int `gorm:"not null;default:0" json:"new_user_days"`

	CreatedBy *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
}

// GrantPerks is a user's resolved view of every grant that applies to them
// right now. An *Until of nil with the perk on means "no end date".
type GrantPerks struct {
	AdFree        bool       `json:"ad_free"`
	AdFreeUntil   *time.Time `json:"ad_free_until,omitempty"`
	CoinFree      bool       `json:"coin_free"`
	CoinFreeUntil *time.Time `json:"coin_free_until,omitempty"`
	// Label of the longest-running grant, for the app's offer badge.
	Label string `json:"label,omitempty"`
}
