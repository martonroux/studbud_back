package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadValidatesRequiredFields(t *testing.T) {
	clearEnv(t)
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing required env vars, got nil")
	}
}

func TestLoadReturnsFilledConfig(t *testing.T) {
	clearEnv(t)
	setEnv(t, map[string]string{
		"ENV":          "dev",
		"PORT":         "8080",
		"FRONTEND_URL": "http://localhost:5173",
		"BACKEND_URL":  "http://localhost:8080",
		"DATABASE_URL": "postgres://x@localhost/y",
		"JWT_SECRET":   "a-minimum-32-byte-secret-xxxxxxxxxx",
		"JWT_ISSUER":   "studbud",
		"JWT_TTL":      "720h",
		"SMTP_HOST":    "localhost",
		"SMTP_PORT":    "1025",
		"SMTP_FROM":    "no-reply@studbud.local",
		"UPLOAD_DIR":   "./uploads",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want dev", cfg.Env)
	}
	if cfg.JWTTTL != 720*time.Hour {
		t.Errorf("JWTTTL = %v, want 720h", cfg.JWTTTL)
	}
}

func TestLoadRejectsShortJWTSecret(t *testing.T) {
	clearEnv(t)
	setEnv(t, minValidEnv())
	t.Setenv("JWT_SECRET", "too-short")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for short JWT secret, got nil")
	}
}

func TestLoadRejectsLiveStripeOutsideProd(t *testing.T) {
	clearEnv(t)
	setEnv(t, minValidEnv())
	t.Setenv("STRIPE_MODE", "live")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for live stripe mode in non-prod env")
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ENV", "PORT", "FRONTEND_URL", "BACKEND_URL", "DATABASE_URL",
		"JWT_SECRET", "JWT_ISSUER", "JWT_TTL",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_FROM",
		"UPLOAD_DIR", "ANTHROPIC_API_KEY", "AI_MODEL",
		"STRIPE_MODE", "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET",
		"STRIPE_PRICE_PRO_MONTHLY", "STRIPE_PRICE_PRO_ANNUAL",
		"ADMIN_BOOTSTRAP_EMAIL",
	} {
		_ = os.Unsetenv(k)
	}
}

func setEnv(t *testing.T, m map[string]string) {
	t.Helper()
	for k, v := range m {
		t.Setenv(k, v)
	}
}

func minValidEnv() map[string]string {
	return map[string]string{
		"ENV":          "dev",
		"PORT":         "8080",
		"FRONTEND_URL": "http://localhost:5173",
		"BACKEND_URL":  "http://localhost:8080",
		"DATABASE_URL": "postgres://x@localhost/y",
		"JWT_SECRET":   "a-minimum-32-byte-secret-xxxxxxxxxx",
		"JWT_ISSUER":   "studbud",
		"JWT_TTL":      "720h",
		"SMTP_HOST":    "localhost",
		"SMTP_PORT":    "1025",
		"SMTP_FROM":    "no-reply@studbud.local",
		"UPLOAD_DIR":   "./uploads",
	}
}
