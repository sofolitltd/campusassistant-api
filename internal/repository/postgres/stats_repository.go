package postgres

import (
	"campusassistant-api/internal/domain"
	"context"

	"gorm.io/gorm"
)

type statsRepository struct {
	db *gorm.DB
}

func NewStatsRepository(db *gorm.DB) domain.StatsRepository {
	return &statsRepository{db: db}
}

func (r *statsRepository) GetDashboardStats(ctx context.Context) (*domain.DashboardStats, error) {
	var stats domain.DashboardStats

	// Total Users
	r.db.WithContext(ctx).Model(&domain.User{}).Count(&stats.TotalUsers)

	// Active Banners
	r.db.WithContext(ctx).Model(&domain.Banner{}).Where("is_active = ?", true).Count(&stats.ActiveBanners)

	// Total Subscriptions
	// Check if table exists first to avoid crash if migrations haven't run
	if r.db.Migrator().HasTable("user_subscriptions") {
		r.db.WithContext(ctx).Table("user_subscriptions").Count(&stats.TotalSubscriptions)
		
		var revenue float64 = 0 // Placeholder until revenue tracking is finalized
		stats.TotalRevenue = revenue
	}

	// Mocking trends for now
	stats.UserTrend = "+12%"
	stats.BannerTrend = "+2"
	stats.SubTrend = "+18%"
	stats.RevenueTrend = "+8.4%"

	return &stats, nil
}
