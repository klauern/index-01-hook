package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var workflowUsesPattern = regexp.MustCompile(`(?m)^\s*uses:\s+\S+@([^\s#]+)`)
var fullCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func TestCIWorkflowSafetyBoundary(t *testing.T) {
	contents, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	workflow := string(contents)

	for _, job := range []string{"test", "container", "manifests", "security"} {
		if !strings.Contains(workflow, "\n  "+job+":\n") {
			t.Errorf("CI workflow has no %s job", job)
		}
	}
	matches := workflowUsesPattern.FindAllStringSubmatch(workflow, -1)
	if len(matches) == 0 {
		t.Fatal("CI workflow uses no pinned actions")
	}
	for _, match := range matches {
		if !fullCommitPattern.MatchString(match[1]) {
			t.Errorf("CI action is not pinned to a full commit: %q", match[0])
		}
	}

	for _, required := range []string{
		"permissions:\n  contents: read",
		"runs-on: ubuntu-24.04",
		"persist-credentials: false",
		"go test -race ./...",
		"go vet ./...",
		"task test",
		"./scripts/compose-volume_test.sh",
		"./scripts/ci-manifests.sh",
		"govulncheck ./...",
		"gitleaks git --redact --no-banner --exit-code 1 --log-opts=\"--all\" .",
		"gitleaks dir --redact --no-banner --exit-code 1 .",
		"VERSION=v0.0.0-ci",
		"actionlint .github/workflows/*.yml",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("CI workflow does not contain required boundary %q", required)
		}
	}

	for _, forbidden := range []string{
		"pull_request_target",
		"self-hosted",
		"secrets.",
		"packages: write",
		"test-deepseek-live",
		"server-dry-run",
		"task deploy",
		"KUBE_CONTEXT",
		"INDEX01_DEEPSEEK_TOKEN",
		"INDEX01_TICKTICK_TOKEN",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("CI workflow contains forbidden value %q", forbidden)
		}
	}
}

func TestDependabotCoversGoAndActions(t *testing.T) {
	contents, err := os.ReadFile(".github/dependabot.yml")
	if err != nil {
		t.Fatalf("read Dependabot configuration: %v", err)
	}
	configuration := string(contents)
	for _, ecosystem := range []string{"package-ecosystem: gomod", "package-ecosystem: github-actions"} {
		if !strings.Contains(configuration, ecosystem) {
			t.Errorf("Dependabot configuration does not contain %q", ecosystem)
		}
	}
}

func TestCIContainerIsolationAndBuilderPin(t *testing.T) {
	volumeTest, err := os.ReadFile("scripts/compose-volume_test.sh")
	if err != nil {
		t.Fatalf("read container volume test: %v", err)
	}
	text := string(volumeTest)
	runLines := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "docker run ") {
			runLines++
			for _, flag := range []string{"--network none", "--cap-drop ALL", "--security-opt no-new-privileges:true"} {
				if !strings.Contains(line, flag) {
					t.Errorf("Docker runtime test line does not contain %s: %s", flag, line)
				}
			}
		}
	}
	if runLines == 0 {
		t.Error("container volume test runs no Docker containers")
	}

	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	const builder = "ARG GO_IMAGE=docker.io/library/golang:1.26.6@sha256:0d1d3a794be25f809dd2cb3160d8c73276c4056a9f8242a138e908ddeee7b6b6"
	if !strings.Contains(string(dockerfile), builder) {
		t.Error("Dockerfile does not use the approved default builder digest")
	}
}

func TestDockerfileFrontendIsDigestPinned(t *testing.T) {
	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}

	const reviewedFrontend = "docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e"
	matches := regexp.MustCompile(`(?m)^# syntax=([^[:space:]]+)$`).FindAllStringSubmatch(string(dockerfile), -1)
	if len(matches) != 1 {
		t.Fatalf("Dockerfile must contain one frontend syntax directive, found %d", len(matches))
	}
	if matches[0][1] != reviewedFrontend {
		t.Errorf("Dockerfile frontend is not pinned to the reviewed digest: %q", matches[0][1])
	}
}

func TestCIManifestScriptIsExecutable(t *testing.T) {
	info, err := os.Stat("scripts/ci-manifests.sh")
	if err != nil {
		t.Fatalf("stat CI manifest script: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("scripts/ci-manifests.sh is not executable")
	}
}

func TestDockerignoreExcludesLocalState(t *testing.T) {
	t.Helper()
	contents, err := os.ReadFile(".dockerignore")
	if err != nil {
		t.Fatalf("read .dockerignore: %v", err)
	}
	text := string(contents)
	for _, required := range []string{".beads", ".agents", ".claude", ".codex", ".serena", "AGENTS.md", "CLAUDE.md"} {
		if !strings.Contains(text, "\n"+required+"\n") && !strings.HasPrefix(text, required+"\n") {
			t.Errorf(".dockerignore does not exclude %q", required)
		}
	}
}
