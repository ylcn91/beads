package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestMigrateUp_AlreadyCurrent_CreatesNoNewSchemaCommits is the regression
// guard for GH#4274: a Dolt-backed store that is already at the latest schema
// must not accumulate no-op schema-migration commits on ordinary command
// traffic.
//
// Background: a deleted compat-migration runner used to call
// DOLT_COMMIT('-m', 'schema: auto-migrate') on (nearly) every bd invocation,
// because registering dolt_ignore left dolt_status non-empty even when no
// schema actually changed. In an 18-day production city this produced 31,501
// reachable no-op commits and grew the store to 7.3 GB. The path has since
// been consolidated into schema.MigrateUp, which gates on the schema_migrations
// version cursor (migrationWorkNeeded) before staging or committing.
//
// This test pins that behavior: repeatedly running the store-open migration
// pass — and performing a trivial write in between — must leave the count of
// schema-migration commits unchanged.
func TestMigrateUp_AlreadyCurrent_CreatesNoNewSchemaCommits(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// setupTestStore already ran one migration pass against a shared DB that is
	// at the latest schema, so the branch starts fully migrated. Capture the
	// baseline before exercising the hot path.
	baseline := countSchemaMigrationCommits(ctx, t, store)

	// Simulate repeated store opens: every writable `bd` command runs this same
	// migration pass via initSchema -> MigrateUpWithLock -> MigrateUp.
	const reopens = 5
	for i := 0; i < reopens; i++ {
		if _, err := initSchemaOnDB(ctx, store.db); err != nil {
			t.Fatalf("reopen %d: initSchemaOnDB: %v", i, err)
		}
	}

	// A trivial write must produce a data commit, never a schema-migration
	// commit, and must not leave the schema dirty enough to trip a later pass.
	issue := &types.Issue{
		ID:        "noop-commit-1",
		Title:     "trivial write",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
	}
	if err := store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// One more migration pass after the write, mirroring the next command.
	if _, err := initSchemaOnDB(ctx, store.db); err != nil {
		t.Fatalf("post-write initSchemaOnDB: %v", err)
	}

	after := countSchemaMigrationCommits(ctx, t, store)
	if after != baseline {
		t.Fatalf("schema-migration commits grew from %d to %d across %d reopens + a write; "+
			"an already-current store must not create no-op schema commits (GH#4274)",
			baseline, after, reopens)
	}
}

// countSchemaMigrationCommits returns the number of schema-migration commits
// reachable from the current branch's HEAD.
//
// The match is a `schema:` prefix rather than the exact current message
// ('schema: apply migrations'). That deliberately also covers the legacy
// 'schema: auto-migrate' message from GH#4274 and any future schema commit
// message, so the regression keeps biting even if the runner's message string
// changes again — the bug is "no-op schema commits accumulate", not "this
// exact string appears".
func countSchemaMigrationCommits(ctx context.Context, t *testing.T, store *DoltStore) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dolt_log WHERE message LIKE 'schema:%'",
	).Scan(&count); err != nil {
		t.Fatalf("counting schema-migration commits in dolt_log: %v", err)
	}
	return count
}
