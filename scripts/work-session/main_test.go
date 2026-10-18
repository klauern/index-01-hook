//go:build unix

package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv removes inherited Git repository overrides and preserves explicit fixture settings.
func testEnv(extra ...string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_QUARANTINE_PATH",
			"GIT_PREFIX", "GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM",
			"GIT_IMPLICIT_WORK_TREE", "GIT_NAMESPACE", "GIT_GRAFT_FILE", "GIT_SHALLOW_FILE",
			"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_CONFIG",
			"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT":
			continue
		}
		if strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, extra...)
}

// buildWorkSession builds the documented command so tests exercise the actual launcher.
func buildWorkSession(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "work-session")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Env = testEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build session command: %v: %s", err, output)
	}
	return binary
}

// git runs a Git command with synthetic identity and no inherited user configuration.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = testEnv("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=test@example.invalid")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

// helper prepares a session command in the specified repository or linked worktree.
func helper(binary, dir string, args ...string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = testEnv()
	return cmd
}

// TestWorkSessionExcludesOtherWorktreesAndReleasesAfterExit checks exclusion, child status, and release after normal or killed exits.
func TestWorkSessionExcludesOtherWorktreesAndReleasesAfterExit(t *testing.T) {
	binary := buildWorkSession(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "Synthetic baseline")
	other := filepath.Join(t.TempDir(), "linked")
	git(t, repo, "worktree", "add", "-qb", "other", other)

	first := helper(binary, repo, "sh", "-c", "echo ready; read -r value")
	stdin, err := first.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := first.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	first.Stderr = &stderr
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); first.Process.Kill(); first.Wait() })
	ready := bufio.NewScanner(stdout)
	if !ready.Scan() || ready.Text() != "ready" {
		t.Fatalf("holder did not start: %s", stderr.String())
	}

	output, err := helper(binary, other, "sh", "-c", "echo must-not-run").CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 75 {
		t.Fatalf("contender = %v, %s", err, output)
	}
	if strings.Contains(string(output), "must-not-run") || !strings.Contains(string(output), errBusy.Error()) {
		t.Fatalf("unexpected contention output: %s", output)
	}
	stdin.Write([]byte("done\n"))
	if err := first.Wait(); err != nil {
		t.Fatal(err)
	}
	if output, err := helper(binary, other, "sh", "-c", "exit 7").CombinedOutput(); err == nil {
		t.Fatalf("command failure was lost: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
		t.Fatalf("command exit = %v", err)
	}
	if output, err := helper(binary, repo, "true").CombinedOutput(); err != nil {
		t.Fatalf("lock not released: %v: %s", err, output)
	}
	// A killed holder must not leave a stale lock.
	killed := helper(binary, repo, "sh", "-c", "echo ready; read -r value")
	input, _ := killed.StdinPipe()
	defer input.Close()
	pipe, _ := killed.StdoutPipe()
	if err := killed.Start(); err != nil {
		t.Fatal(err)
	}
	if !bufio.NewScanner(pipe).Scan() {
		t.Fatal("second holder did not start")
	}
	if err := killed.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	killed.Wait()
	if output, err := helper(binary, other, "true").CombinedOutput(); err != nil {
		t.Fatalf("killed holder retained lock: %v: %s", err, output)
	}
}

// TestWorkSessionErrorsReleaseLock checks invalid commands and directories without leaving a held lock.
func TestWorkSessionErrorsReleaseLock(t *testing.T) {
	binary := buildWorkSession(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	for _, args := range [][]string{nil, {"index01-command-that-does-not-exist"}} {
		if err := helper(binary, repo, args...).Run(); err == nil {
			t.Fatalf("expected failure for %v", args)
		}
	}
	if err := helper(binary, repo, "true").Run(); err != nil {
		t.Fatalf("failed command retained lock: %v", err)
	}
	if err := helper(binary, t.TempDir(), "true").Run(); err == nil {
		t.Fatal("accepted a non-repository directory")
	}
}

// TestFixtureSubprocessesIgnoreGitLocationOverrides protects an outer repository from fixture commands and lock discovery.
func TestFixtureSubprocessesIgnoreGitLocationOverrides(t *testing.T) {
	binary := buildWorkSession(t)
	// Bootstrap with a captured clean environment before introducing disposable overrides.
	cleanEnv := testEnv("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=test@example.invalid")
	cleanGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, cleanEnv
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("clean fixture git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	outer := t.TempDir()
	cleanGit(outer, "init", "-q")
	if err := os.WriteFile(filepath.Join(outer, "sentinel"), []byte("synthetic outer content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanGit(outer, "add", "sentinel")
	cleanGit(outer, "-c", "commit.gpgsign=false", "commit", "-qm", "Outer baseline")
	outerHead := cleanGit(outer, "rev-parse", "HEAD")
	outerIndex := filepath.Join(outer, ".git", "index")
	indexBefore, err := os.ReadFile(outerIndex)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"GIT_DIR":              filepath.Join(outer, ".git"),
		"GIT_COMMON_DIR":       filepath.Join(outer, ".git"),
		"GIT_WORK_TREE":        outer,
		"GIT_INDEX_FILE":       outerIndex,
		"GIT_OBJECT_DIRECTORY": filepath.Join(outer, ".git", "objects"),
	} {
		t.Setenv(key, value)
	}
	fixture := t.TempDir()
	git(t, fixture, "init", "-q")
	git(t, fixture, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "Fixture baseline")
	if _, err := os.Stat(filepath.Join(fixture, ".git")); err != nil {
		t.Errorf("fixture repository was not created: %v", err)
	} else {
		want, err := filepath.EvalSymlinks(filepath.Join(fixture, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		output, err := helper(binary, fixture, "git", "rev-parse", "--absolute-git-dir").CombinedOutput()
		if err != nil {
			t.Errorf("fixture session failed: %v: %s", err, output)
		} else if got, err := filepath.EvalSymlinks(strings.TrimSpace(string(output))); err != nil || got != want {
			t.Errorf("session repository = %q (%v), want %q", got, err, want)
		}
	}
	if err := helper(binary, t.TempDir(), "true").Run(); err == nil {
		t.Error("session accepted a non-repository through inherited Git overrides")
	}
	if got := cleanGit(outer, "rev-parse", "HEAD"); got != outerHead {
		t.Errorf("fixture commands changed outer HEAD: %s -> %s", outerHead, got)
	}
	indexAfter, err := os.ReadFile(outerIndex)
	if err != nil || !bytes.Equal(indexAfter, indexBefore) {
		t.Errorf("fixture commands changed outer index: %v", err)
	}
}
