package issueops

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// TestBuildReadyWorkPredicatesIncludesCustomActiveStatus is a regression test
// for gastownhall/beads#3268: when custom statuses are configured, a custom
// status in the "active" category is open-equivalent and must appear in the
// ready-work status predicate alongside the built-in open/in_progress
// statuses. A "wip" status must NOT, since it is excluded from bd ready.
func TestBuildReadyWorkPredicatesIncludesCustomActiveStatus(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	mock.ExpectQuery(`SELECT name, category FROM custom_statuses`).
		WillReturnRows(sqlmock.NewRows([]string{"name", "category"}).
			AddRow("triaged", string(types.CategoryActive)).
			AddRow("review", string(types.CategoryWIP)))

	preds, err := buildReadyWorkPredicates(
		context.Background(),
		tx,
		types.WorkFilter{IncludeDeferred: true},
		IssuesFilterTables,
	)
	if err != nil {
		t.Fatalf("buildReadyWorkPredicates: %v", err)
	}

	hasArg := func(want string) bool {
		for _, a := range preds.args {
			if s, ok := a.(string); ok && s == want {
				return true
			}
		}
		return false
	}

	if !hasArg(string(types.StatusOpen)) || !hasArg(string(types.StatusInProgress)) {
		t.Fatalf("ready predicate must retain built-in open statuses, args = %v", preds.args)
	}
	if !hasArg("triaged") {
		t.Fatalf("custom active status %q missing from ready predicate, args = %v", "triaged", preds.args)
	}
	if hasArg("review") {
		t.Fatalf("custom wip status %q must not appear in ready predicate, args = %v", "review", preds.args)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
