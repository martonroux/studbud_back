package testutil

import "sync"

// Email is one captured message.
type Email struct {
	To      string
	Subject string
	Body    string
}

// EmailRecorder is a test double that records emails instead of sending them.
type EmailRecorder struct {
	mu   sync.Mutex
	sent []Email
}

// NewEmailRecorder constructs an empty recorder.
func NewEmailRecorder() *EmailRecorder { return &EmailRecorder{} }

// Send appends the message to the captured list.
func (r *EmailRecorder) Send(to, subject, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, Email{To: to, Subject: subject, Body: body})
	return nil
}

// Sent returns a copy of all captured messages.
func (r *EmailRecorder) Sent() []Email {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Email, len(r.sent))
	copy(out, r.sent)
	return out
}

// Reset empties the captured list.
func (r *EmailRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = nil
}
