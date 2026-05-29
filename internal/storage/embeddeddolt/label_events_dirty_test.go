//go:build cgo

package embeddeddolt_test

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// TestRunInTransactionLabelStagesEventTable is a regression test for GH#3850.
// Adding or removing a label inside RunInTransaction (the path `bd label
// add/remove` uses) also INSERTs a row into the events table. The transaction
// must mark that event table dirty so it is staged in the Dolt version commit;
// otherwise the event write is left in the working set and the next
// `bd dolt push`/`pull` is blocked by uncommitted changes.
func TestRunInTransactionLabelStagesEventTable(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "le3850")
	ctx := t.Context()

	issue := &types.Issue{
		ID:        "le3850-1",
		Title:     "Label event staging",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := te.store.Commit(ctx, "create"); err != nil {
		t.Fatalf("Commit (baseline): %v", err)
	}
	assertLabelEventWorkingSetClean(t, ctx, te, "baseline after create+commit")

	// Mirror `bd label add`: RunInTransaction stages only the dirty tables, with
	// no subsequent full commit — so the event write must be staged here.
	if err := te.store.RunInTransaction(ctx, "bd: label add", func(tx storage.Transaction) error {
		return tx.AddLabel(ctx, "le3850-1", "needs-review", "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction(AddLabel): %v", err)
	}
	assertLabelEventWorkingSetClean(t, ctx, te, "after AddLabel")

	// Mirror `bd label remove`.
	if err := te.store.RunInTransaction(ctx, "bd: label remove", func(tx storage.Transaction) error {
		return tx.RemoveLabel(ctx, "le3850-1", "needs-review", "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction(RemoveLabel): %v", err)
	}
	assertLabelEventWorkingSetClean(t, ctx, te, "after RemoveLabel")
}

func assertLabelEventWorkingSetClean(t *testing.T, ctx context.Context, te *testEnv, when string) {
	t.Helper()
	var dirty int
	te.queryScalar(t, ctx,
		"SELECT COUNT(*) FROM dolt_status WHERE table_name IN ('labels', 'events')",
		nil, &dirty)
	if dirty != 0 {
		t.Errorf("%s: expected labels/events to be staged & committed, found %d dirty working-set table(s) (GH#3850)", when, dirty)
	}
}
