package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

type boundaryEntry struct {
	File     string `json:"file"`
	Function string `json:"function"`
	Owner    string `json:"owner"`
}
type boundaryInventory struct {
	SchemaVersion int                      `json:"schema_version"`
	Surfaces      map[string][]string      `json:"surfaces"`
	EntryPoints   map[string]boundaryEntry `json:"entry_points"`
	Commands      map[string]string        `json:"commands"`
}
type boundaryDiscovery struct {
	Files     map[string]bool
	Functions map[string]bool
	Mains     map[string]bool
	Commands  map[string]bool
}

var boundaryOwners = map[string]bool{
	"application_policy": true, "provider": true, "persistence": true,
	"inbound_interface": true, "runtime": true, "tooling": true,
}

// Exclude only known root artifact and metadata paths, never nested source names.
var boundaryArtifactRoots = map[string]bool{
	".git": true, ".beads": true, ".agents": true, ".claude": true,
	".codex": true, ".serena": true, "dist": true, "node_modules": true, "vendor": true,
}

// These named runtime contracts supplement automatically discovered main functions.
// A renamed or removed contract requires an explicit inventory guard update.
var boundaryRuntimeContracts = []string{
	"dashboard.go#runDashboardWithContext", "worker.go#Worker.Run",
	"evaluation_collection.go#runEvaluationCollection", "operator.go#executeWithInput",
	"main.go#runWithEnvironment", "webhook.go#NewHandler",
}

// discoverBoundarySurfaces scans source so new, unstaged surfaces cannot escape coverage.
// Build-tagged Go files are included even when the host does not compile them.
func discoverBoundarySurfaces(tree fs.FS) (boundaryDiscovery, error) {
	found := boundaryDiscovery{map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}}
	err := fs.WalkDir(tree, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name != "." && (boundaryArtifactRoots[name]) {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("extend inventory discovery for symlink %q", name)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			contents, err := fs.ReadFile(tree, name)
			if err != nil {
				return err
			}
			source, err := parser.ParseFile(token.NewFileSet(), name, contents, parser.ParseComments)
			if err != nil {
				return err
			}
			found.Files[name] = true
			for _, declaration := range source.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				functionName := function.Name.Name
				if function.Recv != nil {
					receiver := function.Recv.List[0].Type
					if pointer, ok := receiver.(*ast.StarExpr); ok {
						receiver = pointer.X
					}
					if receiver, ok := receiver.(*ast.Ident); ok {
						functionName = receiver.Name + "." + functionName
					}
				}
				key := name + "#" + functionName
				found.Functions[key] = true
				if source.Name.Name == "main" && function.Name.Name == "main" && function.Recv == nil {
					found.Mains[key] = true
				}
				if name == "operator.go" && function.Name.Name == "executeWithInput" {
					ast.Inspect(function.Body, func(node ast.Node) bool {
						if comparison, ok := node.(*ast.BinaryExpr); ok && (comparison.Op == token.EQL || comparison.Op == token.NEQ) {
							if argsZero(comparison.X) {
								addBoundaryCommand(found.Commands, comparison.Y)
							}
							if argsZero(comparison.Y) {
								addBoundaryCommand(found.Commands, comparison.X)
							}
						}
						if selection, ok := node.(*ast.SwitchStmt); ok && argsZero(selection.Tag) {
							for _, statement := range selection.Body.List {
								for _, value := range statement.(*ast.CaseClause).List {
									addBoundaryCommand(found.Commands, value)
								}
							}
						}
						return true
					})
				}
			}
			for _, group := range source.Comments {
				for _, comment := range group.List {
					parts := strings.Fields(comment.Text)
					if len(parts) == 0 || parts[0] != "//go:embed" {
						continue
					}
					for _, pattern := range parts[1:] {
						// Current source uses unquoted, flat file globs. Reject new syntax until discovery is extended.
						if strings.ContainsAny(pattern, "\"`") || strings.HasPrefix(pattern, "all:") {
							return fmt.Errorf("extend inventory discovery for embed pattern %q", pattern)
						}
						pattern = path.Join(path.Dir(name), pattern)
						matches, err := fs.Glob(tree, pattern)
						if err != nil {
							return err
						}
						if len(matches) == 0 {
							return fmt.Errorf("embed pattern %q matches no files", pattern)
						}
						for _, match := range matches {
							info, err := fs.Stat(tree, match)
							if err != nil {
								return err
							}
							if info.IsDir() {
								return fmt.Errorf("extend inventory discovery for embedded directory %q", match)
							}
							found.Files[match] = true
						}
					}
				}
			}
		} else if info.Mode().Perm()&0111 != 0 {
			file, err := tree.Open(name)
			if err != nil {
				return err
			}
			prefix := make([]byte, 2)
			_, readErr := io.ReadFull(file, prefix)
			closeErr := file.Close()
			if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			if string(prefix) == "#!" {
				found.Files[name] = true
			}
		}
		return nil
	})
	return found, err
}

// argsZero recognizes the root dispatcher argument expression args[0].
func argsZero(expression ast.Expr) bool {
	index, ok := expression.(*ast.IndexExpr)
	if !ok {
		return false
	}
	name, ok := index.X.(*ast.Ident)
	if !ok || name.Name != "args" {
		return false
	}
	value, ok := index.Index.(*ast.BasicLit)
	return ok && value.Kind == token.INT && value.Value == "0"
}

// addBoundaryCommand records a literal command name from the dispatcher.
func addBoundaryCommand(commands map[string]bool, expression ast.Expr) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return
	}
	if value, err := strconv.Unquote(literal.Value); err == nil {
		commands[value] = true
	}
}

// validateBoundaryInventory checks ownership and completeness against discovered source surfaces.
func validateBoundaryInventory(inventory boundaryInventory, found boundaryDiscovery) []string {
	var problems []string
	if inventory.SchemaVersion != 1 {
		problems = append(problems, "unsupported inventory schema")
	}
	assigned := map[string]bool{}
	for owner, files := range inventory.Surfaces {
		if !boundaryOwners[owner] {
			problems = append(problems, "unknown owner "+owner)
		}
		for _, file := range files {
			if assigned[file] {
				problems = append(problems, "duplicate surface "+file)
			}
			assigned[file] = true
			if !found.Files[file] {
				problems = append(problems, "stale surface "+file)
			}
		}
	}
	for file := range found.Files {
		if !assigned[file] {
			problems = append(problems, "unassigned surface "+file)
		}
	}
	entryFunctions := map[string]string{}
	for name, entry := range inventory.EntryPoints {
		key := entry.File + "#" + entry.Function
		if !found.Functions[key] {
			problems = append(problems, "stale entry point "+name)
		}
		if !boundaryOwners[entry.Owner] {
			problems = append(problems, "unknown entry owner "+name)
		}
		if owner, assigned := entryFunctions[key]; assigned && owner != entry.Owner {
			problems = append(problems, "conflicting entry owners "+key)
		} else {
			entryFunctions[key] = entry.Owner
		}
	}
	for main := range found.Mains {
		if _, assigned := entryFunctions[main]; !assigned {
			problems = append(problems, "unassigned executable "+main)
		}
	}
	for _, contract := range boundaryRuntimeContracts {
		if !found.Functions[contract] {
			problems = append(problems, "missing runtime contract "+contract)
		}
		if _, assigned := entryFunctions[contract]; !assigned {
			problems = append(problems, "unassigned runtime contract "+contract)
		}
	}
	for command, owner := range inventory.Commands {
		if !found.Commands[command] {
			problems = append(problems, "stale command "+command)
		}
		if !boundaryOwners[owner] {
			problems = append(problems, "unknown command owner "+command)
		}
	}
	for command := range found.Commands {
		if _, ok := inventory.Commands[command]; !ok {
			problems = append(problems, "unassigned command "+command)
		}
	}
	sort.Strings(problems)
	return problems
}

// decodeBoundaryInventory accepts one strict inventory value and rejects trailing content.
func decodeBoundaryInventory(reader io.Reader) (boundaryInventory, error) {
	var inventory boundaryInventory
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inventory); err != nil {
		return inventory, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple inventory values")
		}
		return inventory, err
	}
	return inventory, nil
}

// TestArchitectureBoundaryInventory checks the committed inventory against current repository source.
func TestArchitectureBoundaryInventory(t *testing.T) {
	contents, err := os.ReadFile("docs/architecture/boundary-inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := decodeBoundaryInventory(strings.NewReader(string(contents)))
	if err != nil {
		t.Fatal(err)
	}
	found, err := discoverBoundarySurfaces(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateBoundaryInventory(inventory, found); len(problems) > 0 {
		t.Fatal(strings.Join(problems, "\n"))
	}
	t.Logf("assigned %d source/asset/script surfaces, %d executable entry points, %d root commands", len(found.Files), len(found.Mains), len(found.Commands))
}

// TestBoundaryDiscoveryIncludesNewSurfacesAcrossPlatforms checks discovery and missing-assignment failures for new source surfaces.
func TestBoundaryDiscoveryIncludesNewSurfacesAcrossPlatforms(t *testing.T) {
	tree := fstest.MapFS{
		"main.go":                {Data: []byte("package main\n//go:embed assets/*\nvar assets string\nfunc main() {}")},
		".github/actions/new.go": {Data: []byte("package main\nfunc main() {}")},
		"platform_windows.go":    {Data: []byte("//go:build windows\npackage main\nfunc windowsOnly() {}")},
		"assets/page.css":        {Data: []byte("body{}")},
		"tools/new.py":           {Data: []byte("#!/usr/bin/env python3\n"), Mode: 0755},
		"operator.go":            {Data: []byte(`package main; func executeWithInput(args []string) { if args[0]=="new-command" {} }`)},
		"ignored_test.go":        {Data: []byte("invalid test ignored")},
		"dist/generated.go":      {Data: []byte("invalid generated ignored")},
	}
	found, err := discoverBoundarySurfaces(tree)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.go", ".github/actions/new.go", "platform_windows.go", "assets/page.css", "tools/new.py"} {
		if !found.Files[name] {
			t.Errorf("missing new surface %s", name)
		}
	}
	if !found.Mains["main.go#main"] || !found.Commands["new-command"] {
		t.Fatal("executable or dispatched command missing")
	}
	problems := strings.Join(validateBoundaryInventory(boundaryInventory{SchemaVersion: 1}, found), "\n")
	for _, want := range []string{"unassigned surface platform_windows.go", "unassigned surface assets/page.css", "unassigned surface tools/new.py", "unassigned executable main.go#main", "unassigned command new-command"} {
		if !strings.Contains(problems, want) {
			t.Errorf("missing coverage failure %q", want)
		}
	}
}

// TestBoundaryInventoryRejectsStaleDuplicateAndInvalidAssignments checks invalid schema, ownership, and stale or duplicate surfaces.
func TestBoundaryInventoryRejectsStaleDuplicateAndInvalidAssignments(t *testing.T) {
	found := boundaryDiscovery{Files: map[string]bool{"current.go": true}, Functions: map[string]bool{}, Mains: map[string]bool{}, Commands: map[string]bool{}}
	inventory := boundaryInventory{SchemaVersion: 2, Surfaces: map[string][]string{"wrong": {"old.go", "current.go", "current.go"}}, EntryPoints: map[string]boundaryEntry{"missing": {File: "old.go", Function: "main", Owner: "wrong"}}, Commands: map[string]string{"old": "wrong"}}
	problems := strings.Join(validateBoundaryInventory(inventory, found), "\n")
	for _, want := range []string{"unsupported inventory schema", "unknown owner", "stale surface", "duplicate surface", "stale entry point", "unknown entry owner", "stale command", "unknown command owner"} {
		if !strings.Contains(problems, want) {
			t.Errorf("missing validation failure %q", want)
		}
	}
}

// TestBoundaryDiscoveryIncludesNestedArtifactNames checks that nested artifact-like names remain discoverable.
func TestBoundaryDiscoveryIncludesNestedArtifactNames(t *testing.T) {
	for _, name := range []string{"internal/build/main.go", "internal/generated/main.go", "cmd/target/main.go", "internal/tmp/main.go", "internal/dist/main.go"} {
		t.Run(name, func(t *testing.T) {
			found, err := discoverBoundarySurfaces(fstest.MapFS{name: {Data: []byte("package main; func main() {}")}})
			if err != nil {
				t.Fatal(err)
			}
			problems := strings.Join(validateBoundaryInventory(boundaryInventory{SchemaVersion: 1}, found), "\n")
			if !strings.Contains(problems, "unassigned surface "+name) || !strings.Contains(problems, "unassigned executable "+name+"#main") {
				t.Fatalf("source escaped inventory: %s", problems)
			}
		})
	}
}

// TestBoundaryInventoryRequiresRuntimeContracts checks explicit assignment of every required runtime constructor.
func TestBoundaryInventoryRequiresRuntimeContracts(t *testing.T) {
	contents, err := os.ReadFile("docs/architecture/boundary-inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := decodeBoundaryInventory(strings.NewReader(string(contents)))
	if err != nil {
		t.Fatal(err)
	}
	found, err := discoverBoundarySurfaces(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	for name, entry := range inventory.EntryPoints {
		if entry.Function != "main" {
			delete(inventory.EntryPoints, name)
		}
	}
	problems := strings.Join(validateBoundaryInventory(inventory, found), "\n")
	for _, contract := range boundaryRuntimeContracts {
		if !strings.Contains(problems, "unassigned runtime contract "+contract) {
			t.Errorf("runtime assignment escaped check: %s", contract)
		}
	}
}

// TestBoundaryInventoryRejectsTrailingJSON checks rejection of extra values and malformed trailing data.
func TestBoundaryInventoryRejectsTrailingJSON(t *testing.T) {
	for _, suffix := range []string{" garbage", " {}"} {
		if _, err := decodeBoundaryInventory(strings.NewReader(`{"schema_version":1}` + suffix)); err == nil {
			t.Errorf("accepted trailing content %q", suffix)
		}
	}
	if _, err := decodeBoundaryInventory(strings.NewReader("{\"schema_version\":1} \n")); err != nil {
		t.Fatal(err)
	}
}

// TestBoundaryDiscoveryRejectsUnsupportedEmbeds checks that unsupported embed syntax fails discovery.
func TestBoundaryDiscoveryRejectsUnsupportedEmbeds(t *testing.T) {
	for _, pattern := range []string{`"assets/a.css"`, "`assets/a.css`", "all:assets/*", "assets", "missing/*"} {
		t.Run(pattern, func(t *testing.T) {
			tree := fstest.MapFS{"main.go": {Data: []byte("package main\n//go:embed " + pattern + "\nvar assets string")}, "assets/a.css": {Data: []byte("body{}")}}
			if _, err := discoverBoundarySurfaces(tree); err == nil {
				t.Fatalf("accepted unsupported embed %q", pattern)
			}
		})
	}
}

// TestBoundaryInventoryRejectsConflictingEntryOwners checks aliases without permitting conflicting function ownership.
func TestBoundaryInventoryRejectsConflictingEntryOwners(t *testing.T) {
	contents, err := os.ReadFile("docs/architecture/boundary-inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	found, err := discoverBoundarySurfaces(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	for _, conflicting := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflicting=%t", conflicting), func(t *testing.T) {
			inventory, err := decodeBoundaryInventory(strings.NewReader(string(contents)))
			if err != nil {
				t.Fatal(err)
			}
			var entry boundaryEntry
			for _, candidate := range inventory.EntryPoints {
				if candidate.File == "main.go" && candidate.Function == "main" {
					entry = candidate
					break
				}
			}
			if entry.File == "" {
				t.Fatal("root executable assignment missing")
			}
			if conflicting {
				if entry.Owner == "tooling" {
					entry.Owner = "runtime"
				} else {
					entry.Owner = "tooling"
				}
			}
			inventory.EntryPoints["root_alias"] = entry
			problems := validateBoundaryInventory(inventory, found)
			if conflicting {
				if len(problems) != 1 || problems[0] != "conflicting entry owners main.go#main" {
					t.Fatalf("conflicting ownership was not rejected: %v", problems)
				}
			} else if len(problems) != 0 {
				t.Fatalf("consistent alias was rejected: %v", problems)
			}
		})
	}
}
