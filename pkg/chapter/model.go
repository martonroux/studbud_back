package chapter

import "time"

// Chapter groups flashcards inside a subject.
type Chapter struct {
	ID        int64     `json:"id"`         // ID is the chapter primary key
	SubjectID int64     `json:"subject_id"` // SubjectID is the owning subject
	Title     string    `json:"title"`      // Title is the chapter title
	Position  int       `json:"position"`   // Position orders chapters within a subject
	CreatedAt time.Time `json:"created_at"` // CreatedAt is the creation timestamp
	UpdatedAt time.Time `json:"updated_at"` // UpdatedAt is the last update timestamp
}

// CreateInput is the payload to create a chapter.
type CreateInput struct {
	SubjectID int64  `json:"subject_id"` // SubjectID is the parent subject
	Title     string `json:"title"`      // Title is the chapter title
}

// UpdateInput patches a chapter (nil = unchanged).
type UpdateInput struct {
	Title    *string `json:"title"`    // Title updates the title when non-nil
	Position *int    `json:"position"` // Position updates ordering when non-nil
}
