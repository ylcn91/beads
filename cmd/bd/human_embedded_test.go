//go:build cgo

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// bdHuman runs "bd human" with the given args and returns stdout.
func bdHuman(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"human"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd human %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestEmbeddedHuman(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "th")

	// ===== Default Help Output =====

	t.Run("human_default", func(t *testing.T) {
		out := bdHuman(t, bd, dir)
		if len(strings.TrimSpace(out)) == 0 {
			t.Error("expected non-empty human output")
		}
	})

	// ===== List =====

	t.Run("human_list_empty", func(t *testing.T) {
		out := bdHuman(t, bd, dir, "list")
		// No human-labeled issues yet — should succeed without error
		_ = out
	})

	// ===== Stats =====

	t.Run("human_stats", func(t *testing.T) {
		out := bdHuman(t, bd, dir, "stats")
		// Should succeed and produce output
		if len(strings.TrimSpace(out)) == 0 {
			t.Error("expected non-empty stats output")
		}
	})

	// ===== Respond / Dismiss (GH#3596) =====
	// "human" is in noDbCommands, so respond/dismiss must lazily activate the
	// store; before the fix they failed with "storage is nil" in embedded mode.

	t.Run("human_respond", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Needs human input", "--labels", "human")
		out := bdHuman(t, bd, dir, "respond", issue.ID, "--response", "looks good")
		if strings.Contains(out, "storage is nil") {
			t.Fatalf("human respond hit nil store (GH#3596): %s", out)
		}
	})

	t.Run("human_dismiss", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Dismiss me", "--labels", "human")
		out := bdHuman(t, bd, dir, "dismiss", issue.ID, "--reason", "not needed")
		if strings.Contains(out, "storage is nil") {
			t.Fatalf("human dismiss hit nil store (GH#3596): %s", out)
		}
	})
}

// TestEmbeddedHumanConcurrent exercises human operations concurrently.
func TestEmbeddedHumanConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hx")

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

			var args []string
			switch worker % 2 {
			case 0:
				args = []string{"human", "list"}
			case 1:
				args = []string{"human", "stats"}
			}
			cmd := exec.Command(bd, args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("human (worker %d): %v\n%s", worker, err, out)
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
