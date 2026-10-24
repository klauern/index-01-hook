package main

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/application"
)

// TestApplicationValueCompatibility preserves root type identity and the existing JSON schema.
func TestApplicationValueCompatibility(t *testing.T) {
	due := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	item := QueuedItem{Kind: ItemKindTask, Title: "Pay invoice", Content: "Details", Due: &due, Priority: 3, Tags: []string{"finance"}, ProjectAlias: "work"}
	var imported application.Item = item
	if reflect.TypeOf(item) != reflect.TypeOf(imported) {
		t.Fatal("root model diverged from application value")
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Kind":"task","Title":"Pay invoice","Content":"Details","Due":"2026-10-21T09:00:00Z","AllDay":false,"Priority":3,"Tags":["finance"],"ProjectAlias":"work"}`
	if string(data) != want {
		t.Fatalf("wire value=%s, want%s", data, want)
	}
	var decoded application.Item
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, imported) {
		t.Fatalf("round trip=%+v, want%+v", decoded, imported)
	}
	if string(application.Task) != "task" || string(application.Note) != "note" {
		t.Fatal("persisted kinds changed")
	}
}

// TestApplicationValueDependencyBoundary rejects infrastructure dependencies in the application graph.
func TestApplicationValueDependencyBoundary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./internal/application").CombinedOutput()
	if err != nil {
		t.Fatalf("application dependency discovery:%v:%s", err, out)
	}
	module := "github.com/klauern/index-01-hook"
	for _, name := range strings.Fields(string(out)) {
		if name == "database/sql" || strings.HasPrefix(name, "database/sql/") || name == "net/http" || strings.HasPrefix(name, "net/http/") {
			t.Errorf("application imports infrastructure dependency %s", name)
		}
		if strings.HasPrefix(name, module) && name != module+"/internal/application" && !strings.HasPrefix(name, module+"/internal/application/") {
			t.Errorf("application imports outward repository dependency %s", name)
		}
	}
}
