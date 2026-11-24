package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func relGit(t *testing.T, k *kb.KB, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// relFixture mounts the KBs of names, each with origin git.example.com/org/<name>
// and, for a name in deps, the dependencies it declares (name -> remote repo).
type relFixture struct {
	byName map[string]*kb.KB
	srv    map[string]*Server
}

func newRelFixture(t *testing.T, names []string, deps map[string][][2]string) *relFixture {
	t.Helper()
	f := &relFixture{byName: map[string]*kb.KB{}, srv: map[string]*Server{}}
	for _, n := range names {
		k := kbtest.New(t)
		t.Cleanup(func() { waitReadRefresh(k) })
		k.AuthName = n
		relGit(t, k, "remote", "add", "origin", "https://git.example.com/org/"+n+".git")
		if d := deps[n]; len(d) > 0 {
			var b strings.Builder
			b.WriteString("depends_on:\n")
			for _, x := range d {
				b.WriteString("  - {name: " + x[0] + ", remote: git.example.com/org/" + x[1] + "}\n")
			}
			if err := os.WriteFile(filepath.Join(k.Root, kb.SettingsFile), []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			relGit(t, k, "add", kb.SettingsFile)
			relGit(t, k, "commit", "-q", "-m", "deps")
		}
		// What the first sync leaves behind: the remote's default branch is
		// known (Relations errors on a remote without one).
		relGit(t, k, "update-ref", "refs/remotes/origin/main", "HEAD")
		relGit(t, k, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
		f.byName[n] = k
	}
	m := kb.NewMounts(f.byName, names, nil)
	for _, n := range names {
		f.byName[n].SetMounts(m)
		s := New("test")
		RegisterKBTools(s, f.byName[n], Deps{BundleFS: fstest.MapFS{}})
		f.srv[n] = s
	}
	return f
}

// pullRelations returns the raw `relations` bytes of name's sync_pull.
func (f *relFixture) pullRelations(t *testing.T, ctx context.Context, name string) (json.RawMessage, string, bool) {
	t.Helper()
	text, isErr := callJSON(t, f.srv[name], ctx, "sync_pull", `{}`)
	if isErr {
		return nil, text, true
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out["relations"], text, false
}

func readsOnly(names ...string) context.Context {
	var perms []auth.Permission
	for _, n := range names {
		perms = append(perms, auth.Permission{KB: n})
	}
	return auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "u", Policy: auth.Policy{Permissions: perms}})
}

// AC-kbrel-12
func TestSyncPullRelations(t *testing.T) {
	t.Parallel()
	f := newRelFixture(t, []string{"client-kb-1", "core-kb", "base-kb", "lone-kb"}, map[string][][2]string{
		"client-kb-1": {{"core-kb", "core-kb"}, {"other-kb", "other-kb"}},
		"core-kb":     {{"base-kb", "base-kb"}},
	})
	raw, text, isErr := f.pullRelations(t, adminCtx, "client-kb-1")
	if isErr {
		t.Fatal(text)
	}
	var got contract.Relations
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := contract.Relations{
		Declared:   []contract.Relation{{Name: "core-kb", Remote: "git.example.com/org/core-kb"}, {Name: "other-kb", Remote: "git.example.com/org/other-kb"}},
		Resolved:   []string{"core-kb"},
		Closure:    []string{"core-kb", "base-kb"},
		Unresolved: []contract.RelationUnresolved{{Name: "other-kb", Remote: "git.example.com/org/other-kb", Reason: "not_mounted"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("relations = %+v, want %+v", got, want)
	}

	// No declaration: the key is present with four empty arrays, never null.
	raw, text, isErr = f.pullRelations(t, adminCtx, "lone-kb")
	if isErr {
		t.Fatal(text)
	}
	if string(raw) != `{"declared":[],"resolved":[],"closure":[],"unresolved":[]}` {
		t.Errorf("relations of a KB with none = %s", raw)
	}
}

// AC-kbrel-13
func TestSyncPullRelationsUnreadableEqualsUnmounted(t *testing.T) {
	t.Parallel()
	deps := map[string][][2]string{"client-kb-1": {{"core-kb", "core-kb"}}}
	withCore := newRelFixture(t, []string{"client-kb-1", "core-kb"}, deps)
	without := newRelFixture(t, []string{"client-kb-1"}, deps)

	hidden, text, isErr := withCore.pullRelations(t, readsOnly("client-kb-1"), "client-kb-1")
	if isErr {
		t.Fatal(text)
	}
	absent, text, isErr := without.pullRelations(t, readsOnly("client-kb-1"), "client-kb-1")
	if isErr {
		t.Fatal(text)
	}
	if string(hidden) != string(absent) {
		t.Errorf("unreadable target = %s, unmounted = %s", hidden, absent)
	}
	if !strings.Contains(string(hidden), `"reason":"not_mounted"`) {
		t.Errorf("want not_mounted: %s", hidden)
	}
}

// AC-kbrel-14
func TestSyncPullRelationsRemoteMismatch(t *testing.T) {
	t.Parallel()
	f := newRelFixture(t, []string{"client-kb-1", "core-kb"}, map[string][][2]string{
		"client-kb-1": {{"core-kb", "elsewhere-kb"}},
	})
	seen, text, isErr := f.pullRelations(t, readsOnly("client-kb-1", "core-kb"), "client-kb-1")
	if isErr {
		t.Fatal(text)
	}
	if !strings.Contains(string(seen), `"reason":"remote_mismatch"`) {
		t.Errorf("reader of the target: %s", seen)
	}
	blind, text, isErr := f.pullRelations(t, readsOnly("client-kb-1"), "client-kb-1")
	if isErr {
		t.Fatal(text)
	}
	if !strings.Contains(string(blind), `"reason":"not_mounted"`) || strings.Contains(string(blind), "remote_mismatch") {
		t.Errorf("non-reader of the target: %s", blind)
	}
}

// AC-kbrel-15
func TestSyncPullRelationsErrorIsAnError(t *testing.T) {
	t.Parallel()
	f := newRelFixture(t, []string{"client-kb-1", "core-kb"}, map[string][][2]string{
		"client-kb-1": {{"core-kb", "core-kb"}},
	})
	// git cannot answer for an entered node: its relations cannot be read. Its
	// origin is memoized first, so it is still a candidate when the read fails.
	f.byName["core-kb"].NormalizedOrigin()
	if err := os.RemoveAll(filepath.Join(f.byName["core-kb"].Root, ".git")); err != nil {
		t.Fatal(err)
	}
	_, text, isErr := f.pullRelations(t, adminCtx, "client-kb-1")
	if !isErr {
		t.Fatalf("want an error result, got a manifest: %.200s", text)
	}
	if !strings.Contains(text, "relations unavailable") {
		t.Errorf("error = %s", text)
	}
}
