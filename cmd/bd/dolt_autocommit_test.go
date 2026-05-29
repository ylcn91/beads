package main

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// fakeAutoCommitStore records which commit method maybeAutoCommitStore invokes.
// It embeds storage.DoltStorage so the large interface is satisfied; only the
// methods maybeAutoCommitStore actually calls are implemented.
type fakeAutoCommitStore struct {
	storage.DoltStorage
	commitMsgs     []string
	withConfigMsgs []string
}

func (f *fakeAutoCommitStore) Commit(_ context.Context, msg string) error {
	f.commitMsgs = append(f.commitMsgs, msg)
	return nil
}

func (f *fakeAutoCommitStore) CommitWithConfig(_ context.Context, msg string) error {
	f.withConfigMsgs = append(f.withConfigMsgs, msg)
	return nil
}

func (f *fakeAutoCommitStore) IsClosed() bool { return false }

// TestMaybeAutoCommitRoutesConfigWrites verifies the GH#4078 fix: a command
// that touched config (IncludeConfig) must commit through CommitWithConfig,
// since the default Commit path excludes the config table (GH#2455) and would
// silently drop config-only writes in server mode.
func TestMaybeAutoCommitRoutesConfigWrites(t *testing.T) {
	old := doltAutoCommit
	defer func() { doltAutoCommit = old }()
	doltAutoCommit = "on"
	ctx := context.Background()

	// Non-config write → Commit (config excluded).
	plain := &fakeAutoCommitStore{}
	if err := maybeAutoCommitStore(ctx, plain, doltAutoCommitParams{Command: "update"}); err != nil {
		t.Fatalf("maybeAutoCommitStore(update): %v", err)
	}
	if len(plain.commitMsgs) != 1 || len(plain.withConfigMsgs) != 0 {
		t.Fatalf("non-config write should use Commit: commit=%d withConfig=%d",
			len(plain.commitMsgs), len(plain.withConfigMsgs))
	}

	// Config-touching write → CommitWithConfig.
	cfg := &fakeAutoCommitStore{}
	if err := maybeAutoCommitStore(ctx, cfg, doltAutoCommitParams{Command: "remember", IncludeConfig: true}); err != nil {
		t.Fatalf("maybeAutoCommitStore(remember): %v", err)
	}
	if len(cfg.withConfigMsgs) != 1 || len(cfg.commitMsgs) != 0 {
		t.Fatalf("config write should use CommitWithConfig: commit=%d withConfig=%d",
			len(cfg.commitMsgs), len(cfg.withConfigMsgs))
	}
}

func TestFormatDoltAutoCommitMessage(t *testing.T) {
	msg := formatDoltAutoCommitMessage("update", "alice", []string{"bd-2", "bd-1", "bd-2", "", "bd-3"})
	if msg != "bd: update (auto-commit) by alice [bd-1, bd-2, bd-3]" {
		t.Fatalf("unexpected message: %q", msg)
	}

	// Caps IDs (max 5) and sorts
	msg = formatDoltAutoCommitMessage("create", "bob", []string{"z-9", "a-1", "m-3", "b-2", "c-4", "d-5", "e-6"})
	if msg != "bd: create (auto-commit) by bob [a-1, b-2, c-4, d-5, e-6]" {
		t.Fatalf("unexpected capped message: %q", msg)
	}

	// Empty command/actor fallbacks
	msg = formatDoltAutoCommitMessage("", "", nil)
	if msg != "bd: write (auto-commit) by unknown" {
		t.Fatalf("unexpected fallback message: %q", msg)
	}
}

func TestIsDoltNothingToCommit(t *testing.T) {
	if isDoltNothingToCommit(nil) {
		t.Fatal("nil error should not be treated as nothing-to-commit")
	}
	if !isDoltNothingToCommit(errors.New("nothing to commit")) {
		t.Fatal("expected nothing-to-commit to be detected")
	}
	if !isDoltNothingToCommit(errors.New("No changes to commit")) {
		t.Fatal("expected no-changes-to-commit to be detected")
	}
	if isDoltNothingToCommit(errors.New("permission denied")) {
		t.Fatal("unexpected classification")
	}
}

func TestGetDoltAutoCommitMode_Batch(t *testing.T) {
	old := doltAutoCommit
	defer func() { doltAutoCommit = old }()

	doltAutoCommit = "batch"
	mode, err := getDoltAutoCommitMode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != doltAutoCommitBatch {
		t.Fatalf("expected batch, got %q", mode)
	}

	// Also verify the other modes still work
	doltAutoCommit = "on"
	mode, err = getDoltAutoCommitMode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != doltAutoCommitOn {
		t.Fatalf("expected on, got %q", mode)
	}

	doltAutoCommit = "off"
	mode, err = getDoltAutoCommitMode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != doltAutoCommitOff {
		t.Fatalf("expected off, got %q", mode)
	}

	// Invalid mode
	doltAutoCommit = "invalid"
	_, err = getDoltAutoCommitMode()
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}
