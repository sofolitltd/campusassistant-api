package service

import (
	"context"
	"sort"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// shipSpeedWindow is how many of a merchant's most recent shipped orders
// feed their average shipping time.
const shipSpeedWindow = 50

// InsightsService answers a seller's "how am I doing?" questions.
type InsightsService struct {
	db *gorm.DB
}

func NewInsightsService(db *gorm.DB) *InsightsService { return &InsightsService{db: db} }

type DailyPoint struct {
	Date    string `json:"date"` // YYYY-MM-DD, server-local day
	Orders  int    `json:"orders"`
	Revenue int    `json:"revenue"`
}

type TopProduct struct {
	ProductID uuid.UUID `json:"product_id"`
	Title     string    `json:"title"`
	Units     int       `json:"units"`
	Revenue   int       `json:"revenue"`
	Views     int       `json:"views"`
}

type MerchantStats struct {
	Days         int          `json:"days"`
	Orders       int          `json:"orders"`
	Delivered    int          `json:"delivered_orders"`
	Units        int          `json:"units"`
	GrossRevenue int          `json:"gross_revenue"`
	NetRevenue   int          `json:"net_revenue"` // after commission
	Views        int          `json:"views"`       // lifetime product views
	AvgOrder     int          `json:"avg_order_value"`
	RatingAvg    float64      `json:"rating_avg"`
	RatingCount  int          `json:"rating_count"`
	AvgShipHours float64      `json:"avg_ship_hours"`
	ShippedCount int          `json:"shipped_count"`
	LowStock     int          `json:"low_stock_products"`
	Daily        []DailyPoint `json:"daily"`
	TopProducts  []TopProduct `json:"top_products"`
}

type statRow struct {
	OrderID        uuid.UUID
	ProductID      uuid.UUID
	ProductTitle   string
	Quantity       int
	UnitPrice      int
	CommissionRate float64
	CreatedAt      time.Time
	Status         domain.OrderStatus
}

// Stats summarises the last `days` days. An order counts once it is live
// (paid, processing, shipped or delivered); unpaid and cancelled orders are
// not sales.
func (s *InsightsService) Stats(ctx context.Context, merchantID uuid.UUID, days int) (*MerchantStats, error) {
	if days < 1 || days > 365 {
		days = 30
	}
	since := time.Now().AddDate(0, 0, -days)

	var rows []statRow
	err := s.db.WithContext(ctx).Table("order_items").
		Select(`order_items.order_id AS order_id, order_items.product_id AS product_id, order_items.product_title AS product_title,
			order_items.quantity AS quantity, order_items.unit_price AS unit_price,
			order_items.commission_rate_snapshot AS commission_rate, orders.created_at AS created_at, orders.status AS status`).
		Joins("JOIN orders ON orders.id = order_items.order_id").
		Where("order_items.merchant_id = ? AND orders.created_at >= ? AND orders.status IN ? AND orders.deleted_at IS NULL AND order_items.deleted_at IS NULL",
			merchantID, since, []domain.OrderStatus{domain.OrderStatusPaid, domain.OrderStatusProcessing, domain.OrderStatusShipped, domain.OrderStatusDelivered}).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := &MerchantStats{Days: days, Daily: []DailyPoint{}, TopProducts: []TopProduct{}}
	orders := map[uuid.UUID]bool{}
	delivered := map[uuid.UUID]bool{}
	dayOrders := map[string]map[uuid.UUID]bool{}
	dayRev := map[string]int{}
	type agg struct {
		title        string
		units, value int
	}
	byProduct := map[uuid.UUID]*agg{}
	net := 0.0
	for _, r := range rows {
		line := r.Quantity * r.UnitPrice
		orders[r.OrderID] = true
		if r.Status == domain.OrderStatusDelivered {
			delivered[r.OrderID] = true
		}
		out.Units += r.Quantity
		out.GrossRevenue += line
		net += float64(line) * (1 - r.CommissionRate/100)
		day := r.CreatedAt.Format("2006-01-02")
		if dayOrders[day] == nil {
			dayOrders[day] = map[uuid.UUID]bool{}
		}
		dayOrders[day][r.OrderID] = true
		dayRev[day] += line
		a := byProduct[r.ProductID]
		if a == nil {
			a = &agg{title: r.ProductTitle}
			byProduct[r.ProductID] = a
		}
		a.units += r.Quantity
		a.value += line
	}
	out.Orders, out.Delivered = len(orders), len(delivered)
	out.NetRevenue = int(net + 0.5)
	if out.Orders > 0 {
		out.AvgOrder = out.GrossRevenue / out.Orders
	}

	// One point per calendar day in the window, zero-filled, so a chart
	// doesn't have to guess at gaps.
	for d := 0; d < days; d++ {
		day := time.Now().AddDate(0, 0, -(days-1)+d).Format("2006-01-02")
		out.Daily = append(out.Daily, DailyPoint{Date: day, Orders: len(dayOrders[day]), Revenue: dayRev[day]})
	}

	views := map[uuid.UUID]int{}
	var products []domain.Product
	if err := s.db.WithContext(ctx).Select("id, view_count, stock").Where("merchant_id = ?", merchantID).Find(&products).Error; err != nil {
		return nil, err
	}
	for _, p := range products {
		views[p.ID] = p.ViewCount
		out.Views += p.ViewCount
		if p.Stock <= 3 {
			out.LowStock++
		}
	}
	for id, a := range byProduct {
		out.TopProducts = append(out.TopProducts, TopProduct{ProductID: id, Title: a.title, Units: a.units, Revenue: a.value, Views: views[id]})
	}
	sort.Slice(out.TopProducts, func(i, j int) bool {
		if out.TopProducts[i].Revenue != out.TopProducts[j].Revenue {
			return out.TopProducts[i].Revenue > out.TopProducts[j].Revenue
		}
		return out.TopProducts[i].Title < out.TopProducts[j].Title
	})
	if len(out.TopProducts) > 5 {
		out.TopProducts = out.TopProducts[:5]
	}

	var m domain.Merchant
	if err := s.db.WithContext(ctx).Select("rating_avg, rating_count, avg_ship_hours, shipped_count").First(&m, "id = ?", merchantID).Error; err == nil {
		out.RatingAvg, out.RatingCount, out.AvgShipHours, out.ShippedCount = m.RatingAvg, m.RatingCount, m.AvgShipHours, m.ShippedCount
	}
	return out, nil
}

// RecordShipSpeed refreshes a merchant's average time-to-ship from their most
// recent shipped orders. Hours are measured from the order going live (paid,
// or processing for cash on delivery) to the shipped event.
func (s *InsightsService) RecordShipSpeed(ctx context.Context, merchantID uuid.UUID) error {
	var orderIDs []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&domain.OrderItem{}).Where("merchant_id = ?", merchantID).Distinct().Pluck("order_id", &orderIDs).Error; err != nil {
		return err
	}
	if len(orderIDs) == 0 {
		return nil
	}
	var events []domain.OrderEvent
	if err := s.db.WithContext(ctx).
		Where("order_id IN ? AND status IN ?", orderIDs, []domain.OrderStatus{domain.OrderStatusPaid, domain.OrderStatusProcessing, domain.OrderStatusShipped}).
		Order("created_at asc").Find(&events).Error; err != nil {
		return err
	}
	type span struct{ live, shipped time.Time }
	spans := map[uuid.UUID]*span{}
	for _, e := range events {
		sp := spans[e.OrderID]
		if sp == nil {
			sp = &span{}
			spans[e.OrderID] = sp
		}
		switch e.Status {
		case domain.OrderStatusShipped:
			if sp.shipped.IsZero() {
				sp.shipped = e.CreatedAt
			}
		default:
			if sp.live.IsZero() {
				sp.live = e.CreatedAt
			}
		}
	}
	var done []*span
	for _, sp := range spans {
		if !sp.live.IsZero() && !sp.shipped.IsZero() && !sp.shipped.Before(sp.live) {
			done = append(done, sp)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].shipped.After(done[j].shipped) })
	if len(done) > shipSpeedWindow {
		done = done[:shipSpeedWindow]
	}
	if len(done) == 0 {
		return nil
	}
	total := 0.0
	for _, sp := range done {
		total += sp.shipped.Sub(sp.live).Hours()
	}
	return s.db.WithContext(ctx).Exec("UPDATE merchants SET avg_ship_hours = ?, shipped_count = ? WHERE id = ?",
		total/float64(len(done)), len(done), merchantID).Error
}

// MarketplaceMetrics are the health numbers an operator watches: is supply
// growing, are buyers coming back, do orders end happily?
type MarketplaceMetrics struct {
	ListedProducts     int64   `json:"listed_products"`
	ActiveSellers      int64   `json:"active_sellers"` // approved, with a published product
	SellersWithSales   int64   `json:"sellers_with_sales_30d"`
	Orders30d          int64   `json:"orders_30d"`
	GMV30d             int64   `json:"gmv_30d"`
	Buyers30d          int64   `json:"buyers_30d"`
	RepeatBuyerRate    float64 `json:"repeat_buyer_rate"` // % of all-time buyers with 2+ orders
	Delivered30d       int64   `json:"delivered_30d"`
	Cancelled30d       int64   `json:"cancelled_30d"`
	Refunded30d        int64   `json:"refunded_30d"`
	SmoothDeliveryRate float64 `json:"smooth_delivery_rate"` // % of finished orders delivered and not refunded
	ReviewCount        int64   `json:"review_count"`
	AvgRating          float64 `json:"avg_rating"`
}

var liveStatuses = []domain.OrderStatus{domain.OrderStatusPaid, domain.OrderStatusProcessing, domain.OrderStatusShipped, domain.OrderStatusDelivered}

func (s *InsightsService) Metrics(ctx context.Context) (*MarketplaceMetrics, error) {
	db := s.db.WithContext(ctx)
	since := time.Now().AddDate(0, 0, -30)
	m := &MarketplaceMetrics{}
	count := func(dst *int64, q *gorm.DB) error { return q.Count(dst).Error }

	if err := count(&m.ListedProducts, db.Model(&domain.Product{}).Where("is_published = ?", true)); err != nil {
		return nil, err
	}
	if err := count(&m.ActiveSellers, db.Model(&domain.Merchant{}).
		Where("status = ? AND is_platform = ?", domain.MerchantStatusApproved, false).
		Where("EXISTS (SELECT 1 FROM products p WHERE p.merchant_id = merchants.id AND p.is_published = ? AND p.deleted_at IS NULL)", true)); err != nil {
		return nil, err
	}
	if err := count(&m.SellersWithSales, db.Table("order_items").
		Joins("JOIN orders ON orders.id = order_items.order_id").
		Where("orders.created_at >= ? AND orders.status IN ? AND orders.deleted_at IS NULL", since, liveStatuses).
		Distinct("order_items.merchant_id")); err != nil {
		return nil, err
	}

	live30 := db.Model(&domain.Order{}).Where("created_at >= ? AND status IN ?", since, liveStatuses)
	if err := count(&m.Orders30d, live30.Session(&gorm.Session{})); err != nil {
		return nil, err
	}
	var gmv struct{ Total int64 }
	if err := live30.Session(&gorm.Session{}).Select("COALESCE(SUM(total_amount), 0) AS total").Scan(&gmv).Error; err != nil {
		return nil, err
	}
	m.GMV30d = gmv.Total
	if err := count(&m.Buyers30d, live30.Session(&gorm.Session{}).Distinct("buyer_id")); err != nil {
		return nil, err
	}

	// Repeat rate over everyone who has ever bought.
	var perBuyer []struct{ N int }
	if err := db.Model(&domain.Order{}).Select("COUNT(*) AS n").Where("status IN ?", liveStatuses).Group("buyer_id").Scan(&perBuyer).Error; err != nil {
		return nil, err
	}
	repeat := 0
	for _, b := range perBuyer {
		if b.N >= 2 {
			repeat++
		}
	}
	if len(perBuyer) > 0 {
		m.RepeatBuyerRate = float64(repeat) / float64(len(perBuyer)) * 100
	}

	if err := count(&m.Delivered30d, db.Model(&domain.Order{}).Where("created_at >= ? AND status = ?", since, domain.OrderStatusDelivered)); err != nil {
		return nil, err
	}
	if err := count(&m.Cancelled30d, db.Model(&domain.Order{}).Where("created_at >= ? AND status = ?", since, domain.OrderStatusCancelled)); err != nil {
		return nil, err
	}
	if err := count(&m.Refunded30d, db.Model(&domain.Refund{}).Where("created_at >= ? AND kind = ?", since, "order")); err != nil {
		return nil, err
	}
	if finished := m.Delivered30d + m.Cancelled30d; finished > 0 {
		m.SmoothDeliveryRate = float64(m.Delivered30d) / float64(finished) * 100
	}

	var rv struct {
		N   int64
		Avg float64
	}
	if err := db.Model(&domain.ProductReview{}).Where("is_hidden = ?", false).
		Select("COUNT(*) AS n, COALESCE(AVG(rating), 0) AS avg").Scan(&rv).Error; err != nil {
		return nil, err
	}
	m.ReviewCount, m.AvgRating = rv.N, rv.Avg
	return m, nil
}
