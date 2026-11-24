package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

const toolTestDashboard = "title: Week\nwidgets:\n  - id: open\n    type: list\n  - id: memo\n    type: note\n    text: private words\n"

func writeDashboardFile(t *testing.T, k *kb.KB, name, content string) {
	t.Helper()
	dir := filepath.Join(k.Root, "dashboards")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dashboardServer(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	s, k := workServer(t)
	writeDashboardFile(t, k, "week", toolTestDashboard)
	return s, k
}

// TestDashboardToolsAreAdvanced: the two read tools are advanced, read-only,
// registered and of the collection class; the write names are not read-only.
func TestDashboardToolsAreAdvanced(t *testing.T) {
	t.Parallel()
	// The write names join advancedToolNames with their tools: the registry
	// test refuses a name no tool carries.
	for _, n := range []string{"dashboard_list", "dashboard_get"} {
		if !ToolAdvanced(n) {
			t.Errorf("%s must be advanced", n)
		}
	}
	s, _ := workServer(t)
	for _, n := range []string{"dashboard_list", "dashboard_get"} {
		tool, ok := s.Tools()[n]
		if !ok || !tool.ReadOnly || !client.ReadOnlyToolNames[n] || resourceClassForTool(n) != resourceCollection {
			t.Errorf("%s: registered=%v readonly=%v", n, ok, tool.ReadOnly)
		}
	}
	for _, n := range []string{"dashboard_patch", "dashboard_delete"} {
		if client.ReadOnlyToolNames[n] {
			t.Errorf("%s must not be read-only", n)
		}
	}
}

// TestDashboardGetMissingIsEmpty: an unknown name is an empty dashboard with
// hash ""; a KB the caller cannot read is refused by the policy gate.
func TestDashboardGetMissingIsEmpty(t *testing.T) {
	t.Parallel()
	s, _ := dashboardServer(t)
	res := callTool(t, s, "dashboard_get", `{"name":"nothing-here"}`)
	if res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	var out struct {
		Name    string            `json:"name"`
		Hash    string            `json:"hash"`
		Widgets []json.RawMessage `json:"widgets"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "nothing-here" || out.Hash != "" || out.Widgets == nil || len(out.Widgets) != 0 {
		t.Fatalf("not empty: %s", res.Content[0].Text)
	}
	if res := callTool(t, s, "dashboard_get", `{"name":"../x"}`); !res.IsError {
		t.Fatal("a bad name was accepted")
	}
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "other"}}})
	if res := s.callTool(ctx, "dashboard_get", json.RawMessage(`{"name":"week"}`)); !res.IsError || strings.Contains(res.Content[0].Text, "private") {
		t.Fatalf("a caller with no access to the KB read: %+v", res)
	}
}

func TestDashboardListAndGet(t *testing.T) {
	t.Parallel()
	s, k := dashboardServer(t)
	writeDashboardFile(t, k, "bad", "title: [unclosed\n")
	res := callTool(t, s, "dashboard_list", `{}`)
	var list struct {
		Dashboards []dashboardListEntry `json:"dashboards"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Dashboards) != 2 || list.Dashboards[0].Name != "bad" || list.Dashboards[0].Error == "" {
		t.Fatalf("list: %s", res.Content[0].Text)
	}
	w := list.Dashboards[1]
	if w.Name != "week" || w.Title != "Week" || w.Widgets != 2 || w.Hash != kb.DashboardHash([]byte(toolTestDashboard)) {
		t.Fatalf("week: %+v", w)
	}
	var got dashboardGetResult
	res = callTool(t, s, "dashboard_get", `{"name":"week"}`)
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatal(err)
	}
	if got.Hash != w.Hash || len(got.Widgets) != 2 || got.Widgets[0].Total != 4 || got.Widgets[1].Text != "private words" {
		t.Fatalf("get: %s", res.Content[0].Text)
	}
	if res := callTool(t, s, "dashboard_get", `{"name":"bad"}`); !res.IsError {
		t.Fatal("a file that is not YAML must be an error")
	}
}

// TestDashboardGetShowsOnlyWhatTheCallerCanRead: a caller narrowed to one map
// gets neither the entries of the hidden one nor a note's free text.
func TestDashboardGetShowsOnlyWhatTheCallerCanRead(t *testing.T) {
	t.Parallel()
	s, _ := dashboardServer(t)
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	res := s.callTool(ctx, "dashboard_get", json.RawMessage(`{"name":"week"}`))
	if res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	text := res.Content[0].Text
	if strings.Contains(text, "hidden") || strings.Contains(text, "private") {
		t.Fatalf("leak: %s", text)
	}
	if !strings.Contains(text, `"total":3`) {
		t.Fatalf("visible entries missing: %s", text)
	}
}
