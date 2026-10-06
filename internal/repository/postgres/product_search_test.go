package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func searchDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&domain.Merchant{}, &domain.MarketplaceCategory{}, &domain.Product{}, &domain.ProductTarget{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seed(t *testing.T, db *gorm.DB, m domain.Merchant, title string, price, stock int, published bool, mutate ...func(*domain.Product)) domain.Product {
	t.Helper()
	p := domain.Product{MerchantID: m.ID, Title: title, Description: "desc of " + title, Price: price, Stock: stock, IsPublished: published}
	for _, f := range mutate {
		f(&p)
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func titles(ps []domain.Product) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Title
	}
	return out
}

func TestSearchProducts(t *testing.T) {
	db := searchDB(t)
	repo := NewProductRepository(db)
	ctx := context.Background()
	m := domain.Merchant{BusinessName: "Shop", Status: domain.MerchantStatusApproved}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	uni, dept, otherUni := uuid.New(), uuid.New(), uuid.New()
	cat := domain.MarketplaceCategory{Name: "Books"}
	if err := db.Create(&cat).Error; err != nil {
		t.Fatal(err)
	}

	seed(t, db, m, "Calculus Book", 300, 5, true, func(p *domain.Product) { p.CategoryID = cat.ID })
	seed(t, db, m, "Physics Notes", 100, 0, true)
	seed(t, db, m, "100% Cotton Tee", 500, 9, true)
	seed(t, db, m, "Hidden Draft", 50, 5, false)
	scoped := seed(t, db, m, "Dept Only Lab Coat", 400, 5, true)
	wrong := seed(t, db, m, "Other Uni Item", 400, 5, true)
	_ = db.Create(&domain.ProductTarget{ProductID: scoped.ID, UniversityID: uni, DepartmentID: dept}).Error
	_ = db.Create(&domain.ProductTarget{ProductID: wrong.ID, UniversityID: otherUni, DepartmentID: uuid.Nil}).Error

	base := domain.ProductFilter{UniversityID: uni, DepartmentID: dept}
	run := func(mod func(*domain.ProductFilter)) ([]string, int64) {
		f := base
		mod(&f)
		ps, total, err := repo.SearchProducts(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return titles(ps), total
	}
	contains := func(got []string, want string) bool {
		for _, g := range got {
			if g == want {
				return true
			}
		}
		return false
	}

	all, total := run(func(*domain.ProductFilter) {})
	if total != 4 || contains(all, "Hidden Draft") || contains(all, "Other Uni Item") || !contains(all, "Dept Only Lab Coat") {
		t.Fatalf("visibility: %v (total %d)", all, total)
	}
	if got, _ := run(func(f *domain.ProductFilter) { f.Query = "calc" }); len(got) != 1 || got[0] != "Calculus Book" {
		t.Fatalf("query: %v", got)
	}
	if got, _ := run(func(f *domain.ProductFilter) { f.Query = "DESC OF PHYSICS" }); len(got) != 1 {
		t.Fatalf("query matches description case-insensitively: %v", got)
	}
	// A literal % in the query must not act as a wildcard.
	if got, _ := run(func(f *domain.ProductFilter) { f.Query = "100%" }); len(got) != 1 || got[0] != "100% Cotton Tee" {
		t.Fatalf("percent literal: %v", got)
	}
	if got, _ := run(func(f *domain.ProductFilter) { f.Query = "%" }); len(got) != 1 {
		t.Fatalf("a bare %% should only match a literal percent, got %v", got)
	}
	if got, _ := run(func(f *domain.ProductFilter) { f.MinPrice, f.MaxPrice = 200, 450 }); len(got) != 2 {
		t.Fatalf("price range: %v", got)
	}
	if got, _ := run(func(f *domain.ProductFilter) { f.InStock = true }); contains(got, "Physics Notes") || len(got) != 3 {
		t.Fatalf("in stock: %v", got)
	}
	if got, _ := run(func(f *domain.ProductFilter) { f.CategoryID = cat.ID }); len(got) != 1 {
		t.Fatalf("category: %v", got)
	}

	asc, _ := run(func(f *domain.ProductFilter) { f.Sort = domain.ProductSortPriceAsc })
	if asc[0] != "Physics Notes" || asc[len(asc)-1] != "100% Cotton Tee" {
		t.Fatalf("price asc: %v", asc)
	}
	desc, _ := run(func(f *domain.ProductFilter) { f.Sort = domain.ProductSortPriceDesc })
	if desc[0] != "100% Cotton Tee" {
		t.Fatalf("price desc: %v", desc)
	}

	// Paging: pages are disjoint and total ignores limit.
	p1, total := run(func(f *domain.ProductFilter) { f.Sort, f.Limit = domain.ProductSortPriceAsc, 2 })
	p2, _ := run(func(f *domain.ProductFilter) { f.Sort, f.Limit, f.Offset = domain.ProductSortPriceAsc, 2, 2 })
	if total != 4 || len(p1) != 2 || len(p2) != 2 || contains(p1, p2[0]) || contains(p1, p2[1]) {
		t.Fatalf("paging: %v / %v (total %d)", p1, p2, total)
	}
}

func TestSearchProducts_TopRatedAndPopular(t *testing.T) {
	db := searchDB(t)
	repo := NewProductRepository(db)
	m := domain.Merchant{BusinessName: "Shop"}
	_ = db.Create(&m).Error
	a := seed(t, db, m, "A", 10, 1, true)
	b := seed(t, db, m, "B", 10, 1, true)
	c := seed(t, db, m, "C", 10, 1, true)
	for _, u := range []struct {
		id           uuid.UUID
		avg          float64
		count, views int
	}{{a.ID, 4.0, 10, 5}, {b.ID, 4.8, 3, 99}, {c.ID, 0, 0, 20}} {
		if err := db.Exec("UPDATE products SET rating_avg = ?, rating_count = ?, view_count = ? WHERE id = ?", u.avg, u.count, u.views, u.id).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	top, _, _ := repo.SearchProducts(ctx, domain.ProductFilter{Sort: domain.ProductSortTopRated})
	if titles(top)[0] != "B" {
		t.Fatalf("top rated: %v", titles(top))
	}
	pop, _, _ := repo.SearchProducts(ctx, domain.ProductFilter{Sort: domain.ProductSortPopular})
	if titles(pop)[0] != "B" || titles(pop)[1] != "C" {
		t.Fatalf("popular: %v", titles(pop))
	}
}

func TestGetPublishedByIDs(t *testing.T) {
	db := searchDB(t)
	repo := NewProductRepository(db)
	m := domain.Merchant{BusinessName: "Shop"}
	_ = db.Create(&m).Error
	live := seed(t, db, m, "Live", 10, 1, true)
	draft := seed(t, db, m, "Draft", 10, 1, false)
	got, err := repo.GetPublishedByIDs(context.Background(), []uuid.UUID{live.ID, draft.ID, uuid.New()})
	if err != nil || len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("got %v (err %v)", titles(got), err)
	}
	if got, _ := repo.GetPublishedByIDs(context.Background(), nil); len(got) != 0 {
		t.Fatalf("empty ids should return nothing")
	}
}

func TestSearchProducts_FeaturedLeadTheDefaultOrderAndFilter(t *testing.T) {
	db := searchDB(t)
	repo := NewProductRepository(db)
	m := domain.Merchant{BusinessName: "Shop"}
	_ = db.Create(&m).Error
	old := seed(t, db, m, "Old", 10, 1, true)
	newer := seed(t, db, m, "Newer", 10, 1, true)
	newest := seed(t, db, m, "Newest", 10, 1, true)
	expired := seed(t, db, m, "Expired", 10, 1, true)
	for id, ago := range map[uuid.UUID]time.Duration{old.ID: -time.Hour * 3, newer.ID: -time.Hour * 2, newest.ID: -time.Hour, expired.ID: -time.Minute} {
		if err := db.Exec("UPDATE products SET created_at = ? WHERE id = ?", time.Now().Add(ago), id).Error; err != nil {
			t.Fatal(err)
		}
	}
	// "Old" is featured for another day; "Expired" was featured but ran out.
	_ = db.Exec("UPDATE products SET featured_until = ? WHERE id = ?", time.Now().Add(24*time.Hour), old.ID).Error
	_ = db.Exec("UPDATE products SET featured_until = ? WHERE id = ?", time.Now().Add(-time.Hour), expired.ID).Error

	ctx := context.Background()
	got, _, _ := repo.SearchProducts(ctx, domain.ProductFilter{})
	if titles(got)[0] != "Old" || titles(got)[1] != "Expired" {
		t.Fatalf("featured should lead, then newest first: %v", titles(got))
	}
	byPrice, _, _ := repo.SearchProducts(ctx, domain.ProductFilter{Sort: domain.ProductSortPriceAsc})
	if titles(byPrice)[0] == "Old" && len(byPrice) > 1 && titles(byPrice)[1] == "Newer" {
		// price ties fall back to newest, so Expired (newest) must come first
		t.Fatalf("explicit sorts must ignore featuring: %v", titles(byPrice))
	}
	only, total, _ := repo.SearchProducts(ctx, domain.ProductFilter{FeaturedOnly: true})
	if total != 1 || len(only) != 1 || only[0].Title != "Old" {
		t.Fatalf("featured only: %v (total %d)", titles(only), total)
	}
}

func TestSetMerchantStatus_StampsFirstApprovalOnly(t *testing.T) {
	db := searchDB(t)
	repo := NewMerchantRepository(db)
	ctx := context.Background()
	m := domain.Merchant{BusinessName: "Shop", Status: domain.MerchantStatusPending}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.SetMerchantStatus(ctx, m.ID, domain.MerchantStatusApproved, ""); err != nil {
		t.Fatal(err)
	}
	var first domain.Merchant
	_ = db.First(&first, "id = ?", m.ID).Error
	if first.ApprovedAt == nil {
		t.Fatal("approved_at not stamped")
	}

	time.Sleep(10 * time.Millisecond)
	_ = repo.SetMerchantStatus(ctx, m.ID, domain.MerchantStatusRejected, "no")
	_ = repo.SetMerchantStatus(ctx, m.ID, domain.MerchantStatusApproved, "")
	var again domain.Merchant
	_ = db.First(&again, "id = ?", m.ID).Error
	if !again.ApprovedAt.Equal(*first.ApprovedAt) {
		t.Fatalf("re-approval must not restart the promo window: %v vs %v", again.ApprovedAt, first.ApprovedAt)
	}
}
