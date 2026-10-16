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

// Build the documented CLI instead of bypassing its launcher through a test helper.
func buildWorkSession(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "work-session")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build session command: %v: %s", err, output)
	}
	return binary
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=test@example.invalid")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func helper(binary, dir string, args ...string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	return cmd
}

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
