package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func revisionOf(t *testing.T, ctx context.Context, k *kb.KB) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/revision", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	uiRevision(w, r, k)
	if w.Code != http.StatusOK {
		t.Fatalf("revision: %d %s", w.Code, w.Body.String())
	}
	return decodeUI(t, w)["revision"].(string)
}

func putPage(t *testing.T, k *kb.KB, id, body string) {
	t.Helper()
	p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\ntype: Note\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// AC-atlas-kb-map-2
func TestRevisionFollowsLinkedKBs(t *testing.T) {
	// Not parallel: it narrows the poll's trust window, a package variable.
	setRevisionMaxAge(t, 0)
	r := newCrossRig(t) // kb-a links kb-b; kb-c is unrelated
	ctx := crossCallers()["reads-a-b"]
	admin := crossCallers()["admin"]
	base := revisionOf(t, ctx, r.a)
	if !strings.Contains(base, "-x") {
		t.Fatalf("a KB linked to a KB the caller reads has no suffix: %q", base)
	}
	if again := revisionOf(t, ctx, r.a); again != base {
		t.Fatalf("revision moved with nothing changed: %q -> %q", base, again)
	}
	putPage(t, r.b, "ops/new", "new\n")
	added := revisionOf(t, ctx, r.a)
	if added == base {
		t.Error("revision did not move when a linked KB gained a page")
	}
	if err := os.Remove(filepath.Join(r.b.DataRoot(), "ops", "new.md")); err != nil {
		t.Fatal(err)
	}
	if removed := revisionOf(t, ctx, r.a); removed == added {
		t.Error("revision did not move when a linked KB lost a page")
	}
	// An unrelated KB moves nothing.
	before := revisionOf(t, admin, r.a)
	putPage(t, r.c, "z", "z\n")
	if after := revisionOf(t, admin, r.a); after != before {
		t.Errorf("an unrelated KB moved the revision: %q -> %q", before, after)
	}
	// A KB that gains a link to A is now a linked KB.
	putPage(t, r.c, "z", "[[kb:kb-a/p]]\n")
	linked := revisionOf(t, admin, r.a)
	if linked == before {
		t.Error("revision did not move when a KB gained a link to this one")
	}
	putPage(t, r.c, "z2", "more\n")
	if revisionOf(t, admin, r.a) == linked {
		t.Error("revision did not follow a KB that links to this one")
	}
}

// AC-atlas-kb-map-3
func TestRevisionBlindToUnreadableKB(t *testing.T) {
	setRevisionMaxAge(t, 0)
	r := newCrossRig(t)
	for _, who := range []string{"a-only", "b-scoped"} {
		ctx := crossCallers()[who]
		before := revisionOf(t, ctx, r.a)
		if strings.Contains(before, "-x") {
			t.Errorf("%s: suffix for a KB it cannot read whole: %q", who, before)
		}
		putPage(t, r.b, "ops/"+who, "x\n")
		putPage(t, r.b, "back-"+who, "[[kb:kb-a/p]]\n")
		if after := revisionOf(t, ctx, r.a); after != before {
			t.Errorf("%s: revision moved for an unreadable KB: %q -> %q", who, before, after)
		}
	}
	// A KB with no cross link has today's token.
	if tok := revisionOf(t, crossCallers()["admin"], r.c); strings.Contains(tok, "-x") || !strings.HasPrefix(tok, uiRevisionBoot+"-") {
		t.Errorf("KB with no cross link: %q", tok)
	}
}

// AC-atlas-kb-map-3: the revision of A never reads the view of a KB the caller
// cannot read whole, so a poll cannot time that KB's revalidation.
func TestRevisionDoesNotReadUnreadableKBView(t *testing.T) {
	setRevisionMaxAge(t, 0)
	r := newCrossRig(t)
	putPage(t, r.b, "back", "[[kb:kb-a/p]]\n")
	putPage(t, r.c, "back", "[[kb:kb-a/p]]\n")
	ctx := crossCallers()["a-only"]
	revisionOf(t, ctx, r.a) // warm
	b, c := r.b.GraphValidations(), r.c.GraphValidations()
	for i := 0; i < 3; i++ {
		revisionOf(t, ctx, r.a)
	}
	if n := r.b.GraphValidations(); n != b {
		t.Errorf("kb-b, unreadable, was validated %d times", n-b)
	}
	if n := r.c.GraphValidations(); n != c {
		t.Errorf("kb-c, unreadable, was validated %d times", n-c)
	}
}
