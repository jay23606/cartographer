package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// dashboardWidgetDoc is the widget field list both read tools carry, so an
// agent that has only seen one of them can still write a dashboard.
const dashboardWidgetDoc = "Widget fields: id (slug), type (kanban|list|stat|agenda|note), title, icon, accent, width 1-4, " +
	"source (work, default | concepts), filter {status, map, type, id_prefix, stale, has_items, fields {key: value}, text}, " +
	"group_by (status|map|type|field:<key>), columns (kanban), sort (age|title|status|field:<key>), limit, " +
	"text (note), date_field and horizon_days (agenda), show (stat: count|groups)."

// dashboardListEntry is one dashboard of dashboard_list.
type dashboardListEntry struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Widgets int    `json:"widgets"`
	Hash    string `json:"hash"`
	Error   string `json:"error,omitempty"`
}

// dashboardGetResult is the resolved dashboard plus its name and the hash of
// its file ("" when it does not exist yet).
type dashboardGetResult struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
	resolvedDashboard
}

// toolDashboardList names the dashboards of the KB (dashboards/<name>.yaml).
// It reads files, not concepts, so nothing in it depends on what the caller
// may read of the KB's pages.
func toolDashboardList(k *kb.KB) Tool {
	return Tool{
		Name:        "dashboard_list",
		ReadOnly:    true,
		Description: "Lists the KB's dashboards: name, title, widget count and file hash. A dashboard is a file of widgets that hold queries over the KB; dashboard_get resolves one. " + dashboardWidgetDoc,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			names, err := k.ListDashboards()
			if err != nil {
				return errorResult("dashboard_list: " + relPathErr(err, k.Root)), nil
			}
			out := make([]dashboardListEntry, 0, len(names))
			for _, n := range names {
				e := dashboardListEntry{Name: n}
				data, err := k.ReadDashboard(n)
				if err != nil {
					e.Error = relPathErr(err, k.Root)
					out = append(out, e)
					continue
				}
				e.Hash = kb.DashboardHash(data)
				if d, perr := kb.ParseDashboard(data); perr != nil {
					e.Error = perr.Error()
				} else {
					e.Title, e.Widgets = d.Title, len(d.Widgets)
				}
				out = append(out, e)
			}
			return jsonResult(map[string]any{"dashboards": out}), nil
		},
	}
}

// toolDashboardGet returns one dashboard with its widgets resolved for the
// caller. A name with no file answers an empty dashboard with hash "", the
// starting point of a first patch.
func toolDashboardGet(k *kb.KB, cc *conformanceCache) Tool {
	return Tool{
		Name:        "dashboard_get",
		ReadOnly:    true,
		Description: "Returns dashboard <name> resolved for you: each widget with its entries (or groups) and total, or its own error; a name with no file is an empty dashboard with hash \"\". Entries are only what you may read. See dashboard_list. " + dashboardWidgetDoc,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if !kb.ValidDashboardName(p.Name) {
				return errorResult(fmt.Sprintf("dashboard_get: invalid name %q: want a lowercase slug of at most 48 characters", p.Name)), nil
			}
			data, err := k.ReadDashboard(p.Name)
			switch {
			case errors.Is(err, os.ErrNotExist):
				return jsonResult(dashboardGetResult{Name: p.Name, resolvedDashboard: resolvedDashboard{Widgets: []resolvedWidget{}}}), nil
			case err != nil:
				return errorResult("dashboard_get: " + relPathErr(err, k.Root)), nil
			}
			d, err := kb.ParseDashboard(data)
			if err != nil {
				return errorResult("dashboard_get: " + p.Name + ": " + err.Error()), nil
			}
			if !WholeVisible(ctx, k, false) {
				// A note is free text of the whole KB, not of a collection:
				// a caller narrowed to some maps does not read it.
				for i := range d.Widgets {
					d.Widgets[i].Text = ""
				}
			}
			res := dashboardGetResult{Name: p.Name, Hash: kb.DashboardHash(data), resolvedDashboard: resolveDashboard(ctx, k, cc, d)}
			return jsonResult(res), nil
		},
	}
}
