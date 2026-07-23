package domain

// MarketplaceCategory groups products in the Campus Marketplace (e.g., Stationery,
// Books, Electronics). Managed via generic CRUD, same pattern as CourseCategory.
type MarketplaceCategory struct {
	Base
	Name        string `gorm:"size:100;not null" json:"name"`
	ImageURL    string `gorm:"size:500" json:"image_url"`
	Description string `gorm:"type:text" json:"description"`
	Index       int    `gorm:"default:0" json:"index"` // sort order
}
