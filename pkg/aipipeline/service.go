package aipipeline

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"studbud/backend/internal/aiProvider"
	"studbud/backend/pkg/access"
)

// Service is the AI pipeline facade.
type Service struct {
	db       *pgxpool.Pool     // db is the shared pool
	provider aiProvider.Client // provider is the Anthropic (or noop) client
	access   *access.Service   // access answers entitlement questions
	limits   QuotaLimits       // limits bounds per-feature daily calls
	model    string            // model is the provider model identifier
}

// NewService constructs a Service. Methods are filled in across later tasks.
func NewService(db *pgxpool.Pool, provider aiProvider.Client, access *access.Service, limits QuotaLimits, model string) *Service {
	return &Service{db: db, provider: provider, access: access, limits: limits, model: model}
}
