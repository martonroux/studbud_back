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

// StatsResponse is returned from GET /chapter-stats.
type StatsResponse struct {
	TotalCards     int     `json:"totalCards"`     // TotalCards is the total number of cards in the chapter
	GoodCount      int     `json:"goodCount"`      // GoodCount is cards whose last review was good (2)
	OkCount        int     `json:"okCount"`        // OkCount is cards whose last review was ok (1)
	BadCount       int     `json:"badCount"`       // BadCount is cards whose last review was bad (0)
	NewCount       int     `json:"newCount"`       // NewCount is cards not yet reviewed (-1)
	CardsStudied   int     `json:"cardsStudied"`   // CardsStudied is TotalCards - NewCount
	MasteryPercent float64 `json:"masteryPercent"` // MasteryPercent weights good=1, ok=0.5 against TotalCards
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
