package domain

import (
	"context"
)

type DailyCount struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type RecentSubscriber struct {
	UserID string  `json:"user_id"`
	Name   string  `json:"name"`
	Plan   string  `json:"plan"`
	Price  float64 `json:"price"`
	Status string  `json:"status"`
	Date   string  `json:"date"`
}

type DashboardStats struct {
	TotalUsers          int64              `json:"total_users"`
	ActiveBanners       int64              `json:"active_banners"`
	TotalSubscriptions  int64              `json:"total_subscriptions"`
	PaidSubscriptions   int64              `json:"paid_subscriptions"`
	FreeSubscriptions   int64              `json:"free_subscriptions"` // grant_reason = "admin_grant"
	TotalRevenue        float64            `json:"total_revenue"`
	UserTrend           string             `json:"user_trend"`
	BannerTrend         string             `json:"banner_trend"`
	SubTrend            string             `json:"sub_trend"`
	RevenueTrend        string             `json:"revenue_trend"`
	UserGrowth          []DailyCount       `json:"user_growth"`
	RecentSubscriptions []RecentSubscriber `json:"recent_subscriptions"`
}

type UserStats struct {
	Total      int64 `json:"total"`
	Active     int64 `json:"active"`
	Inactive   int64 `json:"inactive"`
	Verified   int64 `json:"verified"`
	Unverified int64 `json:"unverified"`
	Students   int64 `json:"students"`
	Teachers   int64 `json:"teachers"`
	Staff      int64 `json:"staff"`
}

// UserGrowth is the signup series behind the dashboard's User Growth chart.
// Granularity is "hour" (today), "day" (7d/30d), "month" (12m, lifetime) or
// "year" (lifetime spanning more than three years); Points are oldest first,
// zero-filled, and bucketed in Bangladesh time.
type UserGrowth struct {
	Range       string       `json:"range"`
	Granularity string       `json:"granularity"`
	Total       int64        `json:"total"`
	Points      []DailyCount `json:"points"`
}

// SubscriptionPoint is one bucket in a SubscriptionAnalytics series.
type SubscriptionPoint struct {
	Date    string  `json:"date"`
	Count   int64   `json:"count"`
	Revenue float64 `json:"revenue"`
}

// PlanBreakdown is the count/revenue share of one plan within the analytics range.
type PlanBreakdown struct {
	Plan    string  `json:"plan"`
	Count   int64   `json:"count"`
	Revenue float64 `json:"revenue"`
}

// SubscriptionAnalytics is the series behind the Subscriptions page's
// Analytics tab. Bucketing/zero-filling rules mirror UserGrowth; points are
// bucketed by subscription StartDate, oldest first.
type SubscriptionAnalytics struct {
	Range         string              `json:"range"`
	Granularity   string              `json:"granularity"`
	TotalCount    int64               `json:"total_count"`
	TotalRevenue  float64             `json:"total_revenue"`
	Points        []SubscriptionPoint `json:"points"`
	PlanBreakdown []PlanBreakdown     `json:"plan_breakdown"`
}

// ActiveUserStats is distinct-user counts derived from UserDevice.LastSeenAt,
// which devices bump via the /devices/heartbeat endpoint while foregrounded.
// "Now" is a short window (a few minutes) so it approximates concurrent
// online users; the others are standard rolling windows.
type ActiveUserStats struct {
	Now   int64 `json:"now"`
	Today int64 `json:"today"`
	Week  int64 `json:"week"`
	Month int64 `json:"month"`
}

type StatsRepository interface {
	GetDashboardStats(ctx context.Context) (*DashboardStats, error)
	GetUserGrowth(ctx context.Context, rng string) (*UserGrowth, error)
	GetUserStats(ctx context.Context) (*UserStats, error)
	GetSubscriptionAnalytics(ctx context.Context, rng string) (*SubscriptionAnalytics, error)
	GetActiveUserStats(ctx context.Context) (*ActiveUserStats, error)
}
