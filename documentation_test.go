package main

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var markdownLinkPattern = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+["'][^)]*["'])?\)`)
var formFieldPattern = regexp.MustCompile(`--form\s+(?:transcription|audio)(?:=|\s|$)`)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	return workingDirectory
}

func ignoredDocumentationDirectory(name string) bool {
	switch name {
	case ".beads", ".git", ".serena", ".agents", ".cache", "build", "coverage", "dist", "generated", "node_modules", "out", "target", "tmp", "vendor":
		return true
	default:
		return false
	}
}

func markdownFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && ignoredDocumentationDirectory(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk Markdown files: %v", err)
	}
	return files
}

func relativeMarkdownTarget(target string) (string, bool) {
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") {
		return "", false
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.IsAbs() {
		return "", false
	}
	if parsed.Path == "" {
		return "", false
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", false
	}
	return path, true
}

func TestMarkdownRelativeLinks(t *testing.T) {
	root := repositoryRoot(t)
	for _, file := range markdownFiles(t, root) {
		displayPath, _ := filepath.Rel(root, file)
		contents, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, match := range markdownLinkPattern.FindAllStringSubmatch(string(contents), -1) {
			target, ok := relativeMarkdownTarget(match[1])
			if !ok {
				continue
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(file), filepath.FromSlash(target)))
			relative, err := filepath.Rel(root, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				t.Errorf("%s links outside the repository: %q", displayPath, match[1])
				continue
			}
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s links to missing target %q: %v", displayPath, match[1], err)
			}
		}
	}
}

func TestREADMEReferenceLinks(t *testing.T) {
	contents, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	for _, link := range []string{
		"[Architecture](docs/architecture.md)",
		"[Configuration](docs/configuration.md)",
		"[API](docs/api.md)",
		"[Operator](docs/operator.md)",
		"[Provider setup](docs/provider-setup.md)",
		"[Docker Compose](docs/docker-compose.md)",
		"[Kubernetes](docs/kubernetes.md)",
		"[Public support contract](docs/public-support-contract.md)",
		"[Security](SECURITY.md)",
		"[Support](SUPPORT.md)",
		"[Contributing](CONTRIBUTING.md)",
		"[Third-party notices](THIRD_PARTY_NOTICES.md)",
		"[License](LICENSE)",
	} {
		if !strings.Contains(string(contents), link) {
			t.Errorf("README.md does not contain required link %s", link)
		}
	}
}

func requiredEnvironmentValues(t *testing.T, path string, keys []string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	for _, key := range keys {
		if value, ok := values[key]; !ok {
			t.Errorf("%s does not define required value %s", path, key)
		} else if value != "" {
			t.Errorf("%s contains a non-empty required value %s", path, key)
		}
	}
}

func TestExampleEnvironmentValuesAreEmpty(t *testing.T) {
	required := []string{
		"INDEX01_WEBHOOK_TOKEN",
		"INDEX01_DEEPSEEK_TOKEN",
		"INDEX01_TICKTICK_TOKEN",
		"INDEX01_TICKTICK_DEFAULT_PROJECT_ID",
		"INDEX01_TICKTICK_NOTE_PROJECT_ID",
	}
	requiredEnvironmentValues(t, ".env.example", required)
	requiredEnvironmentValues(t, "deploy/kubernetes/index-01-hook-secrets.env.example", required)
}

func TestComposeSmokeWebhookIsProviderFree(t *testing.T) {
	contents, err := os.ReadFile("docs/docker-compose.md")
	if err != nil {
		t.Fatalf("read docs/docker-compose.md: %v", err)
	}
	text := string(contents)
	start := strings.Index(text, "## First provider-free webhook request")
	if start < 0 {
		t.Fatal("Compose guide has no provider-free webhook section")
	}
	section := text[start:]
	if next := strings.Index(section[1:], "\n## "); next >= 0 {
		section = section[:next+1]
	}
	if formFieldPattern.MatchString(section) {
		t.Fatal("Compose smoke webhook contains a transcription or audio form field")
	}
	for _, required := range []string{"provider-free webhook", "recordedAt", "client", "queued` set to `false", "no provider call", "TickTick"} {
		if !strings.Contains(section, required) {
			t.Errorf("Compose smoke section does not state %q", required)
		}
	}
}

func TestDocumentationHasNoRemovedPrivateDeploymentValues(t *testing.T) {
	root := repositoryRoot(t)
	for _, file := range markdownFiles(t, root) {
		displayPath, _ := filepath.Rel(root, file)
		contents, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		privateDeploymentPatterns := []*regexp.Regexp{
			// The removed private deployment used hostnames under a personal
			// .work TLD. Match every .work hostname so no private value can be
			// reintroduced; false positives on unrelated .work domains are
			// acceptable for public documentation.
			regexp.MustCompile(`(?i)\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.work\b`),
			// Match all numbered Docker registry hosts (registry-N.docker.io),
			// not only the removed literal. Intentionally over-matches to keep
			// private registry references out of public documentation.
			regexp.MustCompile(`(?i)\bregistry-[0-9]+\.docker\.io\b`),
			regexp.MustCompile(`(?i)\bgitea[_-]admin\b`),
		}
		for _, forbidden := range privateDeploymentPatterns {
			if forbidden.Match(contents) {
				t.Errorf("%s contains a removed private deployment pattern %q", displayPath, forbidden.String())
			}
		}
	}
}

func normalContributorCode(contents string) string {
	var builder strings.Builder
	section := ""
	inFence := false
	for _, line := range strings.Split(contents, "\n") {
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			inFence = false
			continue
		}
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence && (section == "Build and test" || section == "Local development" || section == "Test requirements") {
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func TestNormalContributorCommandsAreOffline(t *testing.T) {
	for _, path := range []string{"README.md", "CONTRIBUTING.md"} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		commands := normalContributorCode(string(contents))
		for _, pattern := range []string{
			"test-deepseek-live",
			"task deploy",
			"task server-dry-run",
			"kubectl ",
		} {
			if strings.Contains(commands, pattern) {
				t.Errorf("%s contributor command invokes live provider or deployment: %q", path, pattern)
			}
		}
	}
}

func TestPublicProxyControlsAreDocumented(t *testing.T) {
	caddy, err := os.ReadFile("deploy/compose/Caddyfile.example")
	if err != nil {
		t.Fatalf("read Caddy example: %v", err)
	}
	caddyText := string(caddy)
	for _, required := range []string{"request_body", "max_size 64MiB"} {
		if !strings.Contains(caddyText, required) {
			t.Errorf("Caddy example does not enforce %q", required)
		}
	}
	for _, path := range []string{"docs/docker-compose.md", "docs/kubernetes.md", "docs/public-support-contract.md"} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(contents)
		for _, required := range []string{"64 MiB", "10 webhook requests per minute", "burst of 20"} {
			if !strings.Contains(text, required) {
				t.Errorf("%s does not document %q", path, required)
			}
		}
	}
}
