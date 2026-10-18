package service

import (
	"context"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

func grant(t *testing.T, e *env, g domain.AccessGrant) {
	t.Helper()
	if g.StartsAt.IsZero() {
		g.StartsAt = time.Now().Add(-time.Hour)
	}
	if g.Label == "" {
		g.Label = "test"
	}
	if err := e.db.Create(&g).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
}

func enrol(t *testing.T, e *env, u domain.User) (uni, dept, batch uuid.UUID) {
	t.Helper()
	uni, dept, batch = uuid.New(), uuid.New(), uuid.New()
	uid := u.ID
	s := domain.Student{UserID: &uid, UniversityID: uni, DepartmentID: dept, BatchID: batch}
	if err := e.db.Omit("Batch", "Department", "University", "Hall").Create(&s).Error; err != nil {
		t.Fatalf("create student: %v", err)
	}
	return uni, dept, batch
}

func TestGrantPerks_ScopesMatchThroughStudentRow(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	u, other := e.user(t), e.user(t)
	uni, dept, batch := enrol(t, e, u)
	_, _, otherBatch := enrol(t, e, other)

	grant(t, e, domain.AccessGrant{ScopeType: domain.GrantScopeUniversity, ScopeID: &uni, AdFree: true})
	grant(t, e, domain.AccessGrant{ScopeType: domain.GrantScopeBatch, ScopeID: &otherBatch, CoinFree: true})

	p, err := ResolveGrantPerks(e.db, u.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !p.AdFree || p.AdFreeUntil != nil || p.CoinFree {
		t.Fatalf("university grant: %+v", p)
	}

	grant(t, e, domain.AccessGrant{ScopeType: domain.GrantScopeDepartment, ScopeID: &dept, CoinFree: true})
	grant(t, e, domain.AccessGrant{ScopeType: domain.GrantScopeBatch, ScopeID: &batch, CoinFree: true})
	p, _ = ResolveGrantPerks(e.db, u.ID, now)
	if !p.CoinFree {
		t.Fatalf("department/batch grant: %+v", p)
	}

	p, _ = ResolveGrantPerks(e.db, other.ID, now)
	if p.AdFree || !p.CoinFree {
		t.Fatalf("other user should only get their batch grant: %+v", p)
	}
}

func TestGrantPerks_ExpiredFutureAndLatestWins(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	u := e.user(t)
	past, soon, later := now.Add(-time.Minute), now.Add(24*time.Hour), now.Add(72*time.Hour)

	grant(t, e, domain.AccessGrant{ScopeType: domain.GrantScopeAll, AdFree: true, StartsAt: now.Add(-48 * time.Hour), EndsAt: &past})
	grant(t, e, domain.AccessGrant{ScopeType: domain.GrantScopeAll, AdFree: true, StartsAt: now.Add(time.Hour)})
	p, _ := ResolveGrantPerks(e.db, u.ID, now)
	if p.AdFree {
		t.Fatalf("expired/future grants applied: %+v", p)
	}

	uid := u.ID
	grant(t, e, domain.AccessGrant{Label: "short", ScopeType: domain.GrantScopeAll, AdFree: true, EndsAt: &soon})
	grant(t, e, domain.AccessGrant{Label: "long", ScopeType: domain.GrantScopeUser, ScopeID: &uid, AdFree: true, EndsAt: &later})
	p, _ = ResolveGrantPerks(e.db, u.ID, now)
	if !p.AdFree || p.AdFreeUntil == nil || !p.AdFreeUntil.Equal(later) || p.Label != "long" {
		t.Fatalf("latest end should win: %+v", p)
	}
}

func TestGrantPerks_NewUserDays(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	fresh, old := e.user(t), e.user(t)
	e.db.Model(&domain.User{}).Where("id = ?", old.ID).Update("created_at", now.AddDate(0, 0, -10))

	grant(t, e, domain.AccessGrant{ScopeType: domain.GrantScopeAll, AdFree: true, NewUserDays: 7})

	p, _ := ResolveGrantPerks(e.db, fresh.ID, now)
	if !p.AdFree || p.AdFreeUntil == nil || p.AdFreeUntil.Before(now.AddDate(0, 0, 6)) {
		t.Fatalf("fresh user: %+v", p)
	}
	p, _ = ResolveGrantPerks(e.db, old.ID, now)
	if p.AdFree {
		t.Fatalf("10-day-old user got a 7-day grant: %+v", p)
	}
}

func TestEntitlements_GrantSwitchesFeaturesOnForFreeUser(t *testing.T) {
	e := newEnv(t)
	u := e.user(t)
	uid := u.ID
	grant(t, e, domain.AccessGrant{ScopeType: domain.GrantScopeUser, ScopeID: &uid, AdFree: true})

	got, err := NewEntitlementService(e.db).ForUser(context.Background(), u.ID)
	if err != nil || got.IsPro {
		t.Fatalf("%+v err=%v", got, err)
	}
	if !got.Features[domain.FeatureAdFree].Allowed || got.Features[domain.FeatureUnlimitedDownloads].Allowed {
		t.Fatalf("features: %+v", got.Features)
	}
}
