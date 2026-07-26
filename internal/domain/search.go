package domain

import (
	"context"
)

// SearchResults is the top-level payload for GET /search, grouping hits by
// entity type. Each entry in Results is the exact same raw JSON shape as
// that entity type's own list endpoint returns per item (e.g. the "resource"
// slice is []Resource, same as GET /resources) — so the client can run the
// SAME fromJson parsers/card widgets against search results unmodified.
// interface{} (rather than a generic type param) is required here because a
// single map fans out across ten unrelated concrete slice types.
type SearchResults struct {
	Query   string                 `json:"query"`
	Total   int                    `json:"total"`
	Results map[string]interface{} `json:"results"`
}

// SearchRepository is dedicated (not generic CRUD) because it fans a single
// query out across many unrelated tables, same reasoning as StatsRepository.
type SearchRepository interface {
	Search(ctx context.Context, query string, types []string, universityID, departmentID string, limitPerType int) (*SearchResults, error)
}
