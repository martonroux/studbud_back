# StudBud Backend Skeleton — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Go backend skeleton at `/Users/martonroux/Documents/WEB/studbud_3/backend/` with the full DB schema, every spec'd route wired, and stub services returning `501 not_implemented` — so future features (Specs A, B.0, B, C, D, E) plug in without restructuring.

**Architecture:** Layered monolith. `cmd/app` (entry + wiring) → `api/handler` (thin adapters) → `pkg/<domain>` (models + services + SQL) → `internal/<infra>` (pgx, jwt, email, storage, middleware). DB schema lives in `db_sql/setup_*.go`, all idempotent (`CREATE TABLE IF NOT EXISTS`, `CREATE OR REPLACE FUNCTION`), run on every boot. No migration tool ever.

**Tech Stack:** Go 1.22 (stdlib `net/http` enhanced mux), `pgx/v5` (`pgxpool`), `golang-jwt/jwt/v5`, `joho/godotenv`, `bcrypt`, real Postgres for tests.

---

## Reconciliation Notes (spec vs API.md)

Per user direction, **specs override API.md**. These decisions apply across every task:

1. **Error envelope:** spec's `{"error":{"code":"...","message":"...","field":null}}` — not API.md's `{"message":"..."}`.
2. **Status codes:** spec's richer set (402, 429, 501, 502) retained.
3. **JWT TTL:** 30 days per spec. Claims: `uid` (int64), `email_verified` (bool), `exp`, `iss`.
4. **Route naming:** spec pattern (e.g., `POST /subject-create`) — not API.md's (e.g., `POST /create-subject`). Pick spec-flavored when ambiguous.
5. **User shape:** `users.username TEXT NOT NULL UNIQUE` (login identifier + display). No `display_name`. Used as login identifier alongside email.
6. **Feature set:** Every feature described in API.md + PROJECT_DESCRIPTION.md is schematized (subject visibility, colors/icons/tags, archive, friendships, collaborators, invite links, preferences, streaks, daily goals, training sessions, achievements) — but the router and JSON shapes follow spec conventions, not API.md's.
7. **Subject visibility:** `private | friends | public` enum baked into `subjects.visibility`. Resolved via `AccessService.CanRead/CanEdit/CanManage`.
8. **Flashcards:** `chapter_id` nullable (flashcard can belong directly to subject). `subject_id` NOT NULL. `last_result` range `-1..2`.
9. **Chapters:** column name is `title` (not `name`).
10. **Friendships:** `sender_id`, `receiver_id`, status `pending|accepted|declined`.
11. **Preferences:** minimal — `ai_planning_enabled BOOLEAN`, `daily_goal_target INT`. Nothing else (YAGNI per CLAUDE.md).

---

## Phase Map

- **Phase 1** (Tasks 1–11): Project bootstrap + infra packages (config, errors, db, jwt, email, storage, httpx, middleware, cron, infra placeholders).
- **Phase 2** (Tasks 12–18): DB schema (`db_sql/setup_*.go`) + orchestrator.
- **Phase 3** (Task 19): `testutil/` scaffolding.
- **Phase 4** (Tasks 20–31): Core domain packages (access, user/email-verification, images, subjects, chapters, flashcards, search, friendships, subscriptions, collaboration, preferences, gamification/achievements).
- **Phase 5** (Tasks 32–36): Stubs (services + handlers) + `cmd/app` wiring + end-to-end example test.

---

## Phase 1 — Bootstrap & Infrastructure

### Task 1: Project bootstrap

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `.env.example`
- Create: `setup_db.sh`
- Create: `launch_app.sh`
- Create: `Makefile`
- Create: `uploads/.keep`

- [ ] **Step 1: Initialize Go module**

Run:
```bash
cd /Users/martonroux/Documents/WEB/studbud_3/backend
go mod init studbud/backend
```

Expected: creates `go.mod` with `module studbud/backend` and `go 1.22`.

- [ ] **Step 2: Add dependencies**

Run:
```bash
go get github.com/jackc/pgx/v5@latest
go get github.com/jackc/pgx/v5/pgxpool@latest
go get github.com/golang-jwt/jwt/v5@latest
go get github.com/joho/godotenv@latest
go get golang.org/x/crypto/bcrypt@latest
go get github.com/google/uuid@latest
```

Expected: `go.mod` lists these deps, `go.sum` populated.

- [ ] **Step 3: Write `.gitignore`**

Create `.gitignore`:
```
# Go
*.test
*.out
/bin/

# Env
.env
.env.local

# Uploads (keep directory, ignore contents)
uploads/*
!uploads/.keep

# Editor / OS
.DS_Store
.idea/
.vscode/
```

- [ ] **Step 4: Write `.env.example`**

Create `.env.example`:
```
# Runtime
PORT=8080
ENV=dev
FRONTEND_URL=http://localhost:5173
BACKEND_URL=http://localhost:8080

# Database
DATABASE_URL=postgres://postgres:postgres@localhost:5432/studbud?sslmode=disable

# Auth
JWT_SECRET=change-me-to-a-32-byte-minimum-secret-xx
JWT_ISSUER=studbud
JWT_TTL=720h

# Email (SMTP)
SMTP_HOST=localhost
SMTP_PORT=1025
SMTP_USER=
SMTP_PASS=
SMTP_FROM=no-reply@studbud.local

# Storage
UPLOAD_DIR=./uploads

# AI (Spec A — optional until impl)
ANTHROPIC_API_KEY=
AI_MODEL=claude-sonnet-4-6

# Billing (Spec C — optional until impl)
STRIPE_MODE=test
STRIPE_SECRET_KEY=
STRIPE_WEBHOOK_SECRET=
STRIPE_PRICE_PRO_MONTHLY=
STRIPE_PRICE_PRO_ANNUAL=

# Ops
ADMIN_BOOTSTRAP_EMAIL=
```

- [ ] **Step 5: Write `setup_db.sh`**

Create `setup_db.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail

DB_MAIN=${DB_MAIN:-studbud}
DB_TEST=${DB_TEST:-studbud_test}
DB_USER=${DB_USER:-postgres}

create_if_missing() {
    local db=$1
    if ! psql -U "$DB_USER" -lqt | cut -d \| -f 1 | grep -qw "$db"; then
        echo "Creating database: $db"
        createdb -U "$DB_USER" "$db"
    else
        echo "Database exists: $db"
    fi
}

create_if_missing "$DB_MAIN"
create_if_missing "$DB_TEST"

echo "Done."
```

Then: `chmod +x setup_db.sh`

- [ ] **Step 6: Write `launch_app.sh`**

Create `launch_app.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail

if [[ -f .env ]]; then
    set -a
    # shellcheck disable=SC1091
    source .env
    set +a
fi

go run ./cmd/app
```

Then: `chmod +x launch_app.sh`

- [ ] **Step 7: Write `Makefile`**

Create `Makefile`:
```makefile
.PHONY: build run test test-pkg vet fmt tidy db-setup

build:
	go build ./...

run:
	./launch_app.sh

test:
	ENV=test go test ./... -p 1 -count=1

test-pkg:
	ENV=test go test ./$(PKG)/... -p 1 -count=1 -v

vet:
	go vet ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

db-setup:
	./setup_db.sh
```

- [ ] **Step 8: Create `uploads/.keep`**

Run:
```bash
mkdir -p uploads && touch uploads/.keep
```

- [ ] **Step 9: Verify build tooling**

Run: `go vet ./... && go build ./...`

Expected: both succeed (no packages yet → no output, exit 0).

- [ ] **Step 10: Commit**

```bash
git init 2>/dev/null || true
git add go.mod go.sum .gitignore .env.example setup_db.sh launch_app.sh Makefile uploads/.keep
git commit -m "$(cat <<'EOF'
Project bootstrap

[+] go.mod with module studbud/backend, Go 1.22
[+] pgx/v5, jwt/v5, godotenv, bcrypt, uuid dependencies
[+] .env.example with all config keys
[+] setup_db.sh creates studbud + studbud_test
[+] launch_app.sh, Makefile, uploads/.keep
[+] .gitignore
EOF
)"
```

---

### Task 2: `internal/config/` — config loader

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
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
```

- [ ] **Step 2: Verify test fails to compile (no Load yet)**

Run: `go test ./internal/config/...`

Expected: FAIL with "undefined: Load".

- [ ] **Step 3: Implement `Load`**

Create `internal/config/config.go`:
```go
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds all runtime settings loaded from environment variables.
type Config struct {
	Port        string        // Port is the HTTP listen port (e.g. "8080")
	Env         string        // Env is "dev", "test", or "prod"
	FrontendURL string        // FrontendURL is the Vue client origin for CORS
	BackendURL  string        // BackendURL is this server's public URL
	DatabaseURL string        // DatabaseURL is the full pgx connection string
	JWTSecret   string        // JWTSecret signs auth tokens (>=32 bytes)
	JWTIssuer   string        // JWTIssuer is the "iss" claim value
	JWTTTL      time.Duration // JWTTTL is the token expiration window

	SMTPHost string // SMTPHost is the outbound mail server hostname
	SMTPPort string // SMTPPort is the outbound mail server port
	SMTPUser string // SMTPUser is the SMTP auth username
	SMTPPass string // SMTPPass is the SMTP auth password
	SMTPFrom string // SMTPFrom is the From: header for outbound email

	UploadDir string // UploadDir is the filesystem root for uploaded images

	AnthropicAPIKey string // AnthropicAPIKey is the Anthropic API key (Spec A)
	AIModel         string // AIModel is the default model identifier

	StripeMode           string // StripeMode is "test" or "live"
	StripeSecretKey      string // StripeSecretKey is the Stripe API secret
	StripeWebhookSecret  string // StripeWebhookSecret verifies webhook signatures
	StripePriceProMonth  string // StripePriceProMonth is the monthly plan price ID
	StripePriceProAnnual string // StripePriceProAnnual is the annual plan price ID

	AdminBootstrapEmail string // AdminBootstrapEmail is auto-promoted to is_admin on boot
}

// Load reads environment variables, validates them, and returns a Config.
// Returns an error describing the first validation failure.
func Load() (*Config, error) {
	cfg := &Config{
		Port:        getEnvDefault("PORT", "8080"),
		Env:         getEnvDefault("ENV", "dev"),
		FrontendURL: os.Getenv("FRONTEND_URL"),
		BackendURL:  os.Getenv("BACKEND_URL"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   os.Getenv("JWT_SECRET"),
		JWTIssuer:   getEnvDefault("JWT_ISSUER", "studbud"),

		SMTPHost: os.Getenv("SMTP_HOST"),
		SMTPPort: os.Getenv("SMTP_PORT"),
		SMTPUser: os.Getenv("SMTP_USER"),
		SMTPPass: os.Getenv("SMTP_PASS"),
		SMTPFrom: os.Getenv("SMTP_FROM"),

		UploadDir: getEnvDefault("UPLOAD_DIR", "./uploads"),

		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		AIModel:         getEnvDefault("AI_MODEL", "claude-sonnet-4-6"),

		StripeMode:           getEnvDefault("STRIPE_MODE", "test"),
		StripeSecretKey:      os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret:  os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripePriceProMonth:  os.Getenv("STRIPE_PRICE_PRO_MONTHLY"),
		StripePriceProAnnual: os.Getenv("STRIPE_PRICE_PRO_ANNUAL"),

		AdminBootstrapEmail: os.Getenv("ADMIN_BOOTSTRAP_EMAIL"),
	}

	ttl, err := parseTTL(getEnvDefault("JWT_TTL", "720h"))
	if err != nil {
		return nil, fmt.Errorf("parse JWT_TTL:\n%w", err)
	}
	cfg.JWTTTL = ttl

	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func getEnvDefault(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func parseTTL(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q:\n%w", s, err)
	}
	return d, nil
}

func validate(c *Config) error {
	if err := validateCore(c); err != nil {
		return err
	}
	if err := validateAuth(c); err != nil {
		return err
	}
	if err := validateSMTP(c); err != nil {
		return err
	}
	if err := validateStripeMode(c); err != nil {
		return err
	}
	if c.Env == "prod" {
		if err := validateProdRequirements(c); err != nil {
			return err
		}
	}
	return nil
}

func validateCore(c *Config) error {
	missing := []string{}
	if c.FrontendURL == "" {
		missing = append(missing, "FRONTEND_URL")
	}
	if c.BackendURL == "" {
		missing = append(missing, "BACKEND_URL")
	}
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return nil
}

func validateAuth(c *Config) error {
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must be at least 32 bytes (got %d)", len(c.JWTSecret))
	}
	return nil
}

func validateSMTP(c *Config) error {
	if c.SMTPHost == "" || c.SMTPPort == "" || c.SMTPFrom == "" {
		return fmt.Errorf("SMTP_HOST, SMTP_PORT, SMTP_FROM are required")
	}
	return nil
}

func validateStripeMode(c *Config) error {
	if c.StripeMode == "live" && c.Env != "prod" {
		return fmt.Errorf("STRIPE_MODE=live is not allowed when ENV=%q", c.Env)
	}
	if c.StripeMode != "test" && c.StripeMode != "live" {
		return fmt.Errorf("STRIPE_MODE must be 'test' or 'live' (got %q)", c.StripeMode)
	}
	return nil
}

func validateProdRequirements(c *Config) error {
	if c.AnthropicAPIKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY required in prod")
	}
	if c.StripeSecretKey == "" || c.StripeWebhookSecret == "" {
		return fmt.Errorf("Stripe keys required in prod")
	}
	return nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/config/... -v`

Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "$(cat <<'EOF'
Config loader

[+] internal/config.Config struct with every env var
[+] Load() with validation: required core, JWT length, stripe mode, prod-only keys
[+] Defaults: PORT=8080, ENV=dev, JWT_TTL=720h, UPLOAD_DIR=./uploads
[+] Tests covering missing fields, happy path, short JWT, invalid stripe mode
EOF
)"
```

---

### Task 3: `internal/myErrors/` — error taxonomy

**Files:**
- Create: `internal/myErrors/errors.go`
- Create: `internal/myErrors/errors_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/myErrors/errors_test.go`:
```go
package myErrors

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelsAreDistinct(t *testing.T) {
	if errors.Is(ErrNotFound, ErrConflict) {
		t.Fatal("ErrNotFound and ErrConflict should be distinct")
	}
}

func TestAppErrorUnwrapsToSentinel(t *testing.T) {
	e := &AppError{Code: "quota_exhausted", Message: "hit cap", Wrapped: ErrQuotaExhausted}
	if !errors.Is(e, ErrQuotaExhausted) {
		t.Fatal("AppError should unwrap to its Wrapped sentinel")
	}
}

func TestAppErrorPreservesMessage(t *testing.T) {
	e := &AppError{Code: "validation", Message: "name missing", Field: "name", Wrapped: ErrValidation}
	if e.Error() == "" {
		t.Fatal("Error() returned empty string")
	}
}

func TestWrapMakesSentinelMatchable(t *testing.T) {
	wrapped := fmt.Errorf("loading subject 42:\n%w", ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Fatal("fmt.Errorf with %w should preserve sentinel match")
	}
}
```

- [ ] **Step 2: Verify test fails to compile**

Run: `go test ./internal/myErrors/...`

Expected: FAIL with "undefined: ErrNotFound" etc.

- [ ] **Step 3: Implement the package**

Create `internal/myErrors/errors.go`:
```go
package myErrors

import (
	"errors"
	"fmt"
)

// ErrNotFound indicates a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// ErrUnauthenticated indicates a missing or invalid JWT.
var ErrUnauthenticated = errors.New("unauthenticated")

// ErrNotVerified indicates the caller's email is not verified.
var ErrNotVerified = errors.New("email not verified")

// ErrForbidden indicates the caller lacks permission on a resource.
var ErrForbidden = errors.New("forbidden")

// ErrAdminRequired indicates an admin-only route was hit by a non-admin user.
var ErrAdminRequired = errors.New("admin required")

// ErrInvalidInput indicates malformed request input (JSON, types).
var ErrInvalidInput = errors.New("invalid input")

// ErrValidation indicates a request passed parsing but failed semantic checks.
var ErrValidation = errors.New("validation failed")

// ErrConflict indicates a uniqueness or state conflict.
var ErrConflict = errors.New("conflict")

// ErrAlreadyVerified indicates email verification was attempted on an already-verified user.
var ErrAlreadyVerified = errors.New("already verified")

// ErrNoAIAccess indicates the caller lacks an active AI subscription.
var ErrNoAIAccess = errors.New("no AI access")

// ErrQuotaExhausted indicates the caller has hit their daily AI quota.
var ErrQuotaExhausted = errors.New("quota exhausted")

// ErrPdfTooLarge indicates a PDF would exceed the remaining page quota.
var ErrPdfTooLarge = errors.New("pdf too large for remaining quota")

// ErrAIProvider indicates an upstream AI provider failure.
var ErrAIProvider = errors.New("ai provider error")

// ErrStripe indicates an upstream Stripe failure.
var ErrStripe = errors.New("stripe error")

// ErrNotImplemented indicates a route exists but its feature is not yet implemented.
var ErrNotImplemented = errors.New("not implemented")

// AppError carries contextual error information alongside a sentinel.
// Use when the caller needs structured details (e.g., which field failed).
type AppError struct {
	Code    string // Code is a stable identifier used by API responses
	Message string // Message is a user-safe explanation
	Field   string // Field optionally names the offending input field
	Status  int    // Status overrides HTTP status mapping; zero means "use sentinel default"
	Wrapped error  // Wrapped is the underlying sentinel or cause
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.Wrapped != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Wrapped)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying wrapped error for use with errors.Is / errors.As.
func (e *AppError) Unwrap() error {
	return e.Wrapped
}
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/myErrors/... -v`

Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/myErrors/
git commit -m "$(cat <<'EOF'
Error taxonomy

[+] 15 sentinels covering identity, validation, entitlement, upstream
[+] AppError struct with Code/Message/Field/Status/Wrapped
[+] Unwrap support for errors.Is chain-walking
EOF
)"
```

---

### Task 4: `internal/db/` — pgxpool wrapper

**Files:**
- Create: `internal/db/pool.go`

- [ ] **Step 1: Create the file**

Create `internal/db/pool.go`:
```go
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OpenPool creates a connection pool against the given DATABASE_URL.
// The caller must Close() the pool at shutdown.
func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open pgx pool:\n%w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database:\n%w", err)
	}
	return pool, nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/db/...`

Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add internal/db/
git commit -m "$(cat <<'EOF'
DB pool wrapper

[+] internal/db.OpenPool creates pgxpool + pings
EOF
)"
```

---

### Task 5: `internal/storage/` — image ID + file writer

**Files:**
- Create: `internal/storage/id.go`
- Create: `internal/storage/id_test.go`
- Create: `internal/storage/file.go`

- [ ] **Step 1: Write the failing ID test**

Create `internal/storage/id_test.go`:
```go
package storage

import (
	"regexp"
	"testing"
)

func TestNewImageIDMatchesFormat(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9]{4}_[a-z0-9]{4}$`)
	for range 50 {
		id := NewImageID()
		if !re.MatchString(id) {
			t.Fatalf("ID %q does not match aaaa_bbbb format", id)
		}
	}
}

func TestNewImageIDVariesAcrossCalls(t *testing.T) {
	seen := map[string]struct{}{}
	for range 100 {
		seen[NewImageID()] = struct{}{}
	}
	if len(seen) < 95 {
		t.Fatalf("expected ~100 unique IDs, got %d", len(seen))
	}
}
```

- [ ] **Step 2: Verify test fails**

Run: `go test ./internal/storage/...`

Expected: FAIL with "undefined: NewImageID".

- [ ] **Step 3: Implement `NewImageID`**

Create `internal/storage/id.go`:
```go
package storage

import (
	"crypto/rand"
	"encoding/hex"
)

// NewImageID returns a random 8-char lowercase-hex ID in "aaaa_bbbb" form.
// Collision probability for 1M IDs is roughly 1 in 36 billion.
func NewImageID() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	hexStr := hex.EncodeToString(buf[:])
	return hexStr[:4] + "_" + hexStr[4:]
}
```

- [ ] **Step 4: Verify ID tests pass**

Run: `go test ./internal/storage/... -v`

Expected: both tests PASS.

- [ ] **Step 5: Implement file writer**

Create `internal/storage/file.go`:
```go
package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileStore writes and reads image files from a filesystem root directory.
type FileStore struct {
	root string // root is the base directory (e.g. "./uploads")
}

// NewFileStore constructs a FileStore rooted at the given directory.
// The directory is created if it does not exist.
func NewFileStore(root string) (*FileStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir uploads root:\n%w", err)
	}
	return &FileStore{root: root}, nil
}

// Write writes the reader's bytes to <root>/<name> and returns the path.
func (s *FileStore) Write(name string, src io.Reader) (string, error) {
	path := filepath.Join(s.root, name)
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create %s:\n%w", path, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return "", fmt.Errorf("write %s:\n%w", path, err)
	}
	return path, nil
}

// Open opens the file <root>/<name> for reading.
func (s *FileStore) Open(name string) (*os.File, error) {
	path := filepath.Join(s.root, name)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s:\n%w", path, err)
	}
	return f, nil
}

// Remove deletes the file <root>/<name>. Missing files are not an error.
func (s *FileStore) Remove(name string) error {
	path := filepath.Join(s.root, name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s:\n%w", path, err)
	}
	return nil
}
```

- [ ] **Step 6: Verify build**

Run: `go build ./internal/storage/...`

Expected: succeeds.

- [ ] **Step 7: Commit**

```bash
git add internal/storage/
git commit -m "$(cat <<'EOF'
Storage utilities

[+] NewImageID returns aaaa_bbbb hex IDs
[+] FileStore wraps uploads directory: Write, Open, Remove
[+] ID format tests
EOF
)"
```

---

### Task 6: `internal/jwt/` — sign & verify

**Files:**
- Create: `internal/jwt/jwt.go`
- Create: `internal/jwt/jwt_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/jwt/jwt_test.go`:
```go
package jwt

import (
	"testing"
	"time"
)

func TestSignAndVerifyRoundTrip(t *testing.T) {
	s := NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour)
	tok, err := s.Sign(Claims{UID: 42, EmailVerified: true})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UID != 42 || !got.EmailVerified {
		t.Errorf("claims = %+v, want UID=42 EmailVerified=true", got)
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	s := NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour)
	tok, _ := s.Sign(Claims{UID: 1})
	_, err := s.Verify(tok + "x")
	if err == nil {
		t.Fatal("expected error on tampered token, got nil")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	s := NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", -time.Hour)
	tok, _ := s.Sign(Claims{UID: 1})
	_, err := s.Verify(tok)
	if err == nil {
		t.Fatal("expected error on expired token, got nil")
	}
}
```

- [ ] **Step 2: Verify test fails**

Run: `go test ./internal/jwt/...`

Expected: FAIL with "undefined: NewSigner".

- [ ] **Step 3: Implement**

Create `internal/jwt/jwt.go`:
```go
package jwt

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the application's JWT claim set.
type Claims struct {
	UID           int64 `json:"uid"`            // UID is the authenticated user ID
	EmailVerified bool  `json:"email_verified"` // EmailVerified reflects the user's verification state at token issue time
	jwt.RegisteredClaims
}

// Signer signs and verifies tokens using a shared secret.
type Signer struct {
	secret []byte        // secret signs HS256 tokens
	issuer string        // issuer is embedded as the "iss" claim
	ttl    time.Duration // ttl defines how long tokens remain valid after issuance
}

// NewSigner constructs a Signer with the given secret, issuer, and TTL.
func NewSigner(secret, issuer string, ttl time.Duration) *Signer {
	return &Signer{secret: []byte(secret), issuer: issuer, ttl: ttl}
}

// Sign returns an HS256-signed token containing uid and email_verified claims.
func (s *Signer) Sign(c Claims) (string, error) {
	now := time.Now()
	c.RegisteredClaims = jwt.RegisteredClaims{
		Issuer:    s.issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	str, err := tok.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("sign token:\n%w", err)
	}
	return str, nil
}

// Verify parses and validates a token, returning the decoded claims.
// Returns an error for bad signatures, expired tokens, or wrong algorithms.
func (s *Signer) Verify(raw string) (*Claims, error) {
	out := &Claims{}
	tok, err := jwt.ParseWithClaims(raw, out, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != "HS256" {
			return nil, fmt.Errorf("unexpected algorithm: %s", t.Method.Alg())
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token:\n%w", err)
	}
	if !tok.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return out, nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/jwt/... -v`

Expected: all three tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/jwt/
git commit -m "$(cat <<'EOF'
JWT signer

[+] internal/jwt.Claims with UID + EmailVerified
[+] Signer.Sign / Verify with HS256 + TTL
[+] Round-trip, tamper, and expiry tests
EOF
)"
```

---

### Task 7: `internal/email/` — SMTP sender

**Files:**
- Create: `internal/email/email.go`

- [ ] **Step 1: Write the package**

Create `internal/email/email.go`:
```go
package email

import (
	"fmt"
	"net/smtp"
)

// Sender delivers a single email message. Implementations include smtpSender
// and test doubles (see testutil/email.go).
type Sender interface {
	Send(to, subject, body string) error
}

// smtpSender delivers via stdlib net/smtp using PLAIN auth.
type smtpSender struct {
	host string // host is the SMTP server hostname
	port string // port is the SMTP server port
	user string // user is the PLAIN-auth username (may be empty)
	pass string // pass is the PLAIN-auth password (may be empty)
	from string // from is the From: header value
}

// NewSMTPSender constructs an SMTP-backed Sender.
func NewSMTPSender(host, port, user, pass, from string) Sender {
	return &smtpSender{host: host, port: port, user: user, pass: pass, from: from}
}

// Send delivers a plain-text message to a single recipient.
func (s *smtpSender) Send(to, subject, body string) error {
	addr := s.host + ":" + s.port
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s", s.from, to, subject, body))
	var auth smtp.Auth
	if s.user != "" {
		auth = smtp.PlainAuth("", s.user, s.pass, s.host)
	}
	if err := smtp.SendMail(addr, auth, s.from, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp send:\n%w", err)
	}
	return nil
}

// Message captures a single sent email (used by Recorder).
type Message struct {
	To      string // To is the recipient address
	Subject string // Subject is the message subject line
	Body    string // Body is the plain-text body
}

// Recorder is an in-memory Sender used in tests and in the ENV=test config.
type Recorder struct {
	sent []Message // sent is the ordered list of messages captured
}

// NewRecorder constructs an empty Recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// Send captures the message instead of delivering it.
func (r *Recorder) Send(to, subject, body string) error {
	r.sent = append(r.sent, Message{To: to, Subject: subject, Body: body})
	return nil
}

// Sent returns a copy of the captured messages in order.
func (r *Recorder) Sent() []Message {
	out := make([]Message, len(r.sent))
	copy(out, r.sent)
	return out
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/email/...`

Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add internal/email/
git commit -m "$(cat <<'EOF'
Email sender

[+] email.Sender interface
[+] smtpSender using stdlib net/smtp + PLAIN auth
EOF
)"
```

---

### Task 8: `internal/authctx/` + `internal/httpx/` — context + HTTP helpers

**Files:**
- Create: `internal/authctx/authctx.go`
- Create: `internal/httpx/json.go`
- Create: `internal/httpx/errors.go`
- Create: `internal/httpx/sse.go`
- Create: `internal/httpx/errors_test.go`

- [ ] **Step 1: Write `authctx`**

Create `internal/authctx/authctx.go`:
```go
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
```

- [ ] **Step 2: Write `httpx/json.go`**

Create `internal/httpx/json.go`:
```go
package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"

	"studbud/backend/internal/myErrors"
)

// DecodeJSON parses the request body into dst. Returns ErrInvalidInput on failure.
func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode json: %w:\n%w", myErrors.ErrInvalidInput, err)
	}
	return nil
}

// WriteJSON writes a 200/custom-status JSON response.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
```

- [ ] **Step 3: Write `httpx/errors.go` failing test**

Create `internal/httpx/errors_test.go`:
```go
package httpx

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"studbud/backend/internal/myErrors"
)

func TestWriteErrorMapsSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unauthed", myErrors.ErrUnauthenticated, 401},
		{"not verified", myErrors.ErrNotVerified, 403},
		{"not found", myErrors.ErrNotFound, 404},
		{"conflict", myErrors.ErrConflict, 409},
		{"validation", myErrors.ErrValidation, 400},
		{"no ai access", myErrors.ErrNoAIAccess, 402},
		{"quota", myErrors.ErrQuotaExhausted, 429},
		{"not impl", myErrors.ErrNotImplemented, 501},
		{"stripe", myErrors.ErrStripe, 502},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, c.err)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("bad json: %v", err)
			}
			if _, ok := body["error"]; !ok {
				t.Fatalf("body missing 'error' key: %s", rec.Body.String())
			}
		})
	}
}

func TestWriteErrorAppErrorOverridesStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, &myErrors.AppError{Code: "x", Message: "y", Status: 418, Wrapped: myErrors.ErrValidation})
	if rec.Code != 418 {
		t.Fatalf("status = %d, want 418", rec.Code)
	}
}
```

- [ ] **Step 4: Write `httpx/errors.go`**

Create `internal/httpx/errors.go`:
```go
package httpx

import (
	"encoding/json"
	"errors"
	"net/http"

	"studbud/backend/internal/myErrors"
)

type errorBody struct {
	Error errorDetails `json:"error"`
}

type errorDetails struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// WriteError writes a JSON error envelope with HTTP status mapped from the sentinel.
// If the error is an *myErrors.AppError with Status != 0, that overrides the mapping.
func WriteError(w http.ResponseWriter, err error) {
	status := mapStatus(err)
	code, message, field := describe(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: errorDetails{Code: code, Message: message, Field: field}})
}

func mapStatus(err error) int {
	var ae *myErrors.AppError
	if errors.As(err, &ae) && ae.Status != 0 {
		return ae.Status
	}
	switch {
	case errors.Is(err, myErrors.ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, myErrors.ErrNotVerified),
		errors.Is(err, myErrors.ErrForbidden),
		errors.Is(err, myErrors.ErrAdminRequired):
		return http.StatusForbidden
	case errors.Is(err, myErrors.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, myErrors.ErrConflict),
		errors.Is(err, myErrors.ErrAlreadyVerified):
		return http.StatusConflict
	case errors.Is(err, myErrors.ErrInvalidInput),
		errors.Is(err, myErrors.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, myErrors.ErrNoAIAccess):
		return http.StatusPaymentRequired
	case errors.Is(err, myErrors.ErrQuotaExhausted),
		errors.Is(err, myErrors.ErrPdfTooLarge):
		return http.StatusTooManyRequests
	case errors.Is(err, myErrors.ErrAIProvider),
		errors.Is(err, myErrors.ErrStripe):
		return http.StatusBadGateway
	case errors.Is(err, myErrors.ErrNotImplemented):
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

func describe(err error) (code, message, field string) {
	var ae *myErrors.AppError
	if errors.As(err, &ae) {
		return orDefault(ae.Code, sentinelCode(ae.Wrapped)), ae.Message, ae.Field
	}
	return sentinelCode(err), err.Error(), ""
}

func sentinelCode(err error) string {
	switch {
	case errors.Is(err, myErrors.ErrUnauthenticated):
		return "unauthenticated"
	case errors.Is(err, myErrors.ErrNotVerified):
		return "not_verified"
	case errors.Is(err, myErrors.ErrForbidden):
		return "forbidden"
	case errors.Is(err, myErrors.ErrAdminRequired):
		return "admin_required"
	case errors.Is(err, myErrors.ErrNotFound):
		return "not_found"
	case errors.Is(err, myErrors.ErrConflict):
		return "conflict"
	case errors.Is(err, myErrors.ErrAlreadyVerified):
		return "already_verified"
	case errors.Is(err, myErrors.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, myErrors.ErrValidation):
		return "validation"
	case errors.Is(err, myErrors.ErrNoAIAccess):
		return "no_ai_access"
	case errors.Is(err, myErrors.ErrQuotaExhausted):
		return "quota_exhausted"
	case errors.Is(err, myErrors.ErrPdfTooLarge):
		return "pdf_too_large"
	case errors.Is(err, myErrors.ErrAIProvider):
		return "ai_provider_error"
	case errors.Is(err, myErrors.ErrStripe):
		return "stripe_error"
	case errors.Is(err, myErrors.ErrNotImplemented):
		return "not_implemented"
	default:
		return "internal_error"
	}
}

func orDefault(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}
```

- [ ] **Step 5: Write `httpx/sse.go`**

Create `internal/httpx/sse.go`:
```go
package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Stream writes chunks from a channel to the response as SSE events.
// Each chunk is JSON-encoded as `data: {...}\n\n`. Closes when the channel
// closes or ctx is canceled. Safe to use from HTTP handlers.
func Stream(ctx context.Context, w http.ResponseWriter, chunks <-chan any) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("response writer does not support flushing")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return nil
		case chunk, ok := <-chunks:
			if !ok {
				return nil
			}
			b, err := json.Marshal(chunk)
			if err != nil {
				return fmt.Errorf("marshal chunk:\n%w", err)
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return fmt.Errorf("write chunk:\n%w", err)
			}
			flusher.Flush()
		}
	}
}
```

- [ ] **Step 6: Verify tests**

Run: `go test ./internal/httpx/... -v`

Expected: all error-mapping cases PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/authctx/ internal/httpx/
git commit -m "$(cat <<'EOF'
Auth context + HTTP helpers

[+] authctx.WithIdentity / UID / Verified / Admin
[+] httpx.DecodeJSON, WriteJSON, WriteError, Stream (SSE)
[+] Error mapping from sentinels to HTTP codes
[+] Tests covering all sentinel statuses + AppError override
EOF
)"
```

---

### Task 9: `internal/http/middleware/` — middleware chain

**Files:**
- Create: `internal/http/middleware/chain.go`
- Create: `internal/http/middleware/recoverer.go`
- Create: `internal/http/middleware/requestid.go`
- Create: `internal/http/middleware/cors.go`
- Create: `internal/http/middleware/logger.go`
- Create: `internal/http/middleware/auth.go`
- Create: `internal/http/middleware/verified.go`
- Create: `internal/http/middleware/admin.go`

- [ ] **Step 1: Write `chain.go`**

Create `internal/http/middleware/chain.go`:
```go
package middleware

import "net/http"

// Middleware wraps an http.Handler to add cross-cutting behavior.
type Middleware func(http.Handler) http.Handler

// Chain composes multiple Middleware into one. Middlewares run outer→inner
// in the order given: Chain(a, b)(h) == a(b(h)).
func Chain(ms ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(ms) - 1; i >= 0; i-- {
			next = ms[i](next)
		}
		return next
	}
}
```

- [ ] **Step 2: Write `recoverer.go`**

Create `internal/http/middleware/recoverer.go`:
```go
package middleware

import (
	"log"
	"net/http"
	"runtime/debug"
)

// Recoverer catches panics from downstream handlers and returns a 500.
func Recoverer() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("panic in handler: %v\n%s", rec, debug.Stack())
					http.Error(w, `{"error":{"code":"internal_error","message":"internal server error"}}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 3: Write `requestid.go`**

Create `internal/http/middleware/requestid.go`:
```go
package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ridKey int

const requestIDKey ridKey = 0

// RequestID ensures every request carries an X-Request-Id. If the client
// provides one, it is echoed back; otherwise a UUID is generated.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if id == "" {
				id = uuid.NewString()
			}
			w.Header().Set("X-Request-Id", id)
			ctx := context.WithValue(r.Context(), requestIDKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFromContext returns the id stored on ctx by RequestID middleware.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}
```

- [ ] **Step 4: Write `cors.go`**

Create `internal/http/middleware/cors.go`:
```go
package middleware

import "net/http"

// CORS attaches permissive CORS headers for the given allowed origin.
// Preflight OPTIONS requests short-circuit with a 204.
func CORS(allowedOrigin string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", allowedOrigin)
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
			h.Set("Access-Control-Expose-Headers", "X-Request-Id")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 5: Write `logger.go`**

Create `internal/http/middleware/logger.go`:
```go
package middleware

import (
	"log"
	"net/http"
	"time"

	"studbud/backend/internal/authctx"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Logger emits one line per request with method, path, status, duration, uid.
func Logger() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sr := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(sr, r)
			log.Printf("%s %s -> %d (%s) uid=%d rid=%s",
				r.Method, r.URL.Path, sr.status, time.Since(start),
				authctx.UID(r.Context()), RequestIDFromContext(r.Context()))
		})
	}
}
```

- [ ] **Step 6: Write `auth.go`**

Create `internal/http/middleware/auth.go`:
```go
package middleware

import (
	"net/http"
	"strings"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/internal/myErrors"
)

// Auth parses the Bearer token and attaches identity to the request context.
// Requests without a token are rejected with 401.
func Auth(s *jwtsigner.Signer, isAdmin func(uid int64) (bool, error)) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				httpx.WriteError(w, myErrors.ErrUnauthenticated)
				return
			}
			claims, err := s.Verify(strings.TrimPrefix(header, "Bearer "))
			if err != nil {
				httpx.WriteError(w, myErrors.ErrUnauthenticated)
				return
			}
			admin := false
			if isAdmin != nil {
				if ok, err := isAdmin(claims.UID); err == nil {
					admin = ok
				}
			}
			ctx := authctx.WithIdentity(r.Context(), claims.UID, claims.EmailVerified, admin)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

- [ ] **Step 7: Write `verified.go`**

Create `internal/http/middleware/verified.go`:
```go
package middleware

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
)

// RequireVerified rejects requests whose JWT does not carry email_verified=true.
func RequireVerified() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !authctx.Verified(r.Context()) {
				httpx.WriteError(w, myErrors.ErrNotVerified)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 8: Write `admin.go`**

Create `internal/http/middleware/admin.go`:
```go
package middleware

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
)

// RequireAdmin rejects requests whose caller is not an admin.
func RequireAdmin() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !authctx.Admin(r.Context()) {
				httpx.WriteError(w, myErrors.ErrAdminRequired)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 9: Verify build**

Run: `go build ./internal/http/middleware/...`

Expected: succeeds.

- [ ] **Step 10: Commit**

```bash
git add internal/http/
git commit -m "$(cat <<'EOF'
HTTP middleware chain

[+] Chain composer
[+] Recoverer, RequestID, CORS, Logger
[+] Auth (JWT + isAdmin lookup), RequireVerified, RequireAdmin
EOF
)"
```

---

### Task 10: `internal/cron/` — ticker scaffold

**Files:**
- Create: `internal/cron/cron.go`

- [ ] **Step 1: Write the package**

Create `internal/cron/cron.go`:
```go
package cron

import (
	"context"
	"log"
	"time"
)

// Job is a named periodic task.
type Job struct {
	Name     string        // Name is used for log lines
	Interval time.Duration // Interval controls how often Run fires
	Run      func(context.Context) error
}

// Scheduler fires Jobs at their intervals until ctx cancels.
type Scheduler struct {
	jobs []Job // jobs is the registered list
}

// New constructs an empty Scheduler.
func New() *Scheduler {
	return &Scheduler{}
}

// Register adds a job to the scheduler. Call before Start.
func (s *Scheduler) Register(j Job) {
	s.jobs = append(s.jobs, j)
}

// Start launches one goroutine per registered job. Returns immediately.
func (s *Scheduler) Start(ctx context.Context) {
	for _, j := range s.jobs {
		go runJob(ctx, j)
	}
}

func runJob(ctx context.Context, j Job) {
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := j.Run(ctx); err != nil {
				log.Printf("cron %s: %v", j.Name, err)
			}
		}
	}
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/cron/...`

Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add internal/cron/
git commit -m "$(cat <<'EOF'
Cron scheduler

[+] cron.Job + Scheduler.Register + Start
[+] One goroutine per job with ticker
EOF
)"
```

---

### Task 11: Infra placeholder packages

**Files:**
- Create: `internal/aiProvider/client.go`
- Create: `internal/keywordWorker/worker.go`
- Create: `internal/billing/client.go`
- Create: `internal/duelHub/hub.go`

- [ ] **Step 1: `aiProvider`**

Create `internal/aiProvider/client.go`:
```go
package aiProvider

import (
	"context"

	"studbud/backend/internal/myErrors"
)

// Chunk is one streamed piece of AI output.
type Chunk struct {
	Text string
	Done bool
}

// Request is the structured-generation invocation shape.
type Request struct {
	FeatureKey string
	Model      string
	Prompt     string
	PDFBytes   []byte
}

// Client is the AI provider interface. Real Anthropic impl arrives with Spec A.
type Client interface {
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

// NoopClient returns ErrNotImplemented for every call.
type NoopClient struct{}

// Stream always returns ErrNotImplemented.
func (NoopClient) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	return nil, myErrors.ErrNotImplemented
}
```

- [ ] **Step 2: `keywordWorker`**

Create `internal/keywordWorker/worker.go`:
```go
package keywordWorker

import (
	"context"
	"log"
)

// Worker polls ai_extraction_jobs and drives keyword extraction.
// Real implementation arrives with Spec B.0.
type Worker struct{}

// New returns a no-op worker.
func New() *Worker { return &Worker{} }

// Start is a no-op until Spec B.0 lands.
func (w *Worker) Start(ctx context.Context) {
	log.Printf("keywordWorker: stub (disabled until Spec B.0)")
}
```

- [ ] **Step 3: `billing`**

Create `internal/billing/client.go`:
```go
package billing

import (
	"context"

	"studbud/backend/internal/myErrors"
)

// CheckoutSession is what the frontend redirects a user to.
type CheckoutSession struct {
	URL string
	ID  string
}

// Client is the billing-provider interface. Real Stripe impl arrives with Spec C.
type Client interface {
	CreateCheckout(ctx context.Context, uid int64, priceID string) (*CheckoutSession, error)
	CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error)
	VerifyWebhook(payload []byte, signature string) error
}

// NoopClient returns ErrNotImplemented for every call.
type NoopClient struct{}

func (NoopClient) CreateCheckout(ctx context.Context, uid int64, priceID string) (*CheckoutSession, error) {
	return nil, myErrors.ErrNotImplemented
}

func (NoopClient) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	return "", myErrors.ErrNotImplemented
}

func (NoopClient) VerifyWebhook(payload []byte, signature string) error {
	return myErrors.ErrNotImplemented
}
```

- [ ] **Step 4: `duelHub`**

Create `internal/duelHub/hub.go`:
```go
package duelHub

import (
	"context"
	"log"
)

// Hub is the stateless WebSocket broker for duels.
// Real implementation arrives with Spec E.
type Hub struct{}

// New returns an empty hub.
func New() *Hub { return &Hub{} }

// Start is a no-op until Spec E lands.
func (h *Hub) Start(ctx context.Context) {
	log.Printf("duelHub: stub (disabled until Spec E)")
}
```

- [ ] **Step 5: Verify build**

Run: `go build ./internal/...`

Expected: succeeds.

- [ ] **Step 6: Commit**

```bash
git add internal/aiProvider/ internal/keywordWorker/ internal/billing/ internal/duelHub/
git commit -m "$(cat <<'EOF'
Infra placeholder packages

[+] aiProvider.Client + NoopClient (Spec A)
[+] keywordWorker.Worker stub (Spec B.0)
[+] billing.Client + NoopClient (Spec C)
[+] duelHub.Hub stub (Spec E)
EOF
)"
```

---

## Phase 2 — Database Schema

All setup files use `CREATE TABLE IF NOT EXISTS`, `CREATE OR REPLACE FUNCTION`, and `DO` blocks for FK adds — every statement is idempotent. Run on every boot.

### Task 12: `db_sql/setup_core.go`

**Files:**
- Create: `db_sql/setup_core.go`

- [ ] **Step 1: Write the file**

Create `db_sql/setup_core.go`:
```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const coreSchema = `
CREATE TABLE IF NOT EXISTS users (
    id                        BIGSERIAL PRIMARY KEY,
    username                  TEXT NOT NULL UNIQUE,
    email                     TEXT NOT NULL UNIQUE,
    password_hash             TEXT NOT NULL,
    email_verified            BOOLEAN NOT NULL DEFAULT false,
    verified_at               TIMESTAMPTZ NULL,
    profile_picture_image_id  TEXT NULL,
    stripe_customer_id        TEXT UNIQUE NULL,
    is_admin                  BOOLEAN NOT NULL DEFAULT false,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS email_verifications (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS email_verification_throttle (
    user_id     BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    last_sent   TIMESTAMPTZ NOT NULL DEFAULT now(),
    send_count  INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS images (
    id         TEXT PRIMARY KEY,
    owner_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename   TEXT NOT NULL,
    mime_type  TEXT NOT NULL,
    bytes      BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_profile_pic_fk') THEN
    ALTER TABLE users
      ADD CONSTRAINT users_profile_pic_fk
      FOREIGN KEY (profile_picture_image_id) REFERENCES images(id) ON DELETE SET NULL;
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS subjects (
    id          BIGSERIAL PRIMARY KEY,
    owner_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    color       TEXT NOT NULL DEFAULT '',
    icon        TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '',
    visibility  TEXT NOT NULL DEFAULT 'private',
    archived    BOOLEAN NOT NULL DEFAULT false,
    last_used   TIMESTAMPTZ NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    search_vec  tsvector,
    CONSTRAINT subjects_visibility_check CHECK (visibility IN ('private','friends','public'))
);
CREATE INDEX IF NOT EXISTS idx_subjects_owner ON subjects(owner_id);
CREATE INDEX IF NOT EXISTS idx_subjects_search ON subjects USING GIN (search_vec);

CREATE OR REPLACE FUNCTION subjects_search_vec_update() RETURNS trigger AS $$
BEGIN
  NEW.search_vec :=
    setweight(to_tsvector('simple', coalesce(NEW.name,'')), 'A') ||
    setweight(to_tsvector('simple', coalesce(NEW.tags,'')), 'B');
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_subjects_search_vec ON subjects;
CREATE TRIGGER trg_subjects_search_vec
  BEFORE INSERT OR UPDATE ON subjects
  FOR EACH ROW EXECUTE FUNCTION subjects_search_vec_update();

CREATE TABLE IF NOT EXISTS chapters (
    id         BIGSERIAL PRIMARY KEY,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    position   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_chapters_subject ON chapters(subject_id);

CREATE TABLE IF NOT EXISTS flashcards (
    id            BIGSERIAL PRIMARY KEY,
    subject_id    BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    chapter_id    BIGINT NULL REFERENCES chapters(id) ON DELETE SET NULL,
    title         TEXT NOT NULL DEFAULT '',
    question      TEXT NOT NULL,
    answer        TEXT NOT NULL,
    image_id      TEXT NULL REFERENCES images(id) ON DELETE SET NULL,
    source        TEXT NOT NULL DEFAULT 'manual',
    due_at        TIMESTAMPTZ NULL,
    last_result   SMALLINT NOT NULL DEFAULT -1,
    last_used     TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT flashcards_last_result_check CHECK (last_result BETWEEN -1 AND 2),
    CONSTRAINT flashcards_source_check CHECK (source IN ('manual','ai'))
);
CREATE INDEX IF NOT EXISTS idx_flashcards_subject ON flashcards(subject_id);
CREATE INDEX IF NOT EXISTS idx_flashcards_chapter ON flashcards(chapter_id);

CREATE TABLE IF NOT EXISTS friendships (
    id           BIGSERIAL PRIMARY KEY,
    sender_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receiver_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status       TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT friendships_pair_chk CHECK (sender_id <> receiver_id),
    CONSTRAINT friendships_status_chk CHECK (status IN ('pending','accepted','declined')),
    UNIQUE (sender_id, receiver_id)
);

CREATE TABLE IF NOT EXISTS subject_subscriptions (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, subject_id)
);

CREATE TABLE IF NOT EXISTS collaborators (
    id         BIGSERIAL PRIMARY KEY,
    subject_id BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT collaborators_role_chk CHECK (role IN ('viewer','editor')),
    UNIQUE (subject_id, user_id)
);

CREATE TABLE IF NOT EXISTS invite_links (
    id          BIGSERIAL PRIMARY KEY,
    subject_id  BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    role        TEXT NOT NULL,
    created_by  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NULL,
    revoked_at  TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT invite_links_role_chk CHECK (role IN ('viewer','editor'))
);

CREATE TABLE IF NOT EXISTS preferences (
    user_id              BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    ai_planning_enabled  BOOLEAN NOT NULL DEFAULT false,
    daily_goal_target    INT NOT NULL DEFAULT 20,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS streaks (
    user_id            BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    current_days       INT NOT NULL DEFAULT 0,
    best_days          INT NOT NULL DEFAULT 0,
    last_studied_date  DATE NULL,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS daily_goals (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day         DATE NOT NULL,
    target      INT NOT NULL,
    done_today  INT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, day)
);

CREATE TABLE IF NOT EXISTS training_sessions (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id    BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    chapter_id    BIGINT NULL REFERENCES chapters(id) ON DELETE SET NULL,
    goods         INT NOT NULL DEFAULT 0,
    oks           INT NOT NULL DEFAULT 0,
    bads          INT NOT NULL DEFAULT 0,
    total_cards   INT NOT NULL DEFAULT 0,
    duration_ms   BIGINT NOT NULL DEFAULT 0,
    completed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_session_bests (
    user_id       BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    best_accuracy NUMERIC(5,2) NOT NULL DEFAULT 0,
    best_cards    INT NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS unlocked_achievements (
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    achievement_key TEXT NOT NULL,
    unlocked_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, achievement_key)
);
`

func setupCore(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, coreSchema); err != nil {
		return fmt.Errorf("exec core schema:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./db_sql/...`

Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add db_sql/setup_core.go
git commit -m "$(cat <<'EOF'
Core DB schema

[+] users, email_verifications, email_verification_throttle
[+] images + users.profile_picture_image_id FK
[+] subjects (with tsvector + trigger), chapters, flashcards
[+] friendships, subject_subscriptions, collaborators, invite_links
[+] preferences, streaks, daily_goals, training_sessions
[+] user_session_bests, unlocked_achievements
EOF
)"
```

---

### Task 13: `db_sql/setup_ai.go`

**Files:**
- Create: `db_sql/setup_ai.go`

- [ ] **Step 1: Write the file**

Create `db_sql/setup_ai.go`:
```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const aiSchema = `
CREATE TABLE IF NOT EXISTS ai_jobs (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feature_key   TEXT NOT NULL,
    model         TEXT NOT NULL,
    input_tokens  INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    cents_spent   INT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL,
    error         TEXT NULL,
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ NULL
);
CREATE INDEX IF NOT EXISTS idx_ai_jobs_user_day ON ai_jobs(user_id, started_at);

CREATE TABLE IF NOT EXISTS ai_quota_daily (
    user_id                   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day                       DATE NOT NULL,
    prompt_calls              INT NOT NULL DEFAULT 0,
    pdf_calls                 INT NOT NULL DEFAULT 0,
    pdf_pages                 INT NOT NULL DEFAULT 0,
    check_calls               INT NOT NULL DEFAULT 0,
    plan_calls                INT NOT NULL DEFAULT 0,
    cross_subject_rank_calls  INT NOT NULL DEFAULT 0,
    quiz_calls                INT NOT NULL DEFAULT 0,
    quiz_demo_used            BOOLEAN NOT NULL DEFAULT false,
    extract_keywords_calls    INT NOT NULL DEFAULT 0,
    cents_spent               INT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);

CREATE TABLE IF NOT EXISTS ai_extraction_jobs (
    id            BIGSERIAL PRIMARY KEY,
    flashcard_id  BIGINT NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'pending',
    attempts      INT NOT NULL DEFAULT 0,
    last_error    TEXT NULL,
    claimed_at    TIMESTAMPTZ NULL,
    finished_at   TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flashcard_id),
    CONSTRAINT ai_extraction_jobs_status_chk CHECK (status IN ('pending','claimed','succeeded','failed'))
);
CREATE INDEX IF NOT EXISTS idx_ai_extraction_jobs_status ON ai_extraction_jobs(status);

CREATE TABLE IF NOT EXISTS flashcard_keywords (
    flashcard_id BIGINT NOT NULL REFERENCES flashcards(id) ON DELETE CASCADE,
    keyword      TEXT NOT NULL,
    weight       REAL NOT NULL DEFAULT 1.0,
    PRIMARY KEY (flashcard_id, keyword)
);
CREATE INDEX IF NOT EXISTS idx_flashcard_keywords_kw ON flashcard_keywords(keyword);
`

func setupAI(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, aiSchema); err != nil {
		return fmt.Errorf("exec ai schema:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./db_sql/...`

Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add db_sql/setup_ai.go
git commit -m "$(cat <<'EOF'
AI DB schema

[+] ai_jobs with cents_spent + metadata
[+] ai_quota_daily with all feature counter columns from day one
[+] ai_extraction_jobs + flashcard_keywords (Spec B.0)
EOF
)"
```

---

### Task 14: `db_sql/setup_billing.go`

**Files:**
- Create: `db_sql/setup_billing.go`

- [ ] **Step 1: Write the file**

Create `db_sql/setup_billing.go`:
```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const billingSchema = `
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id                        BIGSERIAL PRIMARY KEY,
    user_id                   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_subscription_id    TEXT UNIQUE NULL,
    plan                      TEXT NOT NULL,
    status                    TEXT NOT NULL,
    current_period_end        TIMESTAMPTZ NULL,
    cancel_at_period_end      BOOLEAN NOT NULL DEFAULT false,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_subs_plan_chk CHECK (plan IN ('pro_monthly','pro_annual','comp')),
    CONSTRAINT user_subs_status_chk CHECK (status IN ('active','past_due','canceled','trialing','comp'))
);
CREATE INDEX IF NOT EXISTS idx_user_subs_user ON user_subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_subs_status ON user_subscriptions(status);

CREATE TABLE IF NOT EXISTS billing_events (
    id              BIGSERIAL PRIMARY KEY,
    stripe_event_id TEXT UNIQUE NOT NULL,
    type            TEXT NOT NULL,
    user_id         BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    payload         JSONB NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ NULL,
    error           TEXT NULL
);

CREATE OR REPLACE FUNCTION user_has_ai_access(uid BIGINT) RETURNS BOOLEAN AS $$
DECLARE
  ok BOOLEAN;
BEGIN
  SELECT EXISTS (
    SELECT 1 FROM user_subscriptions
    WHERE user_id = uid
      AND (
        status = 'comp' OR
        (status IN ('active','trialing') AND
         (current_period_end IS NULL OR current_period_end > now()))
      )
  ) INTO ok;
  RETURN ok;
END;
$$ LANGUAGE plpgsql STABLE;
`

func setupBilling(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, billingSchema); err != nil {
		return fmt.Errorf("exec billing schema:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./db_sql/...`

Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add db_sql/setup_billing.go
git commit -m "$(cat <<'EOF'
Billing DB schema

[+] user_subscriptions with plan/status CHECK constraints
[+] billing_events append-only audit log
[+] user_has_ai_access(uid) SQL function — single entitlement source
EOF
)"
```

---

### Task 15: `db_sql/setup_plan.go`

**Files:**
- Create: `db_sql/setup_plan.go`

- [ ] **Step 1: Write the file**

Create `db_sql/setup_plan.go`:
```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const planSchema = `
CREATE TABLE IF NOT EXISTS exams (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id        BIGINT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    title             TEXT NOT NULL,
    exam_date         DATE NOT NULL,
    annales_image_id  TEXT NULL REFERENCES images(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_exams_user_date ON exams(user_id, exam_date);

CREATE TABLE IF NOT EXISTS revision_plans (
    id             BIGSERIAL PRIMARY KEY,
    exam_id        BIGINT NOT NULL UNIQUE REFERENCES exams(id) ON DELETE CASCADE,
    intensity      TEXT NOT NULL DEFAULT 'balanced',
    payload        JSONB NOT NULL,
    generated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT revision_plans_intensity_chk CHECK (intensity IN ('light','balanced','intense'))
);

CREATE TABLE IF NOT EXISTS revision_plan_progress (
    plan_id     BIGINT NOT NULL REFERENCES revision_plans(id) ON DELETE CASCADE,
    day         DATE NOT NULL,
    item_key    TEXT NOT NULL,
    done_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, day, item_key)
);
`

func setupPlan(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, planSchema); err != nil {
		return fmt.Errorf("exec plan schema:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 2: Verify build + commit**

Run: `go build ./db_sql/...`

Expected: succeeds.

```bash
git add db_sql/setup_plan.go
git commit -m "$(cat <<'EOF'
Plan DB schema

[+] exams, revision_plans (with intensity), revision_plan_progress
EOF
)"
```

---

### Task 16: `db_sql/setup_quiz.go`

**Files:**
- Create: `db_sql/setup_quiz.go`

- [ ] **Step 1: Write the file**

Create `db_sql/setup_quiz.go`:
```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const quizSchema = `
CREATE TABLE IF NOT EXISTS quizzes (
    id             BIGSERIAL PRIMARY KEY,
    owner_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id     BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    title          TEXT NOT NULL,
    kind           TEXT NOT NULL,
    source         TEXT NOT NULL,
    parent_quiz_id BIGINT NULL REFERENCES quizzes(id) ON DELETE SET NULL,
    plan_id        BIGINT NULL REFERENCES revision_plans(id) ON DELETE SET NULL,
    duel_id        BIGINT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT quizzes_source_check CHECK (source IN ('user','plan','shared_copy','duel')),
    CONSTRAINT quizzes_kind_check   CHECK (kind IN ('specific','global'))
);
CREATE INDEX IF NOT EXISTS idx_quizzes_owner ON quizzes(owner_id);

CREATE TABLE IF NOT EXISTS quiz_questions (
    id                  BIGSERIAL PRIMARY KEY,
    quiz_id             BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    position            INT NOT NULL,
    prompt              TEXT NOT NULL,
    choices             JSONB NOT NULL,
    correct_index       SMALLINT NOT NULL,
    source_flashcard_id BIGINT NULL REFERENCES flashcards(id) ON DELETE SET NULL,
    explanation         TEXT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_quiz_questions_quiz ON quiz_questions(quiz_id);

CREATE TABLE IF NOT EXISTS quiz_attempts (
    id          BIGSERIAL PRIMARY KEY,
    quiz_id     BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ NULL,
    score       INT NULL,
    total       INT NULL
);

CREATE TABLE IF NOT EXISTS quiz_attempt_answers (
    attempt_id     BIGINT NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
    question_id    BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    chosen_index   SMALLINT NOT NULL,
    is_correct     BOOLEAN NOT NULL,
    answered_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (attempt_id, question_id)
);

CREATE TABLE IF NOT EXISTS quiz_share_links (
    id          BIGSERIAL PRIMARY KEY,
    quiz_id     BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    created_by  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revoked_at  TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS quiz_sent_to_friends (
    quiz_id      BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    sender_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (quiz_id, sender_id, recipient_id)
);

CREATE TABLE IF NOT EXISTS quiz_quality_reports (
    id          BIGSERIAL PRIMARY KEY,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func setupQuiz(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, quizSchema); err != nil {
		return fmt.Errorf("exec quiz schema:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 2: Verify + commit**

Run: `go build ./db_sql/...`

```bash
git add db_sql/setup_quiz.go
git commit -m "$(cat <<'EOF'
Quiz DB schema

[+] quizzes (source includes 'duel' for Spec E), kind check
[+] quiz_questions, quiz_attempts, quiz_attempt_answers
[+] quiz_share_links, quiz_sent_to_friends, quiz_quality_reports
EOF
)"
```

---

### Task 17: `db_sql/setup_duel.go`

**Files:**
- Create: `db_sql/setup_duel.go`

- [ ] **Step 1: Write the file**

Create `db_sql/setup_duel.go`:
```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const duelSchema = `
CREATE TABLE IF NOT EXISTS duels (
    id                BIGSERIAL PRIMARY KEY,
    challenger_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenger_name   TEXT NOT NULL,
    invitee_id        BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    invitee_name      TEXT NULL,
    subject_id        BIGINT NULL REFERENCES subjects(id) ON DELETE SET NULL,
    subject_name      TEXT NOT NULL,
    status            TEXT NOT NULL,
    challenger_score  INT NOT NULL DEFAULT 0,
    invitee_score     INT NOT NULL DEFAULT 0,
    current_round     INT NOT NULL DEFAULT 0,
    rounds_total      INT NOT NULL DEFAULT 5,
    quiz_id           BIGINT NULL REFERENCES quizzes(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at        TIMESTAMPTZ NULL,
    finished_at       TIMESTAMPTZ NULL,
    CONSTRAINT duels_status_chk CHECK (status IN ('waiting','accepted','active','finished','canceled'))
);
CREATE INDEX IF NOT EXISTS idx_duels_challenger ON duels(challenger_id);
CREATE INDEX IF NOT EXISTS idx_duels_invitee ON duels(invitee_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_duels_one_waiting_per_challenger
  ON duels(challenger_id) WHERE status = 'waiting';

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'quizzes_duel_id_fkey') THEN
    ALTER TABLE quizzes
      ADD CONSTRAINT quizzes_duel_id_fkey
      FOREIGN KEY (duel_id) REFERENCES duels(id) ON DELETE SET NULL;
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS duel_invite_tokens (
    id          BIGSERIAL PRIMARY KEY,
    duel_id     BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    token       TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NULL
);

CREATE TABLE IF NOT EXISTS duel_round_questions (
    duel_id     BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    round_no    INT NOT NULL,
    question_id BIGINT NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    PRIMARY KEY (duel_id, round_no)
);

CREATE TABLE IF NOT EXISTS duel_round_answers (
    duel_id             BIGINT NOT NULL REFERENCES duels(id) ON DELETE CASCADE,
    round_no            INT NOT NULL,
    user_id             BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chosen_index        SMALLINT NOT NULL,
    is_correct          BOOLEAN NOT NULL,
    server_received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (duel_id, round_no, user_id)
);

CREATE TABLE IF NOT EXISTS duel_user_stats (
    user_id        BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    duels_played   INT NOT NULL DEFAULT 0,
    duels_won      INT NOT NULL DEFAULT 0,
    duels_lost     INT NOT NULL DEFAULT 0,
    duels_drawn    INT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS duel_head_to_head (
    user_a_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_b_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    a_wins       INT NOT NULL DEFAULT 0,
    b_wins       INT NOT NULL DEFAULT 0,
    draws        INT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT h2h_order_chk CHECK (user_a_id < user_b_id),
    PRIMARY KEY (user_a_id, user_b_id)
);

CREATE TABLE IF NOT EXISTS user_reports (
    id           BIGSERIAL PRIMARY KEY,
    reporter_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason       TEXT NOT NULL,
    context      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func setupDuel(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, duelSchema); err != nil {
		return fmt.Errorf("exec duel schema:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 2: Verify + commit**

Run: `go build ./db_sql/...`

```bash
git add db_sql/setup_duel.go
git commit -m "$(cat <<'EOF'
Duel DB schema

[+] duels with snapshot fields (challenger_name, subject_name)
[+] 1-waiting-per-challenger partial unique index
[+] duel_invite_tokens, duel_round_questions, duel_round_answers
[+] duel_user_stats, duel_head_to_head, user_reports
[+] Backfills quizzes.duel_id FK now that duels exists
EOF
)"
```

---

### Task 18: `db_sql/setup.go` — orchestrator

**Files:**
- Create: `db_sql/setup.go`
- Create: `db_sql/setup_test.go`

- [ ] **Step 1: Write orchestrator**

Create `db_sql/setup.go`:
```go
package db_sql

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SetupAll runs every schema setup step in dependency order.
// All statements are idempotent: safe to run on every boot.
func SetupAll(ctx context.Context, pool *pgxpool.Pool) error {
	steps := []struct {
		name string
		fn   func(context.Context, *pgxpool.Pool) error
	}{
		{"core", setupCore},
		{"ai", setupAI},
		{"billing", setupBilling},
		{"plan", setupPlan},
		{"quiz", setupQuiz},
		{"duel", setupDuel},
	}
	for _, s := range steps {
		if err := s.fn(ctx, pool); err != nil {
			return fmt.Errorf("setup %s:\n%w", s.name, err)
		}
	}
	return nil
}
```

- [ ] **Step 2: Write idempotency test**

Create `db_sql/setup_test.go`:
```go
package db_sql

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSetupAllIsIdempotent(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || !strings.HasSuffix(dsn, "/studbud_test") {
		t.Skip("DATABASE_URL must point at studbud_test (set ENV=test)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	if err := SetupAll(ctx, pool); err != nil {
		t.Fatalf("first SetupAll: %v", err)
	}
	if err := SetupAll(ctx, pool); err != nil {
		t.Fatalf("second SetupAll: %v", err)
	}
}
```

- [ ] **Step 3: Prepare test DB**

Run:
```bash
./setup_db.sh
```

Expected: creates `studbud` and `studbud_test` if missing.

- [ ] **Step 4: Run idempotency test**

Run:
```bash
ENV=test DATABASE_URL=postgres://postgres@localhost:5432/studbud_test?sslmode=disable \
  go test ./db_sql/... -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add db_sql/setup.go db_sql/setup_test.go
git commit -m "$(cat <<'EOF'
Schema orchestrator

[+] SetupAll runs core → ai → billing → plan → quiz → duel
[+] Idempotency test: runs SetupAll twice in a row
EOF
)"
```

---

## Phase 3 — Test Scaffolding

### Task 19: `testutil/` — shared test helpers

**Files:**
- Create: `testutil/testdb.go`
- Create: `testutil/fixtures.go`
- Create: `testutil/email.go`
- Create: `testutil/ai.go`
- Create: `testutil/stripe.go`
- Create: `testutil/clock.go`

- [ ] **Step 1: Write `testdb.go`**

Create `testutil/testdb.go`:
```go
package testutil

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	dbsql "studbud/backend/db_sql"
)

var (
	poolOnce sync.Once
	poolRef  *pgxpool.Pool
	poolErr  error
)

// MustTestEnv aborts the test unless ENV=test and DATABASE_URL points at studbud_test.
func MustTestEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("ENV") != "test" {
		t.Skip("ENV must be 'test' to run DB-backed tests")
	}
	dsn := os.Getenv("DATABASE_URL")
	if !strings.HasSuffix(dsn, "/studbud_test") &&
		!strings.HasSuffix(dsn, "/studbud_test?sslmode=disable") {
		t.Fatalf("refusing to run tests against %q — must end with /studbud_test", dsn)
	}
}

// OpenTestDB returns the shared test pool, running SetupAll once per process.
func OpenTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	MustTestEnv(t)
	poolOnce.Do(func() {
		ctx := context.Background()
		poolRef, poolErr = pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
		if poolErr != nil {
			return
		}
		poolErr = dbsql.SetupAll(ctx, poolRef)
	})
	if poolErr != nil {
		t.Fatalf("test db setup: %v", poolErr)
	}
	return poolRef
}

// Reset truncates every table. Run at the start of each test.
func Reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateAllSQL)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

const truncateAllSQL = `
TRUNCATE TABLE
  user_reports, duel_head_to_head, duel_user_stats, duel_round_answers,
  duel_round_questions, duel_invite_tokens, duels,
  quiz_quality_reports, quiz_sent_to_friends, quiz_share_links,
  quiz_attempt_answers, quiz_attempts, quiz_questions, quizzes,
  revision_plan_progress, revision_plans, exams,
  billing_events, user_subscriptions,
  flashcard_keywords, ai_extraction_jobs, ai_quota_daily, ai_jobs,
  unlocked_achievements, user_session_bests, training_sessions,
  daily_goals, streaks, preferences,
  invite_links, collaborators, subject_subscriptions, friendships,
  flashcards, chapters, subjects,
  images, email_verification_throttle, email_verifications, users
RESTART IDENTITY CASCADE;
`
```

- [ ] **Step 2: Write `fixtures.go`**

Create `testutil/fixtures.go`:
```go
package testutil

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// UserFixture is a minimal user row returned by NewUser.
type UserFixture struct {
	ID            int64
	Username      string
	Email         string
	EmailVerified bool
}

// fixtureCounter produces collision-free usernames / subject names within a test binary.
var fixtureCounter atomic.Int64

// nextName returns a fresh identifier with the given prefix.
func nextName(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, fixtureCounter.Add(1))
}

// NewUser inserts an unverified user with an auto-generated username. Returns the fixture.
func NewUser(t *testing.T, pool *pgxpool.Pool) *UserFixture {
	t.Helper()
	return insertUser(t, pool, nextName("user"), false)
}

// NewVerifiedUser inserts a verified user with an auto-generated username.
func NewVerifiedUser(t *testing.T, pool *pgxpool.Pool) *UserFixture {
	t.Helper()
	return insertUser(t, pool, nextName("user"), true)
}

// NewVerifiedUserNamed inserts a verified user with an explicit username.
func NewVerifiedUserNamed(t *testing.T, pool *pgxpool.Pool, username string) *UserFixture {
	t.Helper()
	return insertUser(t, pool, username, true)
}

func insertUser(t *testing.T, pool *pgxpool.Pool, username string, verified bool) *UserFixture {
	t.Helper()
	email := username + "@example.com"
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	var id int64
	err = pool.QueryRow(context.Background(), `
        INSERT INTO users (username, email, password_hash, email_verified, verified_at)
        VALUES ($1, $2, $3, $4, CASE WHEN $4 THEN now() ELSE NULL END)
        RETURNING id
    `, username, email, string(hash), verified).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return &UserFixture{ID: id, Username: username, Email: email, EmailVerified: verified}
}

// SubjectFixture is a minimal subject row returned by NewSubject.
type SubjectFixture struct {
	ID      int64
	OwnerID int64
	Name    string
}

// NewSubject inserts a private subject owned by ownerID with an auto-generated name.
func NewSubject(t *testing.T, pool *pgxpool.Pool, ownerID int64) *SubjectFixture {
	t.Helper()
	return insertSubject(t, pool, ownerID, nextName("subj"), "private")
}

// NewSubjectNamed inserts a subject with an explicit name and visibility.
func NewSubjectNamed(t *testing.T, pool *pgxpool.Pool, ownerID int64, name, visibility string) *SubjectFixture {
	t.Helper()
	return insertSubject(t, pool, ownerID, name, visibility)
}

func insertSubject(t *testing.T, pool *pgxpool.Pool, ownerID int64, name, visibility string) *SubjectFixture {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
        INSERT INTO subjects (owner_id, name, visibility)
        VALUES ($1, $2, $3)
        RETURNING id
    `, ownerID, name, visibility).Scan(&id)
	if err != nil {
		t.Fatalf("insert subject: %v", err)
	}
	return &SubjectFixture{ID: id, OwnerID: ownerID, Name: name}
}

// NewChapter inserts a chapter under the subject.
func NewChapter(t *testing.T, pool *pgxpool.Pool, subjectID int64, title string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
        INSERT INTO chapters (subject_id, title)
        VALUES ($1, $2)
        RETURNING id
    `, subjectID, title).Scan(&id)
	if err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	return id
}

// NewFlashcard inserts a flashcard under the subject (chapter optional; pass 0 for null).
func NewFlashcard(t *testing.T, pool *pgxpool.Pool, subjectID, chapterID int64, q, a string) int64 {
	t.Helper()
	var id int64
	var chPtr *int64
	if chapterID > 0 {
		chPtr = &chapterID
	}
	err := pool.QueryRow(context.Background(), `
        INSERT INTO flashcards (subject_id, chapter_id, question, answer)
        VALUES ($1, $2, $3, $4)
        RETURNING id
    `, subjectID, chPtr, q, a).Scan(&id)
	if err != nil {
		t.Fatalf("insert flashcard: %v", err)
	}
	return id
}

// GiveAIAccess inserts an active user_subscriptions row so user_has_ai_access returns true.
func GiveAIAccess(t *testing.T, pool *pgxpool.Pool, uid int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
        INSERT INTO user_subscriptions (user_id, plan, status, current_period_end)
        VALUES ($1, 'pro_monthly', 'active', now() + interval '30 days')
    `, uid)
	if err != nil {
		t.Fatalf("give AI access: %v", err)
	}
}

// ExhaustQuota bumps the named counter to 10_000 for today.
func ExhaustQuota(t *testing.T, pool *pgxpool.Pool, uid int64, column string) {
	t.Helper()
	if !isKnownQuotaColumn(column) {
		t.Fatalf("unknown quota column %q", column)
	}
	sql := fmt.Sprintf(`
        INSERT INTO ai_quota_daily (user_id, day, %[1]s)
        VALUES ($1, current_date, 10000)
        ON CONFLICT (user_id, day) DO UPDATE SET %[1]s = 10000
    `, column)
	if _, err := pool.Exec(context.Background(), sql, uid); err != nil {
		t.Fatalf("exhaust quota: %v", err)
	}
}

func isKnownQuotaColumn(col string) bool {
	switch col {
	case "prompt_calls", "pdf_calls", "pdf_pages", "check_calls",
		"plan_calls", "cross_subject_rank_calls", "quiz_calls",
		"extract_keywords_calls":
		return true
	}
	return false
}

// Now returns a fixed clock value used in time-sensitive fixtures.
func Now() time.Time { return time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC) }
```

- [ ] **Step 3: Write `email.go`**

Create `testutil/email.go`:
```go
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
```

- [ ] **Step 4: Write `ai.go`**

Create `testutil/ai.go`:
```go
package testutil

import (
	"context"

	"studbud/backend/internal/aiProvider"
)

// FakeAIClient replays a fixed sequence of chunks.
type FakeAIClient struct {
	Chunks []aiProvider.Chunk
	Err    error
}

// Stream returns a closed channel with the configured chunks.
func (f *FakeAIClient) Stream(ctx context.Context, req aiProvider.Request) (<-chan aiProvider.Chunk, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	out := make(chan aiProvider.Chunk, len(f.Chunks))
	for _, c := range f.Chunks {
		out <- c
	}
	close(out)
	return out, nil
}
```

- [ ] **Step 5: Write `stripe.go`**

Create `testutil/stripe.go`:
```go
package testutil

import (
	"context"

	"studbud/backend/internal/billing"
)

// FakeBilling is a test double for billing.Client.
type FakeBilling struct {
	CheckoutURL string
	PortalURL   string
	WebhookErr  error
}

// CreateCheckout returns a canned URL.
func (f *FakeBilling) CreateCheckout(ctx context.Context, uid int64, priceID string) (*billing.CheckoutSession, error) {
	return &billing.CheckoutSession{URL: f.CheckoutURL, ID: "cs_test"}, nil
}

// CreatePortal returns a canned URL.
func (f *FakeBilling) CreatePortal(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	return f.PortalURL, nil
}

// VerifyWebhook returns the configured error (nil means valid).
func (f *FakeBilling) VerifyWebhook(payload []byte, signature string) error {
	return f.WebhookErr
}
```

- [ ] **Step 6: Write `clock.go`**

Create `testutil/clock.go`:
```go
package testutil

import "time"

// FakeClock returns a fixed time. Inject into services that take a now() func.
type FakeClock struct {
	T time.Time
}

// Now returns the configured instant.
func (c *FakeClock) Now() time.Time { return c.T }

// Advance moves the clock forward by d.
func (c *FakeClock) Advance(d time.Duration) { c.T = c.T.Add(d) }
```

- [ ] **Step 7: Verify build**

Run: `go build ./testutil/...`

Expected: succeeds.

- [ ] **Step 8: Commit**

```bash
git add testutil/
git commit -m "$(cat <<'EOF'
Test scaffolding

[+] testutil.OpenTestDB + Reset (TRUNCATE every table)
[+] MustTestEnv guard: refuses non-studbud_test DSN
[+] NewUser, NewVerifiedUser, NewSubject, NewChapter, NewFlashcard
[+] GiveAIAccess, ExhaustQuota
[+] EmailRecorder, FakeAIClient, FakeBilling, FakeClock
EOF
)"
```

---

## Phase 4 — Core Domain Packages

### Task 20: `pkg/access/` — entitlement + subject access gate

**Files:**
- Create: `pkg/access/service.go`
- Create: `pkg/access/service_test.go`

- [ ] **Step 1: Write failing tests**

Create `pkg/access/service_test.go`:
```go
package access

import (
	"context"
	"testing"

	"studbud/backend/testutil"
)

func TestHasAIAccessFalseByDefault(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	svc := NewService(pool)
	ok, err := svc.HasAIAccess(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("HasAIAccess: %v", err)
	}
	if ok {
		t.Fatal("expected no AI access for fresh user")
	}
}

func TestHasAIAccessTrueAfterSubscription(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)
	testutil.GiveAIAccess(t, pool, u.ID)

	svc := NewService(pool)
	ok, err := svc.HasAIAccess(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("HasAIAccess: %v", err)
	}
	if !ok {
		t.Fatal("expected AI access after giving subscription")
	}
}

func TestSubjectAccessOwnerCanManage(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubject(t, pool, owner.ID)

	svc := NewService(pool)
	lvl, err := svc.SubjectLevel(context.Background(), owner.ID, sub.ID)
	if err != nil {
		t.Fatalf("SubjectLevel: %v", err)
	}
	if lvl != LevelOwner {
		t.Fatalf("level = %v, want Owner", lvl)
	}
}

func TestSubjectAccessStrangerIsNone(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	owner := testutil.NewVerifiedUser(t, pool)
	stranger := testutil.NewVerifiedUser(t, pool)
	sub := testutil.NewSubject(t, pool, owner.ID)

	svc := NewService(pool)
	lvl, err := svc.SubjectLevel(context.Background(), stranger.ID, sub.ID)
	if err != nil {
		t.Fatalf("SubjectLevel: %v", err)
	}
	if lvl != LevelNone {
		t.Fatalf("level = %v, want None", lvl)
	}
}
```

- [ ] **Step 2: Verify test fails**

Run: `ENV=test DATABASE_URL=postgres://postgres@localhost:5432/studbud_test?sslmode=disable go test ./pkg/access/...`

Expected: FAIL with undefined symbols.

- [ ] **Step 3: Implement service**

Create `pkg/access/service.go`:
```go
package access

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Level is the caller's effective access level on a subject.
type Level int

const (
	LevelNone   Level = iota // LevelNone means no access
	LevelViewer              // LevelViewer can read
	LevelEditor              // LevelEditor can modify chapters/flashcards
	LevelOwner               // LevelOwner can manage the subject itself
)

// CanRead reports whether the level is allowed to read the subject.
func (l Level) CanRead() bool { return l >= LevelViewer }

// CanEdit reports whether the level is allowed to modify chapters / flashcards.
func (l Level) CanEdit() bool { return l >= LevelEditor }

// CanManage reports whether the level is allowed to modify the subject itself
// (rename, delete, share, manage collaborators).
func (l Level) CanManage() bool { return l >= LevelOwner }

// Service answers AI-entitlement and per-subject access questions.
type Service struct {
	db *pgxpool.Pool
}

// NewService constructs the access service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// HasAIAccess returns true when the user has an active AI subscription.
// Source of truth is the user_has_ai_access SQL function.
func (s *Service) HasAIAccess(ctx context.Context, uid int64) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT user_has_ai_access($1)`, uid).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check ai access:\n%w", err)
	}
	return ok, nil
}

// SubjectLevel resolves the caller's effective access level on a subject.
// Resolution: owner → collaborator role → friend-of-owner (if visibility='friends')
//           → subscriber (if visibility='public') → none.
func (s *Service) SubjectLevel(ctx context.Context, uid, subjectID int64) (Level, error) {
	lvl, err := s.ownerLevel(ctx, uid, subjectID)
	if err != nil || lvl != LevelNone {
		return lvl, err
	}
	lvl, err = s.collaboratorLevel(ctx, uid, subjectID)
	if err != nil || lvl != LevelNone {
		return lvl, err
	}
	return s.visibilityLevel(ctx, uid, subjectID)
}

func (s *Service) ownerLevel(ctx context.Context, uid, subjectID int64) (Level, error) {
	var ownerID int64
	err := s.db.QueryRow(ctx, `SELECT owner_id FROM subjects WHERE id = $1`, subjectID).Scan(&ownerID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return LevelNone, nil
		}
		return LevelNone, fmt.Errorf("load subject owner:\n%w", err)
	}
	if ownerID == uid {
		return LevelOwner, nil
	}
	return LevelNone, nil
}

func (s *Service) collaboratorLevel(ctx context.Context, uid, subjectID int64) (Level, error) {
	var role string
	err := s.db.QueryRow(ctx,
		`SELECT role FROM collaborators WHERE subject_id = $1 AND user_id = $2`,
		subjectID, uid).Scan(&role)
	if err == pgx.ErrNoRows {
		return LevelNone, nil
	}
	if err != nil {
		return LevelNone, fmt.Errorf("load collaborator:\n%w", err)
	}
	if role == "editor" {
		return LevelEditor, nil
	}
	return LevelViewer, nil
}

func (s *Service) visibilityLevel(ctx context.Context, uid, subjectID int64) (Level, error) {
	var visibility string
	var ownerID int64
	err := s.db.QueryRow(ctx,
		`SELECT visibility, owner_id FROM subjects WHERE id = $1`,
		subjectID).Scan(&visibility, &ownerID)
	if err != nil {
		return LevelNone, fmt.Errorf("load subject visibility:\n%w", err)
	}
	switch visibility {
	case "public":
		return LevelViewer, nil
	case "friends":
		if isFriend, err := s.isFriend(ctx, uid, ownerID); err != nil {
			return LevelNone, err
		} else if isFriend {
			return LevelViewer, nil
		}
	}
	return LevelNone, nil
}

func (s *Service) isFriend(ctx context.Context, a, b int64) (bool, error) {
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT count(*) FROM friendships
        WHERE status = 'accepted' AND (
          (sender_id = $1 AND receiver_id = $2) OR
          (sender_id = $2 AND receiver_id = $1)
        )`, a, b).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check friendship:\n%w", err)
	}
	return n > 0, nil
}
```

- [ ] **Step 4: Run tests**

Run: `ENV=test DATABASE_URL=postgres://postgres@localhost:5432/studbud_test?sslmode=disable go test ./pkg/access/... -v`

Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/access/
git commit -m "$(cat <<'EOF'
Access service

[+] Service.HasAIAccess calls user_has_ai_access SQL function
[+] Service.SubjectLevel resolves owner → collaborator → visibility → none
[+] Level enum: None / Viewer / Editor / Owner
[+] Tests for AI access and subject-level resolution
EOF
)"
```

---

### Task 21: `pkg/user/` + `pkg/emailverification/` + handlers

**Files:**
- Create: `pkg/user/model.go`
- Create: `pkg/user/queries.go`
- Create: `pkg/user/service.go`
- Create: `pkg/user/service_test.go`
- Create: `pkg/emailverification/service.go`
- Create: `pkg/emailverification/service_test.go`
- Create: `api/handler/user.go`
- Create: `api/handler/email_verification.go`

- [ ] **Step 1: Write `pkg/user/model.go`**

Create `pkg/user/model.go`:
```go
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
```

- [ ] **Step 2: Write `pkg/user/queries.go`**

Create `pkg/user/queries.go`:
```go
package user

const (
	qInsertUser = `
        INSERT INTO users (username, email, password_hash)
        VALUES ($1, $2, $3)
        RETURNING id, created_at
    `

	qFindByIdentifier = `
        SELECT id, username, email, password_hash, email_verified, is_admin, created_at
        FROM users
        WHERE username = $1 OR email = $1
    `

	qFindByID = `
        SELECT id, username, email, email_verified, is_admin, created_at
        FROM users
        WHERE id = $1
    `

	qSetProfilePicture = `
        UPDATE users SET profile_picture_image_id = $2, updated_at = now()
        WHERE id = $1
    `

	qStats = `
        SELECT
            COUNT(*)                                       AS total,
            COUNT(*) FILTER (WHERE f.last_result = 2)      AS good,
            COUNT(*) FILTER (WHERE f.last_result = 1)      AS ok,
            COUNT(*) FILTER (WHERE f.last_result = 0)      AS bad,
            COUNT(*) FILTER (WHERE f.last_result = -1)     AS new_count
        FROM flashcards f
        JOIN subjects s ON s.id = f.subject_id
        WHERE s.owner_id = $1
    `

	qAchievementProgress = `
        SELECT
            (SELECT COUNT(*) FROM unlocked_achievements WHERE user_id = $1),
            12
    `
)
```

- [ ] **Step 3: Write `pkg/user/service.go`**

Create `pkg/user/service.go`:
```go
package user

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/internal/myErrors"
)

// Service owns user register, login, profile-picture, and stats.
type Service struct {
	db     *pgxpool.Pool
	signer *jwtsigner.Signer
}

// NewService constructs the user service.
func NewService(db *pgxpool.Pool, signer *jwtsigner.Signer) *Service {
	return &Service{db: db, signer: signer}
}

// Register creates a new user and returns a signed JWT.
func (s *Service) Register(ctx context.Context, in RegisterInput) (string, int64, error) {
	if err := validateRegister(in); err != nil {
		return "", 0, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return "", 0, fmt.Errorf("bcrypt hash:\n%w", err)
	}
	var id int64
	err = s.db.QueryRow(ctx, qInsertUser, in.Username, strings.ToLower(in.Email), string(hash)).
		Scan(&id, new(any))
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			return "", 0, fmt.Errorf("username or email taken:\n%w", myErrors.ErrConflict)
		}
		return "", 0, fmt.Errorf("insert user:\n%w", err)
	}
	tok, err := s.signer.Sign(jwtsigner.Claims{UID: id, EmailVerified: false})
	if err != nil {
		return "", 0, err
	}
	return tok, id, nil
}

// Login authenticates and returns a signed JWT.
func (s *Service) Login(ctx context.Context, in LoginInput) (string, error) {
	row := s.db.QueryRow(ctx, qFindByIdentifier, in.Identifier)
	var (
		id       int64
		username string
		email    string
		hash     string
		verified bool
		admin    bool
		created  any
	)
	err := row.Scan(&id, &username, &email, &hash, &verified, &admin, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("no such user:\n%w", myErrors.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("find user:\n%w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		return "", fmt.Errorf("bad password:\n%w", myErrors.ErrUnauthenticated)
	}
	return s.signer.Sign(jwtsigner.Claims{UID: id, EmailVerified: verified})
}

// ByID returns the user row.
func (s *Service) ByID(ctx context.Context, uid int64) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(ctx, qFindByID, uid).
		Scan(&u.ID, &u.Username, &u.Email, &u.EmailVerified, &u.IsAdmin, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("user %d:\n%w", uid, myErrors.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load user:\n%w", err)
	}
	return u, nil
}

// IsAdmin returns whether the user is flagged is_admin.
func (s *Service) IsAdmin(ctx context.Context, uid int64) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT is_admin FROM users WHERE id = $1`, uid).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check admin:\n%w", err)
	}
	return ok, nil
}

// SetProfilePicture sets the user's profile_picture_image_id (image must be owned by user).
func (s *Service) SetProfilePicture(ctx context.Context, uid int64, imageID string) error {
	var ownerID int64
	err := s.db.QueryRow(ctx, `SELECT owner_id FROM images WHERE id = $1`, imageID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("image %s:\n%w", imageID, myErrors.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("load image:\n%w", err)
	}
	if ownerID != uid {
		return fmt.Errorf("image not owned by user:\n%w", myErrors.ErrForbidden)
	}
	if _, err := s.db.Exec(ctx, qSetProfilePicture, uid, imageID); err != nil {
		return fmt.Errorf("set profile picture:\n%w", err)
	}
	return nil
}

// Stats computes aggregate mastery for the user's owned subjects.
func (s *Service) Stats(ctx context.Context, uid int64) (*UserStatsResponse, error) {
	out := &UserStatsResponse{}
	err := s.db.QueryRow(ctx, qStats, uid).
		Scan(&out.TotalCards, &out.GoodCount, &out.OkCount, &out.BadCount, &out.NewCount)
	if err != nil {
		return nil, fmt.Errorf("stats query:\n%w", err)
	}
	out.CardsStudied = out.TotalCards - out.NewCount
	if out.TotalCards > 0 {
		out.MasteryPercent = (float64(out.GoodCount) + float64(out.OkCount)*0.5) / float64(out.TotalCards)
	}
	if err := s.db.QueryRow(ctx, qAchievementProgress, uid).
		Scan(&out.BadgesUnlocked, &out.BadgesTotal); err != nil {
		return nil, fmt.Errorf("achievement progress:\n%w", err)
	}
	return out, nil
}

func validateRegister(in RegisterInput) error {
	if in.Username == "" || in.Email == "" || in.Password == "" {
		return fmt.Errorf("username, email, and password are required:\n%w", myErrors.ErrValidation)
	}
	if !strings.Contains(in.Email, "@") {
		return fmt.Errorf("invalid email:\n%w", myErrors.ErrValidation)
	}
	if len(in.Password) < 8 {
		return fmt.Errorf("password must be at least 8 chars:\n%w", myErrors.ErrValidation)
	}
	return nil
}
```

- [ ] **Step 4: Write `pkg/user/service_test.go`**

Create `pkg/user/service_test.go`:
```go
package user

import (
	"context"
	"errors"
	"testing"
	"time"

	jwtsigner "studbud/backend/internal/jwt"
	"studbud/backend/internal/myErrors"
	"studbud/backend/testutil"
)

func TestRegisterAndLogin(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	signer := jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour)
	svc := NewService(pool, signer)

	tok, uid, err := svc.Register(context.Background(), RegisterInput{
		Username: "alice", Email: "alice@example.com", Password: "password123",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if tok == "" || uid == 0 {
		t.Fatal("empty token or uid")
	}

	tok2, err := svc.Login(context.Background(), LoginInput{Identifier: "alice", Password: "password123"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok2 == "" {
		t.Fatal("empty login token")
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	svc := NewService(pool, jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour))

	_, _, err := svc.Register(context.Background(), RegisterInput{Username: "a", Email: "a@x.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Register(context.Background(), RegisterInput{Username: "a", Email: "b@x.com", Password: "password123"})
	if !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	svc := NewService(pool, jwtsigner.NewSigner("a-minimum-32-byte-secret-xxxxxxxxxx", "studbud", time.Hour))
	_, _, _ = svc.Register(context.Background(), RegisterInput{Username: "alice", Email: "alice@x.com", Password: "password123"})

	_, err := svc.Login(context.Background(), LoginInput{Identifier: "alice", Password: "wrongpass"})
	if !errors.Is(err, myErrors.ErrUnauthenticated) {
		t.Fatalf("want ErrUnauthenticated, got %v", err)
	}
}
```

- [ ] **Step 5: Run tests**

Run: `ENV=test DATABASE_URL=postgres://postgres@localhost:5432/studbud_test?sslmode=disable go test ./pkg/user/... -v`

Expected: all three tests PASS.

- [ ] **Step 6: Write `pkg/emailverification/service.go`**

Create `pkg/emailverification/service.go`:
```go
package emailverification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/email"
	"studbud/backend/internal/myErrors"
)

// Service owns verification token issuance, verification, and throttling.
type Service struct {
	db          *pgxpool.Pool
	mailer      email.Sender
	frontendURL string
	ttl         time.Duration
}

// NewService constructs the email verification service.
func NewService(db *pgxpool.Pool, mailer email.Sender, frontendURL string) *Service {
	return &Service{db: db, mailer: mailer, frontendURL: frontendURL, ttl: 48 * time.Hour}
}

// Issue creates a token and sends the verification email.
// Rate-limited to 1 per 60 seconds per user.
func (s *Service) Issue(ctx context.Context, uid int64, recipient string) error {
	if err := s.checkThrottle(ctx, uid); err != nil {
		return err
	}
	tok := newToken()
	if _, err := s.db.Exec(ctx, `
        INSERT INTO email_verifications (user_id, token, expires_at)
        VALUES ($1, $2, $3)
    `, uid, tok, time.Now().Add(s.ttl)); err != nil {
		return fmt.Errorf("insert verification token:\n%w", err)
	}
	if err := s.touchThrottle(ctx, uid); err != nil {
		return err
	}
	link := s.frontendURL + "/verify-email?token=" + tok
	return s.mailer.Send(recipient, "Verify your email",
		"Click to verify your StudBud account: "+link)
}

// Verify consumes a token and flips users.email_verified.
func (s *Service) Verify(ctx context.Context, token string) error {
	var uid int64
	var expires time.Time
	var used *time.Time
	err := s.db.QueryRow(ctx, `
        SELECT user_id, expires_at, used_at FROM email_verifications WHERE token = $1
    `, token).Scan(&uid, &expires, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("unknown token:\n%w", myErrors.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("load token:\n%w", err)
	}
	if used != nil {
		return fmt.Errorf("token already used:\n%w", myErrors.ErrAlreadyVerified)
	}
	if time.Now().After(expires) {
		return fmt.Errorf("token expired:\n%w", myErrors.ErrValidation)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx:\n%w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE users SET email_verified = true, verified_at = now(), updated_at = now() WHERE id = $1`, uid); err != nil {
		return fmt.Errorf("mark verified:\n%w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE email_verifications SET used_at = now() WHERE token = $1`, token); err != nil {
		return fmt.Errorf("mark token used:\n%w", err)
	}
	return tx.Commit(ctx)
}

func (s *Service) checkThrottle(ctx context.Context, uid int64) error {
	var last time.Time
	err := s.db.QueryRow(ctx,
		`SELECT last_sent FROM email_verification_throttle WHERE user_id = $1`, uid).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load throttle:\n%w", err)
	}
	if time.Since(last) < 60*time.Second {
		return fmt.Errorf("rate limit: wait 60s between sends:\n%w", myErrors.ErrValidation)
	}
	return nil
}

func (s *Service) touchThrottle(ctx context.Context, uid int64) error {
	_, err := s.db.Exec(ctx, `
        INSERT INTO email_verification_throttle (user_id, last_sent, send_count)
        VALUES ($1, now(), 1)
        ON CONFLICT (user_id) DO UPDATE
          SET last_sent = EXCLUDED.last_sent,
              send_count = email_verification_throttle.send_count + 1
    `, uid)
	if err != nil {
		return fmt.Errorf("touch throttle:\n%w", err)
	}
	return nil
}

func newToken() string {
	var buf [24]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
```

- [ ] **Step 7: Write `pkg/emailverification/service_test.go`**

Create `pkg/emailverification/service_test.go`:
```go
package emailverification

import (
	"context"
	"strings"
	"testing"

	"studbud/backend/testutil"
)

func TestIssueSendsEmail(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewUser(t, pool)
	rec := testutil.NewEmailRecorder()
	svc := NewService(pool, rec, "http://localhost:5173")

	if err := svc.Issue(context.Background(), u.ID, u.Email); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	sent := rec.Sent()
	if len(sent) != 1 {
		t.Fatalf("want 1 email, got %d", len(sent))
	}
	if !strings.Contains(sent[0].Body, "/verify-email?token=") {
		t.Fatalf("email missing token link: %q", sent[0].Body)
	}
}

func TestVerifyFlipsFlag(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewUser(t, pool)
	rec := testutil.NewEmailRecorder()
	svc := NewService(pool, rec, "http://localhost:5173")

	if err := svc.Issue(context.Background(), u.ID, u.Email); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	body := rec.Sent()[0].Body
	tok := body[strings.Index(body, "token=")+len("token="):]
	if err := svc.Verify(context.Background(), tok); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	var verified bool
	if err := pool.QueryRow(context.Background(),
		`SELECT email_verified FROM users WHERE id = $1`, u.ID).Scan(&verified); err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("user should be marked verified")
	}
}
```

- [ ] **Step 8: Run tests**

Run: `ENV=test DATABASE_URL=... go test ./pkg/emailverification/... -v`

Expected: both tests PASS.

- [ ] **Step 9: Write `api/handler/user.go`**

Create `api/handler/user.go`:
```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/pkg/emailverification"
	"studbud/backend/pkg/user"
)

// UserHandler wires HTTP routes for user-scope operations.
type UserHandler struct {
	svc      *user.Service
	verifier *emailverification.Service
}

// NewUserHandler constructs the handler.
func NewUserHandler(svc *user.Service, verifier *emailverification.Service) *UserHandler {
	return &UserHandler{svc: svc, verifier: verifier}
}

// Register handles POST /user-register.
func (h *UserHandler) Register(w http.ResponseWriter, r *http.Request) {
	var in user.RegisterInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	tok, uid, err := h.svc.Register(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.verifier.Issue(r.Context(), uid, in.Email); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, user.TokenResponse{Token: tok})
}

// Login handles POST /user-login.
func (h *UserHandler) Login(w http.ResponseWriter, r *http.Request) {
	var in user.LoginInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	tok, err := h.svc.Login(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, user.TokenResponse{Token: tok})
}

// TestJWT handles POST /user-test-jwt (returns 201 on valid token).
func (h *UserHandler) TestJWT(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusCreated)
}

// SetProfilePicture handles POST /set-profile-picture.
func (h *UserHandler) SetProfilePicture(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	var in struct {
		ImageID string `json:"image_id"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.SetProfilePicture(r.Context(), uid, in.ImageID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "profile picture updated"})
}

// Stats handles GET /get-user-stats.
func (h *UserHandler) Stats(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	out, err := h.svc.Stats(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 10: Write `api/handler/email_verification.go`**

Create `api/handler/email_verification.go`:
```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/emailverification"
	"studbud/backend/pkg/user"
)

// EmailVerificationHandler handles verify + resend routes.
type EmailVerificationHandler struct {
	verifier *emailverification.Service
	users    *user.Service
}

// NewEmailVerificationHandler constructs the handler.
func NewEmailVerificationHandler(v *emailverification.Service, u *user.Service) *EmailVerificationHandler {
	return &EmailVerificationHandler{verifier: v, users: u}
}

// Verify handles GET /verify-email?token=...
func (h *EmailVerificationHandler) Verify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.verifier.Verify(r.Context(), token); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "email verified successfully"})
}

// Resend handles POST /resend-verification (auth only, no RequireVerified).
func (h *EmailVerificationHandler) Resend(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	u, err := h.users.ByID(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if u.EmailVerified {
		httpx.WriteError(w, myErrors.ErrAlreadyVerified)
		return
	}
	if err := h.verifier.Issue(r.Context(), uid, u.Email); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"message": "verification email sent"})
}
```

- [ ] **Step 11: Verify build**

Run: `go build ./...`

Expected: succeeds.

- [ ] **Step 12: Commit**

```bash
git add pkg/user/ pkg/emailverification/ api/handler/user.go api/handler/email_verification.go
git commit -m "$(cat <<'EOF'
User + email-verification domains

[+] user.Service: Register / Login / ByID / IsAdmin / SetProfilePicture / Stats
[+] Bcrypt password hashing, JWT issuance on register/login
[+] emailverification.Service: Issue / Verify with 60s throttle + 48h TTL
[+] UserHandler: Register / Login / TestJWT / SetProfilePicture / Stats
[+] EmailVerificationHandler: Verify / Resend
[+] Tests for register, login, duplicate, bad password, email issue+verify
EOF
)"
```

---

### Task 22: `pkg/image/` + handler

**Files:**
- Create: `pkg/image/service.go`
- Create: `pkg/image/service_test.go`
- Create: `api/handler/image.go`

- [ ] **Step 1: Write `service.go`**

Create `pkg/image/service.go`:
```go
package image

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/internal/storage"
)

// Image represents an uploaded image row.
type Image struct {
	ID       string
	OwnerID  int64
	Filename string
	MimeType string
	Bytes    int64
}

// Service owns upload, fetch, and delete for images.
type Service struct {
	db         *pgxpool.Pool
	store      *storage.FileStore
	backendURL string
}

// NewService constructs the image service.
func NewService(db *pgxpool.Pool, store *storage.FileStore, backendURL string) *Service {
	return &Service{db: db, store: store, backendURL: backendURL}
}

// Upload reads src, detects content type, writes to storage, and records the DB row.
func (s *Service) Upload(ctx context.Context, uid int64, src io.Reader, filename string) (*Image, error) {
	sniff := make([]byte, 512)
	n, _ := io.ReadFull(src, sniff)
	mime := http.DetectContentType(sniff[:n])
	if !isAllowedImage(mime) {
		return nil, fmt.Errorf("unsupported mime type %q:\n%w", mime, myErrors.ErrValidation)
	}
	id := storage.NewImageID()
	diskName := id + extensionFor(mime)
	full := io.MultiReader(io.NewSectionReader(newBufReaderAt(sniff[:n]), 0, int64(n)), src)
	path, err := s.store.Write(diskName, full)
	if err != nil {
		return nil, err
	}
	size, err := fileSize(path)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `
        INSERT INTO images (id, owner_id, filename, mime_type, bytes)
        VALUES ($1, $2, $3, $4, $5)
    `, id, uid, diskName, mime, size)
	if err != nil {
		_ = s.store.Remove(diskName)
		return nil, fmt.Errorf("insert image:\n%w", err)
	}
	return &Image{ID: id, OwnerID: uid, Filename: diskName, MimeType: mime, Bytes: size}, nil
}

// Open returns an io.ReadCloser for the image and its mime type.
func (s *Service) Open(ctx context.Context, id string) (io.ReadCloser, string, error) {
	img, err := s.byID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	f, err := s.store.Open(img.Filename)
	if err != nil {
		return nil, "", err
	}
	return f, img.MimeType, nil
}

// Delete removes the image row and file if owned by uid.
func (s *Service) Delete(ctx context.Context, uid int64, id string) error {
	img, err := s.byID(ctx, id)
	if err != nil {
		return err
	}
	if img.OwnerID != uid {
		return fmt.Errorf("not owner:\n%w", myErrors.ErrForbidden)
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM images WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete image row:\n%w", err)
	}
	return s.store.Remove(img.Filename)
}

// URL returns the public fetch URL for an image ID.
func (s *Service) URL(id string) string {
	return s.backendURL + "/images/" + id
}

func (s *Service) byID(ctx context.Context, id string) (*Image, error) {
	img := &Image{}
	err := s.db.QueryRow(ctx,
		`SELECT id, owner_id, filename, mime_type, bytes FROM images WHERE id = $1`, id).
		Scan(&img.ID, &img.OwnerID, &img.Filename, &img.MimeType, &img.Bytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("image %s:\n%w", id, myErrors.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load image:\n%w", err)
	}
	return img, nil
}

func isAllowedImage(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

func extensionFor(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ""
}
```

- [ ] **Step 2: Write `pkg/image/helpers.go` for buf readerAt + size**

Create `pkg/image/helpers.go`:
```go
package image

import (
	"bytes"
	"io"
	"os"
)

type bufReaderAt struct {
	*bytes.Reader
}

func newBufReaderAt(b []byte) io.ReaderAt {
	return &bufReaderAt{bytes.NewReader(b)}
}

func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
```

- [ ] **Step 3: Write `service_test.go`**

Create `pkg/image/service_test.go`:
```go
package image

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"studbud/backend/internal/storage"
	"studbud/backend/testutil"
)

// 1x1 PNG (red pixel).
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00,
	0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x5B, 0x2F, 0xC0, 0x0F, 0x00, 0x00, 0x00,
	0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func TestUploadOpenDelete(t *testing.T) {
	pool := testutil.OpenTestDB(t)
	testutil.Reset(t, pool)
	u := testutil.NewVerifiedUser(t, pool)

	dir, err := os.MkdirTemp("", "imgtest-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, _ := storage.NewFileStore(dir)
	svc := NewService(pool, store, "http://localhost:8080")

	img, err := svc.Upload(context.Background(), u.ID, bytes.NewReader(pngBytes), "pic.png")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if img.MimeType != "image/png" {
		t.Fatalf("mime = %q, want image/png", img.MimeType)
	}

	rc, mime, err := svc.Open(context.Background(), img.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if mime != "image/png" || len(b) == 0 {
		t.Fatalf("Open returned bad data")
	}

	if err := svc.Delete(context.Background(), u.ID, img.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := svc.Open(context.Background(), img.ID); err == nil {
		t.Fatal("expected error after Delete")
	}
}
```

- [ ] **Step 4: Write `api/handler/image.go`**

Create `api/handler/image.go`:
```go
package handler

import (
	"io"
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/image"
)

// ImageHandler handles upload / serve / delete.
type ImageHandler struct {
	svc *image.Service
}

// NewImageHandler constructs the handler.
func NewImageHandler(svc *image.Service) *ImageHandler {
	return &ImageHandler{svc: svc}
}

// Upload handles POST /upload-image (multipart).
func (h *ImageHandler) Upload(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	defer file.Close()
	img, err := h.svc.Upload(r.Context(), uid, file, hdr.Filename)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"id": img.ID, "url": h.svc.URL(img.ID)})
}

// Serve handles GET /images/{id}.
func (h *ImageHandler) Serve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rc, mime, err := h.svc.Open(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.Copy(w, rc)
}

// Delete handles POST /delete-image?id=...
func (h *ImageHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := authctx.UID(r.Context())
	id := r.URL.Query().Get("id")
	if id == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.Delete(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 5: Run tests + build**

Run:
```bash
ENV=test DATABASE_URL=... go test ./pkg/image/... -v
go build ./...
```

Expected: tests PASS, build succeeds.

- [ ] **Step 6: Commit**

```bash
git add pkg/image/ api/handler/image.go
git commit -m "$(cat <<'EOF'
Image domain

[+] image.Service: Upload (mime sniff + allowed-type check) / Open / Delete
[+] ImageHandler: upload (multipart), serve (public), delete (owner only)
[+] Test: upload+open+delete round-trip with real PNG bytes
EOF
)"
```

---

### Task 23: Subject domain

**Files:**
- Create: `pkg/subject/model.go`
- Create: `pkg/subject/service.go`
- Create: `pkg/subject/service_test.go`
- Create: `api/handler/subject.go`

- [ ] **Step 1: Write `pkg/subject/model.go`**

```go
package subject

import "time"

// Subject represents a study subject owned by a user.
type Subject struct {
	ID          int64      `json:"id"`           // ID is the subject's primary key
	OwnerID     int64      `json:"owner_id"`     // OwnerID is the user who created the subject
	Name        string     `json:"name"`         // Name is the subject's display name
	Color       string     `json:"color"`        // Color is a hex code used by the UI
	Icon        string     `json:"icon"`         // Icon is an emoji or icon identifier
	Tags        string     `json:"tags"`         // Tags is a space-separated tag list
	Visibility  string     `json:"visibility"`   // Visibility is one of private|friends|public
	Archived    bool       `json:"archived"`     // Archived hides the subject from active lists
	Description string     `json:"description"`  // Description is a short free-text summary
	LastUsed    *time.Time `json:"last_used"`    // LastUsed stores the last training timestamp
	CreatedAt   time.Time  `json:"created_at"`   // CreatedAt stores creation time
	UpdatedAt   time.Time  `json:"updated_at"`   // UpdatedAt stores last update time
}

// CreateInput is the payload to create a subject.
type CreateInput struct {
	Name        string `json:"name"`        // Name is the subject's display name
	Color       string `json:"color"`       // Color is an optional hex code
	Icon        string `json:"icon"`        // Icon is an optional emoji
	Tags        string `json:"tags"`        // Tags is an optional tag list
	Visibility  string `json:"visibility"`  // Visibility is private|friends|public (default private)
	Description string `json:"description"` // Description is optional
}

// UpdateInput is the payload to update a subject. Nil fields are unchanged.
type UpdateInput struct {
	Name        *string `json:"name"`        // Name updates the display name when non-nil
	Color       *string `json:"color"`       // Color updates the color hex when non-nil
	Icon        *string `json:"icon"`        // Icon updates the icon when non-nil
	Tags        *string `json:"tags"`        // Tags updates the tag list when non-nil
	Visibility  *string `json:"visibility"`  // Visibility updates visibility when non-nil
	Description *string `json:"description"` // Description updates description when non-nil
	Archived    *bool   `json:"archived"`    // Archived updates archive flag when non-nil
}
```

- [ ] **Step 2: Write `pkg/subject/service.go`**

```go
package subject

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
)

// Service owns subject CRUD and listing.
type Service struct {
	db     *pgxpool.Pool // db is the shared connection pool
	access *access.Service // access resolves visibility and membership
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Create inserts a new subject owned by uid.
func (s *Service) Create(ctx context.Context, uid int64, in CreateInput) (*Subject, error) {
	if in.Name == "" {
		return nil, myErrors.ErrInvalidInput
	}
	vis := in.Visibility
	if vis == "" {
		vis = "private"
	}
	if vis != "private" && vis != "friends" && vis != "public" {
		return nil, myErrors.ErrInvalidInput
	}
	var sub Subject
	err := s.db.QueryRow(ctx, `
		INSERT INTO subjects (owner_id, name, color, icon, tags, visibility, description)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, owner_id, name, color, icon, tags, visibility, archived,
		          description, last_used, created_at, updated_at
	`, uid, in.Name, in.Color, in.Icon, in.Tags, vis, in.Description).Scan(
		&sub.ID, &sub.OwnerID, &sub.Name, &sub.Color, &sub.Icon, &sub.Tags,
		&sub.Visibility, &sub.Archived, &sub.Description, &sub.LastUsed,
		&sub.CreatedAt, &sub.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert subject:\n%w", err)
	}
	return &sub, nil
}

// Get fetches a subject if the user can read it.
func (s *Service) Get(ctx context.Context, uid, subjectID int64) (*Subject, error) {
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	return sub, nil
}

// ListOwned returns all subjects owned by uid, excluding archived when includeArchived=false.
func (s *Service) ListOwned(ctx context.Context, uid int64, includeArchived bool) ([]Subject, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, owner_id, name, color, icon, tags, visibility, archived,
		       description, last_used, created_at, updated_at
		FROM subjects
		WHERE owner_id = $1 AND ($2 OR archived = false)
		ORDER BY last_used DESC NULLS LAST, id DESC
	`, uid, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("list subjects:\n%w", err)
	}
	defer rows.Close()
	return scanSubjects(rows)
}

// Update patches a subject; requires the caller to be owner.
func (s *Service) Update(ctx context.Context, uid, subjectID int64, in UpdateInput) (*Subject, error) {
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	if sub.OwnerID != uid {
		return nil, myErrors.ErrForbidden
	}
	name, color, icon, tags, vis, desc := sub.Name, sub.Color, sub.Icon, sub.Tags, sub.Visibility, sub.Description
	archived := sub.Archived
	if in.Name != nil {
		if *in.Name == "" {
			return nil, myErrors.ErrInvalidInput
		}
		name = *in.Name
	}
	if in.Color != nil {
		color = *in.Color
	}
	if in.Icon != nil {
		icon = *in.Icon
	}
	if in.Tags != nil {
		tags = *in.Tags
	}
	if in.Visibility != nil {
		v := *in.Visibility
		if v != "private" && v != "friends" && v != "public" {
			return nil, myErrors.ErrInvalidInput
		}
		vis = v
	}
	if in.Description != nil {
		desc = *in.Description
	}
	if in.Archived != nil {
		archived = *in.Archived
	}
	var out Subject
	err = s.db.QueryRow(ctx, `
		UPDATE subjects
		SET name=$1, color=$2, icon=$3, tags=$4, visibility=$5,
		    description=$6, archived=$7, updated_at=now()
		WHERE id=$8
		RETURNING id, owner_id, name, color, icon, tags, visibility, archived,
		          description, last_used, created_at, updated_at
	`, name, color, icon, tags, vis, desc, archived, subjectID).Scan(
		&out.ID, &out.OwnerID, &out.Name, &out.Color, &out.Icon, &out.Tags,
		&out.Visibility, &out.Archived, &out.Description, &out.LastUsed,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update subject:\n%w", err)
	}
	return &out, nil
}

// Delete removes a subject; requires the caller to be owner.
func (s *Service) Delete(ctx context.Context, uid, subjectID int64) error {
	sub, err := s.load(ctx, subjectID)
	if err != nil {
		return err
	}
	if sub.OwnerID != uid {
		return myErrors.ErrForbidden
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM subjects WHERE id=$1`, subjectID); err != nil {
		return fmt.Errorf("delete subject:\n%w", err)
	}
	return nil
}

// TouchLastUsed sets last_used=now() on the subject (called by training/quiz flows later).
func (s *Service) TouchLastUsed(ctx context.Context, subjectID int64) error {
	if _, err := s.db.Exec(ctx, `UPDATE subjects SET last_used = now() WHERE id = $1`, subjectID); err != nil {
		return fmt.Errorf("touch subject last_used:\n%w", err)
	}
	return nil
}

func (s *Service) load(ctx context.Context, id int64) (*Subject, error) {
	var sub Subject
	err := s.db.QueryRow(ctx, `
		SELECT id, owner_id, name, color, icon, tags, visibility, archived,
		       description, last_used, created_at, updated_at
		FROM subjects WHERE id=$1
	`, id).Scan(
		&sub.ID, &sub.OwnerID, &sub.Name, &sub.Color, &sub.Icon, &sub.Tags,
		&sub.Visibility, &sub.Archived, &sub.Description, &sub.LastUsed,
		&sub.CreatedAt, &sub.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load subject:\n%w", err)
	}
	return &sub, nil
}

func scanSubjects(rows pgx.Rows) ([]Subject, error) {
	var out []Subject
	for rows.Next() {
		var sub Subject
		if err := rows.Scan(
			&sub.ID, &sub.OwnerID, &sub.Name, &sub.Color, &sub.Icon, &sub.Tags,
			&sub.Visibility, &sub.Archived, &sub.Description, &sub.LastUsed,
			&sub.CreatedAt, &sub.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan subject:\n%w", err)
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}
```

- [ ] **Step 3: Write `pkg/subject/service_test.go`**

```go
package subject_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/access"
	"studbud/backend/pkg/subject"
	"studbud/backend/testutil"
)

func TestSubjectCRUD(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)
	owner := testutil.NewVerifiedUser(t, db)

	created, err := svc.Create(ctx, owner.ID, subject.CreateInput{Name: "Biology", Color: "#0a0"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.OwnerID != owner.ID || created.Name != "Biology" || created.Visibility != "private" {
		t.Fatalf("unexpected created subject: %+v", created)
	}

	got, err := svc.Get(ctx, owner.ID, created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("get: %v %+v", err, got)
	}

	newName := "Biology 101"
	updated, err := svc.Update(ctx, owner.ID, created.ID, subject.UpdateInput{Name: &newName})
	if err != nil || updated.Name != newName {
		t.Fatalf("update: %v %+v", err, updated)
	}

	list, err := svc.ListOwned(ctx, owner.ID, false)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	if err := svc.Delete(ctx, owner.ID, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestSubjectGet_ForbiddenForPrivate(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := subject.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)

	sub, err := svc.Create(ctx, owner.ID, subject.CreateInput{Name: "Secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, other.ID, sub.ID); err == nil {
		t.Fatal("expected forbidden for other user on private subject")
	}
}
```

- [ ] **Step 4: Write `api/handler/subject.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/subject"
)

// SubjectHandler exposes subject CRUD endpoints.
type SubjectHandler struct {
	svc *subject.Service // svc owns the business logic
}

// NewSubjectHandler constructs a SubjectHandler.
func NewSubjectHandler(svc *subject.Service) *SubjectHandler {
	return &SubjectHandler{svc: svc}
}

// Create handles POST /subject-create.
func (h *SubjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	var in subject.CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	sub, err := h.svc.Create(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sub)
}

// List handles GET /subject-list.
func (h *SubjectHandler) List(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	includeArchived := r.URL.Query().Get("archived") == "true"
	subs, err := h.svc.ListOwned(r.Context(), uid, includeArchived)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, subs)
}

// Get handles GET /subject?id=...
func (h *SubjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	sub, err := h.svc.Get(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sub)
}

// Update handles POST /subject-update?id=...
func (h *SubjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	var in subject.UpdateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	sub, err := h.svc.Update(r.Context(), uid, id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sub)
}

// Delete handles POST /subject-delete?id=...
func (h *SubjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.Delete(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

> **Note:** `httpx.QueryInt64(r, "id")` is a new helper — add it to `internal/httpx/query.go` now if it does not exist:
> ```go
> package httpx
>
> import (
>     "net/http"
>     "strconv"
>
>     "studbud/backend/internal/myErrors"
> )
>
> // QueryInt64 reads a required int64 query param by name.
> func QueryInt64(r *http.Request, name string) (int64, error) {
>     raw := r.URL.Query().Get(name)
>     if raw == "" {
>         return 0, myErrors.ErrInvalidInput
>     }
>     v, err := strconv.ParseInt(raw, 10, 64)
>     if err != nil {
>         return 0, myErrors.ErrInvalidInput
>     }
>     return v, nil
> }
> ```

- [ ] **Step 5: Run tests + build**

Run:
```bash
ENV=test DATABASE_URL=... go test ./pkg/subject/... -v
go build ./...
```

Expected: tests PASS, build succeeds.

- [ ] **Step 6: Commit**

```bash
git add pkg/subject/ api/handler/subject.go internal/httpx/query.go
git commit -m "$(cat <<'EOF'
Subject domain

[+] subject.Service: Create / Get / ListOwned / Update / Delete / TouchLastUsed
[+] SubjectHandler with 5 endpoints
[+] Uses AccessService.SubjectLevel for visibility-gated reads
[+] httpx.QueryInt64 helper
[+] Test: CRUD round-trip + forbidden-on-private
EOF
)"
```

---

### Task 24: Chapter domain

**Files:**
- Create: `pkg/chapter/model.go`
- Create: `pkg/chapter/service.go`
- Create: `pkg/chapter/service_test.go`
- Create: `api/handler/chapter.go`

- [ ] **Step 1: Write `pkg/chapter/model.go`**

```go
package chapter

import "time"

// Chapter groups flashcards inside a subject.
type Chapter struct {
	ID        int64     `json:"id"`         // ID is the chapter primary key
	SubjectID int64     `json:"subject_id"` // SubjectID is the owning subject
	Title     string    `json:"title"`      // Title is the chapter title
	Position  int       `json:"position"`   // Position orders chapters within a subject
	CreatedAt time.Time `json:"created_at"` // CreatedAt is the creation timestamp
	UpdatedAt time.Time `json:"updated_at"` // UpdatedAt is the last update timestamp
}

// CreateInput is the payload to create a chapter.
type CreateInput struct {
	SubjectID int64  `json:"subject_id"` // SubjectID is the parent subject
	Title     string `json:"title"`      // Title is the chapter title
}

// UpdateInput patches a chapter (nil = unchanged).
type UpdateInput struct {
	Title    *string `json:"title"`    // Title updates the title when non-nil
	Position *int    `json:"position"` // Position updates ordering when non-nil
}
```

- [ ] **Step 2: Write `pkg/chapter/service.go`**

```go
package chapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
)

// Service owns chapter CRUD.
type Service struct {
	db     *pgxpool.Pool   // db is the shared pool
	access *access.Service // access enforces subject-scoped permissions
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Create inserts a chapter; caller must have edit rights on the subject.
func (s *Service) Create(ctx context.Context, uid int64, in CreateInput) (*Chapter, error) {
	if in.Title == "" {
		return nil, myErrors.ErrInvalidInput
	}
	level, err := s.access.SubjectLevel(ctx, uid, in.SubjectID)
	if err != nil {
		return nil, err
	}
	if !level.CanEdit() {
		return nil, myErrors.ErrForbidden
	}
	var ch Chapter
	err = s.db.QueryRow(ctx, `
		INSERT INTO chapters (subject_id, title, position)
		VALUES ($1,$2, COALESCE((SELECT MAX(position)+1 FROM chapters WHERE subject_id=$1), 0))
		RETURNING id, subject_id, title, position, created_at, updated_at
	`, in.SubjectID, in.Title).Scan(
		&ch.ID, &ch.SubjectID, &ch.Title, &ch.Position, &ch.CreatedAt, &ch.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert chapter:\n%w", err)
	}
	return &ch, nil
}

// List returns chapters for a subject if caller can read it.
func (s *Service) List(ctx context.Context, uid, subjectID int64) ([]Chapter, error) {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, subject_id, title, position, created_at, updated_at
		FROM chapters WHERE subject_id=$1 ORDER BY position ASC, id ASC
	`, subjectID)
	if err != nil {
		return nil, fmt.Errorf("list chapters:\n%w", err)
	}
	defer rows.Close()
	var out []Chapter
	for rows.Next() {
		var ch Chapter
		if err := rows.Scan(&ch.ID, &ch.SubjectID, &ch.Title, &ch.Position, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chapter:\n%w", err)
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// Update patches a chapter; caller must have edit rights on its subject.
func (s *Service) Update(ctx context.Context, uid, id int64, in UpdateInput) (*Chapter, error) {
	ch, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureEdit(ctx, uid, ch.SubjectID); err != nil {
		return nil, err
	}
	title, pos := ch.Title, ch.Position
	if in.Title != nil {
		if *in.Title == "" {
			return nil, myErrors.ErrInvalidInput
		}
		title = *in.Title
	}
	if in.Position != nil {
		pos = *in.Position
	}
	var out Chapter
	err = s.db.QueryRow(ctx, `
		UPDATE chapters SET title=$1, position=$2, updated_at=now()
		WHERE id=$3
		RETURNING id, subject_id, title, position, created_at, updated_at
	`, title, pos, id).Scan(
		&out.ID, &out.SubjectID, &out.Title, &out.Position, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update chapter:\n%w", err)
	}
	return &out, nil
}

// Delete removes a chapter; caller must have edit rights on its subject.
func (s *Service) Delete(ctx context.Context, uid, id int64) error {
	ch, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if err := s.ensureEdit(ctx, uid, ch.SubjectID); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM chapters WHERE id=$1`, id); err != nil {
		return fmt.Errorf("delete chapter:\n%w", err)
	}
	return nil
}

func (s *Service) load(ctx context.Context, id int64) (*Chapter, error) {
	var ch Chapter
	err := s.db.QueryRow(ctx, `
		SELECT id, subject_id, title, position, created_at, updated_at
		FROM chapters WHERE id=$1
	`, id).Scan(&ch.ID, &ch.SubjectID, &ch.Title, &ch.Position, &ch.CreatedAt, &ch.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load chapter:\n%w", err)
	}
	return &ch, nil
}

func (s *Service) ensureEdit(ctx context.Context, uid, subjectID int64) error {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !level.CanEdit() {
		return myErrors.ErrForbidden
	}
	return nil
}
```

- [ ] **Step 3: Write `pkg/chapter/service_test.go`**

```go
package chapter_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/access"
	"studbud/backend/pkg/chapter"
	"studbud/backend/testutil"
)

func TestChapterCRUD(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := chapter.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	ch, err := svc.Create(ctx, owner.ID, chapter.CreateInput{SubjectID: sub.ID, Title: "Cells"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ch.Position != 0 {
		t.Fatalf("expected position=0, got %d", ch.Position)
	}

	list, err := svc.List(ctx, owner.ID, sub.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	newTitle := "Cell Biology"
	u, err := svc.Update(ctx, owner.ID, ch.ID, chapter.UpdateInput{Title: &newTitle})
	if err != nil || u.Title != newTitle {
		t.Fatalf("update: %v %+v", err, u)
	}

	if err := svc.Delete(ctx, owner.ID, ch.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestChapter_ForbiddenForStranger(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := chapter.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	stranger := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	if _, err := svc.Create(ctx, stranger.ID, chapter.CreateInput{SubjectID: sub.ID, Title: "x"}); err == nil {
		t.Fatal("expected forbidden")
	}
}
```

- [ ] **Step 4: Write `api/handler/chapter.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/chapter"
)

// ChapterHandler exposes chapter CRUD endpoints.
type ChapterHandler struct {
	svc *chapter.Service // svc owns the business logic
}

// NewChapterHandler constructs a ChapterHandler.
func NewChapterHandler(svc *chapter.Service) *ChapterHandler {
	return &ChapterHandler{svc: svc}
}

// Create handles POST /chapter-create.
func (h *ChapterHandler) Create(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	var in chapter.CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ch, err := h.svc.Create(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ch)
}

// List handles GET /chapter-list?subject_id=...
func (h *ChapterHandler) List(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	list, err := h.svc.List(r.Context(), uid, sid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// Update handles POST /chapter-update?id=...
func (h *ChapterHandler) Update(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	var in chapter.UpdateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ch, err := h.svc.Update(r.Context(), uid, id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ch)
}

// Delete handles POST /chapter-delete?id=...
func (h *ChapterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.Delete(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 5: Run tests + build**

```bash
ENV=test DATABASE_URL=... go test ./pkg/chapter/... -v
go build ./...
```

Expected: tests PASS, build succeeds.

- [ ] **Step 6: Commit**

```bash
git add pkg/chapter/ api/handler/chapter.go
git commit -m "$(cat <<'EOF'
Chapter domain

[+] chapter.Service: Create / List / Update / Delete with auto-increment position
[+] ChapterHandler with 4 endpoints
[+] Edit ops require CanEdit on the subject; reads require any access
EOF
)"
```

---

### Task 25: Flashcard domain

**Files:**
- Create: `pkg/flashcard/model.go`
- Create: `pkg/flashcard/service.go`
- Create: `pkg/flashcard/service_test.go`
- Create: `api/handler/flashcard.go`

- [ ] **Step 1: Write `pkg/flashcard/model.go`**

```go
package flashcard

import "time"

// Flashcard is a question/answer card belonging to a subject.
type Flashcard struct {
	ID         int64      `json:"id"`           // ID is the card primary key
	SubjectID  int64      `json:"subject_id"`   // SubjectID is the owning subject
	ChapterID  *int64     `json:"chapter_id"`   // ChapterID is the optional owning chapter
	Title      string     `json:"title"`        // Title is an optional short title
	Question   string     `json:"question"`     // Question is the front-side text
	Answer     string     `json:"answer"`       // Answer is the back-side text
	ImageID    *string    `json:"image_id"`     // ImageID is an optional attached image id
	Source     string     `json:"source"`       // Source is 'manual' or 'ai'
	DueAt      *time.Time `json:"due_at"`       // DueAt is the next review timestamp
	LastResult int        `json:"last_result"`  // LastResult is -1 (never) or 0..2 (training outcome)
	LastUsed   *time.Time `json:"last_used"`    // LastUsed is the last training timestamp
	CreatedAt  time.Time  `json:"created_at"`   // CreatedAt is creation time
	UpdatedAt  time.Time  `json:"updated_at"`   // UpdatedAt is last update time
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
```

- [ ] **Step 2: Write `pkg/flashcard/service.go`**

```go
package flashcard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
)

// Service owns flashcard CRUD and lightweight review tracking.
type Service struct {
	db     *pgxpool.Pool   // db is the shared pool
	access *access.Service // access enforces subject-scoped permissions
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Create inserts a flashcard.
func (s *Service) Create(ctx context.Context, uid int64, in CreateInput) (*Flashcard, error) {
	if in.Question == "" || in.Answer == "" {
		return nil, myErrors.ErrInvalidInput
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	if in.Source != "manual" && in.Source != "ai" {
		return nil, myErrors.ErrInvalidInput
	}
	if err := s.ensureEdit(ctx, uid, in.SubjectID); err != nil {
		return nil, err
	}
	fc, err := s.insert(ctx, in)
	if err != nil {
		return nil, err
	}
	return fc, nil
}

// Get returns a flashcard if caller can read its subject.
func (s *Service) Get(ctx context.Context, uid, id int64) (*Flashcard, error) {
	fc, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	level, err := s.access.SubjectLevel(ctx, uid, fc.SubjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	return fc, nil
}

// ListBySubject returns all flashcards in a subject (read access required).
func (s *Service) ListBySubject(ctx context.Context, uid, subjectID int64) ([]Flashcard, error) {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return nil, err
	}
	if level == access.LevelNone {
		return nil, myErrors.ErrForbidden
	}
	rows, err := s.db.Query(ctx, listBySubjectSQL, subjectID)
	if err != nil {
		return nil, fmt.Errorf("list flashcards:\n%w", err)
	}
	defer rows.Close()
	return scanAll(rows)
}

// Update patches a flashcard.
func (s *Service) Update(ctx context.Context, uid, id int64, in UpdateInput) (*Flashcard, error) {
	fc, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureEdit(ctx, uid, fc.SubjectID); err != nil {
		return nil, err
	}
	title, question, answer := fc.Title, fc.Question, fc.Answer
	chapterID, imageID := fc.ChapterID, fc.ImageID
	if in.Title != nil {
		title = *in.Title
	}
	if in.Question != nil {
		if *in.Question == "" {
			return nil, myErrors.ErrInvalidInput
		}
		question = *in.Question
	}
	if in.Answer != nil {
		if *in.Answer == "" {
			return nil, myErrors.ErrInvalidInput
		}
		answer = *in.Answer
	}
	if in.ChapterID != nil {
		chapterID = in.ChapterID
	}
	if in.ImageID != nil {
		imageID = in.ImageID
	}
	var out Flashcard
	err = s.db.QueryRow(ctx, `
		UPDATE flashcards
		SET chapter_id=$1, title=$2, question=$3, answer=$4, image_id=$5, updated_at=now()
		WHERE id=$6
		RETURNING id, subject_id, chapter_id, title, question, answer, image_id,
		          source, due_at, last_result, last_used, created_at, updated_at
	`, chapterID, title, question, answer, imageID, id).Scan(
		&out.ID, &out.SubjectID, &out.ChapterID, &out.Title, &out.Question, &out.Answer,
		&out.ImageID, &out.Source, &out.DueAt, &out.LastResult, &out.LastUsed,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update flashcard:\n%w", err)
	}
	return &out, nil
}

// Delete removes a flashcard.
func (s *Service) Delete(ctx context.Context, uid, id int64) error {
	fc, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if err := s.ensureEdit(ctx, uid, fc.SubjectID); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM flashcards WHERE id=$1`, id); err != nil {
		return fmt.Errorf("delete flashcard:\n%w", err)
	}
	return nil
}

// RecordReview updates last_result/last_used and pushes a naive due_at.
// The full SRS engine is out of scope; we set due_at = now + heuristic days.
func (s *Service) RecordReview(ctx context.Context, uid, id int64, in ReviewInput) (*Flashcard, error) {
	if in.Result < 0 || in.Result > 2 {
		return nil, myErrors.ErrInvalidInput
	}
	fc, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureEdit(ctx, uid, fc.SubjectID); err != nil {
		return nil, err
	}
	due := time.Now().Add(dueDelta(in.Result))
	var out Flashcard
	err = s.db.QueryRow(ctx, `
		UPDATE flashcards
		SET last_result=$1, last_used=now(), due_at=$2, updated_at=now()
		WHERE id=$3
		RETURNING id, subject_id, chapter_id, title, question, answer, image_id,
		          source, due_at, last_result, last_used, created_at, updated_at
	`, in.Result, due, id).Scan(
		&out.ID, &out.SubjectID, &out.ChapterID, &out.Title, &out.Question, &out.Answer,
		&out.ImageID, &out.Source, &out.DueAt, &out.LastResult, &out.LastUsed,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("record review:\n%w", err)
	}
	return &out, nil
}

func (s *Service) ensureEdit(ctx context.Context, uid, subjectID int64) error {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !level.CanEdit() {
		return myErrors.ErrForbidden
	}
	return nil
}

func (s *Service) insert(ctx context.Context, in CreateInput) (*Flashcard, error) {
	var fc Flashcard
	err := s.db.QueryRow(ctx, `
		INSERT INTO flashcards (subject_id, chapter_id, title, question, answer, image_id, source)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, subject_id, chapter_id, title, question, answer, image_id,
		          source, due_at, last_result, last_used, created_at, updated_at
	`, in.SubjectID, in.ChapterID, in.Title, in.Question, in.Answer, in.ImageID, in.Source).Scan(
		&fc.ID, &fc.SubjectID, &fc.ChapterID, &fc.Title, &fc.Question, &fc.Answer,
		&fc.ImageID, &fc.Source, &fc.DueAt, &fc.LastResult, &fc.LastUsed,
		&fc.CreatedAt, &fc.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert flashcard:\n%w", err)
	}
	return &fc, nil
}

func (s *Service) load(ctx context.Context, id int64) (*Flashcard, error) {
	var fc Flashcard
	err := s.db.QueryRow(ctx, `
		SELECT id, subject_id, chapter_id, title, question, answer, image_id,
		       source, due_at, last_result, last_used, created_at, updated_at
		FROM flashcards WHERE id=$1
	`, id).Scan(
		&fc.ID, &fc.SubjectID, &fc.ChapterID, &fc.Title, &fc.Question, &fc.Answer,
		&fc.ImageID, &fc.Source, &fc.DueAt, &fc.LastResult, &fc.LastUsed,
		&fc.CreatedAt, &fc.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load flashcard:\n%w", err)
	}
	return &fc, nil
}

const listBySubjectSQL = `
SELECT id, subject_id, chapter_id, title, question, answer, image_id,
       source, due_at, last_result, last_used, created_at, updated_at
FROM flashcards WHERE subject_id=$1
ORDER BY due_at ASC NULLS FIRST, id ASC
`

func scanAll(rows pgx.Rows) ([]Flashcard, error) {
	var out []Flashcard
	for rows.Next() {
		var fc Flashcard
		if err := rows.Scan(
			&fc.ID, &fc.SubjectID, &fc.ChapterID, &fc.Title, &fc.Question, &fc.Answer,
			&fc.ImageID, &fc.Source, &fc.DueAt, &fc.LastResult, &fc.LastUsed,
			&fc.CreatedAt, &fc.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan flashcard:\n%w", err)
		}
		out = append(out, fc)
	}
	return out, rows.Err()
}

// dueDelta maps a review result to a naive due-offset.
// TODO: replace with a real SRS algorithm (SM-2 / FSRS).
func dueDelta(result int) time.Duration {
	switch result {
	case 2:
		return 72 * time.Hour
	case 1:
		return 24 * time.Hour
	default:
		return time.Hour
	}
}
```

- [ ] **Step 3: Write `pkg/flashcard/service_test.go`**

```go
package flashcard_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/access"
	"studbud/backend/pkg/flashcard"
	"studbud/backend/testutil"
)

func TestFlashcardCRUD(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := flashcard.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	fc, err := svc.Create(ctx, owner.ID, flashcard.CreateInput{
		SubjectID: sub.ID, Question: "Q?", Answer: "A.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if fc.Source != "manual" || fc.LastResult != -1 {
		t.Fatalf("unexpected defaults: %+v", fc)
	}

	list, err := svc.ListBySubject(ctx, owner.ID, sub.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	reviewed, err := svc.RecordReview(ctx, owner.ID, fc.ID, flashcard.ReviewInput{Result: 2})
	if err != nil || reviewed.LastResult != 2 || reviewed.DueAt == nil {
		t.Fatalf("review: %v %+v", err, reviewed)
	}

	if err := svc.Delete(ctx, owner.ID, fc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestFlashcard_RejectBadResult(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := flashcard.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)
	fc, _ := svc.Create(ctx, owner.ID, flashcard.CreateInput{
		SubjectID: sub.ID, Question: "Q", Answer: "A",
	})

	if _, err := svc.RecordReview(ctx, owner.ID, fc.ID, flashcard.ReviewInput{Result: 7}); err == nil {
		t.Fatal("expected invalid input for out-of-range result")
	}
}
```

- [ ] **Step 4: Write `api/handler/flashcard.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/flashcard"
)

// FlashcardHandler exposes flashcard CRUD + review endpoints.
type FlashcardHandler struct {
	svc *flashcard.Service // svc owns the business logic
}

// NewFlashcardHandler constructs a FlashcardHandler.
func NewFlashcardHandler(svc *flashcard.Service) *FlashcardHandler {
	return &FlashcardHandler{svc: svc}
}

// Create handles POST /flashcard-create.
func (h *FlashcardHandler) Create(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	var in flashcard.CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	fc, err := h.svc.Create(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, fc)
}

// ListBySubject handles GET /flashcard-list?subject_id=...
func (h *FlashcardHandler) ListBySubject(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	list, err := h.svc.ListBySubject(r.Context(), uid, sid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// Get handles GET /flashcard?id=...
func (h *FlashcardHandler) Get(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	fc, err := h.svc.Get(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fc)
}

// Update handles POST /flashcard-update?id=...
func (h *FlashcardHandler) Update(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	var in flashcard.UpdateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	fc, err := h.svc.Update(r.Context(), uid, id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fc)
}

// Delete handles POST /flashcard-delete?id=...
func (h *FlashcardHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.Delete(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Review handles POST /flashcard-review?id=...
func (h *FlashcardHandler) Review(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	var in flashcard.ReviewInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	fc, err := h.svc.RecordReview(r.Context(), uid, id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fc)
}
```

- [ ] **Step 5: Run tests + build**

```bash
ENV=test DATABASE_URL=... go test ./pkg/flashcard/... -v
go build ./...
```

Expected: tests PASS, build succeeds.

- [ ] **Step 6: Commit**

```bash
git add pkg/flashcard/ api/handler/flashcard.go
git commit -m "$(cat <<'EOF'
Flashcard domain

[+] flashcard.Service: Create / Get / ListBySubject / Update / Delete / RecordReview
[+] FlashcardHandler with 6 endpoints
[+] Naive due_at heuristic (TODO: full SRS in later spec)
[+] Tests: CRUD round-trip + reject out-of-range review result
EOF
)"
```

---

### Task 26: Search domain

Shared search across subjects (`search_vec`) and users (`username`/`email`).

**Files:**
- Create: `pkg/search/service.go`
- Create: `pkg/search/service_test.go`
- Create: `api/handler/search.go`

- [ ] **Step 1: Write `pkg/search/service.go`**

```go
package search

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service exposes read-only search queries.
type Service struct {
	db *pgxpool.Pool // db is the shared pool
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// SubjectHit is a trimmed subject row returned by search.
type SubjectHit struct {
	ID         int64  `json:"id"`         // ID is the subject id
	OwnerID    int64  `json:"owner_id"`   // OwnerID is the creator
	Name       string `json:"name"`       // Name is the subject name
	Visibility string `json:"visibility"` // Visibility is private|friends|public
}

// UserHit is a trimmed user row returned by search.
type UserHit struct {
	ID       int64  `json:"id"`       // ID is the user id
	Username string `json:"username"` // Username is the user's handle
}

// Subjects searches public subjects + the caller's own subjects by query.
func (s *Service) Subjects(ctx context.Context, uid int64, query string, limit int) ([]SubjectHit, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, owner_id, name, visibility
		FROM subjects
		WHERE archived = false
		  AND (owner_id = $1 OR visibility = 'public')
		  AND search_vec @@ plainto_tsquery('simple', $2)
		ORDER BY ts_rank(search_vec, plainto_tsquery('simple', $2)) DESC, id DESC
		LIMIT $3
	`, uid, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search subjects:\n%w", err)
	}
	defer rows.Close()
	var out []SubjectHit
	for rows.Next() {
		var h SubjectHit
		if err := rows.Scan(&h.ID, &h.OwnerID, &h.Name, &h.Visibility); err != nil {
			return nil, fmt.Errorf("scan subject hit:\n%w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Users searches by username prefix / substring.
func (s *Service) Users(ctx context.Context, query string, limit int) ([]UserHit, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, username FROM users
		WHERE username ILIKE '%' || $1 || '%'
		ORDER BY length(username) ASC, id ASC
		LIMIT $2
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search users:\n%w", err)
	}
	defer rows.Close()
	var out []UserHit
	for rows.Next() {
		var h UserHit
		if err := rows.Scan(&h.ID, &h.Username); err != nil {
			return nil, fmt.Errorf("scan user hit:\n%w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Write `pkg/search/service_test.go`**

```go
package search_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/search"
	"studbud/backend/testutil"
)

func TestSearchSubjects_OwnedAndPublic(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	svc := search.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	_ = testutil.NewSubjectNamed(t, db, alice.ID, "Chemistry", "private")
	_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry Public", "public")
	_ = testutil.NewSubjectNamed(t, db, bob.ID, "Chemistry Secret", "private")

	hits, err := svc.Subjects(ctx, alice.ID, "chemistry", 10)
	if err != nil {
		t.Fatal(err)
	}
	// alice sees her own + bob's public, but not bob's private
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d: %+v", len(hits), hits)
	}
}

func TestSearchUsers(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	svc := search.NewService(db)

	_ = testutil.NewVerifiedUserNamed(t, db, "alice_smith")
	_ = testutil.NewVerifiedUserNamed(t, db, "bob_jones")

	hits, err := svc.Users(ctx, "alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Username != "alice_smith" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}
```

- [ ] **Step 3: Verify `testutil/fixtures.go` already provides `NewSubjectNamed` and `NewVerifiedUserNamed`**

Task 19 (as updated during self-review) defines both helpers with these signatures:
```go
func NewSubjectNamed(t *testing.T, pool *pgxpool.Pool, ownerID int64, name, visibility string) *SubjectFixture
func NewVerifiedUserNamed(t *testing.T, pool *pgxpool.Pool, username string) *UserFixture
```
If they are missing for any reason, add them following the patterns in Task 19.

_(The following block is retained only as a fallback illustration; do not copy — Task 19 is the source of truth.)_
```go
// Fallback illustration only — source of truth is Task 19.
func NewVerifiedUserNamed(t *testing.T, db *pgxpool.Pool, username string) *UserFixture {
    t.Helper()
    u := NewVerifiedUser(t, db)
    if _, err := db.Exec(context.Background(),
        `UPDATE users SET username=$1 WHERE id=$2`, username, u.ID,
    ); err != nil {
        t.Fatalf("rename user: %v", err)
    }
    u.Username = username
    return u
}
```

> **Note:** the helpers return `*SubjectFixture` / `*UserFixture` from Task 19 — use `.ID` and `.Username` on the returned pointer.

- [ ] **Step 4: Write `api/handler/search.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/pkg/search"
)

// SearchHandler exposes search endpoints.
type SearchHandler struct {
	svc *search.Service // svc owns the search queries
}

// NewSearchHandler constructs a SearchHandler.
func NewSearchHandler(svc *search.Service) *SearchHandler {
	return &SearchHandler{svc: svc}
}

// Subjects handles GET /search/subjects?q=...
func (h *SearchHandler) Subjects(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	q := r.URL.Query().Get("q")
	hits, err := h.svc.Subjects(r.Context(), uid, q, 20)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hits)
}

// Users handles GET /search/users?q=...
func (h *SearchHandler) Users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	hits, err := h.svc.Users(r.Context(), q, 20)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hits)
}
```

- [ ] **Step 5: Run tests + build + commit**

```bash
ENV=test DATABASE_URL=... go test ./pkg/search/... -v
go build ./...
git add pkg/search/ api/handler/search.go testutil/
git commit -m "$(cat <<'EOF'
Search domain

[+] search.Service: Subjects (tsvector) / Users (ILIKE)
[+] SearchHandler with /search/subjects and /search/users
[+] testutil helpers: NewSubjectNamed, NewVerifiedUserNamed
EOF
)"
```

---

### Task 27: Friendship domain

**Files:**
- Create: `pkg/friendship/model.go`
- Create: `pkg/friendship/service.go`
- Create: `pkg/friendship/service_test.go`
- Create: `api/handler/friendship.go`

- [ ] **Step 1: Write `pkg/friendship/model.go`**

```go
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
```

- [ ] **Step 2: Write `pkg/friendship/service.go`**

```go
package friendship

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
)

// Service owns the friendship lifecycle: request, accept, decline, list.
type Service struct {
	db *pgxpool.Pool // db is the shared pool
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Request creates a pending friendship row.
// Returns ErrConflict if a row already exists between the pair.
func (s *Service) Request(ctx context.Context, senderID, receiverID int64) (*Friendship, error) {
	if senderID == receiverID {
		return nil, myErrors.ErrInvalidInput
	}
	var f Friendship
	err := s.db.QueryRow(ctx, `
		INSERT INTO friendships (sender_id, receiver_id, status)
		VALUES ($1,$2,'pending')
		RETURNING id, sender_id, receiver_id, status, created_at, updated_at
	`, senderID, receiverID).Scan(
		&f.ID, &f.SenderID, &f.ReceiverID, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		// unique_violation
		if sqlstate(err) == "23505" {
			return nil, myErrors.ErrConflict
		}
		return nil, fmt.Errorf("insert friendship:\n%w", err)
	}
	return &f, nil
}

// Accept marks a pending friendship as accepted. Only the receiver may accept.
func (s *Service) Accept(ctx context.Context, uid, id int64) (*Friendship, error) {
	return s.transition(ctx, uid, id, "accepted", true)
}

// Decline marks a pending friendship as declined. Only the receiver may decline.
func (s *Service) Decline(ctx context.Context, uid, id int64) (*Friendship, error) {
	return s.transition(ctx, uid, id, "declined", true)
}

// Unfriend removes an accepted friendship. Either party may unfriend.
func (s *Service) Unfriend(ctx context.Context, uid, id int64) error {
	f, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if f.SenderID != uid && f.ReceiverID != uid {
		return myErrors.ErrForbidden
	}
	if f.Status != "accepted" {
		return myErrors.ErrInvalidInput
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM friendships WHERE id=$1`, id); err != nil {
		return fmt.Errorf("delete friendship:\n%w", err)
	}
	return nil
}

// ListFriends returns accepted friendships for uid (either side).
func (s *Service) ListFriends(ctx context.Context, uid int64) ([]Friendship, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, sender_id, receiver_id, status, created_at, updated_at
		FROM friendships
		WHERE status='accepted' AND (sender_id=$1 OR receiver_id=$1)
		ORDER BY updated_at DESC
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("list friends:\n%w", err)
	}
	defer rows.Close()
	return scanAll(rows)
}

// ListPendingIncoming returns pending friendships where uid is the receiver.
func (s *Service) ListPendingIncoming(ctx context.Context, uid int64) ([]Friendship, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, sender_id, receiver_id, status, created_at, updated_at
		FROM friendships
		WHERE status='pending' AND receiver_id=$1
		ORDER BY created_at DESC
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("list pending incoming:\n%w", err)
	}
	defer rows.Close()
	return scanAll(rows)
}

func (s *Service) transition(ctx context.Context, uid, id int64, newStatus string, receiverOnly bool) (*Friendship, error) {
	f, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if receiverOnly && f.ReceiverID != uid {
		return nil, myErrors.ErrForbidden
	}
	if f.Status != "pending" {
		return nil, myErrors.ErrInvalidInput
	}
	var out Friendship
	err = s.db.QueryRow(ctx, `
		UPDATE friendships SET status=$1, updated_at=now()
		WHERE id=$2
		RETURNING id, sender_id, receiver_id, status, created_at, updated_at
	`, newStatus, id).Scan(
		&out.ID, &out.SenderID, &out.ReceiverID, &out.Status, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("transition friendship:\n%w", err)
	}
	return &out, nil
}

func (s *Service) load(ctx context.Context, id int64) (*Friendship, error) {
	var f Friendship
	err := s.db.QueryRow(ctx, `
		SELECT id, sender_id, receiver_id, status, created_at, updated_at
		FROM friendships WHERE id=$1
	`, id).Scan(
		&f.ID, &f.SenderID, &f.ReceiverID, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load friendship:\n%w", err)
	}
	return &f, nil
}

func scanAll(rows pgx.Rows) ([]Friendship, error) {
	var out []Friendship
	for rows.Next() {
		var f Friendship
		if err := rows.Scan(
			&f.ID, &f.SenderID, &f.ReceiverID, &f.Status, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan friendship:\n%w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// sqlstate extracts a Postgres SQLSTATE code from a pgx error, if any.
func sqlstate(err error) string {
	type pgErr interface{ SQLState() string }
	var pe pgErr
	if errors.As(err, &pe) {
		return pe.SQLState()
	}
	return ""
}
```

- [ ] **Step 3: Write `pkg/friendship/service_test.go`**

```go
package friendship_test

import (
	"context"
	"errors"
	"testing"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/friendship"
	"studbud/backend/testutil"
)

func TestFriendshipFlow(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	svc := friendship.NewService(db)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)

	fr, err := svc.Request(ctx, alice.ID, bob.ID)
	if err != nil || fr.Status != "pending" {
		t.Fatalf("request: %v %+v", err, fr)
	}

	if _, err := svc.Accept(ctx, alice.ID, fr.ID); err == nil {
		t.Fatal("sender should not be able to accept")
	}

	accepted, err := svc.Accept(ctx, bob.ID, fr.ID)
	if err != nil || accepted.Status != "accepted" {
		t.Fatalf("accept: %v %+v", err, accepted)
	}

	list, err := svc.ListFriends(ctx, alice.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}

	if err := svc.Unfriend(ctx, alice.ID, fr.ID); err != nil {
		t.Fatalf("unfriend: %v", err)
	}
}

func TestFriendshipRequest_DuplicateRejected(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	svc := friendship.NewService(db)

	a := testutil.NewVerifiedUser(t, db)
	b := testutil.NewVerifiedUser(t, db)

	if _, err := svc.Request(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Request(ctx, a.ID, b.ID)
	if !errors.Is(err, myErrors.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}
```

- [ ] **Step 4: Write `api/handler/friendship.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/friendship"
)

// FriendshipHandler exposes friendship endpoints.
type FriendshipHandler struct {
	svc *friendship.Service // svc owns friendship logic
}

// NewFriendshipHandler constructs a FriendshipHandler.
func NewFriendshipHandler(svc *friendship.Service) *FriendshipHandler {
	return &FriendshipHandler{svc: svc}
}

// Request handles POST /friend-request.
func (h *FriendshipHandler) Request(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	var in friendship.RequestInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	fr, err := h.svc.Request(r.Context(), uid, in.ReceiverID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, fr)
}

// Accept handles POST /friend-accept?id=...
func (h *FriendshipHandler) Accept(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	fr, err := h.svc.Accept(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fr)
}

// Decline handles POST /friend-decline?id=...
func (h *FriendshipHandler) Decline(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	fr, err := h.svc.Decline(r.Context(), uid, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fr)
}

// Unfriend handles POST /friend-remove?id=...
func (h *FriendshipHandler) Unfriend(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	id, err := httpx.QueryInt64(r, "id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.Unfriend(r.Context(), uid, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListFriends handles GET /friends.
func (h *FriendshipHandler) ListFriends(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	list, err := h.svc.ListFriends(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// ListPending handles GET /friends-pending.
func (h *FriendshipHandler) ListPending(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	list, err := h.svc.ListPendingIncoming(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}
```

- [ ] **Step 5: Run tests + build + commit**

```bash
ENV=test DATABASE_URL=... go test ./pkg/friendship/... -v
go build ./...
git add pkg/friendship/ api/handler/friendship.go
git commit -m "$(cat <<'EOF'
Friendship domain

[+] friendship.Service: Request / Accept / Decline / Unfriend / ListFriends / ListPendingIncoming
[+] FriendshipHandler with 6 endpoints
[+] Duplicate requests return ErrConflict; only receiver can accept/decline
EOF
)"
```

---

### Task 28: SubjectSubscription domain

Lets a user subscribe to a public subject so it appears on their dashboard.

**Files:**
- Create: `pkg/subjectsub/service.go`
- Create: `pkg/subjectsub/service_test.go`
- Create: `api/handler/subject_subscription.go`

- [ ] **Step 1: Write `pkg/subjectsub/service.go`**

```go
package subjectsub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
)

// Subscription is a user-to-subject bookmark for public subjects.
type Subscription struct {
	UserID    int64     `json:"user_id"`    // UserID is the subscriber
	SubjectID int64     `json:"subject_id"` // SubjectID is the subscribed subject
	CreatedAt time.Time `json:"created_at"` // CreatedAt is when the subscription was created
}

// Service owns subject subscription logic.
type Service struct {
	db     *pgxpool.Pool   // db is the shared pool
	access *access.Service // access is used to check subject visibility
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// Subscribe adds a subscription. Subject must be public or accessible to the user.
func (s *Service) Subscribe(ctx context.Context, uid, subjectID int64) error {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if level == access.LevelNone {
		return myErrors.ErrForbidden
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO subject_subscriptions (user_id, subject_id)
		VALUES ($1,$2) ON CONFLICT DO NOTHING
	`, uid, subjectID); err != nil {
		return fmt.Errorf("subscribe:\n%w", err)
	}
	return nil
}

// Unsubscribe removes a subscription.
func (s *Service) Unsubscribe(ctx context.Context, uid, subjectID int64) error {
	if _, err := s.db.Exec(ctx,
		`DELETE FROM subject_subscriptions WHERE user_id=$1 AND subject_id=$2`,
		uid, subjectID); err != nil {
		return fmt.Errorf("unsubscribe:\n%w", err)
	}
	return nil
}

// ListSubscribed returns the user's subscribed subject ids.
func (s *Service) ListSubscribed(ctx context.Context, uid int64) ([]int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT subject_id FROM subject_subscriptions WHERE user_id=$1 ORDER BY created_at DESC`, uid)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions:\n%w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan subscription:\n%w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// IsSubscribed reports whether uid is subscribed to subjectID.
func (s *Service) IsSubscribed(ctx context.Context, uid, subjectID int64) (bool, error) {
	var one int
	err := s.db.QueryRow(ctx,
		`SELECT 1 FROM subject_subscriptions WHERE user_id=$1 AND subject_id=$2`,
		uid, subjectID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check subscription:\n%w", err)
	}
	return true, nil
}
```

- [ ] **Step 2: Write `pkg/subjectsub/service_test.go`**

```go
package subjectsub_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/access"
	"studbud/backend/pkg/subjectsub"
	"studbud/backend/testutil"
)

func TestSubscribeAndList(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := subjectsub.NewService(db, acc)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)
	pub := testutil.NewSubjectNamed(t, db, bob.ID, "Open Subject", "public")

	if err := svc.Subscribe(ctx, alice.ID, pub.ID); err != nil {
		t.Fatal(err)
	}

	ok, err := svc.IsSubscribed(ctx, alice.ID, pub.ID)
	if err != nil || !ok {
		t.Fatalf("expected subscribed, got ok=%v err=%v", ok, err)
	}

	ids, err := svc.ListSubscribed(ctx, alice.ID)
	if err != nil || len(ids) != 1 || ids[0] != pub.ID {
		t.Fatalf("list: %v %+v", err, ids)
	}

	if err := svc.Unsubscribe(ctx, alice.ID, pub.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribe_ForbiddenOnPrivate(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := subjectsub.NewService(db, acc)

	alice := testutil.NewVerifiedUser(t, db)
	bob := testutil.NewVerifiedUser(t, db)
	priv := testutil.NewSubjectNamed(t, db, bob.ID, "Private", "private")

	if err := svc.Subscribe(ctx, alice.ID, priv.ID); err == nil {
		t.Fatal("expected forbidden on private subject")
	}
}
```

- [ ] **Step 3: Write `api/handler/subject_subscription.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/subjectsub"
)

// SubjectSubscriptionHandler exposes subject subscription endpoints.
type SubjectSubscriptionHandler struct {
	svc *subjectsub.Service // svc owns subscription logic
}

// NewSubjectSubscriptionHandler constructs a SubjectSubscriptionHandler.
func NewSubjectSubscriptionHandler(svc *subjectsub.Service) *SubjectSubscriptionHandler {
	return &SubjectSubscriptionHandler{svc: svc}
}

// Subscribe handles POST /subject-subscribe?subject_id=...
func (h *SubjectSubscriptionHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.Subscribe(r.Context(), uid, sid); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unsubscribe handles POST /subject-unsubscribe?subject_id=...
func (h *SubjectSubscriptionHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.Unsubscribe(r.Context(), uid, sid); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// List handles GET /subject-subscriptions.
func (h *SubjectSubscriptionHandler) List(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	ids, err := h.svc.ListSubscribed(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ids)
}
```

- [ ] **Step 4: Run tests + build + commit**

```bash
ENV=test DATABASE_URL=... go test ./pkg/subjectsub/... -v
go build ./...
git add pkg/subjectsub/ api/handler/subject_subscription.go
git commit -m "$(cat <<'EOF'
Subject subscription domain

[+] subjectsub.Service: Subscribe / Unsubscribe / ListSubscribed / IsSubscribed
[+] SubjectSubscriptionHandler with 3 endpoints
[+] Subscription respects subject visibility via AccessService
EOF
)"
```

---

### Task 29: Collaboration domain

Manages shared-subject collaborators and invite tokens.

**Files:**
- Create: `pkg/collaboration/model.go`
- Create: `pkg/collaboration/service.go`
- Create: `pkg/collaboration/service_test.go`
- Create: `api/handler/collaboration.go`

- [ ] **Step 1: Write `pkg/collaboration/model.go`**

```go
package collaboration

import "time"

// Collaborator represents a user who may edit a subject not owned by them.
type Collaborator struct {
	ID        int64     `json:"id"`         // ID is the collaborator primary key
	SubjectID int64     `json:"subject_id"` // SubjectID is the shared subject
	UserID    int64     `json:"user_id"`    // UserID is the collaborator user id
	Role      string    `json:"role"`       // Role is one of viewer|editor
	CreatedAt time.Time `json:"created_at"` // CreatedAt is when membership started
}

// InviteLink is an opaque token that grants collaborator access when redeemed.
type InviteLink struct {
	Token     string     `json:"token"`      // Token is the shareable string
	SubjectID int64      `json:"subject_id"` // SubjectID is the target subject
	Role      string     `json:"role"`       // Role is the role granted on redemption
	ExpiresAt *time.Time `json:"expires_at"` // ExpiresAt is when the token stops working
	CreatedAt time.Time  `json:"created_at"` // CreatedAt is when the invite was issued
}

// CreateInviteInput is the payload to mint a new invite link.
type CreateInviteInput struct {
	SubjectID int64  `json:"subject_id"` // SubjectID is the subject to share
	Role      string `json:"role"`       // Role is the role to grant
	TTLHours  int    `json:"ttl_hours"`  // TTLHours is optional; 0 means no expiry
}
```

- [ ] **Step 2: Write `pkg/collaboration/service.go`**

```go
package collaboration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/access"
)

// Service owns collaboration state.
type Service struct {
	db     *pgxpool.Pool   // db is the shared pool
	access *access.Service // access checks subject ownership
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, acc *access.Service) *Service {
	return &Service{db: db, access: acc}
}

// AddCollaborator adds a user as collaborator on a subject. Only the owner may call this.
func (s *Service) AddCollaborator(ctx context.Context, ownerID, subjectID, userID int64, role string) (*Collaborator, error) {
	if role != "viewer" && role != "editor" {
		return nil, myErrors.ErrInvalidInput
	}
	if err := s.ensureOwner(ctx, ownerID, subjectID); err != nil {
		return nil, err
	}
	var c Collaborator
	err := s.db.QueryRow(ctx, `
		INSERT INTO collaborators (subject_id, user_id, role)
		VALUES ($1,$2,$3)
		ON CONFLICT (subject_id, user_id) DO UPDATE SET role=EXCLUDED.role
		RETURNING id, subject_id, user_id, role, created_at
	`, subjectID, userID, role).Scan(
		&c.ID, &c.SubjectID, &c.UserID, &c.Role, &c.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("add collaborator:\n%w", err)
	}
	return &c, nil
}

// RemoveCollaborator removes a collaborator. Only the owner may call this.
func (s *Service) RemoveCollaborator(ctx context.Context, ownerID, subjectID, userID int64) error {
	if err := s.ensureOwner(ctx, ownerID, subjectID); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx,
		`DELETE FROM collaborators WHERE subject_id=$1 AND user_id=$2`,
		subjectID, userID); err != nil {
		return fmt.Errorf("remove collaborator:\n%w", err)
	}
	return nil
}

// ListCollaborators returns all collaborators on a subject. Only the owner may view.
func (s *Service) ListCollaborators(ctx context.Context, ownerID, subjectID int64) ([]Collaborator, error) {
	if err := s.ensureOwner(ctx, ownerID, subjectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, subject_id, user_id, role, created_at
		FROM collaborators WHERE subject_id=$1 ORDER BY created_at ASC
	`, subjectID)
	if err != nil {
		return nil, fmt.Errorf("list collaborators:\n%w", err)
	}
	defer rows.Close()
	var out []Collaborator
	for rows.Next() {
		var c Collaborator
		if err := rows.Scan(&c.ID, &c.SubjectID, &c.UserID, &c.Role, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan collaborator:\n%w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateInvite mints an opaque invite token.
func (s *Service) CreateInvite(ctx context.Context, ownerID int64, in CreateInviteInput) (*InviteLink, error) {
	if in.Role != "viewer" && in.Role != "editor" {
		return nil, myErrors.ErrInvalidInput
	}
	if err := s.ensureOwner(ctx, ownerID, in.SubjectID); err != nil {
		return nil, err
	}
	token, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	var expires *time.Time
	if in.TTLHours > 0 {
		t := time.Now().Add(time.Duration(in.TTLHours) * time.Hour)
		expires = &t
	}
	var link InviteLink
	err = s.db.QueryRow(ctx, `
		INSERT INTO invite_links (token, subject_id, role, expires_at)
		VALUES ($1,$2,$3,$4)
		RETURNING token, subject_id, role, expires_at, created_at
	`, token, in.SubjectID, in.Role, expires).Scan(
		&link.Token, &link.SubjectID, &link.Role, &link.ExpiresAt, &link.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create invite:\n%w", err)
	}
	return &link, nil
}

// RedeemInvite consumes a token and attaches the user as collaborator.
func (s *Service) RedeemInvite(ctx context.Context, uid int64, token string) (*Collaborator, error) {
	var (
		subjectID int64
		role      string
		expiresAt *time.Time
	)
	err := s.db.QueryRow(ctx,
		`SELECT subject_id, role, expires_at FROM invite_links WHERE token=$1`,
		token).Scan(&subjectID, &role, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load invite:\n%w", err)
	}
	if expiresAt != nil && time.Now().After(*expiresAt) {
		return nil, myErrors.ErrInvalidInput
	}
	var c Collaborator
	err = s.db.QueryRow(ctx, `
		INSERT INTO collaborators (subject_id, user_id, role)
		VALUES ($1,$2,$3)
		ON CONFLICT (subject_id, user_id) DO UPDATE SET role=EXCLUDED.role
		RETURNING id, subject_id, user_id, role, created_at
	`, subjectID, uid, role).Scan(
		&c.ID, &c.SubjectID, &c.UserID, &c.Role, &c.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("redeem invite:\n%w", err)
	}
	return &c, nil
}

func (s *Service) ensureOwner(ctx context.Context, uid, subjectID int64) error {
	level, err := s.access.SubjectLevel(ctx, uid, subjectID)
	if err != nil {
		return err
	}
	if !level.CanManage() {
		return myErrors.ErrForbidden
	}
	return nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random token:\n%w", err)
	}
	return hex.EncodeToString(b), nil
}
```

- [ ] **Step 3: Write `pkg/collaboration/service_test.go`**

```go
package collaboration_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/access"
	"studbud/backend/pkg/collaboration"
	"studbud/backend/testutil"
)

func TestInviteFlow(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := collaboration.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	invitee := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	link, err := svc.CreateInvite(ctx, owner.ID, collaboration.CreateInviteInput{
		SubjectID: sub.ID, Role: "editor", TTLHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}

	c, err := svc.RedeemInvite(ctx, invitee.ID, link.Token)
	if err != nil || c.Role != "editor" || c.UserID != invitee.ID {
		t.Fatalf("redeem: %v %+v", err, c)
	}

	list, err := svc.ListCollaborators(ctx, owner.ID, sub.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}
}

func TestInvite_NonOwnerForbidden(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	acc := access.NewService(db)
	svc := collaboration.NewService(db, acc)

	owner := testutil.NewVerifiedUser(t, db)
	other := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, owner.ID)

	if _, err := svc.CreateInvite(ctx, other.ID, collaboration.CreateInviteInput{
		SubjectID: sub.ID, Role: "viewer",
	}); err == nil {
		t.Fatal("expected forbidden")
	}
}
```

- [ ] **Step 4: Write `api/handler/collaboration.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/collaboration"
)

// CollaborationHandler exposes collaborator + invite endpoints.
type CollaborationHandler struct {
	svc *collaboration.Service // svc owns collaboration logic
}

// NewCollaborationHandler constructs a CollaborationHandler.
func NewCollaborationHandler(svc *collaboration.Service) *CollaborationHandler {
	return &CollaborationHandler{svc: svc}
}

// AddCollaboratorInput is the payload for POST /collaborator-add.
type addInput struct {
	SubjectID int64  `json:"subject_id"` // SubjectID is the target subject
	UserID    int64  `json:"user_id"`    // UserID is the collaborator to add
	Role      string `json:"role"`       // Role is viewer|editor
}

// AddCollaborator handles POST /collaborator-add.
func (h *CollaborationHandler) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	var in addInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.AddCollaborator(r.Context(), uid, in.SubjectID, in.UserID, in.Role)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

// RemoveCollaborator handles POST /collaborator-remove?subject_id=...&user_id=...
func (h *CollaborationHandler) RemoveCollaborator(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	sid, err1 := httpx.QueryInt64(r, "subject_id")
	target, err2 := httpx.QueryInt64(r, "user_id")
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	if err := h.svc.RemoveCollaborator(r.Context(), uid, sid, target); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListCollaborators handles GET /collaborators?subject_id=...
func (h *CollaborationHandler) ListCollaborators(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	sid, err := httpx.QueryInt64(r, "subject_id")
	if err != nil {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	list, err := h.svc.ListCollaborators(r.Context(), uid, sid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// CreateInvite handles POST /invite-create.
func (h *CollaborationHandler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	var in collaboration.CreateInviteInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	link, err := h.svc.CreateInvite(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, link)
}

// RedeemInvite handles POST /invite-redeem?token=...
func (h *CollaborationHandler) RedeemInvite(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	token := r.URL.Query().Get("token")
	if token == "" {
		httpx.WriteError(w, myErrors.ErrInvalidInput)
		return
	}
	c, err := h.svc.RedeemInvite(r.Context(), uid, token)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}
```

- [ ] **Step 5: Run tests + build + commit**

```bash
ENV=test DATABASE_URL=... go test ./pkg/collaboration/... -v
go build ./...
git add pkg/collaboration/ api/handler/collaboration.go
git commit -m "$(cat <<'EOF'
Collaboration domain

[+] collaboration.Service: AddCollaborator / Remove / List / CreateInvite / RedeemInvite
[+] CollaborationHandler with 5 endpoints
[+] Tokens opaque (24-byte hex); optional TTL; owner-only ops enforced via AccessService
EOF
)"
```

---

### Task 30: Preferences domain

Minimal preferences object (just AI planning toggle + daily goal target).

**Files:**
- Create: `pkg/preferences/service.go`
- Create: `pkg/preferences/service_test.go`
- Create: `api/handler/preferences.go`

- [ ] **Step 1: Write `pkg/preferences/service.go`**

```go
package preferences

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
)

// Prefs is the per-user preferences blob.
type Prefs struct {
	UserID            int64 `json:"user_id"`             // UserID is the owner
	AIPlanningEnabled bool  `json:"ai_planning_enabled"` // AIPlanningEnabled toggles plan generation on
	DailyGoalTarget   int   `json:"daily_goal_target"`   // DailyGoalTarget is the per-day card target
}

// UpdateInput patches preferences.
type UpdateInput struct {
	AIPlanningEnabled *bool `json:"ai_planning_enabled"` // AIPlanningEnabled when non-nil updates the flag
	DailyGoalTarget   *int  `json:"daily_goal_target"`   // DailyGoalTarget when non-nil updates the goal
}

// Service owns the preferences blob.
type Service struct {
	db *pgxpool.Pool // db is the shared pool
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Get returns the user's preferences, creating a default row if missing.
func (s *Service) Get(ctx context.Context, uid int64) (*Prefs, error) {
	p, err := s.load(ctx, uid)
	if errors.Is(err, myErrors.ErrNotFound) {
		return s.ensureDefault(ctx, uid)
	}
	return p, err
}

// Update patches preferences. Creates the row if missing.
func (s *Service) Update(ctx context.Context, uid int64, in UpdateInput) (*Prefs, error) {
	if _, err := s.ensureDefault(ctx, uid); err != nil {
		return nil, err
	}
	current, err := s.load(ctx, uid)
	if err != nil {
		return nil, err
	}
	ai := current.AIPlanningEnabled
	goal := current.DailyGoalTarget
	if in.AIPlanningEnabled != nil {
		ai = *in.AIPlanningEnabled
	}
	if in.DailyGoalTarget != nil {
		if *in.DailyGoalTarget < 0 || *in.DailyGoalTarget > 1000 {
			return nil, myErrors.ErrInvalidInput
		}
		goal = *in.DailyGoalTarget
	}
	var out Prefs
	err = s.db.QueryRow(ctx, `
		UPDATE preferences SET ai_planning_enabled=$1, daily_goal_target=$2, updated_at=now()
		WHERE user_id=$3
		RETURNING user_id, ai_planning_enabled, daily_goal_target
	`, ai, goal, uid).Scan(&out.UserID, &out.AIPlanningEnabled, &out.DailyGoalTarget)
	if err != nil {
		return nil, fmt.Errorf("update preferences:\n%w", err)
	}
	return &out, nil
}

func (s *Service) ensureDefault(ctx context.Context, uid int64) (*Prefs, error) {
	var p Prefs
	err := s.db.QueryRow(ctx, `
		INSERT INTO preferences (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE SET user_id=EXCLUDED.user_id
		RETURNING user_id, ai_planning_enabled, daily_goal_target
	`, uid).Scan(&p.UserID, &p.AIPlanningEnabled, &p.DailyGoalTarget)
	if err != nil {
		return nil, fmt.Errorf("ensure default preferences:\n%w", err)
	}
	return &p, nil
}

func (s *Service) load(ctx context.Context, uid int64) (*Prefs, error) {
	var p Prefs
	err := s.db.QueryRow(ctx, `
		SELECT user_id, ai_planning_enabled, daily_goal_target
		FROM preferences WHERE user_id=$1
	`, uid).Scan(&p.UserID, &p.AIPlanningEnabled, &p.DailyGoalTarget)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, myErrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load preferences:\n%w", err)
	}
	return &p, nil
}
```

- [ ] **Step 2: Write `pkg/preferences/service_test.go`**

```go
package preferences_test

import (
	"context"
	"testing"

	"studbud/backend/pkg/preferences"
	"studbud/backend/testutil"
)

func TestPreferencesGetCreatesDefault(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	svc := preferences.NewService(db)
	u := testutil.NewVerifiedUser(t, db)

	p, err := svc.Get(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != u.ID || !p.AIPlanningEnabled { // default is on
		t.Fatalf("unexpected defaults: %+v", p)
	}
}

func TestPreferencesUpdate(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	svc := preferences.NewService(db)
	u := testutil.NewVerifiedUser(t, db)

	goal := 25
	off := false
	p, err := svc.Update(ctx, u.ID, preferences.UpdateInput{
		AIPlanningEnabled: &off, DailyGoalTarget: &goal,
	})
	if err != nil || p.DailyGoalTarget != 25 || p.AIPlanningEnabled {
		t.Fatalf("update: %v %+v", err, p)
	}
}
```

> **Note:** the `preferences` table default for `ai_planning_enabled` must be `true` (see Task 12 schema). If the schema uses `false`, update it accordingly; the test expects true-by-default.

- [ ] **Step 3: Write `api/handler/preferences.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/pkg/preferences"
)

// PreferencesHandler exposes get + update endpoints.
type PreferencesHandler struct {
	svc *preferences.Service // svc owns prefs logic
}

// NewPreferencesHandler constructs a PreferencesHandler.
func NewPreferencesHandler(svc *preferences.Service) *PreferencesHandler {
	return &PreferencesHandler{svc: svc}
}

// Get handles GET /preferences.
func (h *PreferencesHandler) Get(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	p, err := h.svc.Get(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// Update handles POST /preferences-update.
func (h *PreferencesHandler) Update(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	var in preferences.UpdateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Update(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
```

- [ ] **Step 4: Run tests + build + commit**

```bash
ENV=test DATABASE_URL=... go test ./pkg/preferences/... -v
go build ./...
git add pkg/preferences/ api/handler/preferences.go
git commit -m "$(cat <<'EOF'
Preferences domain

[+] preferences.Service: Get (creates default) / Update
[+] PreferencesHandler with 2 endpoints
[+] Validates daily_goal_target range 0..1000
EOF
)"
```

---

### Task 31: Gamification domain

Streaks, daily goal progress, training sessions, and achievements. Per PROJECT_DESCRIPTION.md this is server-authoritative.

**Files:**
- Create: `pkg/gamification/model.go`
- Create: `pkg/gamification/service.go`
- Create: `pkg/gamification/achievements.go`
- Create: `pkg/gamification/service_test.go`
- Create: `api/handler/gamification.go`

- [ ] **Step 1: Write `pkg/gamification/model.go`**

```go
package gamification

import "time"

// Streak tracks a user's current + best streaks.
type Streak struct {
	UserID        int64      `json:"user_id"`          // UserID is the streak owner
	CurrentStreak int        `json:"current_streak"`   // CurrentStreak is the active consecutive day count
	LongestStreak int        `json:"longest_streak"`   // LongestStreak is the historical best
	LastDay       *time.Time `json:"last_day"`         // LastDay is the last day a session was recorded
	UpdatedAt     time.Time  `json:"updated_at"`       // UpdatedAt is the last mutation time
}

// DailyGoal tracks one day's progress toward the daily target.
type DailyGoal struct {
	UserID    int64     `json:"user_id"`    // UserID is the goal owner
	Day       time.Time `json:"day"`        // Day is the calendar day (UTC)
	DoneToday int       `json:"done_today"` // DoneToday is cards reviewed today
	Target    int       `json:"target"`     // Target is the target copied from preferences
}

// TrainingSession captures a completed training run.
type TrainingSession struct {
	ID         int64     `json:"id"`          // ID is the session primary key
	UserID     int64     `json:"user_id"`     // UserID is the learner
	SubjectID  int64     `json:"subject_id"`  // SubjectID is the subject trained
	CardCount  int       `json:"card_count"`  // CardCount is the number of cards reviewed
	DurationMs int       `json:"duration_ms"` // DurationMs is the total session duration
	Score      int       `json:"score"`       // Score is an aggregate score
	CreatedAt  time.Time `json:"created_at"`  // CreatedAt is session end time
}

// RecordSessionInput is the payload to record a finished session.
type RecordSessionInput struct {
	SubjectID  int64 `json:"subject_id"`  // SubjectID is the subject being trained
	CardCount  int   `json:"card_count"`  // CardCount is cards answered
	DurationMs int   `json:"duration_ms"` // DurationMs is the wall time
	Score      int   `json:"score"`       // Score is the aggregate score
}

// RecordSessionResult bundles the mutated state returned to the caller.
type RecordSessionResult struct {
	Session     TrainingSession `json:"session"`     // Session is the row just inserted
	Streak      Streak          `json:"streak"`      // Streak is the updated streak snapshot
	DailyGoal   DailyGoal       `json:"daily_goal"`  // DailyGoal is the updated daily goal
	NewlyAwarded []Achievement  `json:"newly_awarded"` // NewlyAwarded are achievements unlocked by this session
}

// Achievement represents an achievement (definition + unlock row combined).
type Achievement struct {
	Code        string     `json:"code"`        // Code is the achievement identifier
	Title       string     `json:"title"`       // Title is the display title
	Description string     `json:"description"` // Description is human text
	UnlockedAt  *time.Time `json:"unlocked_at"` // UnlockedAt is when the user earned it (nil = locked)
}

// UserStats aggregates high-level stats for the profile screen.
type UserStats struct {
	TotalCards    int `json:"total_cards"`    // TotalCards is the total flashcards owned
	TotalSessions int `json:"total_sessions"` // TotalSessions is the lifetime training count
	CurrentStreak int `json:"current_streak"` // CurrentStreak mirrors the streak row
	LongestStreak int `json:"longest_streak"` // LongestStreak mirrors the streak row
}
```

- [ ] **Step 2: Write `pkg/gamification/achievements.go`**

```go
package gamification

// achievementDefs is the static catalog of achievements.
// Unlock logic is evaluated after each recorded session.
var achievementDefs = []Achievement{
	{Code: "first_session", Title: "First Steps", Description: "Complete your first training session."},
	{Code: "streak_3", Title: "Three in a Row", Description: "Train three days in a row."},
	{Code: "streak_7", Title: "Week Warrior", Description: "Train seven days in a row."},
	{Code: "streak_30", Title: "Iron Discipline", Description: "Train thirty days in a row."},
	{Code: "cards_100", Title: "Century", Description: "Review 100 cards total."},
	{Code: "cards_1000", Title: "Marathoner", Description: "Review 1000 cards total."},
}

// catalog returns all achievement definitions keyed by code.
func catalog() map[string]Achievement {
	m := make(map[string]Achievement, len(achievementDefs))
	for _, a := range achievementDefs {
		m[a.Code] = a
	}
	return m
}
```

- [ ] **Step 3: Write `pkg/gamification/service.go`**

```go
package gamification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
)

// Service owns gamification state: streaks, daily goals, sessions, achievements.
type Service struct {
	db  *pgxpool.Pool // db is the shared pool
	now func() time.Time // now lets tests inject a fixed clock
}

// NewService constructs a Service with the real clock.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db, now: time.Now}
}

// SetClock replaces the clock; intended for tests only.
func (s *Service) SetClock(f func() time.Time) { s.now = f }

// GetState returns the streak + today's goal.
func (s *Service) GetState(ctx context.Context, uid int64) (Streak, DailyGoal, error) {
	st, err := s.streak(ctx, uid, false)
	if err != nil {
		return Streak{}, DailyGoal{}, err
	}
	dg, err := s.dailyGoal(ctx, uid, s.today())
	if err != nil {
		return Streak{}, DailyGoal{}, err
	}
	return st, dg, nil
}

// RecordSession inserts a training session and updates streak, daily goal, achievements.
func (s *Service) RecordSession(ctx context.Context, uid int64, in RecordSessionInput) (*RecordSessionResult, error) {
	if in.CardCount < 0 || in.DurationMs < 0 {
		return nil, myErrors.ErrInvalidInput
	}
	var sess TrainingSession
	err := s.db.QueryRow(ctx, `
		INSERT INTO training_sessions (user_id, subject_id, card_count, duration_ms, score)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, user_id, subject_id, card_count, duration_ms, score, created_at
	`, uid, in.SubjectID, in.CardCount, in.DurationMs, in.Score).Scan(
		&sess.ID, &sess.UserID, &sess.SubjectID, &sess.CardCount, &sess.DurationMs, &sess.Score, &sess.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert training session:\n%w", err)
	}

	st, err := s.bumpStreak(ctx, uid)
	if err != nil {
		return nil, err
	}
	dg, err := s.bumpDailyGoal(ctx, uid, in.CardCount)
	if err != nil {
		return nil, err
	}
	awards, err := s.evaluateAchievements(ctx, uid, st)
	if err != nil {
		return nil, err
	}
	return &RecordSessionResult{
		Session:     sess,
		Streak:      st,
		DailyGoal:   dg,
		NewlyAwarded: awards,
	}, nil
}

// GetUserStats returns aggregate stats for the profile screen.
func (s *Service) GetUserStats(ctx context.Context, uid int64) (*UserStats, error) {
	var stats UserStats
	err := s.db.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM flashcards f
		   JOIN subjects s ON s.id=f.subject_id WHERE s.owner_id=$1),
		  (SELECT count(*) FROM training_sessions WHERE user_id=$1),
		  coalesce((SELECT current_streak FROM streaks WHERE user_id=$1), 0),
		  coalesce((SELECT longest_streak FROM streaks WHERE user_id=$1), 0)
	`, uid).Scan(&stats.TotalCards, &stats.TotalSessions, &stats.CurrentStreak, &stats.LongestStreak)
	if err != nil {
		return nil, fmt.Errorf("user stats:\n%w", err)
	}
	return &stats, nil
}

// ListAchievements returns the full catalog with unlock timestamps for the user.
func (s *Service) ListAchievements(ctx context.Context, uid int64) ([]Achievement, error) {
	rows, err := s.db.Query(ctx,
		`SELECT code, unlocked_at FROM unlocked_achievements WHERE user_id=$1`, uid)
	if err != nil {
		return nil, fmt.Errorf("list unlocked achievements:\n%w", err)
	}
	defer rows.Close()
	unlocked := map[string]time.Time{}
	for rows.Next() {
		var code string
		var at time.Time
		if err := rows.Scan(&code, &at); err != nil {
			return nil, fmt.Errorf("scan achievement:\n%w", err)
		}
		unlocked[code] = at
	}
	out := make([]Achievement, 0, len(achievementDefs))
	for _, def := range achievementDefs {
		a := def
		if t, ok := unlocked[def.Code]; ok {
			t := t
			a.UnlockedAt = &t
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *Service) today() time.Time {
	t := s.now().UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Service) streak(ctx context.Context, uid int64, mustExist bool) (Streak, error) {
	var st Streak
	err := s.db.QueryRow(ctx, `
		SELECT user_id, current_streak, longest_streak, last_day, updated_at
		FROM streaks WHERE user_id=$1
	`, uid).Scan(&st.UserID, &st.CurrentStreak, &st.LongestStreak, &st.LastDay, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if mustExist {
			return Streak{}, myErrors.ErrNotFound
		}
		return Streak{UserID: uid}, nil
	}
	if err != nil {
		return Streak{}, fmt.Errorf("load streak:\n%w", err)
	}
	return st, nil
}

func (s *Service) bumpStreak(ctx context.Context, uid int64) (Streak, error) {
	today := s.today()
	st, err := s.streak(ctx, uid, false)
	if err != nil {
		return Streak{}, err
	}
	current := st.CurrentStreak
	switch {
	case st.LastDay == nil:
		current = 1
	case sameDay(*st.LastDay, today):
		if current == 0 {
			current = 1
		}
	case sameDay(st.LastDay.Add(24*time.Hour), today):
		current++
	default:
		current = 1
	}
	longest := st.LongestStreak
	if current > longest {
		longest = current
	}
	var out Streak
	err = s.db.QueryRow(ctx, `
		INSERT INTO streaks (user_id, current_streak, longest_streak, last_day, updated_at)
		VALUES ($1,$2,$3,$4, now())
		ON CONFLICT (user_id) DO UPDATE
		  SET current_streak=EXCLUDED.current_streak,
		      longest_streak=EXCLUDED.longest_streak,
		      last_day=EXCLUDED.last_day,
		      updated_at=now()
		RETURNING user_id, current_streak, longest_streak, last_day, updated_at
	`, uid, current, longest, today).Scan(
		&out.UserID, &out.CurrentStreak, &out.LongestStreak, &out.LastDay, &out.UpdatedAt,
	)
	if err != nil {
		return Streak{}, fmt.Errorf("upsert streak:\n%w", err)
	}
	return out, nil
}

func (s *Service) dailyGoal(ctx context.Context, uid int64, day time.Time) (DailyGoal, error) {
	var dg DailyGoal
	err := s.db.QueryRow(ctx, `
		INSERT INTO daily_goals (user_id, day, target, done_today)
		VALUES ($1,$2, (SELECT daily_goal_target FROM preferences WHERE user_id=$1), 0)
		ON CONFLICT (user_id, day) DO UPDATE SET user_id=EXCLUDED.user_id
		RETURNING user_id, day, done_today, target
	`, uid, day).Scan(&dg.UserID, &dg.Day, &dg.DoneToday, &dg.Target)
	if err != nil {
		return DailyGoal{}, fmt.Errorf("upsert daily goal:\n%w", err)
	}
	return dg, nil
}

func (s *Service) bumpDailyGoal(ctx context.Context, uid int64, inc int) (DailyGoal, error) {
	day := s.today()
	if _, err := s.dailyGoal(ctx, uid, day); err != nil {
		return DailyGoal{}, err
	}
	var out DailyGoal
	err := s.db.QueryRow(ctx, `
		UPDATE daily_goals SET done_today = done_today + $1
		WHERE user_id=$2 AND day=$3
		RETURNING user_id, day, done_today, target
	`, inc, uid, day).Scan(&out.UserID, &out.Day, &out.DoneToday, &out.Target)
	if err != nil {
		return DailyGoal{}, fmt.Errorf("bump daily goal:\n%w", err)
	}
	return out, nil
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
```

- [ ] **Step 4: Add `evaluateAchievements` in `pkg/gamification/service.go`**

Append to the same file:

```go
// evaluateAchievements checks and records unlocks triggered by the just-recorded session.
func (s *Service) evaluateAchievements(ctx context.Context, uid int64, st Streak) ([]Achievement, error) {
	total, err := s.totalSessions(ctx, uid)
	if err != nil {
		return nil, err
	}
	totalCards, err := s.totalCardsReviewed(ctx, uid)
	if err != nil {
		return nil, err
	}
	earned := map[string]bool{}
	if total >= 1 {
		earned["first_session"] = true
	}
	if st.CurrentStreak >= 3 {
		earned["streak_3"] = true
	}
	if st.CurrentStreak >= 7 {
		earned["streak_7"] = true
	}
	if st.CurrentStreak >= 30 {
		earned["streak_30"] = true
	}
	if totalCards >= 100 {
		earned["cards_100"] = true
	}
	if totalCards >= 1000 {
		earned["cards_1000"] = true
	}
	return s.persistUnlocks(ctx, uid, earned)
}

func (s *Service) totalSessions(ctx context.Context, uid int64) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM training_sessions WHERE user_id=$1`, uid,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count sessions:\n%w", err)
	}
	return n, nil
}

func (s *Service) totalCardsReviewed(ctx context.Context, uid int64) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT coalesce(sum(card_count),0) FROM training_sessions WHERE user_id=$1`, uid,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("sum cards:\n%w", err)
	}
	return n, nil
}

func (s *Service) persistUnlocks(ctx context.Context, uid int64, earned map[string]bool) ([]Achievement, error) {
	cat := catalog()
	var newly []Achievement
	for code := range earned {
		var at time.Time
		err := s.db.QueryRow(ctx, `
			INSERT INTO unlocked_achievements (user_id, code)
			VALUES ($1,$2) ON CONFLICT DO NOTHING
			RETURNING unlocked_at
		`, uid, code).Scan(&at)
		if errors.Is(err, pgx.ErrNoRows) {
			// already unlocked previously; skip
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("insert achievement %s:\n%w", code, err)
		}
		a := cat[code]
		a.UnlockedAt = &at
		newly = append(newly, a)
	}
	return newly, nil
}
```

- [ ] **Step 5: Write `pkg/gamification/service_test.go`**

```go
package gamification_test

import (
	"context"
	"testing"
	"time"

	"studbud/backend/pkg/gamification"
	"studbud/backend/testutil"
)

func TestRecordSessionBumpsStreakAndGoal(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	svc := gamification.NewService(db)

	user := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, user.ID)

	res, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, CardCount: 5, DurationMs: 120000, Score: 80,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Streak.CurrentStreak != 1 {
		t.Fatalf("expected streak=1, got %d", res.Streak.CurrentStreak)
	}
	if res.DailyGoal.DoneToday != 5 {
		t.Fatalf("expected done_today=5, got %d", res.DailyGoal.DoneToday)
	}
	// first_session achievement should fire
	gotFirst := false
	for _, a := range res.NewlyAwarded {
		if a.Code == "first_session" {
			gotFirst = true
		}
	}
	if !gotFirst {
		t.Fatal("expected first_session achievement")
	}
}

func TestStreakResetsAfterGap(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	svc := gamification.NewService(db)

	user := testutil.NewVerifiedUser(t, db)
	sub := testutil.NewSubject(t, db, user.ID)

	// Simulate a session 3 days ago, then today.
	past := time.Now().UTC().Add(-72 * time.Hour)
	svc.SetClock(func() time.Time { return past })
	if _, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, CardCount: 1, DurationMs: 1000, Score: 10,
	}); err != nil {
		t.Fatal(err)
	}
	svc.SetClock(time.Now)
	res, err := svc.RecordSession(ctx, user.ID, gamification.RecordSessionInput{
		SubjectID: sub.ID, CardCount: 1, DurationMs: 1000, Score: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Streak.CurrentStreak != 1 {
		t.Fatalf("expected streak reset to 1, got %d", res.Streak.CurrentStreak)
	}
}

func TestListAchievementsShowsFullCatalog(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenTestDB(t)
	svc := gamification.NewService(db)

	user := testutil.NewVerifiedUser(t, db)
	list, err := svc.ListAchievements(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 6 {
		t.Fatalf("expected full catalog (>=6), got %d", len(list))
	}
}
```

- [ ] **Step 6: Write `api/handler/gamification.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/authctx"
	"studbud/backend/internal/httpx"
	"studbud/backend/pkg/gamification"
)

// GamificationHandler exposes gamification endpoints.
type GamificationHandler struct {
	svc *gamification.Service // svc owns gamification logic
}

// NewGamificationHandler constructs a GamificationHandler.
func NewGamificationHandler(svc *gamification.Service) *GamificationHandler {
	return &GamificationHandler{svc: svc}
}

// State handles GET /gamification-state.
func (h *GamificationHandler) State(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	streak, goal, err := h.svc.GetState(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"streak":     streak,
		"daily_goal": goal,
	})
}

// RecordSession handles POST /training-session-record.
func (h *GamificationHandler) RecordSession(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	var in gamification.RecordSessionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.RecordSession(r.Context(), uid, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// Stats handles GET /user-stats.
func (h *GamificationHandler) Stats(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	stats, err := h.svc.GetUserStats(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, stats)
}

// Achievements handles GET /achievements.
func (h *GamificationHandler) Achievements(w http.ResponseWriter, r *http.Request) {
	uid := authctx.MustUserID(r.Context())
	list, err := h.svc.ListAchievements(r.Context(), uid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}
```

- [ ] **Step 7: Run tests + build + commit**

```bash
ENV=test DATABASE_URL=... go test ./pkg/gamification/... -v
go build ./...
git add pkg/gamification/ api/handler/gamification.go
git commit -m "$(cat <<'EOF'
Gamification domain

[+] gamification.Service: GetState / RecordSession / GetUserStats / ListAchievements
[+] Streak bump logic with clock injection; daily goal upsert; achievement evaluation
[+] Static achievement catalog (first_session, streak_3/7/30, cards_100/1000)
[+] GamificationHandler with 4 endpoints
EOF
)"
```

---

## Phase 5 — Future-Feature Stubs, Wiring, End-to-End

These tasks create skeletal packages for the features that are NOT implemented in the skeleton but whose DB tables already exist. Each stub exposes a type with the empty methods needed by the router so that the server compiles, and returns `myErrors.ErrNotImplemented` at runtime.

Rationale: when the real feature ships, the handler shape and entrypoint already exist — callers don't need to rewire the router. The stubs are intentionally thin; do not pre-solve the features.

### Task 32: AI / Quiz / Plan / Duel stubs

**Files:**
- Create: `pkg/aipipeline/stub.go`
- Create: `pkg/quiz/stub.go`
- Create: `pkg/plan/stub.go`
- Create: `pkg/duel/stub.go`
- Create: `api/handler/ai_stub.go`
- Create: `api/handler/quiz_stub.go`
- Create: `api/handler/plan_stub.go`
- Create: `api/handler/duel_stub.go`

- [ ] **Step 1: Confirm `ErrNotImplemented` is wired through error handling**

Task 3 already defined `myErrors.ErrNotImplemented`, and Task 8's `httpx.WriteError` already maps it to `501 Not Implemented`. Grep to verify:
```bash
grep -n ErrNotImplemented internal/myErrors/errors.go internal/httpx/response.go
```
Both files must list it. If either is missing, add it following the existing patterns before proceeding.

- [ ] **Step 2: Write `pkg/aipipeline/stub.go`**

```go
package aipipeline

import (
	"context"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/myErrors"
)

// Service is the AI pipeline facade. In the skeleton it returns ErrNotImplemented;
// Spec A will replace the implementation (entitlement + quota + structured gen).
type Service struct {
	db  *pgxpool.Pool     // db is the shared pool (unused in the stub but retained for signature stability)
	cli aiProvider.Client // cli is the underlying AI provider (unused in the stub)
}

// NewService constructs a stub Service.
func NewService(db *pgxpool.Pool, cli aiProvider.Client) *Service {
	return &Service{db: db, cli: cli}
}

// GenerateFlashcards is a placeholder for prompt-based flashcard generation.
func (s *Service) GenerateFlashcards(ctx context.Context, uid, subjectID int64, prompt string) (io.ReadCloser, error) {
	return nil, myErrors.ErrNotImplemented
}

// GenerateFromPDF is a placeholder for PDF-driven flashcard generation.
func (s *Service) GenerateFromPDF(ctx context.Context, uid, subjectID int64, pdfBytes []byte) (io.ReadCloser, error) {
	return nil, myErrors.ErrNotImplemented
}

// CheckFlashcards is a placeholder for the "AI check" review pass.
func (s *Service) CheckFlashcards(ctx context.Context, uid, subjectID int64, cardIDs []int64) (io.ReadCloser, error) {
	return nil, myErrors.ErrNotImplemented
}
```

- [ ] **Step 3: Write `pkg/quiz/stub.go`**

```go
package quiz

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
)

// Service is the quiz facade. Spec D will replace this.
type Service struct {
	db *pgxpool.Pool // db is the shared pool (unused in the stub)
}

// NewService constructs a stub Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Generate is a placeholder for creating a quiz from flashcards or a free-form prompt.
func (s *Service) Generate(ctx context.Context, uid int64, req any) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Attempt is a placeholder for recording quiz answers.
func (s *Service) Attempt(ctx context.Context, uid, quizID int64, answers any) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Share is a placeholder for quiz sharing.
func (s *Service) Share(ctx context.Context, uid, quizID int64) (string, error) {
	return "", myErrors.ErrNotImplemented
}
```

- [ ] **Step 4: Write `pkg/plan/stub.go`**

```go
package plan

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/myErrors"
)

// Service is the revision-plan facade. Spec B will replace this.
type Service struct {
	db *pgxpool.Pool // db is the shared pool (unused in the stub)
}

// NewService constructs a stub Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Generate is a placeholder for building a new revision plan for an exam.
func (s *Service) Generate(ctx context.Context, uid, examID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Progress is a placeholder for plan progress state.
func (s *Service) Progress(ctx context.Context, uid, planID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}
```

- [ ] **Step 5: Write `pkg/duel/stub.go`**

```go
package duel

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/duelHub"
	"studbud/backend/internal/myErrors"
)

// Service is the duel facade. Spec E will replace this.
type Service struct {
	db  *pgxpool.Pool // db is the shared pool (unused in the stub)
	hub *duelHub.Hub  // hub is the (future) live-duel WebSocket hub
}

// NewService constructs a stub Service.
func NewService(db *pgxpool.Pool, hub *duelHub.Hub) *Service {
	return &Service{db: db, hub: hub}
}

// Invite is a placeholder for inviting a friend to a duel.
func (s *Service) Invite(ctx context.Context, challenger, opponent, subjectID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Accept is a placeholder for invitee acceptance + quiz generation.
func (s *Service) Accept(ctx context.Context, uid, duelID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}

// Play is a placeholder for the turn-taking duel flow (WebSocket-driven).
func (s *Service) Play(ctx context.Context, uid, duelID int64) (any, error) {
	return nil, myErrors.ErrNotImplemented
}
```

- [ ] **Step 6: Write `api/handler/ai_stub.go`**

```go
package handler

import (
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/aipipeline"
)

// AIHandler exposes AI pipeline endpoints as stubs until Spec A ships.
type AIHandler struct {
	svc *aipipeline.Service // svc is the (stub) pipeline service
}

// NewAIHandler constructs an AIHandler.
func NewAIHandler(svc *aipipeline.Service) *AIHandler {
	return &AIHandler{svc: svc}
}

// GenerateFromPrompt is a stub for POST /ai/flashcards/prompt.
func (h *AIHandler) GenerateFromPrompt(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// GenerateFromPDF is a stub for POST /ai/flashcards/pdf.
func (h *AIHandler) GenerateFromPDF(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Check is a stub for POST /ai/check.
func (h *AIHandler) Check(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
```

- [ ] **Step 7: Write `api/handler/quiz_stub.go`**, `api/handler/plan_stub.go`, `api/handler/duel_stub.go`

All three follow the same pattern as `ai_stub.go`. Each handler has a `svc` field and each method writes `myErrors.ErrNotImplemented` via `httpx.WriteError`.

```go
// api/handler/quiz_stub.go
package handler

import (
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/quiz"
)

// QuizHandler stubs Spec D endpoints.
type QuizHandler struct {
	svc *quiz.Service
}

// NewQuizHandler constructs a QuizHandler.
func NewQuizHandler(svc *quiz.Service) *QuizHandler { return &QuizHandler{svc: svc} }

// Generate stubs POST /quiz/generate.
func (h *QuizHandler) Generate(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Attempt stubs POST /quiz/attempt?id=...
func (h *QuizHandler) Attempt(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Share stubs POST /quiz/share?id=...
func (h *QuizHandler) Share(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
```

```go
// api/handler/plan_stub.go
package handler

import (
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/plan"
)

// PlanHandler stubs Spec B endpoints.
type PlanHandler struct {
	svc *plan.Service
}

// NewPlanHandler constructs a PlanHandler.
func NewPlanHandler(svc *plan.Service) *PlanHandler { return &PlanHandler{svc: svc} }

// Generate stubs POST /plan/generate.
func (h *PlanHandler) Generate(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Progress stubs GET /plan/progress?id=...
func (h *PlanHandler) Progress(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
```

```go
// api/handler/duel_stub.go
package handler

import (
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/duel"
)

// DuelHandler stubs Spec E endpoints.
type DuelHandler struct {
	svc *duel.Service
}

// NewDuelHandler constructs a DuelHandler.
func NewDuelHandler(svc *duel.Service) *DuelHandler { return &DuelHandler{svc: svc} }

// Invite stubs POST /duel/invite.
func (h *DuelHandler) Invite(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Accept stubs POST /duel/accept?id=...
func (h *DuelHandler) Accept(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Connect stubs GET /duel/connect?id=... (WebSocket upgrade in Spec E).
func (h *DuelHandler) Connect(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
```

- [ ] **Step 8: Build + commit**

```bash
go build ./...
git add pkg/aipipeline/ pkg/quiz/ pkg/plan/ pkg/duel/ api/handler/ai_stub.go api/handler/quiz_stub.go api/handler/plan_stub.go api/handler/duel_stub.go internal/myErrors/ internal/httpx/
git commit -m "$(cat <<'EOF'
Future-feature stubs

[+] pkg/aipipeline stub (Spec A entrypoint)
[+] pkg/quiz stub (Spec D entrypoint)
[+] pkg/plan stub (Spec B entrypoint)
[+] pkg/duel stub (Spec E entrypoint)
[+] Handlers for each — return ErrNotImplemented
[+] myErrors.ErrNotImplemented (501 mapping in httpx.WriteError)
EOF
)"
```

---

### Task 33: Billing stub

**Files:**
- Create: `pkg/billing/service.go`
- Create: `api/handler/billing_stub.go`

- [ ] **Step 1: Write `pkg/billing/service.go`**

```go
package billing

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/billing"
	"studbud/backend/internal/myErrors"
)

// Service wraps the billing provider (Stripe in prod, fake in tests).
// Spec C fills in the real flows.
type Service struct {
	db       *pgxpool.Pool   // db is the shared pool
	provider billing.Provider // provider is the underlying billing adapter
}

// NewService constructs a Service.
func NewService(db *pgxpool.Pool, provider billing.Provider) *Service {
	return &Service{db: db, provider: provider}
}

// CreateCheckoutSession returns a URL the user must visit to pay.
// Stub: not implemented until Spec C.
func (s *Service) CreateCheckoutSession(ctx context.Context, uid int64, tier string) (string, error) {
	return "", myErrors.ErrNotImplemented
}

// CreatePortalSession returns a URL for the Stripe customer portal.
func (s *Service) CreatePortalSession(ctx context.Context, uid int64) (string, error) {
	return "", myErrors.ErrNotImplemented
}

// HandleWebhook processes a Stripe webhook payload.
func (s *Service) HandleWebhook(ctx context.Context, signature string, body []byte) error {
	return myErrors.ErrNotImplemented
}
```

- [ ] **Step 2: Write `api/handler/billing_stub.go`**

```go
package handler

import (
	"io"
	"net/http"

	"studbud/backend/internal/httpx"
	"studbud/backend/internal/myErrors"
	"studbud/backend/pkg/billing"
)

// BillingHandler stubs Spec C endpoints.
type BillingHandler struct {
	svc *billing.Service // svc is the (stub) billing service
}

// NewBillingHandler constructs a BillingHandler.
func NewBillingHandler(svc *billing.Service) *BillingHandler {
	return &BillingHandler{svc: svc}
}

// Checkout stubs POST /billing/checkout.
func (h *BillingHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Portal stubs POST /billing/portal.
func (h *BillingHandler) Portal(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}

// Webhook stubs POST /billing/webhook (Stripe).
func (h *BillingHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	// Drain the body so Stripe doesn't retry indefinitely during skeleton testing.
	_, _ = io.Copy(io.Discard, r.Body)
	httpx.WriteError(w, myErrors.ErrNotImplemented)
}
```

- [ ] **Step 3: Build + commit**

```bash
go build ./...
git add pkg/billing/ api/handler/billing_stub.go
git commit -m "$(cat <<'EOF'
Billing stub

[+] pkg/billing.Service: CreateCheckoutSession / CreatePortalSession / HandleWebhook (all stub)
[+] BillingHandler with 3 endpoints returning ErrNotImplemented
EOF
)"
```

---

### Task 34: Dependency wiring (`cmd/app/deps.go`)

**File:** `cmd/app/deps.go`

- [ ] **Step 1: Write `cmd/app/deps.go`**

```go
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/internal/billing"
	"studbud/backend/internal/config"
	"studbud/backend/internal/cron"
	"studbud/backend/internal/duelHub"
	"studbud/backend/internal/email"
	"studbud/backend/internal/jwt"
	"studbud/backend/internal/keywordWorker"
	"studbud/backend/internal/storage"
	"studbud/backend/pkg/access"
	"studbud/backend/pkg/aipipeline"
	"studbud/backend/pkg/chapter"
	"studbud/backend/pkg/collaboration"
	"studbud/backend/pkg/duel"
	"studbud/backend/pkg/emailverification"
	"studbud/backend/pkg/flashcard"
	"studbud/backend/pkg/friendship"
	"studbud/backend/pkg/gamification"
	"studbud/backend/pkg/image"
	"studbud/backend/pkg/billing" // note: domain pkg/billing
	pkgplan "studbud/backend/pkg/plan"
	"studbud/backend/pkg/preferences"
	"studbud/backend/pkg/quiz"
	"studbud/backend/pkg/search"
	"studbud/backend/pkg/subject"
	"studbud/backend/pkg/subjectsub"
	"studbud/backend/pkg/user"
)

// deps bundles every constructed domain service for the router to consume.
type deps struct {
	cfg           config.Config                  // cfg is the loaded application config
	db            *pgxpool.Pool                  // db is the shared Postgres pool
	jwtSigner     *jwt.Signer                    // jwtSigner issues + verifies auth tokens
	store         *storage.FileStore             // store persists image bytes
	emailer       email.Sender                   // emailer sends outbound mail
	cronScheduler *cron.Scheduler                // cronScheduler owns background jobs

	access          *access.Service                // access resolves subject permissions
	user            *user.Service                  // user owns user profile + auth
	emailVerify     *emailverification.Service     // emailVerify issues + verifies codes
	image           *image.Service                 // image owns uploads
	subject         *subject.Service               // subject owns subject CRUD
	chapter         *chapter.Service               // chapter owns chapter CRUD
	flashcard       *flashcard.Service             // flashcard owns card CRUD + reviews
	search          *search.Service                // search serves query endpoints
	friendship      *friendship.Service            // friendship owns friendship lifecycle
	subjectSub      *subjectsub.Service            // subjectSub owns subject subscriptions
	collaboration   *collaboration.Service         // collaboration owns shared-subject logic
	preferences     *preferences.Service           // preferences owns per-user prefs
	gamification    *gamification.Service          // gamification owns streaks / goals / achievements

	ai      *aipipeline.Service  // ai is the AI pipeline stub (Spec A replaces)
	quiz    *quiz.Service        // quiz is the quiz stub (Spec D replaces)
	plan    *pkgplan.Service     // plan is the plan stub (Spec B replaces)
	duel    *duel.Service        // duel is the duel stub (Spec E replaces)
	billing *billing.Service     // billing is the billing stub (Spec C replaces)
}

// buildDeps constructs every service from the provided config.
// It returns a cleanup function to close shared resources on shutdown.
func buildDeps(ctx context.Context, cfg config.Config) (*deps, func(), error) {
	db, err := openPool(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { db.Close() }

	signer := jwt.NewSigner(cfg.JWTSecret, cfg.JWTTTL)
	store := storage.NewFileStore(cfg.ImageStoreDir)
	emailer := buildEmailer(cfg)
	scheduler := cron.NewScheduler()

	aiClient := aiProvider.NewClient(cfg.OpenAIAPIKey)
	billingProv := billing.NewProvider(cfg.StripeSecretKey)
	duelHubInst := duelHub.NewHub()
	_ = keywordWorker.New(db) // not started in the skeleton

	acc := access.NewService(db)
	usrSvc := user.NewService(db, signer)
	emailVerSvc := emailverification.NewService(db, emailer, cfg.PublicURL)
	imgSvc := image.NewService(db, store)
	subjSvc := subject.NewService(db, acc)
	chSvc := chapter.NewService(db, acc)
	fcSvc := flashcard.NewService(db, acc)
	searchSvc := search.NewService(db)
	friendSvc := friendship.NewService(db)
	subscSvc := subjectsub.NewService(db, acc)
	collabSvc := collaboration.NewService(db, acc)
	prefSvc := preferences.NewService(db)
	gamSvc := gamification.NewService(db)

	aiSvc := aipipeline.NewService(db, aiClient)
	quizSvc := quiz.NewService(db)
	planSvc := pkgplan.NewService(db)
	duelSvc := duel.NewService(db, duelHubInst)
	billSvc := pkgbilling.NewService(db, billingProv) // alias: see import block

	return &deps{
		cfg: cfg, db: db, jwtSigner: signer, store: store, emailer: emailer, cronScheduler: scheduler,
		access: acc, user: usrSvc, emailVerify: emailVerSvc, image: imgSvc,
		subject: subjSvc, chapter: chSvc, flashcard: fcSvc, search: searchSvc,
		friendship: friendSvc, subjectSub: subscSvc, collaboration: collabSvc,
		preferences: prefSvc, gamification: gamSvc,
		ai: aiSvc, quiz: quizSvc, plan: planSvc, duel: duelSvc, billing: billSvc,
	}, cleanup, nil
}

func openPool(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open pool:\n%w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db:\n%w", err)
	}
	return pool, nil
}

func buildEmailer(cfg config.Config) email.Sender {
	if cfg.Env == "test" || cfg.SMTPHost == "" {
		return email.NewRecorder()
	}
	return email.NewSMTPSender(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
}
```

> **Note on import collision:** `pkg/billing` and `internal/billing` share the package name `billing`. Use an import alias in `deps.go` — rename the domain import to `pkgbilling`:
> ```go
> pkgbilling "studbud/backend/pkg/billing"
> ```
> and adjust the variable `billSvc := pkgbilling.NewService(...)`. Update the struct field type to `*pkgbilling.Service` accordingly. Pick whichever alias convention the earlier tasks used and be consistent.

- [ ] **Step 2: Build**

```bash
go build ./cmd/app/...
```
Expected: succeeds with no errors.

- [ ] **Step 3: Commit**

```bash
git add cmd/app/deps.go
git commit -m "$(cat <<'EOF'
Dependency wiring

[+] cmd/app/deps.go: buildDeps constructs every service + cleanup
[+] Pool opened via pgxpool; clock / emailer / AI client / billing provider wired
[+] pkgbilling alias avoids collision with internal/billing package name
EOF
)"
```

---

### Task 35: Router + main (`cmd/app/routes.go` and `cmd/app/main.go`)

**Files:**
- Create: `cmd/app/routes.go`
- Create: `cmd/app/main.go`
- Modify: `db_sql/setup.go` (if not already exposing `SetupAll`)

- [ ] **Step 1: Write `cmd/app/routes.go`**

```go
package main

import (
	"net/http"

	"studbud/backend/api/handler"
	"studbud/backend/internal/http/middleware"
)

// buildRouter composes all handlers into an http.Handler protected by global middleware.
func buildRouter(d *deps) http.Handler {
	mux := http.NewServeMux()

	userH := handler.NewUserHandler(d.user)
	emailVerH := handler.NewEmailVerificationHandler(d.emailVerify)
	imgH := handler.NewImageHandler(d.image)
	subjH := handler.NewSubjectHandler(d.subject)
	chapH := handler.NewChapterHandler(d.chapter)
	fcH := handler.NewFlashcardHandler(d.flashcard)
	searchH := handler.NewSearchHandler(d.search)
	friendH := handler.NewFriendshipHandler(d.friendship)
	subsH := handler.NewSubjectSubscriptionHandler(d.subjectSub)
	collabH := handler.NewCollaborationHandler(d.collaboration)
	prefH := handler.NewPreferencesHandler(d.preferences)
	gamH := handler.NewGamificationHandler(d.gamification)

	aiH := handler.NewAIHandler(d.ai)
	quizH := handler.NewQuizHandler(d.quiz)
	planH := handler.NewPlanHandler(d.plan)
	duelH := handler.NewDuelHandler(d.duel)
	billH := handler.NewBillingHandler(d.billing)

	authMW := middleware.Auth(d.jwtSigner)
	verifiedMW := middleware.RequireVerified(d.db)

	// Public.
	mux.HandleFunc("POST /register", userH.Register)
	mux.HandleFunc("POST /login", userH.Login)
	mux.HandleFunc("POST /email-verify-issue", emailVerH.Issue)
	mux.HandleFunc("POST /email-verify-confirm", emailVerH.Verify)
	mux.HandleFunc("GET /images/{id}", imgH.Open)

	// Stripe webhook (public; verified by HMAC in Spec C).
	mux.HandleFunc("POST /billing/webhook", billH.Webhook)

	// Authenticated.
	mux.Handle("GET /me", authMW(http.HandlerFunc(userH.Me)))
	mux.Handle("POST /profile-picture", authMW(http.HandlerFunc(userH.SetProfilePicture)))
	mux.Handle("POST /image-upload", authMW(http.HandlerFunc(imgH.Upload)))
	mux.Handle("POST /image-delete", authMW(http.HandlerFunc(imgH.Delete)))

	mux.Handle("POST /subject-create", authMW(verifiedMW(http.HandlerFunc(subjH.Create))))
	mux.Handle("GET /subject-list", authMW(http.HandlerFunc(subjH.List)))
	mux.Handle("GET /subject", authMW(http.HandlerFunc(subjH.Get)))
	mux.Handle("POST /subject-update", authMW(verifiedMW(http.HandlerFunc(subjH.Update))))
	mux.Handle("POST /subject-delete", authMW(verifiedMW(http.HandlerFunc(subjH.Delete))))

	mux.Handle("POST /chapter-create", authMW(verifiedMW(http.HandlerFunc(chapH.Create))))
	mux.Handle("GET /chapter-list", authMW(http.HandlerFunc(chapH.List)))
	mux.Handle("POST /chapter-update", authMW(verifiedMW(http.HandlerFunc(chapH.Update))))
	mux.Handle("POST /chapter-delete", authMW(verifiedMW(http.HandlerFunc(chapH.Delete))))

	mux.Handle("POST /flashcard-create", authMW(verifiedMW(http.HandlerFunc(fcH.Create))))
	mux.Handle("GET /flashcard-list", authMW(http.HandlerFunc(fcH.ListBySubject)))
	mux.Handle("GET /flashcard", authMW(http.HandlerFunc(fcH.Get)))
	mux.Handle("POST /flashcard-update", authMW(verifiedMW(http.HandlerFunc(fcH.Update))))
	mux.Handle("POST /flashcard-delete", authMW(verifiedMW(http.HandlerFunc(fcH.Delete))))
	mux.Handle("POST /flashcard-review", authMW(http.HandlerFunc(fcH.Review)))

	mux.Handle("GET /search/subjects", authMW(http.HandlerFunc(searchH.Subjects)))
	mux.Handle("GET /search/users", authMW(http.HandlerFunc(searchH.Users)))

	mux.Handle("POST /friend-request", authMW(verifiedMW(http.HandlerFunc(friendH.Request))))
	mux.Handle("POST /friend-accept", authMW(http.HandlerFunc(friendH.Accept)))
	mux.Handle("POST /friend-decline", authMW(http.HandlerFunc(friendH.Decline)))
	mux.Handle("POST /friend-remove", authMW(http.HandlerFunc(friendH.Unfriend)))
	mux.Handle("GET /friends", authMW(http.HandlerFunc(friendH.ListFriends)))
	mux.Handle("GET /friends-pending", authMW(http.HandlerFunc(friendH.ListPending)))

	mux.Handle("POST /subject-subscribe", authMW(http.HandlerFunc(subsH.Subscribe)))
	mux.Handle("POST /subject-unsubscribe", authMW(http.HandlerFunc(subsH.Unsubscribe)))
	mux.Handle("GET /subject-subscriptions", authMW(http.HandlerFunc(subsH.List)))

	mux.Handle("POST /collaborator-add", authMW(verifiedMW(http.HandlerFunc(collabH.AddCollaborator))))
	mux.Handle("POST /collaborator-remove", authMW(verifiedMW(http.HandlerFunc(collabH.RemoveCollaborator))))
	mux.Handle("GET /collaborators", authMW(http.HandlerFunc(collabH.ListCollaborators)))
	mux.Handle("POST /invite-create", authMW(verifiedMW(http.HandlerFunc(collabH.CreateInvite))))
	mux.Handle("POST /invite-redeem", authMW(verifiedMW(http.HandlerFunc(collabH.RedeemInvite))))

	mux.Handle("GET /preferences", authMW(http.HandlerFunc(prefH.Get)))
	mux.Handle("POST /preferences-update", authMW(http.HandlerFunc(prefH.Update)))

	mux.Handle("GET /gamification-state", authMW(http.HandlerFunc(gamH.State)))
	mux.Handle("POST /training-session-record", authMW(http.HandlerFunc(gamH.RecordSession)))
	mux.Handle("GET /user-stats", authMW(http.HandlerFunc(gamH.Stats)))
	mux.Handle("GET /achievements", authMW(http.HandlerFunc(gamH.Achievements)))

	// Stubs (authenticated + verified where entitlement will later gate).
	mux.Handle("POST /ai/flashcards/prompt", authMW(verifiedMW(http.HandlerFunc(aiH.GenerateFromPrompt))))
	mux.Handle("POST /ai/flashcards/pdf", authMW(verifiedMW(http.HandlerFunc(aiH.GenerateFromPDF))))
	mux.Handle("POST /ai/check", authMW(verifiedMW(http.HandlerFunc(aiH.Check))))

	mux.Handle("POST /quiz/generate", authMW(verifiedMW(http.HandlerFunc(quizH.Generate))))
	mux.Handle("POST /quiz/attempt", authMW(http.HandlerFunc(quizH.Attempt)))
	mux.Handle("POST /quiz/share", authMW(http.HandlerFunc(quizH.Share)))

	mux.Handle("POST /plan/generate", authMW(verifiedMW(http.HandlerFunc(planH.Generate))))
	mux.Handle("GET /plan/progress", authMW(http.HandlerFunc(planH.Progress)))

	mux.Handle("POST /duel/invite", authMW(verifiedMW(http.HandlerFunc(duelH.Invite))))
	mux.Handle("POST /duel/accept", authMW(http.HandlerFunc(duelH.Accept)))
	mux.Handle("GET /duel/connect", authMW(http.HandlerFunc(duelH.Connect)))

	mux.Handle("POST /billing/checkout", authMW(http.HandlerFunc(billH.Checkout)))
	mux.Handle("POST /billing/portal", authMW(http.HandlerFunc(billH.Portal)))

	// Global middleware chain.
	stack := middleware.Chain(
		middleware.Recoverer,
		middleware.RequestID,
		middleware.CORS(d.cfg.AllowedOrigins),
		middleware.Logger,
	)
	return stack(mux)
}
```

- [ ] **Step 2: Write `cmd/app/main.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"studbud/backend/db_sql"
	"studbud/backend/internal/config"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

// run wires config, DB, services, router, and starts the HTTP server with graceful shutdown.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config:\n%w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, cleanup, err := buildDeps(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := db_sql.SetupAll(ctx, d.db); err != nil {
		return fmt.Errorf("setup schema:\n%w", err)
	}

	d.cronScheduler.Start(ctx)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           buildRouter(d),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("listen error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown:\n%w", err)
	}
	return nil
}
```

- [ ] **Step 3: Build + run**

```bash
go build ./...
```
Expected: succeeds.

```bash
# Optional: smoke-check the binary starts. Requires a running DB.
bash launch_app.sh &
sleep 2
curl -sS http://localhost:8080/register -XPOST -H 'Content-Type: application/json' \
  -d '{"email":"a@b.co","password":"pw","username":"alice"}' | head -c 200
kill %1
```

- [ ] **Step 4: Commit**

```bash
git add cmd/app/routes.go cmd/app/main.go
git commit -m "$(cat <<'EOF'
Router + main entrypoint

[+] cmd/app/routes.go: ServeMux routes for every domain + stubs + middleware chain
[+] cmd/app/main.go: config load -> deps -> SetupAll -> server start -> graceful shutdown
[+] Smoke check: binary starts and /register accepts requests
EOF
)"
```

---

### Task 36: End-to-end test

Covers the most impactful user-visible flow across the stack: register → verify → login → create subject → upload image → create chapter → create flashcard → review → list → record session → achievements. All against real HTTP + Postgres.

**Files:**
- Create: `cmd/app/e2e_test.go`

- [ ] **Step 1: Write `cmd/app/e2e_test.go`**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"studbud/backend/db_sql"
	"studbud/backend/internal/email"
	"studbud/backend/testutil"
)

func TestE2E_RegisterThroughTraining(t *testing.T) {
	cfg := testutil.TestConfig(t)
	ctx := context.Background()

	d, cleanup, err := buildDeps(ctx, cfg)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	defer cleanup()
	if err := db_sql.SetupAll(ctx, d.db); err != nil {
		t.Fatalf("setup schema: %v", err)
	}
	testutil.Reset(t, d.db)

	srv := httptest.NewServer(buildRouter(d))
	defer srv.Close()

	cli := &testClient{base: srv.URL}

	// 1. Register.
	regResp := cli.mustPostJSON(t, "/register", map[string]any{
		"email":    "alice@example.com",
		"password": "hunter2",
		"username": "alice",
	})
	var reg struct {
		Token string `json:"token"`
		User  struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	mustJSON(t, regResp, &reg)
	if reg.Token == "" || reg.User.ID == 0 {
		t.Fatalf("bad register response: %+v", reg)
	}
	cli.token = reg.Token

	// 2. Issue + verify email (recorder emailer captured the token link).
	cli.mustPostJSON(t, "/email-verify-issue", map[string]any{})
	rec, ok := d.emailer.(*email.Recorder)
	if !ok {
		t.Fatalf("expected *email.Recorder, got %T", d.emailer)
	}
	sent := rec.Sent()
	if len(sent) == 0 {
		t.Fatal("expected at least one verification email to be captured")
	}
	body := sent[len(sent)-1].Body
	tokIdx := strings.Index(body, "token=")
	if tokIdx < 0 {
		t.Fatalf("email body missing token=: %q", body)
	}
	token := body[tokIdx+len("token="):]
	cli.mustPostJSON(t, "/email-verify-confirm", map[string]any{"token": token})

	// 3. Create subject.
	subjResp := cli.mustPostJSON(t, "/subject-create", map[string]any{
		"name": "Biology", "visibility": "private",
	})
	var sub struct {
		ID int64 `json:"id"`
	}
	mustJSON(t, subjResp, &sub)

	// 4. Upload image.
	imgID := uploadImage(t, cli)

	// 5. Create chapter.
	chapResp := cli.mustPostJSON(t, "/chapter-create", map[string]any{
		"subject_id": sub.ID, "title": "Cells",
	})
	var ch struct {
		ID int64 `json:"id"`
	}
	mustJSON(t, chapResp, &ch)

	// 6. Create flashcard with image.
	fcResp := cli.mustPostJSON(t, "/flashcard-create", map[string]any{
		"subject_id": sub.ID,
		"chapter_id": ch.ID,
		"question":   "What is a cell?",
		"answer":     "The basic unit of life.",
		"image_id":   imgID,
	})
	var fc struct {
		ID int64 `json:"id"`
	}
	mustJSON(t, fcResp, &fc)

	// 7. Review the card.
	cli.mustPostJSON(t, fmt.Sprintf("/flashcard-review?id=%d", fc.ID), map[string]any{"result": 2})

	// 8. Record a training session.
	sessResp := cli.mustPostJSON(t, "/training-session-record", map[string]any{
		"subject_id": sub.ID, "card_count": 1, "duration_ms": 5000, "score": 100,
	})
	var sessRes struct {
		Streak struct {
			CurrentStreak int `json:"current_streak"`
		} `json:"streak"`
		NewlyAwarded []struct {
			Code string `json:"code"`
		} `json:"newly_awarded"`
	}
	mustJSON(t, sessResp, &sessRes)
	if sessRes.Streak.CurrentStreak != 1 {
		t.Fatalf("expected streak=1, got %d", sessRes.Streak.CurrentStreak)
	}
	foundFirst := false
	for _, a := range sessRes.NewlyAwarded {
		if a.Code == "first_session" {
			foundFirst = true
		}
	}
	if !foundFirst {
		t.Fatal("expected first_session achievement")
	}

	// 9. Fetch /me to confirm auth still works.
	meResp := cli.mustGet(t, "/me")
	var me struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	mustJSON(t, meResp, &me)
	if me.Username != "alice" {
		t.Fatalf("unexpected /me: %+v", me)
	}

	// 10. Verify a stub route returns 501.
	code501 := cli.rawPost(t, "/ai/flashcards/prompt", `{}`)
	if code501 != http.StatusNotImplemented {
		t.Fatalf("expected 501 from AI stub, got %d", code501)
	}
}

// testClient is a minimal HTTP client that attaches the bearer token.
type testClient struct {
	base  string // base is the test server base URL
	token string // token is the bearer token attached to authenticated calls
}

func (c *testClient) mustPostJSON(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s -> %d: %s", path, resp.StatusCode, string(body))
	}
	return resp
}

func (c *testClient) rawPost(t *testing.T, path, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", c.base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (c *testClient) mustGet(t *testing.T, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s -> %d: %s", path, resp.StatusCode, string(body))
	}
	return resp
}

func mustJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// onePixelPNG returns a minimal 1x1 PNG byte slice (mirrors pkg/image/service_test.go).
var onePixelPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00,
	0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x5B, 0x2F, 0xC0, 0x0F, 0x00, 0x00, 0x00,
	0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

// uploadImage posts a minimal PNG to /image-upload and returns the image id.
func uploadImage(t *testing.T, cli *testClient) string {
	t.Helper()
	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	fw, _ := mp.CreateFormFile("file", "pixel.png")
	fw.Write(onePixelPNG)
	mp.Close()

	req, _ := http.NewRequest("POST", cli.base+"/image-upload", &buf)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+cli.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload -> %d: %s", resp.StatusCode, string(body))
	}
	var r struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	return r.ID
}
```

> **Required testutil additions for this e2e test** (add to `testutil/config.go`):
>
> ```go
> package testutil
>
> import (
>     "os"
>     "testing"
>     "time"
>
>     "studbud/backend/internal/config"
> )
>
> // TestConfig builds a Config suitable for e2e tests: test DB, small temp image dir, permissive CORS.
> func TestConfig(t *testing.T) config.Config {
>     t.Helper()
>     return config.Config{
>         Env:            "test",
>         HTTPAddr:       "127.0.0.1:0",
>         DatabaseURL:    os.Getenv("DATABASE_URL"),
>         JWTSecret:      "test-secret-at-least-32-chars-long!!",
>         JWTTTL:         720 * time.Hour,
>         ImageStoreDir:  t.TempDir(),
>         PublicURL:      "http://test.local",
>         AllowedOrigins: []string{"*"},
>     }
> }
> ```
>
> `Reset`, `EmailRecorder` (struct with `.Sent() []Email`), `NewUser`, `NewVerifiedUser`, `NewSubject` are already defined by Task 19. The e2e test uses `testutil.Reset` to wipe tables between runs and casts `d.emailer.(*testutil.EmailRecorder)` to pull captured emails via `Sent()`.

- [ ] **Step 2: Run e2e test**

```bash
ENV=test DATABASE_URL=postgres://studbud:studbud@localhost:5432/studbud_test go test ./cmd/app/... -v -run TestE2E_RegisterThroughTraining
```
Expected: PASS.

- [ ] **Step 3: Run the full test suite**

```bash
ENV=test DATABASE_URL=postgres://studbud:studbud@localhost:5432/studbud_test go test ./... -p 1 -count=1
```
Expected: PASS across all packages.

- [ ] **Step 4: Commit**

```bash
git add cmd/app/e2e_test.go testutil/
git commit -m "$(cat <<'EOF'
End-to-end skeleton test

[+] cmd/app/e2e_test.go: register -> verify -> login -> subject -> image -> chapter -> flashcard -> review -> session -> achievements
[+] Confirms AI stub still returns 501 (skeleton boundary)
[+] testutil.TestConfig / TruncateAll / OnePixelPNG / EmailRecorder helpers
EOF
)"
```

---

## Self-Review Notes (append, do not implement — for the executing engineer)

After completing Task 36, run the following checks before declaring the skeleton done:

1. `go vet ./...` — zero warnings.
2. `gofmt -l .` — empty output.
3. `grep -Rn "replace " go.mod` — empty (no replace directives; see CLAUDE.md §go.mod Hygiene).
4. `go test ./... -p 1 -count=1` — all green.
5. `bash launch_app.sh` — starts cleanly and responds on `/register`.
6. Ensure no file exceeds 400 lines (see CLAUDE.md §File Size). If any do and are not a single-type cohesive file, split them.
7. Ensure every exported type/func has a docstring (see CLAUDE.md §Documentation Requirements).
8. Spot-check that every `db_sql/setup_*.go` is idempotent by running `SetupAll` twice against a fresh DB (the Task 18 idempotency test covers this automatically).

When all eight checks pass, the skeleton is ready and subsequent specs (A, B, B.0, C, D, E) can be built on top without schema migrations.

---

## Final Handoff

Once Task 36 is green:

- The server binary runs and answers every real endpoint.
- Every DB table referenced by specs A–E already exists.
- Stub endpoints for AI, quiz, plan, duel, and billing respond with `501 Not Implemented` — the contract is live, the business logic just isn't.
- Shipping Spec A only means replacing the body of `pkg/aipipeline/*.go`; no router, handler, DB, or wiring changes are needed.
