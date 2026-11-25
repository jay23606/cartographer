package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

func stampHas(fs []lint.Finding, path, check string) bool {
	for _, f := range fs {
		if f.Path == path && f.Check == check {
			return true
		}
	}
	return false
}

// putThroughServer writes (or, with body "", removes) a page the way a write
// through the server does: under the git lock, which forgets the last
// validation of the graph.
func putThroughServer(t *testing.T, k *kb.KB, id, body string) {
	t.Helper()
	p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
	err := k.WithGitLock(func() error {
		if body == "" {
			return os.Remove(p)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte("---\ntype: Note\n---\n"+body), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// AC-cross-kb-links-16
func TestCrossStampThroughTheCache(t *testing.T) {
	t.Parallel()
	a, b, plain := kbtest.New(t), kbtest.New(t), kbtest.New(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	putThroughServer(t, a, "p", "See [[kb:kb-b/ops/x]].\n")
	putThroughServer(t, a, "t", "target\n")
	cc := &conformanceCache{}
	lintOf := func() []lint.Finding {
		t.Helper()
		fs, err := cc.lintFindings(a)
		if err != nil {
			t.Fatal(err)
		}
		return fs
	}
	if !stampHas(lintOf(), "p.md", "broken_cross_kb_link") {
		t.Fatal("a missing target is a finding")
	}
	calls := cc.LintCalls()
	lintOf()
	if cc.LintCalls() != calls {
		t.Fatal("nothing changed: the cached lint must be reused")
	}
	// B gains the target page, with no change in A.
	putThroughServer(t, b, "ops/x", "x\n")
	if got := lintOf(); stampHas(got, "p.md", "broken_cross_kb_link") || cc.LintCalls() == calls {
		t.Errorf("B added the target: the finding must go (recomputed %v)", cc.LintCalls() != calls)
	}
	// And loses it.
	calls = cc.LintCalls()
	putThroughServer(t, b, "ops/x", "")
	if !stampHas(lintOf(), "p.md", "broken_cross_kb_link") || cc.LintCalls() == calls {
		t.Error("B removed the target: the finding must come back")
	}
	// B adds, then removes, a link to A's page t: its orphan goes and returns.
	if !stampHas(lintOf(), "t.md", "orphan") {
		t.Fatal("t is an orphan before B links it")
	}
	putThroughServer(t, b, "from", "[[kb:kb-a/t]]\n")
	if stampHas(lintOf(), "t.md", "orphan") {
		t.Error("B links t: its orphan must go with no change in A")
	}
	putThroughServer(t, b, "from", "")
	if !stampHas(lintOf(), "t.md", "orphan") {
		t.Error("B no longer links t: its orphan must return")
	}
	// A KB with no cross link keeps a stamp with no cross part.
	if s := crossStamp(plain); s != "" {
		t.Errorf("a KB with no cross link has stamp %q", s)
	}
	if lintInputsStamp(plain) != lintInputsStamp(plain) {
		t.Error("unstable stamp")
	}
}

// AC-cross-kb-links-16
func TestCrossStampIsCheap(t *testing.T) {
	// Not parallel: the build counter is global.
	a, b := kbtest.New(t), kbtest.New(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	putThroughServer(t, a, "p", "[[kb:kb-b/ops/x]]\n")
	putThroughServer(t, b, "q", "[[kb:kb-a/p]]\n")
	_ = lintInputsStamp(a)
	before := kb.CrossDigestBuilds()
	for i := 0; i < 1000; i++ {
		_ = lintInputsStamp(a)
	}
	if got := kb.CrossDigestBuilds() - before; got != 0 {
		t.Errorf("1000 stamps with nothing changed rebuilt the inbound digest %d times", got)
	}
}
