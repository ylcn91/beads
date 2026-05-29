package issueops

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func dependentsMetadataQueryRegex(table string) string {
	return regexp.QuoteMeta("SELECT issue_id, type FROM " + table + " WHERE " + DepTargetExpr + " = ?")
}

// Regression for #3445: a missing optional wisp_dependencies table must not wipe
// all children of an epic. The permanent dependents must still be returned.
func TestGetDependentsWithMetadataInTxToleratesMissingWispDependencyTable(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	mock.ExpectQuery(dependentsMetadataQueryRegex("dependencies")).
		WithArgs("epic-1").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "type"}).
			AddRow("child-1", "parent-child"))
	mock.ExpectQuery(dependentsMetadataQueryRegex("wisp_dependencies")).
		WithArgs("epic-1").
		WillReturnError(errors.New("Error 1146: Table 'db.wisp_dependencies' doesn't exist"))

	// GetIssuesByIDsInTx hydration path for the single permanent child.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")).
		WillReturnError(errors.New("Error 1146: Table 'db.wisps' doesn't exist"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+IssueSelectColumns+" FROM issues WHERE id IN (?)")).
		WithArgs("child-1").
		WillReturnRows(issueRows().AddRow(issueRowValues("child-1", "Child 1")...))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT issue_id, label FROM labels WHERE issue_id IN (?) ORDER BY issue_id, label")).
		WithArgs("child-1").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))

	got, err := GetDependentsWithMetadataInTx(context.Background(), tx, "epic-1")
	if err != nil {
		t.Fatalf("GetDependentsWithMetadataInTx: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1: %+v", len(got), got)
	}
	if got[0].ID != "child-1" {
		t.Fatalf("child ID = %q, want child-1", got[0].ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

// Regression for #3445: duplicate dependency rows (e.g. a permanent row plus a
// stale wisp_dependencies working-set row) must be deduped so a child is not
// counted twice.
func TestGetDependentsWithMetadataInTxDedupsDuplicateRows(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	mock.ExpectQuery(dependentsMetadataQueryRegex("dependencies")).
		WithArgs("epic-1").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "type"}).
			AddRow("child-1", "parent-child"))
	mock.ExpectQuery(dependentsMetadataQueryRegex("wisp_dependencies")).
		WithArgs("epic-1").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "type"}).
			AddRow("child-1", "parent-child"))

	// The duplicate rows mean ids = [child-1, child-1] is passed to the issue
	// fetch (dedup happens at the results stage, not the fetch stage), so the IN
	// clause has two placeholders.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")).
		WillReturnError(errors.New("Error 1146: Table 'db.wisps' doesn't exist"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+IssueSelectColumns+" FROM issues WHERE id IN (?,?)")).
		WithArgs("child-1", "child-1").
		WillReturnRows(issueRows().AddRow(issueRowValues("child-1", "Child 1")...))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT issue_id, label FROM labels WHERE issue_id IN (?,?) ORDER BY issue_id, label")).
		WithArgs("child-1", "child-1").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))

	got, err := GetDependentsWithMetadataInTx(context.Background(), tx, "epic-1")
	if err != nil {
		t.Fatalf("GetDependentsWithMetadataInTx: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (deduped): %+v", len(got), got)
	}
	if got[0].ID != "child-1" {
		t.Fatalf("child ID = %q, want child-1", got[0].ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
