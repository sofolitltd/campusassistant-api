package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var ErrProductNotFound = errors.New("product not found")

const wishlistNotifType = "WISHLIST"

// WishlistService keeps users' saved products and tells them when a saved
// product is back in stock or gets cheaper.
type WishlistService struct {
	db     *gorm.DB
	sender NotificationSender // optional
}

func NewWishlistService(db *gorm.DB, sender NotificationSender) *WishlistService {
	return &WishlistService{db: db, sender: sender}
}

// Add saves a product; saving it twice is a no-op.
func (s *WishlistService) Add(ctx context.Context, userID, productID uuid.UUID) error {
	var n int64
	if err := s.db.WithContext(ctx).Model(&domain.Product{}).Where("id = ? AND is_published = ?", productID, true).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrProductNotFound
	}
	item := domain.WishlistItem{UserID: userID, ProductID: productID}
	return s.db.WithContext(ctx).
		Where("user_id = ? AND product_id = ?", userID, productID).
		FirstOrCreate(&item).Error
}

// Remove un-saves a product; removing one that isn't saved is a no-op.
func (s *WishlistService) Remove(ctx context.Context, userID, productID uuid.UUID) error {
	return s.db.WithContext(ctx).Unscoped().
		Where("user_id = ? AND product_id = ?", userID, productID).
		Delete(&domain.WishlistItem{}).Error
}

// List returns the user's saved products that are still on sale, most
// recently saved first.
func (s *WishlistService) List(ctx context.Context, userID uuid.UUID) ([]domain.Product, error) {
	var items []domain.WishlistItem
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at desc").Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []domain.Product{}, nil
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.ProductID
	}
	var found []domain.Product
	if err := s.db.WithContext(ctx).Where("id IN ? AND is_published = ?", ids, true).
		Preload("Merchant").Preload("Category").Find(&found).Error; err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]domain.Product, len(found))
	for _, p := range found {
		byID[p.ID] = p
	}
	out := make([]domain.Product, 0, len(found))
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// IDs returns just the saved product ids (for drawing heart icons).
func (s *WishlistService) IDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	ids := []uuid.UUID{}
	err := s.db.WithContext(ctx).Model(&domain.WishlistItem{}).Where("user_id = ?", userID).Pluck("product_id", &ids).Error
	return ids, err
}

// NotifyProductChange compares a product before and after an edit and tells
// everyone who saved it about the good news: back in stock, or a lower price.
// Best-effort; never fails the edit.
func (s *WishlistService) NotifyProductChange(ctx context.Context, before, after domain.Product) {
	if s == nil || s.sender == nil || !after.IsPublished {
		return
	}
	var title, body string
	switch {
	case before.Stock <= 0 && after.Stock > 0:
		title, body = "Back in stock", after.Title+" is available again."
	case after.Price < before.Price && after.Stock > 0:
		title, body = "Price drop", fmt.Sprintf("%s is now ৳%d (was ৳%d).", after.Title, after.Price, before.Price)
	default:
		return
	}

	var users []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&domain.WishlistItem{}).Where("product_id = ?", after.ID).Pluck("user_id", &users).Error; err != nil || len(users) == 0 {
		return
	}
	recipients, err := FilterMutedRecipients(ctx, s.db, users, "marketplace", wishlistNotifType)
	if err != nil || len(recipients) == 0 {
		return
	}
	raw, err := json.Marshal(map[string]interface{}{"action_route": "/campusmarket/" + after.ID.String(), "product_id": after.ID})
	if err != nil {
		return
	}
	data := datatypes.JSON(raw)
	n := domain.Notification{Title: title, Body: body, Type: wishlistNotifType, Scope: "user", Data: &data}
	if _, _, err := s.sender.SendToUsers(ctx, n, recipients, uuid.Nil); err != nil {
		logger.Errorf("[wishlist] notify for product %s: %v", after.ID, err)
	}
}
