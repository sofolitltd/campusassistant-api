package domain

import (
	"time"

	"github.com/google/uuid"
)

// ProductReview is a buyer's rating of a product they actually received. One
// review per (product, user): reviewing again edits it. Reviews are hard
// deleted (not soft) so the unique index never blocks a fresh review.
type ProductReview struct {
	Base
	ProductID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_review_product_user,priority:1" json:"product_id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_review_product_user,priority:2;index" json:"user_id"`
	// MerchantID is the seller at the time of purchase (from the order line).
	MerchantID uuid.UUID `gorm:"type:uuid;not null;index" json:"merchant_id"`
	OrderID    uuid.UUID `gorm:"type:uuid;not null" json:"order_id"`
	// ProductTitle is a snapshot, so the admin list and notifications still
	// read well if the product is later renamed or removed.
	ProductTitle    string     `gorm:"size:255" json:"product_title"`
	Rating          int        `gorm:"not null" json:"rating"` // 1..5
	Comment         string     `gorm:"size:1000" json:"comment"`
	SellerReply     string     `gorm:"size:1000" json:"seller_reply"`
	SellerRepliedAt *time.Time `json:"seller_replied_at,omitempty"`
	// IsHidden is an admin moderation flag; hidden reviews are excluded from
	// the public list and from the rating aggregates.
	IsHidden bool `gorm:"not null;default:false;index" json:"is_hidden"`
}
