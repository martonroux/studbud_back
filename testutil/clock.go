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
