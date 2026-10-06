package domain

import "github.com/google/uuid"

// WishlistItem is a product a user saved for later. Hard-deleted on removal
// so the unique (user, product) index never blocks saving it again.
type WishlistItem struct {
	Base
	UserID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_wishlist_user_product,priority:1" json:"user_id"`
	ProductID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_wishlist_user_product,priority:2;index" json:"product_id"`
}
