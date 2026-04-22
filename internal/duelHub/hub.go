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
