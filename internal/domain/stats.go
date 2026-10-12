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
	TotalUsers          int64               `json:"total_users"`
	ActiveBanners       int64               `json:"active_banners"`
	TotalSubscriptions  int64               `json:"total_subscriptions"`
	TotalRevenue        float64             `json:"total_revenue"`
	UserTrend           string              `json:"user_trend"`
	BannerTrend         string              `json:"banner_trend"`
	SubTrend            string              `json:"sub_trend"`
	RevenueTrend        string              `json:"revenue_trend"`
	UserGrowth          []DailyCount        `json:"user_growth"`
	RecentSubscriptions []RecentSubscriber  `json:"recent_subscriptions"`
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

type StatsRepository interface {
	GetDashboardStats(ctx context.Context) (*DashboardStats, error)
	GetUserStats(ctx context.Context) (*UserStats, error)
}