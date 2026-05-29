package dolt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCircuitBreakerDir_PerUser verifies that the circuit breaker directory is
// scoped to the current user rather than the fixed shared /tmp/beads-circuit
// path. A shared path lets the first user's 0600 state files block every other
// user on a multi-user host, causing server-mode bd to fail closed before it
// can connect (GH#4223).
func TestCircuitBreakerDir_PerUser(t *testing.T) {
	dir := circuitBreakerDir()

	if dir == "/tmp/beads-circuit" {
		t.Fatalf("circuitBreakerDir() returned the shared fixed path %q; it must be per-user", dir)
	}

	// The resolved dir must be user-scoped: either under the user cache dir, or
	// the per-uid TempDir fallback. Both guarantee distinct users never share.
	cache, cacheErr := os.UserCacheDir()
	uidFallback := filepath.Join(os.TempDir(), fmt.Sprintf("beads-circuit-%d", os.Getuid()))

	switch {
	case cacheErr == nil && cache != "" && dir == filepath.Join(cache, "beads-circuit"):
		// user cache dir — already private to the user
	case dir == uidFallback:
		// per-uid TempDir fallback — encodes the uid so it is per-user
		if !strings.Contains(dir, fmt.Sprintf("%d", os.Getuid())) {
			t.Fatalf("TempDir fallback %q does not include the uid", dir)
		}
	default:
		t.Fatalf("circuitBreakerDir() = %q, expected %q or %q",
			dir, filepath.Join(cache, "beads-circuit"), uidFallback)
	}
}

// TestCircuitBreaker_FilePathInPerUserDir verifies a constructed breaker writes
// its state file inside the per-user directory and that state round-trips.
func TestCircuitBreaker_FilePathInPerUserDir(t *testing.T) {
	cb := newCircuitBreaker("127.0.0.1", 43217, "proj_4223")
	t.Cleanup(func() { os.Remove(cb.filePath) })

	if filepath.Dir(cb.filePath) != filepath.Clean(circuitBreakerDir()) {
		t.Fatalf("breaker file %q is not under per-user dir %q", cb.filePath, circuitBreakerDir())
	}
	if filepath.Dir(cb.filePath) == "/tmp/beads-circuit" {
		t.Fatalf("breaker file landed in the shared fixed path: %q", cb.filePath)
	}

	// Round-trip: write an open state, read it back.
	want := circuitState{State: circuitOpen, Failures: circuitFailureThreshold}
	cb.writeState(want)

	got := cb.readState()
	if got.State != circuitOpen {
		t.Fatalf("round-trip state = %q, want %q", got.State, circuitOpen)
	}
	if got.Failures != circuitFailureThreshold {
		t.Fatalf("round-trip failures = %d, want %d", got.Failures, circuitFailureThreshold)
	}
}
