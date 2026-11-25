package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

// gateRig is a git KB kb-a (maps arch and ops) mounted with kb-b, which holds
// ops/x and is readable whole or not by the caller.
func gateRig(t *testing.T, mountB bool) *kb.KB {
	t.Helper()
	a, b := gateKB(t), kbtest.New(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	writeBPage := filepath.Join(b.DataRoot(), "ops", "x.md")
	if err := os.MkdirAll(filepath.Dir(writeBPage), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writeBPage, []byte("---\ntype: Note\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	byName, order := map[string]*kb.KB{"kb-a": a}, []string{"kb-a"}
	if mountB {
		byName["kb-b"], order = b, append(order, "kb-b")
	}
	m := kb.NewMounts(byName, order, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	return a
}

// gateVerdict writes arch/p with body and judges it as ctx would.
func gateVerdict(t *testing.T, k *kb.KB, who string, body string) string {
	t.Helper()
	p := filepath.Join(k.DataRoot(), "arch", "p.md")
	if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: P\nstatus: active\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(p) })
	res := judgeWrite(k, "concept_write", newCallerView(crossCallers()[who], k))
	if res == nil {
		return ""
	}
	return res.Content[0].Text
}

// AC-cross-kb-links-12
func TestWriteGateCrossKB(t *testing.T) {
	missing := "See [[kb:kb-b/ops/gone]].\n"
	for who, want := range map[string]bool{"reads-a-b": true, "admin": true, "a-only": false, "b-scoped": false} {
		k := gateRig(t, true)
		if got := strings.Contains(gateVerdict(t, k, who, missing), "broken_cross_kb_link"); got != want {
			t.Errorf("%s: refused for the missing target = %v, want %v", who, got, want)
		}
	}
	// A page that is there is no finding; a KB that is not mounted is none either.
	if got := gateVerdict(t, gateRig(t, true), "reads-a-b", "See [[kb:kb-b/ops/x]].\n"); strings.Contains(got, "broken_cross_kb_link") {
		t.Errorf("a present target was refused: %s", got)
	}
	if got := gateVerdict(t, gateRig(t, false), "admin", missing); strings.Contains(got, "broken_cross_kb_link") {
		t.Errorf("an unmounted KB was refused: %s", got)
	}
	// AC-cross-kb-links-13: the self-named link is refused for every caller who sees the page.
	for _, who := range []string{"reads-a-b", "a-only", "admin"} {
		got := gateVerdict(t, gateRig(t, true), who, "See [[kb:kb-a/ops/x]].\n")
		if !strings.Contains(got, "cross-KB link names this KB: write [[ops/x]]") {
			t.Errorf("%s: the self-named link was not refused: %q", who, got)
		}
	}
}

// leakWorld is kb-a with a page linking kb-b/ops/x, in one of three worlds
// the caller cannot tell apart: kb-b not mounted, kb-b mounted with the page,
// kb-b mounted without it.
func leakWorld(t *testing.T, world string) *kb.KB {
	t.Helper()
	a, b := kbtest.New(t), kbtest.New(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	put := func(k *kb.KB, id, body string) {
		p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\ntype: Note\n---\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(a, "p", "See [[kb:kb-b/ops/x]].\n")
	byName, order := map[string]*kb.KB{"kb-a": a}, []string{"kb-a"}
	if world != "absent" {
		byName["kb-b"], order = b, append(order, "kb-b")
	}
	if world == "present" {
		put(b, "ops/x", "x\n")
	}
	m := kb.NewMounts(byName, order, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	return a
}

// AC-cross-kb-links-14: what a caller who cannot read kb-b is told is byte for
// byte the same whether kb-b is absent, present with the page, or present
// without it (leak surfaces 1, 2, 4: lint with its totals, gate_check, the
// conformance counts).
func TestCrossKBLeakSurfaces(t *testing.T) {
	hidden := crossCallers()["a-only"]
	call := func(ctx context.Context, tool Tool) string {
		t.Helper()
		res, err := tool.Handler(ctx, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return res.Content[0].Text
	}
	surfaces := map[string]func(k *kb.KB) string{
		"lint":       func(k *kb.KB) string { return call(hidden, toolLint(k)) },
		"gate_check": func(k *kb.KB) string { return call(hidden, toolGateCheck(k)) },
		"ui_findings": func(k *kb.KB) string {
			cc := &conformanceCache{}
			fs, err := cc.lintFindings(k)
			if err != nil {
				t.Fatal(err)
			}
			vis, err := uiVisibleFindingsFrom(hidden, k, "", fs)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(vis)
			return string(b)
		},
	}
	for name, surface := range surfaces {
		want := surface(leakWorld(t, "absent"))
		for _, world := range []string{"present", "missing"} {
			if got := surface(leakWorld(t, world)); got != want {
				t.Errorf("%s: world %q differs from the absent KB:\n%s\n%s", name, world, got, want)
			}
		}
	}
	// A caller who reads kb-b whole does learn the target is missing.
	reader := crossCallers()["reads-a-b"]
	if got := call(reader, toolLint(leakWorld(t, "missing"))); !strings.Contains(got, "broken_cross_kb_link") {
		t.Errorf("a reader of kb-b is not told: %s", got)
	}
}
