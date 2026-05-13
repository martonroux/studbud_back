package quiz

import (
	"encoding/json"
	"time"
)

// Kind is the high-level mode of the quiz.
type Kind string

const (
	// KindSpecific draws questions from a specific subset of the user's flashcards.
	KindSpecific Kind = "specific"
	// KindGlobal draws questions from general subject knowledge (no card grounding).
	KindGlobal Kind = "global"
)

// Source records who/what generated the quiz row.
type Source string

const (
	// SourceUser is a user-initiated standalone quiz.
	SourceUser Source = "user"
	// SourcePlan is a revision-plan-materialised quiz (Spec D2).
	SourcePlan Source = "plan"
	// SourceSharedCopy is a clone produced by accepting a share link (Spec D3).
	SourceSharedCopy Source = "shared_copy"
)

// QuestionType enumerates the supported quiz question shapes.
type QuestionType string

const (
	// QTypeMultiChoice is a multi-choice question with N options and one correct index.
	QTypeMultiChoice QuestionType = "multi_choice"
	// QTypeTrueFalse is a true/false assertion.
	QTypeTrueFalse QuestionType = "true_false"
	// QTypeFillBlank is a fuzzy-matched fill-in-the-blank.
	QTypeFillBlank QuestionType = "fill_blank"
)

// CardFilter narrows the eligible flashcard pool for Kind="specific".
type CardFilter string

const (
	// FilterAll uses every flashcard in the chapter/subject.
	FilterAll CardFilter = "all"
	// FilterBadOK includes only flashcards whose last result was Bad or OK.
	FilterBadOK CardFilter = "bad_ok"
	// FilterDue uses cards whose dueHeuristic fires today.
	FilterDue CardFilter = "due"
)

// GenerateRequest is the input to Service.Generate.
type GenerateRequest struct {
	UserID      int64          // UserID is the authenticated caller
	SubjectID   int64          // SubjectID anchors the prompt + pool
	ChapterID   *int64         // ChapterID narrows the pool; nil = whole subject
	Kind        Kind           // Kind is "specific" or "global"
	Size        int            // Size is the requested question count (5/10/15/20)
	Types       []QuestionType // Types lists allowed question shapes
	CardFilter  CardFilter     // CardFilter narrows the pool (specific only)
	PlanContext *PlanContext   // PlanContext is non-nil iff invoked from a plan slot (Spec D2)
}

// PlanContext carries plan-slot coordinates for plan-materialised quizzes (Spec D2).
type PlanContext struct {
	PlanID    int64  // PlanID is the revision_plans row id
	PlanDate  string // PlanDate is the YYYY-MM-DD bucket
	SlotIndex int    // SlotIndex addresses the slot within the day
}

// GenerateResult is the output of Service.Generate.
type GenerateResult struct {
	QuizID        int64 // QuizID is the new (or existing-idempotent) quiz row id
	QuestionCount int   // QuestionCount mirrors quizzes.question_count
	Kind          Kind  // Kind mirrors the request's kind
}

// Quiz is the persisted projection of a quizzes row.
type Quiz struct {
	ID            int64     // ID is the BIGSERIAL primary key
	UserID        int64     // UserID owns the quiz
	SubjectID     int64     // SubjectID anchors the quiz to a subject
	ChapterID     *int64    // ChapterID is non-nil for chapter-scoped quizzes
	Kind          Kind      // Kind is "specific" or "global"
	Source        Source    // Source records origin (user|plan|shared_copy)
	SourcePlanID  *int64    // SourcePlanID is non-nil when source='plan'
	QuestionCount int       // QuestionCount mirrors quizzes.question_count
	Model         string    // Model is the AI model identifier
	CreatedAt     time.Time // CreatedAt is the insertion timestamp
}

// Question is the persisted projection of a quiz_questions row.
// CorrectJSON is loaded server-side only and never returned to the play API.
type Question struct {
	ID              int64           // ID is the BIGSERIAL primary key
	QuizID          int64           // QuizID is the owning quiz id
	Ordinal         int             // Ordinal is the 1-based question order
	Type            QuestionType    // Type is the question shape
	Stem            string          // Stem is the question text
	Options         json.RawMessage // Options is the MCQ options array (null for non-MCQ)
	CorrectJSON     json.RawMessage // CorrectJSON is the correct-answer payload (server-only)
	Explanation     string          // Explanation is the optional rationale shown after answering
	ReferencedFcIDs []int64         // ReferencedFcIDs are flashcard ids this question draws on
}

// PublicQuestion is the play-facing projection — strips CorrectJSON.
type PublicQuestion struct {
	ID      int64           `json:"id"`
	Ordinal int             `json:"ordinal"`
	Type    QuestionType    `json:"type"`
	Stem    string          `json:"stem"`
	Options json.RawMessage `json:"options,omitempty"`
}

// AttemptState enumerates the lifecycle of a play attempt.
type AttemptState string

const (
	// StateInProgress marks an attempt currently being played.
	StateInProgress AttemptState = "in_progress"
	// StateCompleted marks an attempt that has been finished.
	StateCompleted AttemptState = "completed"
	// StateAbandoned marks an attempt the user gave up on.
	StateAbandoned AttemptState = "abandoned"
)

// Attempt is the persisted projection of a quiz_attempts row.
type Attempt struct {
	ID           int64        `json:"id"`           // ID is the BIGSERIAL primary key
	QuizID       int64        `json:"quizId"`       // QuizID is the owning quiz id
	UserID       int64        `json:"userId"`       // UserID owns the attempt
	State        AttemptState `json:"state"`        // State is the lifecycle marker
	StartedAt    time.Time    `json:"startedAt"`    // StartedAt is the attempt creation timestamp
	CompletedAt  *time.Time   `json:"completedAt"`  // CompletedAt is the completion timestamp (nil while in-progress)
	CorrectCount int          `json:"correctCount"` // CorrectCount counts answered-correctly questions
	TotalCount   int          `json:"totalCount"`   // TotalCount is the question count of the parent quiz
	ScorePct     *int         `json:"scorePct"`     // ScorePct is the integer percentage (nil while in-progress)
	PlanID       *int64       `json:"planId"`       // PlanID is non-nil for plan-materialised attempts (Spec D2)
	PlanDate     *string      `json:"planDate"`     // PlanDate is the YYYY-MM-DD bucket for plan-sourced attempts
}

// Progress is the {answered, total} pill shown during play.
type Progress struct {
	Answered int `json:"answered"` // Answered is the count of submitted answers
	Total    int `json:"total"`    // Total is the question count of the attempt
}
