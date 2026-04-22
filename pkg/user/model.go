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
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginInput is the JSON body for POST /user-login.
type LoginInput struct {
	Identifier string `json:"identifier"` // Identifier is username or email
	Password   string `json:"password"`
}

// TokenResponse is returned on register/login success.
type TokenResponse struct {
	Token string `json:"token"`
}

// UserStatsResponse is returned from /user-stats.
type UserStatsResponse struct {
	MasteryPercent float64 `json:"masteryPercent"`
	CardsStudied   int     `json:"cardsStudied"`
	TotalCards     int     `json:"totalCards"`
	GoodCount      int     `json:"goodCount"`
	OkCount        int     `json:"okCount"`
	BadCount       int     `json:"badCount"`
	NewCount       int     `json:"newCount"`
	BadgesUnlocked int     `json:"badgesUnlocked"`
	BadgesTotal    int     `json:"badgesTotal"`
}
