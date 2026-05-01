package exam

import "time"

// Exam represents an exam owned by a user, scoped to one subject and one date.
type Exam struct {
	ID             int64     `json:"id"`                       // ID is the BIGSERIAL primary key
	UserID         int64     `json:"userId"`                   // UserID owns the exam
	SubjectID      int64     `json:"subjectId"`                // SubjectID identifies the subject the exam targets
	Title          string    `json:"title"`                    // Title is the human-readable label
	Notes          string    `json:"notes"`                    // Notes is an optional free-form description
	ExamDate       time.Time `json:"examDate"`                 // ExamDate is the day the exam is scheduled for
	AnnalesImageID *string   `json:"annalesImageId,omitempty"` // AnnalesImageID points at an uploaded annales PDF (nil when none)
	CreatedAt      time.Time `json:"createdAt"`                // CreatedAt is the row creation timestamp
	UpdatedAt      time.Time `json:"updatedAt"`                // UpdatedAt is the last-modified timestamp
}

// Input is the create/update payload accepted by the exam service.
// All fields are required on Create; on Update they overwrite the stored row.
type Input struct {
	SubjectID      int64     // SubjectID is the target subject; required, not editable post-create
	Title          string    // Title is the human-readable exam label
	Notes          string    // Notes is an optional free-form description
	ExamDate       time.Time // ExamDate is the scheduled date (must be today or later on create)
	AnnalesImageID *string   // AnnalesImageID is the optional annales PDF reference
}
