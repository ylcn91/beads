package issueops

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// issueSelectRow returns a sqlmock row matching IssueSelectColumns (47 columns)
// for an issue in the given status. Only the fields exercised by claim matter;
// the rest are zero/empty so ScanIssueFrom hydrates a minimal issue.
func issueSelectRow(id, status string) *sqlmock.Rows {
	cols := []string{
		"id", "content_hash", "title", "description", "design", "acceptance_criteria", "notes",
		"status", "priority", "issue_type", "assignee", "estimated_minutes",
		"created_at", "created_by", "owner", "updated_at", "started_at", "closed_at", "external_ref", "spec_id",
		"compaction_level", "compacted_at", "compacted_at_commit", "original_size", "source_repo", "close_reason",
		"sender", "ephemeral", "no_history", "wisp_type", "pinned", "is_template",
		"await_type", "await_id", "timeout_ns", "waiters",
		"mol_type",
		"event_kind", "actor", "target", "payload",
		"due_at", "defer_until",
		"work_type", "source_system", "metadata",
	}
	return sqlmock.NewRows(cols).AddRow(
		id, "", "title", "", "", "", "",
		status, 2, "task", nil, nil,
		"2024-01-01T00:00:00Z", "", "", "2024-01-01T00:00:00Z", nil, nil, nil, "",
		0, nil, nil, nil, "", "",
		"", 0, 0, "", 0, 0,
		"", "", nil, "",
		"",
		"", "", "", "",
		nil, nil,
		"", "", "{}",
	)
}

// TestClaimIssueInTxCustomOpenEquivalentStatusIsClaimable verifies that an
// issue sitting in a custom open-equivalent status (CategoryActive) can be
// claimed, not just an issue with the built-in "open" status
// (gastownhall/beads#4164).
func TestClaimIssueInTxCustomOpenEquivalentStatusIsClaimable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	const id = "bd-1"
	const customStatus = "triaged" // open-equivalent custom status

	mock.ExpectBegin()
	// IsActiveWispInTx: not a wisp.
	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs(id).
		WillReturnError(sql.ErrNoRows)
	// GetIssueInTx: issue lives in the issues table with the custom status.
	mock.ExpectQuery("SELECT .* FROM issues WHERE id = \\?").
		WithArgs(id).
		WillReturnRows(issueSelectRow(id, customStatus))
	mock.ExpectQuery("SELECT label FROM labels WHERE issue_id = \\?").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"label"}))
	// ResolveCustomStatusesDetailedInTx: custom_statuses table marks the
	// custom status as open-equivalent (active).
	mock.ExpectQuery("SELECT name, category FROM custom_statuses").
		WillReturnRows(sqlmock.NewRows([]string{"name", "category"}).
			AddRow(customStatus, string(types.CategoryActive)))
	// Conditional UPDATE must accept the custom status and claim the row.
	mock.ExpectExec("UPDATE issues.*status = 'in_progress'.*status IN \\(\\?, \\?\\)").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Claim event recorded.
	mock.ExpectExec("INSERT INTO events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("db.Begin: %v", err)
	}

	res, err := ClaimIssueInTx(context.Background(), tx, id, "alice")
	if err != nil {
		t.Fatalf("ClaimIssueInTx returned error for custom open-equivalent status: %v", err)
	}
	if res == nil || res.OldIssue == nil {
		t.Fatalf("expected non-nil ClaimResult with OldIssue, got %+v", res)
	}
	if res.OldIssue.Status != types.Status(customStatus) {
		t.Fatalf("expected old status %q, got %q", customStatus, res.OldIssue.Status)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("tx.Commit: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
