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
