package service

import (
	"context"
	"errors"
	"testing"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

func newReviewSvc(e *env) (*ReviewService, *fakeSender) {
	fs := &fakeSender{}
	return NewReviewService(e.db, fs), fs
}

func TestReview_RequiresDeliveredPurchase(t *testing.T) {
	e := newEnv(t)
	svc, _ := newReviewSvc(e)
	ctx := context.Background()
	buyer, stranger := e.user(t), e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 5)
	e.deliveredOrderFor(t, buyer, m, p)

	if _, err := svc.Upsert(ctx, stranger.ID, p.ID, 5, "great"); !errors.Is(err, ErrNotPurchased) {
		t.Fatalf("stranger: got %v", err)
	}
	// An order that hasn't been delivered doesn't count either.
	pending := e.user(t)
	e.order(t, pending, domain.PaymentMethodCashOnDelivery, domain.OrderStatusShipped, orderLine{m, 100, 1})
	if _, err := svc.Upsert(ctx, pending.ID, p.ID, 5, ""); !errors.Is(err, ErrNotPurchased) {
		t.Fatalf("undelivered: got %v", err)
	}
	if _, err := svc.Upsert(ctx, buyer.ID, p.ID, 6, ""); !errors.Is(err, ErrInvalidRating) {
		t.Fatalf("rating 6: got %v", err)
	}
	if _, err := svc.Upsert(ctx, buyer.ID, p.ID, 5, "great"); err != nil {
		t.Fatal(err)
	}
}

func TestReview_EditKeepsOneReviewAndUpdatesAggregates(t *testing.T) {
	e := newEnv(t)
	svc, fs := newReviewSvc(e)
	ctx := context.Background()
	a, b := e.user(t), e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 5)
	e.deliveredOrderFor(t, a, m, p)
	e.deliveredOrderFor(t, b, m, p)

	if _, err := svc.Upsert(ctx, a.ID, p.ID, 5, "love it"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upsert(ctx, b.ID, p.ID, 3, "ok"); err != nil {
		t.Fatal(err)
	}
	got := reload[domain.Product](t, e.db, p.ID)
	if got.RatingCount != 2 || got.RatingAvg != 4 {
		t.Fatalf("product rating = %v (%d), want 4.0 (2)", got.RatingAvg, got.RatingCount)
	}

	// Editing replaces, never duplicates, and doesn't alert the seller again.
	if _, err := svc.Upsert(ctx, b.ID, p.ID, 1, "changed my mind"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, &domain.ProductReview{}, "product_id = ?", p.ID); n != 2 {
		t.Fatalf("reviews = %d, want 2", n)
	}
	got = reload[domain.Product](t, e.db, p.ID)
	if got.RatingAvg != 3 {
		t.Fatalf("avg after edit = %v, want 3", got.RatingAvg)
	}
	mer := reload[domain.Merchant](t, e.db, m.ID)
	if mer.RatingCount != 2 || mer.RatingAvg != 3 {
		t.Fatalf("merchant rating = %v (%d)", mer.RatingAvg, mer.RatingCount)
	}
	if n := len(fs.to(m.UserID)); n != 2 {
		t.Fatalf("seller alerts = %d, want 2 (one per new review)", n)
	}
}

func TestReview_DeleteAllowsReReview(t *testing.T) {
	e := newEnv(t)
	svc, _ := newReviewSvc(e)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 5)
	e.deliveredOrderFor(t, buyer, m, p)

	if _, err := svc.Upsert(ctx, buyer.ID, p.ID, 4, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, buyer.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if got := reload[domain.Product](t, e.db, p.ID); got.RatingCount != 0 || got.RatingAvg != 0 {
		t.Fatalf("aggregates after delete = %v (%d)", got.RatingAvg, got.RatingCount)
	}
	if err := svc.Delete(ctx, buyer.ID, p.ID); !errors.Is(err, ErrReviewNotFound) {
		t.Fatalf("second delete: got %v", err)
	}
	if _, err := svc.Upsert(ctx, buyer.ID, p.ID, 5, "back again"); err != nil {
		t.Fatalf("re-review after delete: %v", err)
	}
}

func TestReview_HiddenIsExcludedEverywhere(t *testing.T) {
	e := newEnv(t)
	svc, _ := newReviewSvc(e)
	ctx := context.Background()
	buyer, viewer := e.user(t), e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 5)
	e.deliveredOrderFor(t, buyer, m, p)
	r, _ := svc.Upsert(ctx, buyer.ID, p.ID, 1, "spam")

	if err := svc.SetHidden(ctx, r.ID, true); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListForProduct(ctx, viewer.ID, p.ID, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 0 || len(list.Reviews) != 0 || list.Summary.Count != 0 {
		t.Fatalf("hidden review leaked into list: %+v", list)
	}
	if got := reload[domain.Product](t, e.db, p.ID); got.RatingCount != 0 {
		t.Fatalf("hidden review still counted: %d", got.RatingCount)
	}
	if err := svc.SetHidden(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := reload[domain.Product](t, e.db, p.ID); got.RatingCount != 1 {
		t.Fatalf("unhide should restore the count, got %d", got.RatingCount)
	}
}

func TestReview_ListShowsSummaryPrivacyAndEligibility(t *testing.T) {
	e := newEnv(t)
	svc, _ := newReviewSvc(e)
	ctx := context.Background()
	buyer := e.user(t) // "Test User"
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 5)
	e.deliveredOrderFor(t, buyer, m, p)

	other := e.user(t)
	list, _ := svc.ListForProduct(ctx, other.ID, p.ID, 20, 0)
	if list.CanReview {
		t.Fatalf("a non-buyer must not be offered the review form")
	}
	if _, err := svc.Upsert(ctx, buyer.ID, p.ID, 4, "good"); err != nil {
		t.Fatal(err)
	}

	list, _ = svc.ListForProduct(ctx, other.ID, p.ID, 20, 0)
	if len(list.Reviews) != 1 || list.Reviews[0].ReviewerName != "Test U." {
		t.Fatalf("reviewer should be shown as first name + initial, got %+v", list.Reviews)
	}
	if list.Reviews[0].Mine || list.MyReview != nil {
		t.Fatalf("another user's review must not be marked mine")
	}
	if list.Summary.Distribution[4] != 1 || list.Summary.Average != 4 {
		t.Fatalf("summary wrong: %+v", list.Summary)
	}

	mine, _ := svc.ListForProduct(ctx, buyer.ID, p.ID, 20, 0)
	if mine.MyReview == nil || !mine.MyReview.Mine || !mine.CanReview {
		t.Fatalf("buyer should see their own review and may edit it: %+v", mine)
	}
}

func TestReview_SellerReply(t *testing.T) {
	e := newEnv(t)
	svc, fs := newReviewSvc(e)
	ctx := context.Background()
	buyer := e.user(t)
	m, otherM := e.merchant(t, 10, false), e.merchant(t, 10, false)
	p := e.product(t, m, 100, 5)
	e.deliveredOrderFor(t, buyer, m, p)
	r, _ := svc.Upsert(ctx, buyer.ID, p.ID, 2, "arrived late")

	if _, err := svc.Reply(ctx, otherM.ID, r.ID, "not mine"); !errors.Is(err, ErrReviewNotFound) {
		t.Fatalf("another merchant replying: got %v", err)
	}
	if _, err := svc.Reply(ctx, m.ID, r.ID, "Sorry about that — fixed our courier."); err != nil {
		t.Fatal(err)
	}
	got := reload[domain.ProductReview](t, e.db, r.ID)
	if got.SellerReply == "" || got.SellerRepliedAt == nil {
		t.Fatalf("reply not stored: %+v", got)
	}
	if len(fs.to(buyer.ID)) != 1 {
		t.Fatalf("buyer should be told about the reply")
	}
}

func TestReviewableForOrder(t *testing.T) {
	e := newEnv(t)
	svc, _ := newReviewSvc(e)
	ctx := context.Background()
	buyer, other := e.user(t), e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 5)
	o := e.deliveredOrderFor(t, buyer, m, p)

	if _, err := svc.ReviewableForOrder(ctx, other.ID, o.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other user: got %v", err)
	}
	items, _ := svc.ReviewableForOrder(ctx, buyer.ID, o.ID)
	if len(items) != 1 || items[0].MyRating != nil {
		t.Fatalf("before rating: %+v", items)
	}
	_, _ = svc.Upsert(ctx, buyer.ID, p.ID, 5, "")
	items, _ = svc.ReviewableForOrder(ctx, buyer.ID, o.ID)
	if items[0].MyRating == nil || *items[0].MyRating != 5 {
		t.Fatalf("after rating: %+v", items)
	}
	shipped := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusShipped, orderLine{m, 100, 1})
	if items, _ := svc.ReviewableForOrder(ctx, buyer.ID, shipped.ID); len(items) != 0 {
		t.Fatalf("undelivered order should have nothing to review")
	}
	_ = uuid.Nil
}

// A product or merchant edit (which Saves the whole struct, often built from
// client JSON) must never overwrite the review aggregates.
func TestReview_AggregatesSurviveProductAndMerchantSave(t *testing.T) {
	e := newEnv(t)
	svc, _ := newReviewSvc(e)
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 5)
	e.deliveredOrderFor(t, buyer, m, p)
	if _, err := svc.Upsert(context.Background(), buyer.ID, p.ID, 5, ""); err != nil {
		t.Fatal(err)
	}

	stale := reload[domain.Product](t, e.db, p.ID)
	stale.Title, stale.RatingAvg, stale.RatingCount = "Renamed", 0, 0
	if err := e.db.Save(&stale).Error; err != nil {
		t.Fatal(err)
	}
	staleM := reload[domain.Merchant](t, e.db, m.ID)
	staleM.BusinessName, staleM.RatingAvg, staleM.RatingCount = "Renamed", 0, 0
	if err := e.db.Save(&staleM).Error; err != nil {
		t.Fatal(err)
	}

	if got := reload[domain.Product](t, e.db, p.ID); got.Title != "Renamed" || got.RatingCount != 1 || got.RatingAvg != 5 {
		t.Fatalf("product after save: %+v", got)
	}
	if got := reload[domain.Merchant](t, e.db, m.ID); got.RatingCount != 1 || got.RatingAvg != 5 {
		t.Fatalf("merchant after save: %v (%d)", got.RatingAvg, got.RatingCount)
	}
}
