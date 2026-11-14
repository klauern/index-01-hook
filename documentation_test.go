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
	if target == "" {
		return "", false
	}
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") {
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
		(documentationFile{root: root, path: file}).checkLinks(t)
	}
}

type documentationFile struct {
	root string
	path string
}

func (file documentationFile) checkLinks(t *testing.T) {
	t.Helper()
	contents, err := os.ReadFile(file.path)
	if err != nil {
		t.Fatalf("read %s: %v", file.path, err)
	}
	for _, match := range markdownLinkPattern.FindAllStringSubmatch(string(contents), -1) {
		file.checkLink(t, match[1])
	}
}

func (file documentationFile) checkLink(t *testing.T, link string) {
	t.Helper()
	target, ok := relativeMarkdownTarget(link)
	if !ok {
		return
	}
	displayPath, _ := filepath.Rel(file.root, file.path)
	resolved := filepath.Clean(filepath.Join(filepath.Dir(file.path), filepath.FromSlash(target)))
	if !file.containsTarget(resolved) {
		t.Errorf("%s links outside the repository: %q", displayPath, link)
		return
	}
	if _, err := os.Stat(resolved); err != nil {
		t.Errorf("%s links to missing target %q: %v", displayPath, link, err)
	}
}

func (file documentationFile) containsTarget(path string) bool {
	relative, err := filepath.Rel(file.root, path)
	if err != nil || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
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

type environmentExample struct {
	path     string
	required []string
}

func (example environmentExample) assertEmptyRequiredValues(t *testing.T) {
	path, keys := example.path, example.required
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	values := exampleEnvironmentValues(string(contents))
	for _, key := range keys {
		if value, ok := values[key]; !ok {
			t.Errorf("%s does not define required value %s", path, key)
		} else if value != "" {
			t.Errorf("%s contains a non-empty required value %s", path, key)
		}
	}
}

func exampleEnvironmentValues(contents string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

func TestExampleEnvironmentValuesAreEmpty(t *testing.T) {
	required := []string{
		"INDEX01_WEBHOOK_TOKEN",
		"INDEX01_DEEPSEEK_TOKEN",
		"INDEX01_TICKTICK_TOKEN",
		"INDEX01_TICKTICK_DEFAULT_PROJECT_ID",
		"INDEX01_TICKTICK_NOTE_PROJECT_ID",
	}
	(environmentExample{path: ".env.example", required: required}).assertEmptyRequiredValues(t)
	(environmentExample{path: "deploy/kubernetes/index-01-hook-secrets.env.example", required: required}).assertEmptyRequiredValues(t)
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
		(documentationFile{root: root, path: file}).checkPrivacy(t)
	}
}

func (file documentationFile) checkPrivacy(t *testing.T) {
	t.Helper()
	displayPath, _ := filepath.Rel(file.root, file.path)
	contents, err := os.ReadFile(file.path)
	if err != nil {
		t.Fatalf("read %s: %v", file.path, err)
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
		if inFence && contributorSection(section) {
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func contributorSection(section string) bool {
	switch section {
	case "Build and test", "Local development", "Test requirements":
		return true
	default:
		return false
	}
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
