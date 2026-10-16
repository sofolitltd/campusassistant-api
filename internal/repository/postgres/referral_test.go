package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func referralDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&domain.User{}, &domain.UserReward{}, &domain.RewardTransaction{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedUser(t *testing.T, db *gorm.DB, email string, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	u := domain.User{Email: email, FirstName: "F", LastName: "L", IsActive: true}
	for _, f := range mutate {
		f(&u)
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func balance(t *testing.T, db *gorm.DB, userID uuid.UUID) int {
	t.Helper()
	var r domain.UserReward
	if err := db.First(&r, "user_id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return -1
		}
		t.Fatal(err)
	}
	return r.Balance
}

func TestReferralCodeIsStableAndResolves(t *testing.T) {
	db := referralDB(t)
	ctx := context.Background()
	u := seedUser(t, db, "a@x.com")

	code, err := EnsureReferralCode(ctx, db, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != domain.ReferralCodeLength {
		t.Fatalf("code %q: want length %d", code, domain.ReferralCodeLength)
	}
	again, _ := EnsureReferralCode(ctx, db, u.ID)
	if again != code {
		t.Fatalf("code changed on second call: %q → %q", code, again)
	}
	// Lower-case, padded input still resolves.
	owner, err := FindReferrerByCode(ctx, db, "  "+strings.ToLower(code)+" ")
	if err != nil || owner != u.ID {
		t.Fatalf("FindReferrerByCode = %v, %v; want %v", owner, err, u.ID)
	}
	if _, err := FindReferrerByCode(ctx, db, "NOPE99"); !errors.Is(err, domain.ErrReferralInvalidCode) {
		t.Fatalf("unknown code: got %v, want ErrReferralInvalidCode", err)
	}
}

func TestApplyReferralCodeRules(t *testing.T) {
	db := referralDB(t)
	ctx := context.Background()
	referrer := seedUser(t, db, "referrer@x.com")
	code, _ := EnsureReferralCode(ctx, db, referrer.ID)

	if err := ApplyReferralCode(ctx, db, referrer.ID, code); !errors.Is(err, domain.ErrReferralSelf) {
		t.Fatalf("own code: got %v, want ErrReferralSelf", err)
	}

	old := seedUser(t, db, "old@x.com")
	db.Model(&domain.User{}).Where("id = ?", old.ID).Update("created_at", time.Now().Add(-domain.ReferralApplyWindow-time.Hour))
	if err := ApplyReferralCode(ctx, db, old.ID, code); !errors.Is(err, domain.ErrReferralWindowClosed) {
		t.Fatalf("old account: got %v, want ErrReferralWindowClosed", err)
	}

	friend := seedUser(t, db, "friend@x.com")
	if err := ApplyReferralCode(ctx, db, friend.ID, code); err != nil {
		t.Fatal(err)
	}
	if err := ApplyReferralCode(ctx, db, friend.ID, code); !errors.Is(err, domain.ErrReferralAlreadyApplied) {
		t.Fatalf("second apply: got %v, want ErrReferralAlreadyApplied", err)
	}

	// The referrer can't then use their referee's code back.
	friendCode, _ := EnsureReferralCode(ctx, db, friend.ID)
	if err := ApplyReferralCode(ctx, db, referrer.ID, friendCode); !errors.Is(err, domain.ErrReferralCircular) {
		t.Fatalf("circular: got %v, want ErrReferralCircular", err)
	}

	// Unverified referee: linked, but nobody is paid yet.
	if b := balance(t, db, referrer.ID); b != -1 {
		t.Fatalf("referrer paid before verification (balance %d)", b)
	}
}

func TestGrantReferralRewardPaysOnce(t *testing.T) {
	db := referralDB(t)
	referrer := seedUser(t, db, "referrer@x.com")
	referee := seedUser(t, db, "referee@x.com", func(u *domain.User) { u.ReferredByID = &referrer.ID })
	loner := seedUser(t, db, "loner@x.com")

	for i := 0; i < 3; i++ { // every verification path may call it; only the first pays
		if err := db.Transaction(func(tx *gorm.DB) error { return GrantReferralReward(tx, referee.ID) }); err != nil {
			t.Fatal(err)
		}
	}
	// GetOrCreate seeds new balances at 50.
	if got, want := balance(t, db, referrer.ID), 50+domain.ReferrerRewardAmount; got != want {
		t.Fatalf("referrer balance = %d, want %d", got, want)
	}
	if got, want := balance(t, db, referee.ID), 50+domain.RefereeRewardAmount; got != want {
		t.Fatalf("referee balance = %d, want %d", got, want)
	}
	var txCount int64
	// Excludes the two "Welcome bonus" rows GetOrCreate writes for new wallets.
	db.Model(&domain.RewardTransaction{}).
		Where("description IN ?", []string{domain.ReferrerRewardDescription, domain.RefereeRewardDescription}).
		Count(&txCount)
	if txCount != 2 {
		t.Fatalf("referral reward transactions = %d, want 2", txCount)
	}

	// No referrer → no-op.
	if err := db.Transaction(func(tx *gorm.DB) error { return GrantReferralReward(tx, loner.ID) }); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, db, loner.ID); b != -1 {
		t.Fatalf("user without referrer was paid (balance %d)", b)
	}
}

func TestApplyReferralCodeToVerifiedUserPaysImmediately(t *testing.T) {
	db := referralDB(t)
	ctx := context.Background()
	referrer := seedUser(t, db, "referrer@x.com")
	code, _ := EnsureReferralCode(ctx, db, referrer.ID)
	verified := seedUser(t, db, "verified@x.com", func(u *domain.User) { u.IsVerified = true })

	if err := ApplyReferralCode(ctx, db, verified.ID, code); err != nil {
		t.Fatal(err)
	}
	if got, want := balance(t, db, referrer.ID), 50+domain.ReferrerRewardAmount; got != want {
		t.Fatalf("referrer balance = %d, want %d", got, want)
	}
}
