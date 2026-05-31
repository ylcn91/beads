package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/routing"
)

func TestResolveChangeDirBeadsDirDoesNotChangeCWD(t *testing.T) {
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	startDir := t.TempDir()
	t.Chdir(startDir)

	projectDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
		projectDir = resolved
	}
	beadsDir := filepath.Join(projectDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := resolveChangeDirBeadsDir(projectDir)
	if err != nil {
		t.Fatalf("resolveChangeDirBeadsDir: %v", err)
	}
	if got != beadsDir {
		t.Fatalf("resolveChangeDirBeadsDir() = %q, want %q", got, beadsDir)
	}

	afterWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after resolve: %v", err)
	}
	if afterWD != startDir {
		t.Fatalf("working directory changed to %q, want %q", afterWD, startDir)
	}
}

// TestRoleDetectionDirHonorsChangeDir is the regression guard for GH#4241:
// `bd -C <repo>` must evaluate git beads.role against the target repo, not the
// caller's cwd. -C does not chdir, so role detection has to be pointed at the
// -C target explicitly via roleDetectionDir().
func TestRoleDetectionDirHonorsChangeDir(t *testing.T) {
	// Isolate git config so an ambient global beads.role can't leak in.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "noglobal"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(t.TempDir(), "nosystem"))

	initRepo := func(t *testing.T, role string) string {
		t.Helper()
		dir := t.TempDir()
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		for _, args := range [][]string{
			{"init"},
			{"config", "user.email", "test@example.com"},
			{"config", "user.name", "Test"},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		if role != "" {
			cmd := exec.Command("git", "config", "beads.role", role)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git config beads.role: %v\n%s", err, out)
			}
		}
		return dir
	}

	// Caller cwd: a repo with no beads.role and no remote → defaults to Maintainer.
	callerDir := initRepo(t, "")
	t.Chdir(callerDir)
	// Target repo addressed via -C: beads.role=contributor (an explicit,
	// non-default value that only exists in the target).
	targetDir := initRepo(t, "contributor")

	origChangeDir := changeDir
	t.Cleanup(func() { changeDir = origChangeDir })

	// Without -C, role is read from the caller cwd.
	changeDir = ""
	if role, _ := routing.DetectUserRole(roleDetectionDir()); role != routing.Maintainer {
		t.Errorf("without -C: got role %q, want %q (caller cwd)", role, routing.Maintainer)
	}

	// With -C <target>, role must be read from the target repo.
	changeDir = targetDir
	if role, _ := routing.DetectUserRole(roleDetectionDir()); role != routing.Contributor {
		t.Errorf("with -C %q: got role %q, want %q (target repo)", targetDir, role, routing.Contributor)
	}
}

func TestResolveChangeDirBeadsDirRejectsFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := resolveChangeDirBeadsDir(filePath); err == nil {
		t.Fatal("expected non-directory -C target to fail")
	}
}

func TestResolveChangeDirBeadsDirRejectsDirectoryWithoutProject(t *testing.T) {
	if _, err := resolveChangeDirBeadsDir(t.TempDir()); err == nil {
		t.Fatal("expected -C target without a beads project to fail")
	}
}
