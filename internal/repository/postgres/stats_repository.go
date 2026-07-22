package postgres

import (
	"campusassistant-api/internal/domain"
	"context"
	"fmt"
	"time"

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

	r.db.WithContext(ctx).Model(&domain.User{}).Count(&stats.TotalUsers)
	r.db.WithContext(ctx).Model(&domain.Banner{}).Where("is_active = ?", true).Count(&stats.ActiveBanners)

	if r.db.Migrator().HasTable("user_subscriptions") {
		r.db.WithContext(ctx).Model(&domain.UserSubscription{}).Count(&stats.TotalSubscriptions)

		// Revenue: sum of plan prices for all subscriptions
		type revenueResult struct {
			Total float64
		}
		var rev revenueResult
		r.db.WithContext(ctx).
			Table("user_subscriptions").
			Select("COALESCE(SUM(subscription_plans.price), 0) as total").
			Joins("LEFT JOIN subscription_plans ON subscription_plans.id = user_subscriptions.plan_id").
			Where("user_subscriptions.deleted_at IS NULL").
			Scan(&rev)
		stats.TotalRevenue = rev.Total
	}

	// User growth: daily signups for last 7 days
	type dateCount struct {
		Date  string
		Count int64
	}
	var rawGrowth []dateCount
	sevenDaysAgo := time.Now().AddDate(0, 0, -6)
	r.db.WithContext(ctx).
		Model(&domain.User{}).
		Select("DATE(created_at) as date, COUNT(*) as count").
		Where("created_at >= ?", sevenDaysAgo).
		Group("DATE(created_at)").
		Order("DATE(created_at) ASC").
		Find(&rawGrowth)

	growthMap := make(map[string]int64)
	for _, g := range rawGrowth {
		growthMap[g.Date] = g.Count
	}
	for i := 6; i >= 0; i-- {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		stats.UserGrowth = append(stats.UserGrowth, domain.DailyCount{
			Date:  date,
			Count: growthMap[date],
		})
	}

	// Recent subscriptions
	if r.db.Migrator().HasTable("user_subscriptions") {
		type subRow struct {
			UserID    string
			FirstName string
			LastName  string
			Plan      string
			StartDate time.Time
			EndDate   *time.Time // nil = Lifetime plan, never expires
		}
		var rows []subRow
		r.db.WithContext(ctx).
			Table("user_subscriptions").
			Select("user_subscriptions.user_id, users.first_name, users.last_name, user_subscriptions.plan, user_subscriptions.start_date, user_subscriptions.end_date").
			Joins("JOIN users ON users.id = user_subscriptions.user_id").
			Order("user_subscriptions.created_at DESC").
			Limit(5).
			Find(&rows)

		for _, row := range rows {
			status := "Active"
			if row.EndDate != nil && time.Now().After(*row.EndDate) {
				status = "Expired"
			}
			stats.RecentSubscriptions = append(stats.RecentSubscriptions, domain.RecentSubscriber{
				UserID: row.UserID,
				Name:   row.FirstName + " " + row.LastName,
				Plan:   row.Plan,
				Status: status,
				Date:   row.StartDate.Format("2006-01-02"),
			})
		}
	}

	// Trends: compare this period vs previous period
	now := time.Now()
	prevStart := now.AddDate(0, 0, -13)
	prevEnd := now.AddDate(0, 0, -7)
	currStart := now.AddDate(0, 0, -6)

	var prevUserCount, currUserCount int64
	r.db.WithContext(ctx).Model(&domain.User{}).Where("created_at BETWEEN ? AND ?", prevStart, prevEnd).Count(&prevUserCount)
	r.db.WithContext(ctx).Model(&domain.User{}).Where("created_at >= ?", currStart).Count(&currUserCount)
	stats.UserTrend = trendStr(prevUserCount, currUserCount)

	var prevSubCount, currSubCount int64
	if r.db.Migrator().HasTable("user_subscriptions") {
		r.db.WithContext(ctx).Table("user_subscriptions").Where("created_at BETWEEN ? AND ?", prevStart, prevEnd).Count(&prevSubCount)
		r.db.WithContext(ctx).Table("user_subscriptions").Where("created_at >= ?", currStart).Count(&currSubCount)
	}
	stats.SubTrend = trendStr(prevSubCount, currSubCount)

	stats.BannerTrend = fmt.Sprintf("+%d", stats.ActiveBanners)
	stats.RevenueTrend = "+0%"

	return &stats, nil
}

func trendStr(prev, curr int64) string {
	if prev == 0 {
		return fmt.Sprintf("+%d", curr)
	}
	pct := float64(curr-prev) / float64(prev) * 100
	if pct >= 0 {
		return fmt.Sprintf("+%.1f%%", pct)
	}
	return fmt.Sprintf("%.1f%%", pct)
}
