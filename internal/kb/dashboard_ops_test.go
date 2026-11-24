package kb

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

const exampleOps = `[{"op":"set_title","title":"Week"},{"op":"add","widget":{"id":"blocked","type":"kanban","title":"Blocked","width":2,"filter":{"status":["blocked"]},"group_by":"field:customer"}},{"op":"update","id":"blocked","widget":{"width":4,"icon":null}},{"op":"move","id":"blocked","index":0},{"op":"remove","id":"old"}]`

func mustOps(t *testing.T, s string) []DashboardOp {
	t.Helper()
	ops, err := DecodeDashboardOps([]byte(s))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return ops
}

// AC-kbdash-3
func TestApplyDashboardOpsAllOrNothing(t *testing.T) {
	t.Parallel()
	in := []byte("widgets:\n  - id: a\n    type: stat\n")
	orig := append([]byte(nil), in...)
	ops := mustOps(t, `[{"op":"set_title","title":"T"},{"op":"add","widget":{"id":"b","type":"stat"}},{"op":"remove","id":"missing"},{"op":"set_title","title":"U"}]`)
	out, err := ApplyDashboardOps(in, ops)
	if err == nil || !strings.Contains(err.Error(), "op 3") {
		t.Fatalf("want an error naming op 3, got %v", err)
	}
	if out != nil {
		t.Errorf("a failed patch returned bytes: %q", out)
	}
	if !bytes.Equal(in, orig) {
		t.Errorf("input changed: %q", in)
	}
	// the result as a whole is validated too: a duplicate id appears only at the end
	_, err = ApplyDashboardOps(in, mustOps(t, `[{"op":"add","widget":{"id":"a","type":"stat"}}]`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("want a duplicate id error, got %v", err)
	}
}

// AC-kbdash-27
func TestDashboardOpJSONRoundTrip(t *testing.T) {
	t.Parallel()
	start := []byte("widgets:\n  - id: old\n    type: note\n    text: hi\n")
	out, err := ApplyDashboardOps(start, mustOps(t, exampleOps))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDashboard(out)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Week" || len(d.Widgets) != 1 {
		t.Fatalf("unexpected result: %+v", d)
	}
	w := d.Widgets[0]
	want := DashboardWidget{ID: "blocked", Type: "kanban", Title: "Blocked", Width: 4, Filter: &DashboardFilter{Status: []string{"blocked"}}, GroupBy: "field:customer"}
	if !reflect.DeepEqual(w, want) {
		t.Errorf("widget = %+v, want %+v", w, want)
	}

	// a widget read from a file becomes an add op and reproduces itself
	file := []byte(testDashboard)
	src, err := ParseDashboard(file)
	if err != nil {
		t.Fatal(err)
	}
	var ops []DashboardOp
	for _, sw := range src.Widgets {
		op, err := NewAddOp(sw)
		if err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op)
	}
	out, err = ApplyDashboardOps(nil, ops)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseDashboard(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Widgets, src.Widgets) {
		t.Errorf("round trip differs:\n got %+v\nwant %+v", got.Widgets, src.Widgets)
	}
}

func TestDecodeDashboardOpsStrict(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"unknown key":      `[{"op":"remove","id":"a","colour":"red"}]`,
		"stray key":        `[{"op":"remove","id":"a","index":1}]`,
		"missing key":      `[{"op":"move","id":"a"}]`,
		"unknown op":       `[{"op":"explode"}]`,
		"not an array":     `{"op":"remove"}`,
		"widget not alone": `[{"op":"set_title","title":"x","widget":{}}]`,
	} {
		if _, err := DecodeDashboardOps([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// the widget of an add refuses unknown keys and the read-only error key
	for _, wj := range []string{`{"id":"a","type":"stat","colour":"red"}`, `{"id":"a","type":"stat","error":"x"}`} {
		ops := mustOps(t, `[{"op":"add","widget":`+wj+`}]`)
		if _, err := ApplyDashboardOps(nil, ops); err == nil {
			t.Errorf("widget %s accepted", wj)
		}
	}
}

func TestApplyDashboardOpsEachOp(t *testing.T) {
	t.Parallel()
	base := []byte("widgets:\n  - id: a\n    type: stat\n  - id: b\n    type: stat\n    icon: list\n")
	apply := func(ops string) Dashboard {
		t.Helper()
		out, err := ApplyDashboardOps(base, mustOps(t, ops))
		if err != nil {
			t.Fatal(err)
		}
		d, _ := ParseDashboard(out)
		return d
	}
	ids := func(d Dashboard) string {
		var s []string
		for _, w := range d.Widgets {
			s = append(s, w.ID)
		}
		return strings.Join(s, ",")
	}
	if got := ids(apply(`[{"op":"add","index":1,"widget":{"id":"c","type":"stat"}}]`)); got != "a,c,b" {
		t.Errorf("add at 1: %s", got)
	}
	if got := ids(apply(`[{"op":"move","id":"a","index":1}]`)); got != "b,a" {
		t.Errorf("move: %s", got)
	}
	if got := ids(apply(`[{"op":"remove","id":"a"}]`)); got != "b" {
		t.Errorf("remove: %s", got)
	}
	d := apply(`[{"op":"update","id":"b","widget":{"icon":null,"title":"B"}},{"op":"set_description","description":"d"}]`)
	if d.Widgets[1].Icon != "" || d.Widgets[1].Title != "B" || d.Description != "d" {
		t.Errorf("update: %+v", d)
	}
	d = apply(`[{"op":"set_title","title":"x"},{"op":"set_title","title":""}]`)
	if d.Title != "" {
		t.Errorf("empty title not expressible: %q", d.Title)
	}
	for _, bad := range []string{
		`[{"op":"add","index":5,"widget":{"id":"c","type":"stat"}}]`,
		`[{"op":"update","id":"a","widget":{"id":"z"}}]`,
		`[{"op":"move","id":"a","index":2}]`,
		`[{"op":"update","id":"a","widget":{"type":null}}]`,
	} {
		if _, err := ApplyDashboardOps(base, mustOps(t, bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
