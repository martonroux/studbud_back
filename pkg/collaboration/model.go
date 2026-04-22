package collaboration

import "time"

// Collaborator represents a user granted explicit access to a subject.
type Collaborator struct {
	ID        int64     `json:"id"`         // ID is the collaborator row primary key
	SubjectID int64     `json:"subject_id"` // SubjectID is the subject being shared
	UserID    int64     `json:"user_id"`    // UserID is the grantee user id
	Role      string    `json:"role"`       // Role is viewer|editor
	CreatedAt time.Time `json:"created_at"` // CreatedAt is when the collaboration was granted
}

// InviteLink represents an opaque shareable token that grants access when redeemed.
type InviteLink struct {
	Token     string     `json:"token"`             // Token is the opaque hex string identifying the invite
	SubjectID int64      `json:"subject_id"`        // SubjectID is the subject the invite grants access to
	Role      string     `json:"role"`              // Role is the access level conferred by the invite
	ExpiresAt *time.Time `json:"expires_at"`        // ExpiresAt is the optional invite expiry time (nil = no expiry)
	CreatedAt time.Time  `json:"created_at"`        // CreatedAt is when the invite was issued
}

// CreateInviteInput captures the fields required to mint a new invite link.
type CreateInviteInput struct {
	SubjectID int64  `json:"subject_id"` // SubjectID is the subject to share
	Role      string `json:"role"`       // Role is the role the invite will grant (viewer|editor)
	TTLHours  int    `json:"ttl_hours"`  // TTLHours is the optional lifetime in hours (<=0 means no expiry)
}
