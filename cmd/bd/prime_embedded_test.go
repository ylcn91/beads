//go:build cgo

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// bdPrime runs "bd prime" with the given args and returns stdout.
func bdPrime(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"prime"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd prime %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestEmbeddedPrime(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tp")

	// ===== Default Output =====

	t.Run("prime_default", func(t *testing.T) {
		out := bdPrime(t, bd, dir)
		if len(strings.TrimSpace(out)) == 0 {
			t.Error("expected non-empty prime output")
		}
	})

	// ===== Full Flag =====

	t.Run("prime_full", func(t *testing.T) {
		out := bdPrime(t, bd, dir, "--full")
		if len(strings.TrimSpace(out)) == 0 {
			t.Error("expected non-empty prime --full output")
		}
		// Full mode should include command references
		if !strings.Contains(out, "bd") {
			t.Errorf("expected 'bd' command references in --full output: %s", out[:min(200, len(out))])
		}
	})

	// ===== Export Flag =====

	t.Run("prime_export", func(t *testing.T) {
		out := bdPrime(t, bd, dir, "--export")
		if len(strings.TrimSpace(out)) == 0 {
			t.Error("expected non-empty prime --export output")
		}
	})

	// ===== Memories Injected =====

	t.Run("prime_memories_injected", func(t *testing.T) {
		// Store a memory
		cmd := exec.Command(bd, "remember", "always use -race flag in tests", "--key", "prime-test-mem")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd remember failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}

		// Prime should include the memory
		primeOut := bdPrime(t, bd, dir, "--full")
		if !strings.Contains(primeOut, "race") {
			t.Errorf("expected memory content in prime output: %s", primeOut[:min(500, len(primeOut))])
		}
		memoryIdx := strings.Index(primeOut, "prime-test-mem")
		sessionIdx := strings.Index(primeOut, "SESSION CLOSE PROTOCOL")
		if memoryIdx == -1 || sessionIdx == -1 || memoryIdx > sessionIdx {
			t.Errorf("expected memories before session protocol in prime output")
		}
	})

	// ===== Memories Only =====

	t.Run("prime_memories_only", func(t *testing.T) {
		out := bdPrime(t, bd, dir, "--memories-only")
		if !strings.Contains(out, "prime-test-mem") {
			t.Errorf("expected memory content in --memories-only output: %s", out)
		}
		if strings.Contains(out, "Essential Commands") {
			t.Errorf("expected --memories-only to omit full command guide: %s", out)
		}
	})

	t.Run("prime_memories_only_ignores_custom_PRIME_md", func(t *testing.T) {
		// A custom .beads/PRIME.md overrides the default prime output, but
		// --memories-only must still emit only memories, never the custom
		// document (GH#3941).
		primePath := filepath.Join(dir, ".beads", "PRIME.md")
		const marker = "CUSTOM-PRIME-MARKER-do-not-emit"
		if err := os.WriteFile(primePath, []byte("# Custom\n"+marker+"\n"), 0o644); err != nil {
			t.Fatalf("write custom PRIME.md: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(primePath) })

		// Core of GH#3941: --memories-only must NOT emit the custom PRIME.md,
		// and must still produce the memories-only envelope (truncation
		// directive), not the full custom document.
		out := bdPrime(t, bd, dir, "--memories-only")
		if strings.Contains(out, marker) {
			t.Errorf("--memories-only must not emit the custom PRIME.md: %s", out)
		}
		if !strings.HasPrefix(out, primeTruncationDirective) {
			t.Errorf("--memories-only should emit the memories envelope, got: %s", out)
		}

		// Sanity: plain prime DOES honor the custom override.
		plain := bdPrime(t, bd, dir)
		if !strings.Contains(plain, marker) {
			t.Errorf("plain prime should emit the custom PRIME.md: %s", plain)
		}
	})
}

// TestEmbeddedPrimeConcurrent exercises prime operations concurrently.
func TestEmbeddedPrimeConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "px")

	const numWorkers = 8

	type workerResult struct {
		worker int
		err    error
	}

	results := make([]workerResult, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			r := workerResult{worker: worker}

			cmd := exec.Command(bd, "prime", "--full")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("prime --full (worker %d): %v\n%s", worker, err, out)
				results[worker] = r
				return
			}
			if len(strings.TrimSpace(string(out))) == 0 {
				r.err = fmt.Errorf("prime --full (worker %d): empty output", worker)
				results[worker] = r
				return
			}

			results[worker] = r
		}(w)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil && !strings.Contains(r.err.Error(), "one writer at a time") {
			t.Errorf("worker %d failed: %v", r.worker, r.err)
		}
	}
}
