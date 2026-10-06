package service

import (
	"context"
	"errors"
	"testing"

	"campusassistant-api/internal/domain"
)

func newWishlist(e *env) (*WishlistService, *fakeSender) {
	fs := &fakeSender{}
	return NewWishlistService(e.db, fs), fs
}

func TestWishlist_AddIsIdempotentAndRemoveWorks(t *testing.T) {
	e := newEnv(t)
	w, _ := newWishlist(e)
	ctx := context.Background()
	u := e.user(t)
	p := e.product(t, e.merchant(t, 10, false), 100, 3)

	for i := 0; i < 2; i++ {
		if err := w.Add(ctx, u.ID, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, e.db, &domain.WishlistItem{}, "user_id = ?", u.ID); n != 1 {
		t.Fatalf("items = %d, want 1", n)
	}
	list, _ := w.List(ctx, u.ID)
	if len(list) != 1 || list[0].ID != p.ID {
		t.Fatalf("list = %+v", list)
	}
	if err := w.Remove(ctx, u.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.Remove(ctx, u.ID, p.ID); err != nil {
		t.Fatalf("removing twice should be fine: %v", err)
	}
	if err := w.Add(ctx, u.ID, p.ID); err != nil {
		t.Fatalf("re-saving after removal: %v", err)
	}
}

func TestWishlist_RejectsMissingAndUnpublished(t *testing.T) {
	e := newEnv(t)
	w, _ := newWishlist(e)
	u := e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 3)
	if err := e.db.Model(&domain.Product{}).Where("id = ?", p.ID).Update("is_published", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := w.Add(context.Background(), u.ID, p.ID); !errors.Is(err, ErrProductNotFound) {
		t.Fatalf("unpublished: got %v", err)
	}
}

func TestWishlist_ListHidesProductsNoLongerOnSale(t *testing.T) {
	e := newEnv(t)
	w, _ := newWishlist(e)
	ctx := context.Background()
	u := e.user(t)
	m := e.merchant(t, 10, false)
	keep, drop := e.product(t, m, 100, 3), e.product(t, m, 100, 3)
	_ = w.Add(ctx, u.ID, keep.ID)
	_ = w.Add(ctx, u.ID, drop.ID)
	_ = e.db.Model(&domain.Product{}).Where("id = ?", drop.ID).Update("is_published", false).Error

	list, _ := w.List(ctx, u.ID)
	if len(list) != 1 || list[0].ID != keep.ID {
		t.Fatalf("list = %+v, want only the published product", list)
	}
	// Most recently saved first.
	other := e.product(t, m, 100, 3)
	_ = w.Add(ctx, u.ID, other.ID)
	list, _ = w.List(ctx, u.ID)
	if len(list) != 2 || list[0].ID != other.ID {
		t.Fatalf("order wrong: %+v", list)
	}
}

func TestWishlist_BackInStockAndPriceDropAlerts(t *testing.T) {
	e := newEnv(t)
	w, fs := newWishlist(e)
	ctx := context.Background()
	saver, bystander := e.user(t), e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 0)
	if err := e.db.Model(&domain.Product{}).Where("id = ?", p.ID).Update("stock", 0).Error; err != nil {
		t.Fatal(err)
	}
	p = reload[domain.Product](t, e.db, p.ID)
	_ = w.Add(ctx, saver.ID, p.ID)

	restocked := p
	restocked.Stock = 5
	w.NotifyProductChange(ctx, p, restocked)
	if got := fs.to(saver.ID); len(got) != 1 || got[0].Title != "Back in stock" {
		t.Fatalf("restock alert = %+v", got)
	}

	cheaper := restocked
	cheaper.Price = 80
	w.NotifyProductChange(ctx, restocked, cheaper)
	if got := fs.to(saver.ID); len(got) != 2 || got[1].Title != "Price drop" {
		t.Fatalf("price alert = %+v", got)
	}

	// Edits that aren't good news, or that happen while it's unpublished, stay quiet.
	pricier := cheaper
	pricier.Price = 200
	w.NotifyProductChange(ctx, cheaper, pricier)
	hidden := restocked
	hidden.IsPublished = false
	hidden.Stock = 9
	w.NotifyProductChange(ctx, p, hidden)
	if len(fs.to(saver.ID)) != 2 {
		t.Fatalf("unexpected extra alerts: %d", len(fs.to(saver.ID)))
	}
	if len(fs.to(bystander.ID)) != 0 {
		t.Fatalf("someone who didn't save it was alerted")
	}
}
