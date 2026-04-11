package domain

// ResourceType defines the type of content.
type ResourceType string

const (
	TypeNote     ResourceType = "note"
	TypeQuestion ResourceType = "question"
	TypeSyllabus ResourceType = "syllabus"
	TypeBook     ResourceType = "book"
)
