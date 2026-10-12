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
	if growth, err := r.GetUserGrowth(ctx, "7d"); err == nil {
		stats.UserGrowth = growth.Points
	}

	// Recent subscriptions
	if r.db.Migrator().HasTable("user_subscriptions") {
		type subRow struct {
			UserID    string
			FirstName string
			LastName  string
			Plan      string
			Price     float64
			StartDate time.Time
			EndDate   *time.Time
		}
		var rows []subRow
		r.db.WithContext(ctx).
			Table("user_subscriptions").
			Select("user_subscriptions.user_id, users.first_name, users.last_name, user_subscriptions.plan, user_subscriptions.price, user_subscriptions.start_date, user_subscriptions.end_date").
			Joins("JOIN users ON users.id = user_subscriptions.user_id").
			Order("user_subscriptions.start_date DESC").
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
				Price:  row.Price,
				Status: status,
				Date:   row.StartDate.Format("02/01/2006"),
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

// GetUserStats computes all counts in a single table scan (COUNT ... FILTER)
// rather than issuing one query per metric, so the stat row stays cheap even
// as the users table grows.
func (r *statsRepository) GetUserStats(ctx context.Context) (*domain.UserStats, error) {
	var stats domain.UserStats
	err := r.db.WithContext(ctx).Model(&domain.User{}).Select(`
		COUNT(*) AS total,
		COUNT(*) FILTER (WHERE is_active = true) AS active,
		COUNT(*) FILTER (WHERE is_active = false) AS inactive,
		COUNT(*) FILTER (WHERE is_verified = true) AS verified,
		COUNT(*) FILTER (WHERE is_verified = false) AS unverified,
		COUNT(*) FILTER (WHERE role = 'student') AS students,
		COUNT(*) FILTER (WHERE role = 'teacher') AS teachers,
		COUNT(*) FILTER (WHERE role = 'staff') AS staff
	`).Scan(&stats).Error
	return &stats, err
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

// growthTZ is the zone signups are bucketed in. Bangladesh has no DST, so a
// fixed offset avoids depending on tzdata being present in the container;
// the SQL side uses the matching IANA name, which Postgres always ships.
var growthTZ = time.FixedZone("Asia/Dhaka", 6*60*60)

const growthTZName = "Asia/Dhaka"

// GetUserGrowth returns zero-filled signup counts for one of the dashboard
// ranges: "today" (hourly), "7d"/"30d" (daily), "12m" (monthly) or
// "lifetime" (monthly, or yearly once it spans more than three years).
// Unknown ranges fall back to "7d".
func (r *statsRepository) GetUserGrowth(ctx context.Context, rng string) (*domain.UserGrowth, error) {
	now := time.Now().In(growthTZ)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, growthTZ)
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, growthTZ)

	var (
		granularity string
		start       time.Time
	)
	switch rng {
	case "today":
		granularity, start = "hour", today
	case "30d":
		granularity, start = "day", today.AddDate(0, 0, -29)
	case "12m":
		granularity, start = "month", thisMonth.AddDate(0, -11, 0)
	case "lifetime":
		var first *time.Time
		if err := r.db.WithContext(ctx).Model(&domain.User{}).Select("MIN(created_at)").Scan(&first).Error; err != nil {
			return nil, err
		}
		start = thisMonth
		if first != nil {
			f := first.In(growthTZ)
			start = time.Date(f.Year(), f.Month(), 1, 0, 0, 0, 0, growthTZ)
		}
		granularity = "month"
		if months := (now.Year()-start.Year())*12 + int(now.Month()-start.Month()) + 1; months > 36 {
			granularity = "year"
			start = time.Date(start.Year(), 1, 1, 0, 0, 0, 0, growthTZ)
		}
	default:
		rng = "7d"
		granularity, start = "day", today.AddDate(0, 0, -6)
	}

	keyFormat := map[string]string{"hour": "YYYY-MM-DD\"T\"HH24", "day": "YYYY-MM-DD", "month": "YYYY-MM", "year": "YYYY"}[granularity]
	goFormat := map[string]string{"hour": "2006-01-02T15", "day": "2006-01-02", "month": "2006-01", "year": "2006"}[granularity]

	// TO_CHAR gives a plain string key; scanning DATE()/DATE_TRUNC() straight
	// into a string yields an RFC3339 timestamp that never matched the
	// Go-side keys, which is why the chart used to render all zeros.
	bucket := fmt.Sprintf("TO_CHAR(DATE_TRUNC('%s', created_at AT TIME ZONE '%s'), '%s')", granularity, growthTZName, keyFormat)
	type bucketCount struct {
		Bucket string
		Count  int64
	}
	var rows []bucketCount
	if err := r.db.WithContext(ctx).
		Model(&domain.User{}).
		Select(bucket+" AS bucket, COUNT(*) AS count").
		Where("created_at >= ?", start).
		Group("bucket").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Bucket] = row.Count
	}

	growth := &domain.UserGrowth{Range: rng, Granularity: granularity, Points: []domain.DailyCount{}}
	step := func(t time.Time) time.Time {
		switch granularity {
		case "hour":
			return t.Add(time.Hour)
		case "day":
			return t.AddDate(0, 0, 1)
		case "month":
			return t.AddDate(0, 1, 0)
		default:
			return t.AddDate(1, 0, 0)
		}
	}
	// Today stops at the current hour; the other ranges include the
	// (partial) current day/month/year.
	end := now
	if granularity == "hour" {
		end = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, growthTZ)
	}
	for t := start; !t.After(end); t = step(t) {
		key := t.Format(goFormat)
		growth.Points = append(growth.Points, domain.DailyCount{Date: key, Count: counts[key]})
		growth.Total += counts[key]
	}
	return growth, nil
}
