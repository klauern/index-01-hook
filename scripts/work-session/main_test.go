//go:build unix

package main

import (
	"bufio"
	"bytes"
	"io"
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

	first := startSessionHolder(t, helper(binary, repo, "sh", "-c", "echo ready; read -r value"))
	assertSessionContention(t, helper(binary, other, "sh", "-c", "echo must-not-run"))
	first.finish(t)
	assertSessionExit(t, helper(binary, other, "sh", "-c", "exit 7"), 7)
	assertSessionAvailable(t, helper(binary, repo, "true"))

	// A killed holder must not leave a stale lock.
	killed := startSessionHolder(t, helper(binary, repo, "sh", "-c", "echo ready; read -r value"))
	killed.kill(t)
	assertSessionAvailable(t, helper(binary, other, "true"))
}

// sessionHolder owns a live child and always registers cleanup before assertions.
type sessionHolder struct {
	command *exec.Cmd
	input   io.WriteCloser
}

func startSessionHolder(t *testing.T, command *exec.Cmd) sessionHolder {
	t.Helper()
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); command.Process.Kill(); command.Wait() })
	ready := bufio.NewScanner(output)
	if !ready.Scan() || ready.Text() != "ready" {
		t.Fatalf("holder did not start: %s", stderr.String())
	}
	return sessionHolder{command: command, input: input}
}

func (holder sessionHolder) finish(t *testing.T) {
	t.Helper()
	if _, err := holder.input.Write([]byte("done\n")); err != nil {
		t.Fatal(err)
	}
	if err := holder.command.Wait(); err != nil {
		t.Fatal(err)
	}
}

func (holder sessionHolder) kill(t *testing.T) {
	t.Helper()
	if err := holder.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	holder.command.Wait()
}

func assertSessionContention(t *testing.T, command *exec.Cmd) {
	t.Helper()
	output, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 75 {
		t.Fatalf("contender = %v, %s", err, output)
	}
	if strings.Contains(string(output), "must-not-run") || !strings.Contains(string(output), errBusy.Error()) {
		t.Fatalf("unexpected contention output: %s", output)
	}
}

func assertSessionExit(t *testing.T, command *exec.Cmd, status int) {
	t.Helper()
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("command failure was lost: %s", output)
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != status {
		t.Fatalf("command exit = %v, want %d", err, status)
	}
}

func assertSessionAvailable(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock not released: %v: %s", err, output)
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
	assertFixtureSessionRepository(t, binary, fixture)
	if err := helper(binary, t.TempDir(), "true").Run(); err == nil {
		t.Error("session accepted a non-repository through inherited Git overrides")
	}
	if got := cleanGit(outer, "rev-parse", "HEAD"); got != outerHead {
		t.Errorf("fixture commands changed outer HEAD: %s -> %s", outerHead, got)
	}
	assertUnchangedIndex(t, outerIndex, indexBefore)
}

// assertFixtureSessionRepository checks the child command's real repository path.
func assertFixtureSessionRepository(t *testing.T, binary, fixture string) {
	t.Helper()
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
}

// assertUnchangedIndex checks that fixture commands preserved the outer index bytes.
func assertUnchangedIndex(t *testing.T, outerIndex string, indexBefore []byte) {
	t.Helper()

	indexAfter, err := os.ReadFile(outerIndex)
	if err != nil || !bytes.Equal(indexAfter, indexBefore) {
		t.Errorf("fixture commands changed outer index: %v", err)
	}
}
