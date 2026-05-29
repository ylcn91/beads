package main

import (
	"errors"
	"fmt"
	"testing"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/storage/dberrors"
)

// TestRunWithConflictRetry covers the --append-notes lost-update guard (GH#3964).
// The append read-modify-write runs inside RunInTransaction, whose own retry only
// covers transient connection errors — serialization conflicts (1213/1205) bubble
// out and must be retried here so a concurrent append is not silently lost.
func TestRunWithConflictRetry(t *testing.T) {
	t.Run("retries serialization conflict then succeeds", func(t *testing.T) {
		attempts := 0
		err := runWithConflictRetry(func() error {
			attempts++
			if attempts < 3 {
				return &mysql.MySQLError{Number: 1213, Message: "deadlock"}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected success after retries, got %v", err)
		}
		if attempts != 3 {
			t.Fatalf("expected 3 attempts, got %d", attempts)
		}
	})

	t.Run("retries lock-wait-timeout", func(t *testing.T) {
		attempts := 0
		err := runWithConflictRetry(func() error {
			attempts++
			if attempts < 2 {
				return &mysql.MySQLError{Number: 1205, Message: "lock wait timeout"}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected success after retry, got %v", err)
		}
		if attempts != 2 {
			t.Fatalf("expected 2 attempts, got %d", attempts)
		}
	})

	t.Run("does not retry non-serialization errors", func(t *testing.T) {
		attempts := 0
		want := errors.New("boom")
		err := runWithConflictRetry(func() error {
			attempts++
			return want
		})
		if !errors.Is(err, want) {
			t.Fatalf("expected boom error, got %v", err)
		}
		if attempts != 1 {
			t.Fatalf("non-retryable error must not be retried, got %d attempts", attempts)
		}
	})

	t.Run("gives up after max attempts and returns last error", func(t *testing.T) {
		attempts := 0
		err := runWithConflictRetry(func() error {
			attempts++
			return &mysql.MySQLError{Number: 1213, Message: "deadlock"}
		})
		if !dberrors.IsSerializationConflict(err) {
			t.Fatalf("expected serialization conflict error to be returned, got %v", err)
		}
		if attempts < 2 {
			t.Fatalf("expected multiple attempts before giving up, got %d", attempts)
		}
	})
}

// TestIsSerializationConflict locks down the classifier the retry loop relies on.
func TestIsSerializationConflict(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deadlock 1213", &mysql.MySQLError{Number: 1213}, true},
		{"lock wait 1205", &mysql.MySQLError{Number: 1205}, true},
		{"other mysql error", &mysql.MySQLError{Number: 1146}, false},
		{"wrapped 1213", fmt.Errorf("update: %w", &mysql.MySQLError{Number: 1213}), true},
		{"plain error", errors.New("nope"), false},
	}
	for _, tc := range cases {
		if got := dberrors.IsSerializationConflict(tc.err); got != tc.want {
			t.Errorf("%s: IsSerializationConflict = %v, want %v", tc.name, got, tc.want)
		}
	}
}
