package domain

// CommissionPolicy is the single-row marketplace fee setting: a limited-time
// promotional rate for new sellers, after which each merchant's own
// CommissionRate applies. Percentages, like Merchant.CommissionRate.
type CommissionPolicy struct {
	Base
	PromoActive bool    `gorm:"not null;default:false" json:"promo_active"`
	PromoRate   float64 `gorm:"not null;default:0" json:"promo_rate"`  // % charged during the promo
	PromoDays   int     `gorm:"not null;default:60" json:"promo_days"` // days from approval
}
