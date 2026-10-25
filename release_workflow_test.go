package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseWorkflowContainerInputsAreDigestPinned(t *testing.T) {
	workflow, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}

	const immutableDigest = `@sha256:[0-9a-f]{64}`
	inputs := map[string]*regexp.Regexp{
		"BuildKit driver image": regexp.MustCompile(`(?m)^[ \t]*driver-opts:[ \t]+image=([^ \t\r\n#]+)`),
		"Go builder image":      regexp.MustCompile(`(?m)^[ \t]*GO_IMAGE=([^ \t\r\n#]+)`),
	}
	for name, pattern := range inputs {
		matches := pattern.FindAllStringSubmatch(string(workflow), -1)
		if len(matches) == 0 {
			t.Errorf("release workflow has no %s input", name)
			continue
		}
		for _, match := range matches {
			if !regexp.MustCompile(immutableDigest + `$`).MatchString(match[1]) {
				t.Errorf("release workflow %s is mutable: %q", name, match[1])
			}
		}
	}
}

// TestReleaseWorkflowIsGatedAndImmutable verifies release approvals, minimum permissions, and immutable dependency and builder pins.
func TestReleaseWorkflowIsGatedAndImmutable(t *testing.T) {
	workflow, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)

	required := []string{
		"name: Release",
		"on:\n  push:\n    tags:\n      - 'v*.*.*'",
		"permissions:\n  contents: read",
		"group: release-${{ github.ref }}",
		"cancel-in-progress: false",
		"test \"$GITHUB_REPOSITORY\" = klauern/index-01-hook",
		"vars.PUBLIC_RELEASE_APPROVED",
		"github.ref_protected",
		"./scripts/validate-release-tag.sh",
		"git fetch --force --no-tags origin \"refs/tags/$GITHUB_REF_NAME:refs/tags/$GITHUB_REF_NAME\"",
		"git fetch --no-tags origin refs/heads/main:refs/remotes/origin/main",
		"main_commit=$(git rev-parse refs/remotes/origin/main)",
		"git merge-base --is-ancestor \"$tag_commit\" \"$main_commit\"",
		"tag_commit=$(git rev-parse \"refs/tags/$GITHUB_REF_NAME^{commit}\")",
		"test \"$(git rev-parse HEAD)\" = \"$tag_commit\"",
		"VERSION=\"$GITHUB_REF_NAME\"",
		"COMMIT=$(git rev-parse \"refs/tags/$GITHUB_REF_NAME^{commit}\")",
		"BUILD_DATE=$(date -u",
		"go test -race ./...",
		"go vet ./...",
		"task test",
		"task test-release-artifacts",
		"task test-compose-runtime",
		"./scripts/ci-manifests.sh",
		"gitleaks git --redact --no-banner --exit-code 1 --log-opts=\"--all\" .",
		"gitleaks dir --redact --no-banner --exit-code 1 .",
		"actionlint .github/workflows/*.yml",
		"govulncheck ./...",
		"shellcheck scripts/*.sh",
		"./scripts/build-release-artifacts.sh",
		"go install github.com/anchore/syft/cmd/syft@v1.51.0",
		"spdx-json=dist/release/index-01-hook-source.spdx.json",
		`jq -e '.spdxVersion and (.packages | type == "array") and (.packages | length > 0)'`,
		"sha256sum --check checksums.txt",
		"actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9",
		"skip-decompress: false",
		"digest-mismatch: error",
		"environment: public-release",
		"contents: write",
		"packages: write",
		"id-token: write",
		"attestations: write",
		"actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333",
		"gh api repos/klauern/index-01-hook --jq .visibility",
		"docker.io/library/golang:1.26.10@sha256:3745f1aa11822297b79113f5e84ce2bffeb039734571c39855809cfe9e5d7be6",
		"platforms: linux/amd64,linux/arm64",
		"image=moby/buildkit@sha256:28a898719c18a33f4e8000685287fa36fd0dd9560c6440227d3a732d79bb41d8",
		"push-by-digest=true",
		"provenance: mode=max",
		"sbom: true",
		"org.opencontainers.image.source=https://github.com/klauern/index-01-hook",
		"org.opencontainers.image.revision=",
		"org.opencontainers.image.version=",
		"org.opencontainers.image.created=",
		"org.opencontainers.image.licenses=MIT AND BSD-3-Clause AND LicenseRef-SQLite-Public-Domain AND LicenseRef-CA-Certificates AND LicenseRef-IANA-TZData",
		"sigstore/cosign-installer@d58896d6a1865668819e1d91763c7751a165e159",
		"actions/attest-build-provenance@96278af6caaf10aea03fd8d33a09a777ca52d62f",
		"dist/release/THIRD_PARTY_NOTICES.txt",
		"cosign sign --yes",
		"cosign sign-blob --yes --bundle",
		"cosign verify ",
		"cosign verify-blob --bundle",
		"https://token.actions.githubusercontent.com",
		"gh api /users/klauern/packages/container/index-01-hook --jq .visibility",
		"docker logout ghcr.io",
		"docker buildx imagetools inspect",
		"{{json .SBOM}}",
		"{{json .Provenance}}",
		"gh api --paginate /users/klauern/packages/container/index-01-hook/versions",
		"docker buildx imagetools create",
		"--format '{{.Manifest.Digest}}'",
		"gh release create \"$VERSION\" --draft --verify-tag --generate-notes",
		"workflow_dispatch:\n    inputs:\n      draft_id:",
		"candidate_commit:",
		"test \"$GITHUB_REF\" = \"refs/tags/$GITHUB_REF_NAME\"",
		"Verify frozen dispatched candidate",
		"if: github.event_name == 'workflow_dispatch'",
		"node scripts/release-candidate.mjs verify",
		"gh release upload \"$VERSION\" dist/release/*",
		"if test -n \"$DRAFT_ID\"; then test \"$release_id\" = \"$DRAFT_ID\"; fi",
		"gh api \"repos/klauern/index-01-hook/releases?per_page=100\" --paginate",
		"release_id=$(gh api",
		"gh api \"repos/klauern/index-01-hook/releases/$release_id\"",
		".assets | length')\" = 14",
		"gh release edit \"$VERSION\" --draft=false",
		"Verify published GitHub Release",
	}
	for _, part := range required {
		if !strings.Contains(text, part) {
			t.Errorf("release workflow does not contain %q", part)
		}
	}

	if strings.Contains(text, "ref: ${{ needs.validate.outputs.commit }}") {
		t.Error("release workflow checks out a commit from a job output")
	}
	for _, job := range []string{"build:", "publish:"} {
		jobStart := strings.Index(text, job)
		if jobStart < 0 {
			t.Fatalf("release workflow does not contain %q", job)
		}
		checkout := strings.Index(text[jobStart:], "uses: actions/checkout@")
		verify := strings.Index(text[jobStart:], "run: test \"$(git rev-parse HEAD)\" = \"$VALIDATED_COMMIT\"")
		setup := strings.Index(text[jobStart:], "- name: Set up Go")
		if checkout < 0 || verify < checkout || (setup >= 0 && verify > setup) {
			t.Errorf("%s does not verify the checked out commit before build or publish operations", job)
		}
	}

	forbidden := []string{
		"pull_request",
		"pull_request_target",
		"ghcr.io/klauern/index-01-hook:latest",
		"ghcr.io/klauern/index-01-hook:stable",
		"ghcr.io/klauern/index-01-hook:main",
		"ghcr.io/klauern/index-01-hook:major",
		"ghcr.io/klauern/index-01-hook:minor",
		"secrets.",
		"KUBE_",
		"DEEPSEEK",
		"TICKTICK",
		"INDEX01_",
		"curl ",
	}
	for _, part := range forbidden {
		if strings.Contains(text, part) {
			t.Errorf("release workflow contains forbidden %q", part)
		}
	}

	for _, match := range workflowUsesPattern.FindAllStringSubmatch(text, -1) {
		if !fullCommitPattern.MatchString(match[1]) {
			t.Errorf("release action is not pinned to a full commit: %q", match[0])
		}
	}

	pins := map[string]string{
		"actions/checkout":                "11d5960a326750d5838078e36cf38b85af677262",
		"actions/setup-go":                "40f1582b2485089dde7abd97c1529aa768e1baff",
		"actions/upload-artifact":         "cf430e030ddbb5b0abf93d22962f4752f3646cd9",
		"actions/download-artifact":       "9000827ccba6bdab643e8b6fd33ac0654aef8333",
		"docker/setup-buildx-action":      "f87e5991a6d7451dcb8d9637bfbc97413f497069",
		"docker/login-action":             "c94ce9fb468520275223c153574b00df6fe4bcc9",
		"docker/build-push-action":        "c3c9e263c25d99ce0380d002d59b67737d91b0dc",
		"sigstore/cosign-installer":       "d58896d6a1865668819e1d91763c7751a165e159",
		"actions/attest-build-provenance": "96278af6caaf10aea03fd8d33a09a777ca52d62f",
	}
	for action, pin := range pins {
		if !strings.Contains(text, action+"@"+pin) {
			t.Errorf("action %s is not pinned to %s", action, pin)
		}
	}

	rejectTag := strings.Index(text, "Reject an existing version image tag")
	createTag := strings.Index(text, "Create the version image tag")
	createRelease := strings.Index(text, "Create or adopt the verified draft GitHub Release")
	publishRelease := strings.Index(text, "Publish draft GitHub Release")
	if rejectTag < 0 || createTag <= rejectTag || createRelease <= createTag || publishRelease <= createRelease {
		t.Error("release publication steps are not in the required immutable order")
	}
}
