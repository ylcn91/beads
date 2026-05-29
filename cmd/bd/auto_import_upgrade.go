package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// jsonlImporter is implemented by stores that support single-transaction
// JSONL import (currently EmbeddedDoltStore). Stores that don't implement
// this fall back to the multi-call path.
type jsonlImporter interface {
	ImportJSONLData(ctx context.Context, issues []*types.Issue, configEntries map[string]string, actor string) (int, error)
}

// fallbackImporter is the function maybeAutoImportJSONL invokes for stores
// that do not implement jsonlImporter (server-mode dolt). It exists as a
// package-level variable so tests can substitute a counter and verify the
// top-level emptiness guard prevents the fallback path from running on a
// non-empty database.
//
// Production builds use importFromLocalJSONLConflictSkip (GH#3955): this is
// upgrade-recovery into an empty DB, so insert-if-new and UPSERT are
// equivalent on the legitimate path — but if the emptiness guard above ever
// regresses again (cf. PR #3630), conflict-skip makes the fallback a
// harmless no-op instead of clobbering live rows. Explicit `bd import`,
// `bd bootstrap`, and `bd init --from-jsonl` are unaffected and keep UPSERT.
var fallbackImporter = importFromLocalJSONLConflictSkip

// maybeAutoImportJSONL checks whether the database is empty and a
// issues.jsonl file exists in beadsDir. When both conditions are true it
// auto-imports the JSONL data so users upgrading from pre-0.56 (which used
// .beads/dolt/) to 1.0+ (which uses .beads/embeddeddolt/) don't appear to
// lose their issues.  See GH#2994.
//
// The top-level emptiness guard (GetStatistics) is the primary
// protection for BOTH the embedded fast-path and the server-mode
// fallback. Defense in depth backs each path up: the embedded
// jsonlImporter has its own in-transaction emptiness check (and is
// also insert-if-new, GH#3955), and the fallback path imports via
// importFromLocalJSONLConflictSkip, which is insert-if-new rather than
// UPSERT. So if this guard ever regresses again (cf. PR #3630), a stale
// issues.jsonl can no longer be re-imposed on top of live Dolt rows —
// the worst case degrades to a harmless no-op instead of clobbering
// recent writes.
//
// The function is best-effort: failures are logged as warnings but do not
// prevent the store from being used.
func maybeAutoImportJSONL(ctx context.Context, s storage.DoltStorage, beadsDir string) {
	// Quick check: does the JSONL file exist and have content?
	jsonlPath := configuredImportJSONLPath(beadsDir)
	info, err := os.Stat(jsonlPath)
	if err != nil || info.Size() == 0 {
		return // no JSONL file or empty — nothing to import
	}

	// Skip if we already attempted to import this exact content. The stamp
	// records the source hash of the last attempt (success or failure), so an
	// unchanged file is not re-imported on every command; a changed file
	// produces a new hash and re-imports.
	fingerprint, ferr := hashJSONLContent(jsonlPath)
	if ferr == nil && importStampMatches(beadsDir, fingerprint) {
		return
	}

	// Top-level emptiness guard (covers both embedded and fallback paths).
	// Without this, the fallback path silently re-imposes stale JSONL on
	// top of live Dolt rows via UPSERT semantics on every invocation.
	stats, err := s.GetStatistics(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: auto-import: failed to check issue count: %v\n", err)
		return
	}
	if stats == nil {
		fmt.Fprintf(os.Stderr, "warning: auto-import: issue count unavailable\n")
		return
	}
	if stats.TotalIssues > 0 {
		return // database is not empty — nothing to do
	}

	// Parse the JSONL file without touching the store.
	issues, configEntries, err := parseJSONLFile(jsonlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: auto-import: failed to parse %s: %v\n", jsonlPath, err)
		return
	}
	if len(issues) == 0 {
		return // nothing to import
	}

	// Prefer single-transaction import (embedded mode) to avoid
	// DOLT_COMMIT races with concurrent writers.
	if importer, ok := s.(jsonlImporter); ok {
		imported, err := importer.ImportJSONLData(ctx, issues, configEntries, "auto-import")
		if ferr == nil {
			writeImportStamp(beadsDir, fingerprint)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: auto-import from %s failed: %v\n", jsonlPath, err)
			fmt.Fprintf(os.Stderr, "\nYour issues are still safe in %s.\n", jsonlPath)
			fmt.Fprintf(os.Stderr, "Try: bd init --from-jsonl   (re-initialize and import from the JSONL file)\n")
			fmt.Fprintf(os.Stderr, "If this persists, please report at https://github.com/gastownhall/beads/issues\n\n")
			return
		}
		if imported > 0 {
			// Signal PersistentPostRun to auto-commit (no explicit DOLT_COMMIT here).
			commandDidWrite.Store(true)
			fmt.Fprintf(os.Stderr, "auto-imported %d issues", imported)
			if len(configEntries) > 0 {
				fmt.Fprintf(os.Stderr, " and %d config entries", len(configEntries))
			}
			fmt.Fprintf(os.Stderr, " from %s\n", jsonlPath)
		}
		return
	}

	// Fallback for non-embedded stores: multi-call path (original behavior).
	fmt.Fprintf(os.Stderr, "auto-importing %d bytes from %s into empty database...\n", info.Size(), jsonlPath)

	result, err := fallbackImporter(ctx, s, jsonlPath)
	if ferr == nil {
		writeImportStamp(beadsDir, fingerprint)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: auto-import from %s failed: %v\n", jsonlPath, err)
		fmt.Fprintf(os.Stderr, "\nYour issues are still safe in %s.\n", jsonlPath)
		fmt.Fprintf(os.Stderr, "Try: bd init --from-jsonl   (re-initialize and import from the JSONL file)\n")
		fmt.Fprintf(os.Stderr, "If this persists, please report at https://github.com/gastownhall/beads/issues\n\n")
		return
	}

	// Commit the imported data to Dolt history (fallback path only).
	commitMsg := fmt.Sprintf("auto-import: %d issues from %s (upgrade recovery, GH#2994)", result.Issues, filepath.Base(jsonlPath))
	if result.Memories > 0 {
		commitMsg = fmt.Sprintf("auto-import: %d issues, %d memories from %s (upgrade recovery, GH#2994)", result.Issues, result.Memories, filepath.Base(jsonlPath))
	}
	if err := s.Commit(ctx, commitMsg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: auto-import: dolt commit failed: %v\n", err)
		return
	}

	if result.Memories > 0 {
		fmt.Fprintf(os.Stderr, "auto-imported %d issues and %d memories from %s\n", result.Issues, result.Memories, jsonlPath)
	} else {
		fmt.Fprintf(os.Stderr, "auto-imported %d issues from %s\n", result.Issues, jsonlPath)
	}
}

// autoImportStampFile holds the content hash of the most recent auto-import
// attempt, relative to beadsDir.
const autoImportStampFile = ".auto-import.stamp"

// hashJSONLContent returns a hex SHA-256 of the file's bytes, used to detect
// whether the import source changed since the last attempt.
func hashJSONLContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// importStampMatches reports whether beadsDir already holds a stamp equal to
// fingerprint, i.e. an import of this exact content was already attempted.
func importStampMatches(beadsDir, fingerprint string) bool {
	got, err := os.ReadFile(filepath.Join(beadsDir, autoImportStampFile))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(got)) == fingerprint
}

// writeImportStamp records that an import of fingerprint was attempted so the
// same unchanged source is not re-imported on every command. Best-effort: a
// write failure only means the next command may retry the import.
func writeImportStamp(beadsDir, fingerprint string) {
	_ = os.WriteFile(filepath.Join(beadsDir, autoImportStampFile), []byte(fingerprint+"\n"), 0o600)
}
