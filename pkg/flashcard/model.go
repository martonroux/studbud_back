package flashcard

import "time"

// Flashcard is a question/answer card belonging to a subject.
type Flashcard struct {
	ID         int64      `json:"id"`          // ID is the card primary key
	SubjectID  int64      `json:"subject_id"`  // SubjectID is the owning subject
	ChapterID  *int64     `json:"chapter_id"`  // ChapterID is the optional owning chapter
	Title      string     `json:"title"`       // Title is an optional short title
	Question   string     `json:"question"`    // Question is the front-side text
	Answer     string     `json:"answer"`      // Answer is the back-side text
	ImageID    *string    `json:"image_id"`    // ImageID is an optional attached image id
	Source     string     `json:"source"`      // Source is 'manual' or 'ai'
	DueAt      *time.Time `json:"due_at"`      // DueAt is the next review timestamp
	LastResult int        `json:"last_result"` // LastResult is -1 (never) or 0..2 (training outcome)
	LastUsed   *time.Time `json:"last_used"`   // LastUsed is the last training timestamp
	CreatedAt  time.Time  `json:"created_at"`  // CreatedAt is creation time
	UpdatedAt  time.Time  `json:"updated_at"`  // UpdatedAt is last update time
}

// CreateInput is the payload to create a flashcard.
type CreateInput struct {
	SubjectID int64   `json:"subject_id"` // SubjectID is the owning subject (required)
	ChapterID *int64  `json:"chapter_id"` // ChapterID is optional
	Title     string  `json:"title"`      // Title is optional
	Question  string  `json:"question"`   // Question is required
	Answer    string  `json:"answer"`     // Answer is required
	ImageID   *string `json:"image_id"`   // ImageID is optional
	Source    string  `json:"source"`     // Source is 'manual' or 'ai' (default manual)
}

// UpdateInput patches a flashcard.
type UpdateInput struct {
	ChapterID *int64  `json:"chapter_id"` // ChapterID updates the parent chapter
	Title     *string `json:"title"`      // Title updates the title
	Question  *string `json:"question"`   // Question updates the front
	Answer    *string `json:"answer"`     // Answer updates the back
	ImageID   *string `json:"image_id"`   // ImageID updates the attached image
}

// ReviewInput records a training outcome.
type ReviewInput struct {
	Result int `json:"result"` // Result is 0 (fail), 1 (partial), or 2 (success)
}
