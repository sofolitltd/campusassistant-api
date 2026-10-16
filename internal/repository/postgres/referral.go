package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Referral plumbing. These are plain functions over a *gorm.DB (not a
// repository struct) because GrantReferralReward must run inside the caller's
// verification transaction — pass the tx in.

func newReferralCode() (string, error) {
	alphabet := domain.ReferralCodeAlphabet
	b := make([]byte, domain.ReferralCodeLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

// NormalizeReferralCode upper-cases and trims user input.
func NormalizeReferralCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// EnsureReferralCode returns the user's code, issuing one on first use.
func EnsureReferralCode(ctx context.Context, db *gorm.DB, userID uuid.UUID) (string, error) {
	var user domain.User
	if err := db.WithContext(ctx).Select("id", "referral_code").First(&user, "id = ?", userID).Error; err != nil {
		return "", err
	}
	if user.ReferralCode != nil && *user.ReferralCode != "" {
		return *user.ReferralCode, nil
	}
	for attempt := 0; attempt < 5; attempt++ {
		code, err := newReferralCode()
		if err != nil {
			return "", err
		}
		// Only set when still empty so two concurrent first-opens agree.
		res := db.WithContext(ctx).Model(&domain.User{}).
			Where("id = ? AND referral_code IS NULL", userID).
			Update("referral_code", code)
		if res.Error == nil {
			if res.RowsAffected == 1 {
				return code, nil
			}
			// Someone else issued it first — read theirs back.
			if err := db.WithContext(ctx).Select("referral_code").First(&user, "id = ?", userID).Error; err != nil {
				return "", err
			}
			if user.ReferralCode != nil {
				return *user.ReferralCode, nil
			}
			continue
		}
		if !errors.Is(res.Error, gorm.ErrDuplicatedKey) && !strings.Contains(res.Error.Error(), "duplicate key") {
			return "", res.Error
		}
		// Collision with another user's code; retry with a fresh one.
	}
	return "", errors.New("could not allocate a referral code")
}

// FindReferrerByCode resolves a code to its owner's ID.
func FindReferrerByCode(ctx context.Context, db *gorm.DB, code string) (uuid.UUID, error) {
	code = NormalizeReferralCode(code)
	if code == "" {
		return uuid.Nil, domain.ErrReferralInvalidCode
	}
	var owner domain.User
	if err := db.WithContext(ctx).Select("id").First(&owner, "referral_code = ?", code).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return uuid.Nil, domain.ErrReferralInvalidCode
		}
		return uuid.Nil, err
	}
	return owner.ID, nil
}

// ApplyReferralCode links an existing account to a referrer after signup.
// If the account is already verified the reward is paid immediately, since
// the verification moment that normally triggers it has passed.
func ApplyReferralCode(ctx context.Context, db *gorm.DB, userID uuid.UUID, code string) error {
	referrerID, err := FindReferrerByCode(ctx, db, code)
	if err != nil {
		return err
	}
	if referrerID == userID {
		return domain.ErrReferralSelf
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user domain.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", userID).Error; err != nil {
			return err
		}
		if user.ReferredByID != nil {
			return domain.ErrReferralAlreadyApplied
		}
		if time.Since(user.CreatedAt) > domain.ReferralApplyWindow {
			return domain.ErrReferralWindowClosed
		}
		var referrer domain.User
		if err := tx.Select("id", "referred_by_id").First(&referrer, "id = ?", referrerID).Error; err != nil {
			return err
		}
		if referrer.ReferredByID != nil && *referrer.ReferredByID == userID {
			return domain.ErrReferralCircular
		}
		if err := tx.Model(&domain.User{}).Where("id = ?", userID).
			Update("referred_by_id", referrerID).Error; err != nil {
			return err
		}
		if user.IsVerified {
			return GrantReferralReward(tx, userID)
		}
		return nil
	})
}

// GrantReferralReward pays the referrer and the referee once the referee is
// verified. Safe to call on every verification path: it no-ops for users
// without a referrer and for users already paid out.
func GrantReferralReward(tx *gorm.DB, refereeID uuid.UUID) error {
	now := time.Now()
	// Claim the payout atomically so concurrent verifications can't double-pay.
	res := tx.Model(&domain.User{}).
		Where("id = ? AND referred_by_id IS NOT NULL AND referral_rewarded_at IS NULL", refereeID).
		Update("referral_rewarded_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	var referee domain.User
	if err := tx.Select("id", "referred_by_id").First(&referee, "id = ?", refereeID).Error; err != nil {
		return err
	}
	if err := CreditReward(tx, *referee.ReferredByID, domain.ReferrerRewardAmount,
		domain.ReferrerRewardDescription, &refereeID); err != nil {
		return err
	}
	return CreditReward(tx, refereeID, domain.RefereeRewardAmount,
		domain.RefereeRewardDescription, referee.ReferredByID)
}

// CreditReward adds amount to a user's balance and records the transaction.
// resourceID is stored on the transaction for traceability (for referrals,
// the other party's user ID).
func CreditReward(tx *gorm.DB, userID uuid.UUID, amount int, description string, resourceID *uuid.UUID) error {
	repo := NewRewardRepository(tx)
	ctx := tx.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := repo.GetOrCreate(ctx, userID); err != nil {
		return err
	}
	if err := tx.Model(&domain.UserReward{}).
		Where("user_id = ?", userID).
		Updates(map[string]interface{}{
			"balance":         gorm.Expr("balance + ?", amount),
			"lifetime_earned": gorm.Expr("lifetime_earned + ?", amount),
		}).Error; err != nil {
		return err
	}
	var reward domain.UserReward
	if err := tx.Select("balance").First(&reward, "user_id = ?", userID).Error; err != nil {
		return err
	}
	return repo.CreateTransaction(ctx, &domain.RewardTransaction{
		UserID:       userID,
		Type:         domain.RewardEarn,
		Amount:       amount,
		BalanceAfter: reward.Balance,
		ResourceID:   resourceID,
		Description:  description,
	})
}

// GetReferralSummary powers the app's "Invite & Earn" screen.
func GetReferralSummary(ctx context.Context, db *gorm.DB, userID uuid.UUID) (*domain.ReferralSummary, error) {
	code, err := EnsureReferralCode(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	var user domain.User
	if err := db.WithContext(ctx).Select("id", "referred_by_id", "created_at").First(&user, "id = ?", userID).Error; err != nil {
		return nil, err
	}
	s := &domain.ReferralSummary{
		Code:           code,
		ReferrerReward: domain.ReferrerRewardAmount,
		RefereeReward:  domain.RefereeRewardAmount,
		HasReferrer:    user.ReferredByID != nil,
		CanApplyCode:   user.ReferredByID == nil && time.Since(user.CreatedAt) <= domain.ReferralApplyWindow,
	}
	q := db.WithContext(ctx).Model(&domain.User{}).Where("referred_by_id = ?", userID)
	if err := q.Count(&s.InvitedCount).Error; err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Model(&domain.User{}).
		Where("referred_by_id = ? AND referral_rewarded_at IS NOT NULL", userID).
		Count(&s.VerifiedCount).Error; err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Model(&domain.RewardTransaction{}).
		Where("user_id = ? AND description = ?", userID, domain.ReferrerRewardDescription).
		Select("COALESCE(SUM(amount), 0)").Scan(&s.PointsEarned).Error; err != nil {
		return nil, err
	}
	return s, nil
}

// GetReferralAdminStats powers the admin Referrals page. The leaderboard
// ranks by verified referrals (the ones that paid out), invited as tiebreak.
// days limits the leaderboard and window counts; 0 means all time.
func GetReferralAdminStats(ctx context.Context, db *gorm.DB, days int) (*domain.ReferralAdminStats, error) {
	db = db.WithContext(ctx)
	var since time.Time // zero = all time
	if days > 0 {
		since = time.Now().AddDate(0, 0, -days)
	}

	s := &domain.ReferralAdminStats{
		Days:        days,
		Leaderboard: []domain.ReferralLeaderboardEntry{},
		Recent:      []domain.RecentReferral{},
	}
	t := &s.Totals
	users := func() *gorm.DB { return db.Model(&domain.User{}) }

	if err := users().Where("referral_code IS NOT NULL").Count(&t.CodesIssued).Error; err != nil {
		return nil, err
	}
	if err := users().Where("referred_by_id IS NOT NULL").Count(&t.ReferredUsers).Error; err != nil {
		return nil, err
	}
	if err := users().Where("referral_rewarded_at IS NOT NULL").Count(&t.VerifiedReferrals).Error; err != nil {
		return nil, err
	}
	t.PendingReferrals = t.ReferredUsers - t.VerifiedReferrals
	if err := db.Model(&domain.RewardTransaction{}).
		Where("description IN ?", []string{domain.ReferrerRewardDescription, domain.RefereeRewardDescription}).
		Select("COALESCE(SUM(amount), 0)").Scan(&t.PointsPaid).Error; err != nil {
		return nil, err
	}
	if err := users().Where("referred_by_id IS NOT NULL AND created_at >= ?", since).
		Count(&t.ReferredInWindow).Error; err != nil {
		return nil, err
	}
	if err := users().Where("referral_rewarded_at >= ?", since).
		Count(&t.VerifiedInWindow).Error; err != nil {
		return nil, err
	}

	if err := db.Raw(`
		SELECT r.id::text AS user_id, r.first_name, r.last_name, r.email,
		       COALESCE(u.name, '') AS university_name,
		       COALESCE(r.referral_code, '') AS referral_code,
		       COUNT(f.id) AS invited,
		       COUNT(f.referral_rewarded_at) AS verified
		FROM users f
		JOIN users r ON r.id = f.referred_by_id AND r.deleted_at IS NULL
		LEFT JOIN universities u ON u.id = r.university_id
		WHERE f.deleted_at IS NULL AND f.created_at >= ?
		GROUP BY r.id, r.first_name, r.last_name, r.email, u.name, r.referral_code
		ORDER BY verified DESC, invited DESC
		LIMIT 20`, since).Scan(&s.Leaderboard).Error; err != nil {
		return nil, err
	}

	if err := db.Raw(`
		SELECT f.id::text AS referee_id, f.first_name AS referee_first_name,
		       f.last_name AS referee_last_name, f.email AS referee_email,
		       r.first_name AS referrer_first_name, r.last_name AS referrer_last_name,
		       COALESCE(r.referral_code, '') AS referrer_code,
		       f.created_at AS joined_at, f.referral_rewarded_at AS rewarded_at
		FROM users f
		JOIN users r ON r.id = f.referred_by_id
		WHERE f.deleted_at IS NULL
		ORDER BY f.created_at DESC
		LIMIT 50`).Scan(&s.Recent).Error; err != nil {
		return nil, err
	}
	return s, nil
}
