package domain

import (
	"errors"
	"time"
)

// Referral rewards are paid once, when the referred user becomes verified
// (admin-approved document verification or claiming a profile with a code).
// Paying on signup would let anyone farm points with throwaway accounts.
const (
	ReferrerRewardAmount = 10
	RefereeRewardAmount  = 5

	ReferrerRewardDescription = "Referral reward"
	RefereeRewardDescription  = "Joined with a referral code"

	// A code can only be applied this long after signup; otherwise any
	// long-standing user could swap codes with a friend for free points.
	ReferralApplyWindow = 7 * 24 * time.Hour

	// Unambiguous characters only (no 0/O, 1/I/L) — codes get typed by hand
	// from a photo of a card.
	ReferralCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	ReferralCodeLength   = 6
)

var (
	ErrReferralInvalidCode    = errors.New("invalid referral code")
	ErrReferralSelf           = errors.New("you can't use your own referral code")
	ErrReferralAlreadyApplied = errors.New("a referral code is already applied to your account")
	ErrReferralWindowClosed   = errors.New("referral codes can only be applied within 7 days of signing up")
	ErrReferralCircular       = errors.New("you can't use the code of someone you referred")
)

type ReferralSummary struct {
	Code           string `json:"code"`
	InvitedCount   int64  `json:"invited_count"`
	VerifiedCount  int64  `json:"verified_count"`
	PointsEarned   int64  `json:"points_earned"`
	ReferrerReward int    `json:"referrer_reward"`
	RefereeReward  int    `json:"referee_reward"`
	HasReferrer    bool   `json:"has_referrer"`
	CanApplyCode   bool   `json:"can_apply_code"`
}

// Admin-facing referral analytics (GET /referrals/stats).
type ReferralTotals struct {
	CodesIssued       int64 `json:"codes_issued"`
	ReferredUsers     int64 `json:"referred_users"`
	VerifiedReferrals int64 `json:"verified_referrals"`
	PendingReferrals  int64 `json:"pending_referrals"`
	PointsPaid        int64 `json:"points_paid"`
	// Same counts restricted to the requested window (?days=N).
	ReferredInWindow int64 `json:"referred_in_window"`
	VerifiedInWindow int64 `json:"verified_in_window"`
}

type ReferralLeaderboardEntry struct {
	UserID         string `json:"user_id"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Email          string `json:"email"`
	UniversityName string `json:"university_name"`
	ReferralCode   string `json:"referral_code"`
	Invited        int64  `json:"invited"`
	Verified       int64  `json:"verified"`
}

type RecentReferral struct {
	RefereeID         string     `json:"referee_id"`
	RefereeFirstName  string     `json:"referee_first_name"`
	RefereeLastName   string     `json:"referee_last_name"`
	RefereeEmail      string     `json:"referee_email"`
	ReferrerFirstName string     `json:"referrer_first_name"`
	ReferrerLastName  string     `json:"referrer_last_name"`
	ReferrerCode      string     `json:"referrer_code"`
	JoinedAt          time.Time  `json:"joined_at"`
	RewardedAt        *time.Time `json:"rewarded_at"`
}

type ReferralAdminStats struct {
	Days        int                        `json:"days"`
	Totals      ReferralTotals             `json:"totals"`
	Leaderboard []ReferralLeaderboardEntry `json:"leaderboard"`
	Recent      []RecentReferral           `json:"recent"`
}
