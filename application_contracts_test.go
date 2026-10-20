package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// checkApplicationSpecification type-checks neutral application contract source and rejects adapter imports.
func checkApplicationSpecification(source string) error {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, "contract.go", source, 0)
	if err != nil {
		return err
	}
	if parsed.Name.Name != "application" {
		return fmt.Errorf("contract package must be application")
	}
	for _, imported := range parsed.Imports {
		name, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return err
		}
		if name != "context" && name != "time" {
			return fmt.Errorf("non-neutral contract import %s", name)
		}
	}
	config := types.Config{Importer: importer.Default()}
	_, err = config.Check("contract/application", files, []*ast.File{parsed}, nil)
	return err
}

// TestApplicationContractSpecification checks that the documented contract compiles and exposes typed route selection.
func TestApplicationContractSpecification(t *testing.T) {
	content, err := os.ReadFile("docs/adr/0002-application-contracts.md")
	if err != nil {
		t.Fatal(err)
	}
	_, code, found := strings.Cut(string(content), "```go\n")
	if !found {
		t.Fatal("missing Go contract specification")
	}
	code, _, found = strings.Cut(code, "\n```")
	if !found {
		t.Fatal("unterminated Go contract specification")
	}
	code += `
func selectedRoute(result AssessmentResult) string { return result.Item.ProjectAlias }
func prepareActiveEvidence(result AssessmentResult) ActiveVerificationEvidence {
 return ActiveVerificationEvidence{Decision: result.Decision, Metadata: result.Metadata, Reason: result.Reason, Detail: result.PrivateDetail}
}
func persistActive(ctx context.Context, repository ActiveVerificationRepository, id RecordingID, owner ExtractionFence, evidence []ActiveVerificationEvidence) error {
 return repository.SaveActiveVerification(ctx, id, owner, evidence)
}
func readRecording(ctx context.Context, reader RecordingReader, id RecordingID) (bool, Fingerprint, error) {
 result, err := reader.RecordingStatus(ctx, id)
 return result.Found, result.Status.Fingerprint, err
}
func readDashboard(ctx context.Context, reader DashboardReader, id RecordingID) (int64, bool, error) {
 page, err := reader.Recordings(ctx, RecordingFilter{Page: 1, PageSize: 25, State: "not-started"})
 if err != nil { return 0, false, err }
 detail, err := reader.Recording(ctx, id)
 return page.Total, detail.Found, err
}
func retryOne(ctx context.Context, repository ManualRetryRepository, id DeliveryID, clock Clock) error {
 return repository.RetryDelivery(ctx, id, clock.Now())
}
func purgeWithExplicitTime(ctx context.Context, repository RetentionRepository, clock Clock, retention time.Duration) (int64, error) {
 at := clock.Now()
 recordings, err := repository.PurgeRecordingsBefore(ctx, at.Add(-retention))
 if err != nil { return 0, err }
 _, err = repository.PurgeEvaluationEvidenceThrough(ctx, at)
 return recordings, err
}
func retryThroughService(ctx context.Context, service ManualRetryService, id RecordingID) (RecordingID, error) {
 result, err := service.RetryRecording(ctx, id)
 return result.ID, err
}
func readAggregate(ctx context.Context, reader OperationalReader) (int, error) {
 snapshot, err := reader.OperationalStatus(ctx)
 return snapshot.Queue.Active, err
}
`
	if err := checkApplicationSpecification(code); err != nil {
		t.Fatal(err)
	}
}

// TestApplicationContractRejectsAdapterTypesAndInvalidSignatures checks rejection of adapter imports and invalid declarations.
func TestApplicationContractRejectsAdapterTypesAndInvalidSignatures(t *testing.T) {
	for _, source := range []string{
		`package application; import "database/sql"; type Port interface { Save(*sql.Tx) error }`,
		`package application; import "net/http"; type Port interface { Send(*http.Request) error }`,
		`package application; type Port interface { Save(MissingType) error }`,
		`package adapter; type Port interface { Save() error }`,
	} {
		if err := checkApplicationSpecification(source); err == nil {
			t.Errorf("invalid contract accepted: %s", source)
		}
	}
}

type applicationInvariant struct {
	ID    string   `json:"id"`
	Owner string   `json:"owner"`
	Rule  string   `json:"rule"`
	Tests []string `json:"tests"`
}

// checkApplicationInvariants requires unique invariant identifiers, known owners, rules, and available regression tests.
func checkApplicationInvariants(invariants []applicationInvariant, available map[string]bool) error {
	if len(invariants) == 0 {
		return fmt.Errorf("no invariants")
	}
	seen := map[string]bool{}
	for _, invariant := range invariants {
		if invariant.ID == "" || seen[invariant.ID] {
			return fmt.Errorf("missing or duplicate invariant ID %q", invariant.ID)
		}
		seen[invariant.ID] = true
		if !boundaryOwners[invariant.Owner] || strings.TrimSpace(invariant.Rule) == "" || len(invariant.Tests) == 0 {
			return fmt.Errorf("incomplete invariant %s", invariant.ID)
		}
		for _, reference := range invariant.Tests {
			if !available[reference] {
				return fmt.Errorf("missing invariant regression %s", reference)
			}
		}
	}
	return nil
}

// runnableContractRegression matches Go test naming rules and requires an ordinary *testing.T signature.
func runnableContractRegression(file *ast.File, function *ast.FuncDecl) bool {
	if function.Recv != nil || !strings.HasPrefix(function.Name.Name, "Test") {
		return false
	}
	suffix := strings.TrimPrefix(function.Name.Name, "Test")
	if suffix != "" {
		first, _ := utf8.DecodeRuneInString(suffix)
		if unicode.IsLower(first) {
			return false
		}
	}
	signature := function.Type
	if signature.TypeParams != nil || (signature.Results != nil && len(signature.Results.List) != 0) || signature.Params == nil || len(signature.Params.List) != 1 {
		return false
	}
	parameter := signature.Params.List[0]
	if len(parameter.Names) > 1 {
		return false
	}
	pointer, ok := parameter.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	for _, imported := range file.Imports {
		name, _ := strconv.Unquote(imported.Path.Value)
		if name != "testing" {
			continue
		}
		alias := "testing"
		if imported.Name != nil {
			alias = imported.Name.Name
		}
		if alias == "." {
			value, ok := pointer.X.(*ast.Ident)
			return ok && value.Name == "T"
		}
		value, ok := pointer.X.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		qualifier, ok := value.X.(*ast.Ident)
		return ok && qualifier.Name == alias && value.Sel.Name == "T"
	}
	return false
}

// TestApplicationRegressionDiscoveryRejectsHelpers distinguishes runnable tests from helpers and invalid signatures.
func TestApplicationRegressionDiscoveryRejectsHelpers(t *testing.T) {
	for _, example := range []struct {
		declaration string
		want        bool
	}{
		{"func Testhelper() {}", false},
		{"func TestHelper() {}", false},
		{"func TestReal(t *testing.T) {}", true},
		{"func TestOther(t *testing.B) {}", false},
		{"func TestReturn(t *testing.T) error { return nil }", false},
		{"func TestPair(a, b *testing.T) {}", false},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package fixture; import \"testing\"; "+example.declaration, 0)
		if err != nil {
			t.Fatal(err)
		}
		function := file.Decls[len(file.Decls)-1].(*ast.FuncDecl)
		if got := runnableContractRegression(file, function); got != example.want {
			t.Errorf("%s runnable=%v want=%v", example.declaration, got, example.want)
		}
	}
}

// TestApplicationInvariantReferences checks documented invariant owners and runnable regression references.
func TestApplicationInvariantReferences(t *testing.T) {
	content, err := os.ReadFile("docs/adr/application-invariants.json")
	if err != nil {
		t.Fatal(err)
	}
	var invariants []applicationInvariant
	if err := json.Unmarshal(content, &invariants); err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{}
	for _, name := range names {
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && runnableContractRegression(parsed, function) {
				available[name+"#"+function.Name.Name] = true
			}
		}
	}
	if err := checkApplicationInvariants(invariants, available); err != nil {
		t.Fatal(err)
	}
	t.Logf("validated %d invariant owners and regression references", len(invariants))
}

// TestApplicationInvariantsRejectMissingOwnershipAndRegressions checks incomplete ownership and missing or duplicate regression records.
func TestApplicationInvariantsRejectMissingOwnershipAndRegressions(t *testing.T) {
	for _, invariants := range [][]applicationInvariant{
		nil,
		{{ID: "one", Owner: "unknown", Rule: "rule", Tests: []string{"exists"}}},
		{{ID: "one", Owner: "persistence", Rule: "rule"}},
		{{ID: "one", Owner: "persistence", Rule: "rule", Tests: []string{"missing"}}},
		{{ID: "one", Owner: "persistence", Rule: "rule", Tests: []string{"exists"}}, {ID: "one", Owner: "runtime", Rule: "rule", Tests: []string{"exists"}}},
	} {
		if err := checkApplicationInvariants(invariants, map[string]bool{"exists": true}); err == nil {
			t.Errorf("invalid invariants accepted: %+v", invariants)
		}
	}
}
