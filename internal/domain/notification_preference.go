package domain

import "github.com/google/uuid"

// NotificationMute records that a user has opted out of a specific
// notification category. Absence of a row for (user_id, category) means
// subscribed (the default) — this makes "everyone gets everything" the
// zero-row baseline, so no backfill is needed for existing users and no
// migration is needed when a new category is added later.
type NotificationMute struct {
	Base
	UserID   uuid.UUID `gorm:"type:uuid;index;uniqueIndex:idx_user_category" json:"user_id"`
	Category string    `gorm:"size:30;uniqueIndex:idx_user_category" json:"category"`
}

// NotificationCategories is the fixed set of categories a user can mute,
// derived from every place the backend actually sends a push notification
// today. Keep in sync with:
//   - handler.notifyClubFollowers / notifyAssociationFollowers / notifyLostFoundUser
//   - service.CareerReminderScheduler
//   - handler.MerchantHandler (Type=MERCHANT_STATUS)
//   - handler.ResourceHandler (Type=studyMaterial)
//   - admin broadcasts with Scope in {university, department, batch}
var NotificationCategories = []string{
	"university",
	"department",
	"batch",
	"club",
	"association",
	"lost_found",
	"career",
	"marketplace",
	"study_material",
}

func IsValidNotificationCategory(category string) bool {
	for _, c := range NotificationCategories {
		if c == category {
			return true
		}
	}
	return false
}

// IsGeographicNotificationCategory reports whether category is delivered via
// an FCM topic (university_<id>/department_<id>/batch_<id>) rather than a
// per-user token list — these are the categories that require
// DeviceTopicService.ReconcileTopics to be re-run when toggled.
func IsGeographicNotificationCategory(category string) bool {
	return category == "university" || category == "department" || category == "batch"
}
