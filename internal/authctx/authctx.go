package authctx

import "context"

type key int

const (
	uidKey key = iota
	verifiedKey
	adminKey
)

// WithIdentity stores the caller's uid, verified flag, and admin flag on ctx.
func WithIdentity(ctx context.Context, uid int64, verified, admin bool) context.Context {
	ctx = context.WithValue(ctx, uidKey, uid)
	ctx = context.WithValue(ctx, verifiedKey, verified)
	return context.WithValue(ctx, adminKey, admin)
}

// UID returns the authenticated user ID, or 0 if none is stored.
func UID(ctx context.Context) int64 {
	v, _ := ctx.Value(uidKey).(int64)
	return v
}

// Verified returns the caller's email-verified flag.
func Verified(ctx context.Context) bool {
	v, _ := ctx.Value(verifiedKey).(bool)
	return v
}

// Admin returns the caller's admin flag.
func Admin(ctx context.Context) bool {
	v, _ := ctx.Value(adminKey).(bool)
	return v
}
