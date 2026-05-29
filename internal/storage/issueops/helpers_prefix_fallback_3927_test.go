package issueops

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
)

// TestReadConfigPrefixYAMLFallback covers GH#3927: write commands must not fail
// with ErrNotInitialized when the Dolt DB has no issue_prefix config row but
// .beads/config.yaml carries the prefix. The YAML value is the fallback only
// when the DB value is genuinely empty; a present DB value always wins.
//
// Not parallel: it mutates the process-global viper state via config.Set.
func TestReadConfigPrefixYAMLFallback(t *testing.T) {
	t.Run("falls back to yaml issue-prefix when db value empty", func(t *testing.T) {
		if err := config.Initialize(); err != nil {
			t.Fatalf("config.Initialize: %v", err)
		}
		t.Cleanup(config.ResetForTesting)
		config.Set("issue-prefix", "yt-")

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT value FROM config WHERE `key` = \\?").
			WithArgs("issue_prefix").
			WillReturnRows(sqlmock.NewRows([]string{"value"}))
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("db.Begin: %v", err)
		}
		mock.ExpectRollback()
		defer func() { _ = tx.Rollback() }()

		got, err := ReadConfigPrefix(context.Background(), tx)
		if err != nil {
			t.Fatalf("ReadConfigPrefix returned error: %v", err)
		}
		if got != "yt" {
			t.Fatalf("ReadConfigPrefix() = %q, want %q", got, "yt")
		}
	})

	t.Run("db value wins over yaml fallback", func(t *testing.T) {
		if err := config.Initialize(); err != nil {
			t.Fatalf("config.Initialize: %v", err)
		}
		t.Cleanup(config.ResetForTesting)
		config.Set("issue-prefix", "yt-")

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT value FROM config WHERE `key` = \\?").
			WithArgs("issue_prefix").
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("gt-"))
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("db.Begin: %v", err)
		}
		mock.ExpectRollback()
		defer func() { _ = tx.Rollback() }()

		got, err := ReadConfigPrefix(context.Background(), tx)
		if err != nil {
			t.Fatalf("ReadConfigPrefix returned error: %v", err)
		}
		if got != "gt" {
			t.Fatalf("ReadConfigPrefix() = %q, want %q (db value must win)", got, "gt")
		}
	})

	t.Run("returns ErrNotInitialized when both db and yaml empty", func(t *testing.T) {
		if err := config.Initialize(); err != nil {
			t.Fatalf("config.Initialize: %v", err)
		}
		t.Cleanup(config.ResetForTesting)
		config.Set("issue-prefix", "")
		config.Set("issue_prefix", "")

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT value FROM config WHERE `key` = \\?").
			WithArgs("issue_prefix").
			WillReturnRows(sqlmock.NewRows([]string{"value"}))
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("db.Begin: %v", err)
		}
		mock.ExpectRollback()
		defer func() { _ = tx.Rollback() }()

		_, err = ReadConfigPrefix(context.Background(), tx)
		if err == nil {
			t.Fatal("ReadConfigPrefix() returned nil error, want error")
		}
		if !errors.Is(err, storage.ErrNotInitialized) {
			t.Fatalf("ReadConfigPrefix error = %v, want ErrNotInitialized", err)
		}
	})
}
