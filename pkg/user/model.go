package user

import "time"

// User mirrors the users table row.
type User struct {
	ID            int64     // ID is the primary key
	Username      string    // Username is the unique login identifier
	Email         string    // Email is unique, used for login + verification
	EmailVerified bool      // EmailVerified gates access to most routes
	CreatedAt     time.Time // CreatedAt is the account creation timestamp
	IsAdmin       bool      // IsAdmin flags admin-only route access
}

// RegisterInput is the JSON body for POST /user-register.
type RegisterInput struct {
	Username string `json:"username"` // Username is the desired unique username
	Email    string `json:"email"`    // Email is the account email address
	Password string `json:"password"` // Password is the plaintext password (min 8 chars)
}

// LoginInput is the JSON body for POST /user-login.
type LoginInput struct {
	Identifier string `json:"identifier"` // Identifier is username or email
	Password   string `json:"password"`   // Password is the plaintext password to verify
}

// TokenResponse is returned on register/login success.
type TokenResponse struct {
	Token string `json:"token"` // Token is the signed JWT for subsequent requests
}

// UserStatsResponse is returned from /user-stats.
type UserStatsResponse struct {
	MasteryPercent float64 `json:"masteryPercent"` // MasteryPercent is the weighted ratio of good/ok cards to total cards
	CardsStudied   int     `json:"cardsStudied"`   // CardsStudied is the number of cards that have been reviewed at least once
	TotalCards     int     `json:"totalCards"`     // TotalCards is the total number of cards across all owned subjects
	GoodCount      int     `json:"goodCount"`      // GoodCount is the number of cards rated good
	OkCount        int     `json:"okCount"`        // OkCount is the number of cards rated ok
	BadCount       int     `json:"badCount"`       // BadCount is the number of cards rated bad
	NewCount       int     `json:"newCount"`       // NewCount is the number of cards not yet reviewed
	BadgesUnlocked int     `json:"badgesUnlocked"` // BadgesUnlocked is the count of achievements the user has earned
	BadgesTotal    int     `json:"badgesTotal"`    // BadgesTotal is the total number of achievements available
}
