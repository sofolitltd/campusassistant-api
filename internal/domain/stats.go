package domain

import "context"

type DashboardStats struct {
	TotalUsers         int64   `json:"total_users"`
	ActiveBanners      int64   `json:"active_banners"`
	TotalSubscriptions int64   `json:"total_subscriptions"`
	TotalRevenue       float64 `json:"total_revenue"`
	UserTrend          string  `json:"user_trend"`
	BannerTrend        string  `json:"banner_trend"`
	SubTrend           string  `json:"sub_trend"`
	RevenueTrend       string  `json:"revenue_trend"`
}

type StatsRepository interface {
	GetDashboardStats(ctx context.Context) (*DashboardStats, error)
}
