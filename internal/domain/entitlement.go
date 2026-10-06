package domain

import "github.com/google/uuid"

// Feature names a gated capability. They are plain strings (not an enum
// table) so a new gated feature needs no migration — add a constant, then
// check it with service.EntitlementService / middleware.RequireEntitlement.
const (
	FeatureAdFree             = "ad_free"
	FeatureUnlimitedDownloads = "unlimited_downloads"
	FeaturePremiumContent     = "premium_content"
)

// DefaultProFeatures is what a Pro user gets when their plan has no explicit
// PlanEntitlement rows — i.e. every plan created before this module existed
// keeps behaving exactly as before ("Pro = everything on").
var DefaultProFeatures = []string{FeatureAdFree, FeatureUnlimitedDownloads, FeaturePremiumContent}

const (
	// LimitUnlimited marks a feature with no usage cap.
	LimitUnlimited = -1

	PeriodNone  = "none"  // Limit is a lifetime cap (or irrelevant when unlimited)
	PeriodDaily = "daily" // counter resets every UTC day
	PeriodMonth = "month" // counter resets every UTC month
)

// PlanEntitlement grants a feature, optionally capped, to everyone on a plan.
// Limit is LimitUnlimited (-1) for no cap, 0 for "listed but denied", or a
// positive cap measured per Period.
type PlanEntitlement struct {
	Base
	PlanID  uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_plan_feature,priority:1" json:"plan_id"`
	Feature string    `gorm:"size:60;not null;uniqueIndex:idx_plan_feature,priority:2" json:"feature"`
	Limit   int       `gorm:"column:usage_limit;not null;default:-1" json:"limit"`
	Period  string    `gorm:"size:10;not null;default:'none'" json:"period"`
}

// UsageCounter tracks consumption of a capped feature within one period.
// PeriodKey is e.g. "2026-10-05" (daily), "2026-10" (month) or "all".
type UsageCounter struct {
	Base
	UserID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_usage_key,priority:1" json:"user_id"`
	Feature   string    `gorm:"size:60;not null;uniqueIndex:idx_usage_key,priority:2" json:"feature"`
	PeriodKey string    `gorm:"size:20;not null;uniqueIndex:idx_usage_key,priority:3" json:"period_key"`
	Used      int       `gorm:"not null;default:0" json:"used"`
}

// FeatureAccess is what a user may do with one feature right now.
type FeatureAccess struct {
	Allowed bool   `json:"allowed"`
	Limit   int    `json:"limit"` // LimitUnlimited (-1) when uncapped
	Period  string `json:"period"`
	Used    int    `json:"used"`
	// Remaining is -1 when unlimited.
	Remaining int `json:"remaining"`
}

// UserEntitlements is the resolved view of everything a user is entitled to.
type UserEntitlements struct {
	IsPro    bool                     `json:"is_pro"`
	PlanID   *uuid.UUID               `json:"plan_id,omitempty"`
	Features map[string]FeatureAccess `json:"features"`
}
