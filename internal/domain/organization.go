package domain

// Organization represents a company, institution, or employer where alumni work.
type Organization struct {
	Base
	Name    string `gorm:"size:100;not null;uniqueIndex" json:"name"`
	LogoURL string `json:"logo_url"`
	Website string `gorm:"size:255" json:"website"`
}
