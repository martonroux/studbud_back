package friendship

import "time"

// Friendship represents a friend relationship row.
type Friendship struct {
	ID         int64     `json:"id"`          // ID is the friendship primary key
	SenderID   int64     `json:"sender_id"`   // SenderID is the requester user id
	ReceiverID int64     `json:"receiver_id"` // ReceiverID is the target user id
	Status     string    `json:"status"`      // Status is pending|accepted|declined
	CreatedAt  time.Time `json:"created_at"`  // CreatedAt is request time
	UpdatedAt  time.Time `json:"updated_at"`  // UpdatedAt is last status change
}

// RequestInput contains the target user for a new friend request.
type RequestInput struct {
	ReceiverID int64 `json:"receiver_id"` // ReceiverID is the user being friended
}
