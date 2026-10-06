package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var (
	ErrNotPurchased   = errors.New("only buyers who received this product can review it")
	ErrReviewNotFound = errors.New("review not found")
	ErrInvalidRating  = errors.New("rating must be between 1 and 5")
	ErrReplyTooLong   = errors.New("reply is too long")
)

const (
	maxReviewComment = 1000
	reviewNotifType  = "REVIEW"
)

// ReviewService owns product reviews: purchase-verified create/edit, delete,
// seller replies, admin moderation and the rating aggregates on Product and
// Merchant.
type ReviewService struct {
	db     *gorm.DB
	sender NotificationSender // optional
}

func NewReviewService(db *gorm.DB, sender NotificationSender) *ReviewService {
	return &ReviewService{db: db, sender: sender}
}

// ReviewView is a review as shown to other users: the reviewer is reduced to
// a first name and last initial.
type ReviewView struct {
	ID              uuid.UUID  `json:"id"`
	ProductID       uuid.UUID  `json:"product_id"`
	Rating          int        `json:"rating"`
	Comment         string     `json:"comment"`
	ReviewerName    string     `json:"reviewer_name"`
	CreatedAt       time.Time  `json:"created_at"`
	SellerReply     string     `json:"seller_reply"`
	SellerRepliedAt *time.Time `json:"seller_replied_at,omitempty"`
	Mine            bool       `json:"mine"`
}

type ReviewSummary struct {
	Average      float64     `json:"average"`
	Count        int         `json:"count"`
	Distribution map[int]int `json:"distribution"` // star → count
}

type ProductReviews struct {
	Summary     ReviewSummary `json:"summary"`
	Reviews     []ReviewView  `json:"reviews"`
	Total       int64         `json:"total"`
	MyReview    *ReviewView   `json:"my_review,omitempty"`
	CanReview   bool          `json:"can_review"`
	ReviewOrder uuid.UUID     `json:"review_order_id,omitempty"`
}

type userName struct {
	ID        uuid.UUID
	FirstName string
	LastName  string
}

func displayName(u userName) string {
	first := strings.TrimSpace(u.FirstName)
	last := strings.TrimSpace(u.LastName)
	switch {
	case first == "" && last == "":
		return "Student"
	case last == "":
		return first
	default:
		return first + " " + string([]rune(last)[0]) + "."
	}
}

// purchase finds the buyer's most recent delivered order containing the
// product — the proof they may review it.
func (s *ReviewService) purchase(ctx context.Context, db *gorm.DB, userID, productID uuid.UUID) (*domain.OrderItem, error) {
	var item domain.OrderItem
	err := db.WithContext(ctx).
		Joins("JOIN orders ON orders.id = order_items.order_id").
		Where("orders.buyer_id = ? AND orders.status = ? AND order_items.product_id = ?", userID, domain.OrderStatusDelivered, productID).
		Order("orders.created_at desc").
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotPurchased
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// Upsert creates or edits the user's review of a product.
func (s *ReviewService) Upsert(ctx context.Context, userID, productID uuid.UUID, rating int, comment string) (*domain.ProductReview, error) {
	if rating < 1 || rating > 5 {
		return nil, ErrInvalidRating
	}
	comment = strings.TrimSpace(comment)
	if len([]rune(comment)) > maxReviewComment {
		return nil, fmt.Errorf("comment must be at most %d characters", maxReviewComment)
	}

	var out *domain.ProductReview
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		item, err := s.purchase(ctx, tx, userID, productID)
		if err != nil {
			return err
		}
		var r domain.ProductReview
		err = tx.Where("product_id = ? AND user_id = ?", productID, userID).First(&r).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			r = domain.ProductReview{ProductID: productID, UserID: userID, MerchantID: item.MerchantID,
				OrderID: item.OrderID, ProductTitle: item.ProductTitle, Rating: rating, Comment: comment}
			if err := tx.Create(&r).Error; err != nil {
				return err
			}
			created = true
		case err != nil:
			return err
		default:
			if err := tx.Model(&domain.ProductReview{}).Where("id = ?", r.ID).
				Updates(map[string]interface{}{"rating": rating, "comment": comment}).Error; err != nil {
				return err
			}
			r.Rating, r.Comment = rating, comment
		}
		out = &r
		return recomputeRatings(tx, productID, r.MerchantID)
	})
	if err != nil {
		return nil, err
	}
	if created {
		s.notifySeller(ctx, out)
	}
	return out, nil
}

// Delete removes the user's own review.
func (s *ReviewService) Delete(ctx context.Context, userID, productID uuid.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r domain.ProductReview
		if err := tx.Where("product_id = ? AND user_id = ?", productID, userID).First(&r).Error; err != nil {
			return ErrReviewNotFound
		}
		if err := tx.Unscoped().Delete(&domain.ProductReview{}, "id = ?", r.ID).Error; err != nil {
			return err
		}
		return recomputeRatings(tx, r.ProductID, r.MerchantID)
	})
}

// Reply sets the seller's public reply. merchantID must be the review's
// merchant — the caller has already checked the user owns that merchant.
func (s *ReviewService) Reply(ctx context.Context, merchantID, reviewID uuid.UUID, reply string) (*domain.ProductReview, error) {
	reply = strings.TrimSpace(reply)
	if len([]rune(reply)) > maxReviewComment {
		return nil, ErrReplyTooLong
	}
	var r domain.ProductReview
	if err := s.db.WithContext(ctx).Where("id = ? AND merchant_id = ?", reviewID, merchantID).First(&r).Error; err != nil {
		return nil, ErrReviewNotFound
	}
	var at *time.Time
	if reply != "" {
		now := time.Now()
		at = &now
	}
	if err := s.db.WithContext(ctx).Model(&domain.ProductReview{}).Where("id = ?", r.ID).
		Updates(map[string]interface{}{"seller_reply": reply, "seller_replied_at": at}).Error; err != nil {
		return nil, err
	}
	r.SellerReply, r.SellerRepliedAt = reply, at
	if reply != "" {
		s.send(ctx, r.UserID, "The seller replied to your review",
			truncate(reply, 120), "/campusmarket/"+r.ProductID.String())
	}
	return &r, nil
}

// SetHidden is the admin moderation switch.
func (s *ReviewService) SetHidden(ctx context.Context, reviewID uuid.UUID, hidden bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r domain.ProductReview
		if err := tx.First(&r, "id = ?", reviewID).Error; err != nil {
			return ErrReviewNotFound
		}
		if err := tx.Model(&domain.ProductReview{}).Where("id = ?", r.ID).Update("is_hidden", hidden).Error; err != nil {
			return err
		}
		return recomputeRatings(tx, r.ProductID, r.MerchantID)
	})
}

// ListForProduct returns the public reviews, the rating summary and — for the
// signed-in user — their own review and whether they may write one.
func (s *ReviewService) ListForProduct(ctx context.Context, viewerID, productID uuid.UUID, limit, offset int) (*ProductReviews, error) {
	db := s.db.WithContext(ctx)
	base := db.Model(&domain.ProductReview{}).Where("product_id = ? AND is_hidden = ?", productID, false)

	res := &ProductReviews{Reviews: []ReviewView{}, Summary: ReviewSummary{Distribution: map[int]int{1: 0, 2: 0, 3: 0, 4: 0, 5: 0}}}
	if err := base.Count(&res.Total).Error; err != nil {
		return nil, err
	}

	var dist []struct {
		Rating int
		N      int
	}
	if err := db.Model(&domain.ProductReview{}).Select("rating, COUNT(*) AS n").
		Where("product_id = ? AND is_hidden = ?", productID, false).Group("rating").Scan(&dist).Error; err != nil {
		return nil, err
	}
	sum := 0
	for _, d := range dist {
		res.Summary.Distribution[d.Rating] = d.N
		res.Summary.Count += d.N
		sum += d.Rating * d.N
	}
	if res.Summary.Count > 0 {
		res.Summary.Average = float64(sum) / float64(res.Summary.Count)
	}

	var rows []domain.ProductReview
	if err := db.Where("product_id = ? AND is_hidden = ?", productID, false).
		Order("created_at desc").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, err
	}
	res.Reviews = s.views(ctx, rows, viewerID)

	if viewerID != uuid.Nil {
		var mine domain.ProductReview
		if err := db.Where("product_id = ? AND user_id = ?", productID, viewerID).First(&mine).Error; err == nil {
			v := s.views(ctx, []domain.ProductReview{mine}, viewerID)
			res.MyReview = &v[0]
		}
		if item, err := s.purchase(ctx, db, viewerID, productID); err == nil {
			res.CanReview = true
			res.ReviewOrder = item.OrderID
		}
	}
	return res, nil
}

// ListForMerchant is the seller's feed of reviews on their products.
func (s *ReviewService) ListForMerchant(ctx context.Context, merchantID uuid.UUID, limit, offset int) ([]domain.ProductReview, int64, error) {
	var total int64
	q := s.db.WithContext(ctx).Model(&domain.ProductReview{}).Where("merchant_id = ? AND is_hidden = ?", merchantID, false)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.ProductReview
	err := q.Order("created_at desc").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

// ListAll is the admin moderation list, newest first.
func (s *ReviewService) ListAll(ctx context.Context, hidden *bool, limit, offset int) ([]domain.ProductReview, int64, error) {
	q := s.db.WithContext(ctx).Model(&domain.ProductReview{})
	if hidden != nil {
		q = q.Where("is_hidden = ?", *hidden)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.ProductReview
	err := q.Order("created_at desc").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

// ReviewableItem is one line of a delivered order with the buyer's rating.
type ReviewableItem struct {
	ProductID    uuid.UUID `json:"product_id"`
	ProductTitle string    `json:"product_title"`
	MyRating     *int      `json:"my_rating"`
}

// ReviewableForOrder lists a delivered order's products and what the buyer
// already rated; an order that isn't delivered has nothing to review.
func (s *ReviewService) ReviewableForOrder(ctx context.Context, userID, orderID uuid.UUID) ([]ReviewableItem, error) {
	var order domain.Order
	if err := s.db.WithContext(ctx).Preload("Items").First(&order, "id = ?", orderID).Error; err != nil {
		return nil, ErrOrderNotFound
	}
	if order.BuyerID != userID {
		return nil, ErrForbidden
	}
	out := []ReviewableItem{}
	if order.Status != domain.OrderStatusDelivered {
		return out, nil
	}
	for _, it := range order.Items {
		ri := ReviewableItem{ProductID: it.ProductID, ProductTitle: it.ProductTitle}
		var r domain.ProductReview
		if err := s.db.WithContext(ctx).Where("product_id = ? AND user_id = ?", it.ProductID, userID).First(&r).Error; err == nil {
			rating := r.Rating
			ri.MyRating = &rating
		}
		out = append(out, ri)
	}
	return out, nil
}

// Views turns stored reviews into the public shape (exported for the handler).
func (s *ReviewService) Views(ctx context.Context, rows []domain.ProductReview, viewerID uuid.UUID) []ReviewView {
	return s.views(ctx, rows, viewerID)
}

func (s *ReviewService) views(ctx context.Context, rows []domain.ProductReview, viewerID uuid.UUID) []ReviewView {
	out := make([]ReviewView, 0, len(rows))
	if len(rows) == 0 {
		return out
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	var users []userName
	_ = s.db.WithContext(ctx).Model(&domain.User{}).Select("id, first_name, last_name").Where("id IN ?", ids).Scan(&users).Error
	names := make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		names[u.ID] = displayName(u)
	}
	for _, r := range rows {
		name := names[r.UserID]
		if name == "" {
			name = "Student"
		}
		out = append(out, ReviewView{ID: r.ID, ProductID: r.ProductID, Rating: r.Rating, Comment: r.Comment,
			ReviewerName: name, CreatedAt: r.CreatedAt, SellerReply: r.SellerReply, SellerRepliedAt: r.SellerRepliedAt,
			Mine: r.UserID == viewerID})
	}
	return out
}

// recomputeRatings refreshes the aggregates on the product and its merchant
// from the visible reviews. Raw SQL on purpose: the columns are read-only
// through GORM so ordinary product/merchant edits can't clobber them.
func recomputeRatings(tx *gorm.DB, productID, merchantID uuid.UUID) error {
	if err := tx.Exec(`UPDATE products SET
		rating_avg = COALESCE((SELECT AVG(rating) FROM product_reviews WHERE product_id = ? AND is_hidden = ?), 0),
		rating_count = (SELECT COUNT(*) FROM product_reviews WHERE product_id = ? AND is_hidden = ?)
		WHERE id = ?`, productID, false, productID, false, productID).Error; err != nil {
		return err
	}
	return tx.Exec(`UPDATE merchants SET
		rating_avg = COALESCE((SELECT AVG(rating) FROM product_reviews WHERE merchant_id = ? AND is_hidden = ?), 0),
		rating_count = (SELECT COUNT(*) FROM product_reviews WHERE merchant_id = ? AND is_hidden = ?)
		WHERE id = ?`, merchantID, false, merchantID, false, merchantID).Error
}

func (s *ReviewService) notifySeller(ctx context.Context, r *domain.ProductReview) {
	var m domain.Merchant
	if err := s.db.WithContext(ctx).First(&m, "id = ?", r.MerchantID).Error; err != nil {
		return
	}
	title := fmt.Sprintf("New %d★ review", r.Rating)
	body := r.ProductTitle
	if r.Comment != "" {
		body += ": " + truncate(r.Comment, 100)
	}
	s.send(ctx, m.UserID, title, body, "/merchant/manage/"+r.MerchantID.String())
}

func (s *ReviewService) send(ctx context.Context, userID uuid.UUID, title, body, route string) {
	if s.sender == nil {
		return
	}
	recipients, err := FilterMutedRecipients(ctx, s.db, []uuid.UUID{userID}, "marketplace", reviewNotifType)
	if err != nil || len(recipients) == 0 {
		return
	}
	raw, err := json.Marshal(map[string]interface{}{"action_route": route})
	if err != nil {
		return
	}
	data := datatypes.JSON(raw)
	n := domain.Notification{Title: title, Body: body, Type: reviewNotifType, Scope: "user", Data: &data}
	if _, _, err := s.sender.SendToUsers(ctx, n, recipients, uuid.Nil); err != nil {
		logger.Errorf("[reviews] notify %s: %v", userID, err)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
