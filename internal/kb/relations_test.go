package kb

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const relSettings = "depends_on:\n  - {name: core-kb, remote: git.example.com/org/core}\n"

func commitSettingsHere(t *testing.T, k *KB, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(k.Root, SettingsFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHere(t, k.Root, "add", SettingsFile)
	gitHere(t, k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "deps")
}

// AC-kbrel-3
func TestRelationsAtDefaultBranchOnly(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	commitRawSettings(t, bare, "main", relSettings)
	commitRawSettings(t, bare, "work", "depends_on:\n  - {name: other-kb, remote: git.example.com/org/other}\n")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	// An uncommitted working copy value changes nothing either.
	if err := os.WriteFile(filepath.Join(k.Root, SettingsFile), []byte("depends_on:\n  - {name: local-kb, remote: git.example.com/org/local}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []Relation{{Name: "core-kb", Remote: "git.example.com/org/core"}}
	got, err := k.RelationsAt("origin/main")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("RelationsAt(origin/main) = %v, %v", got, err)
	}
	got, err = k.Relations()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Relations = %v, %v (the default branch is origin/%s)", got, err, k.RemoteDefaultBranch())
	}
}

func TestRelationsNoRemoteReadsHEAD(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := newTestKB(t)
	if got, err := k.Relations(); err != nil || len(got) != 0 {
		t.Fatalf("no settings.yaml: %v, %v", got, err)
	}
	commitSettingsHere(t, k, relSettings)
	k.forgetRelationsValidation()
	got, err := k.Relations()
	if err != nil || len(got) != 1 || got[0].Name != "core-kb" {
		t.Fatalf("Relations = %v, %v", got, err)
	}
}

// AC-kbrel-4
func TestRelationsAtGitFailureIsAnError(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := newTestKB(t)
	if got, err := k.RelationsAt("no-such-ref"); err == nil {
		t.Fatalf("an unknown ref was read as none: %v", got)
	}
	if err := os.Rename(filepath.Join(k.Root, ".git"), filepath.Join(k.Root, ".git-away")); err != nil {
		t.Fatal(err)
	}
	if got, err := k.Relations(); err == nil {
		t.Fatalf("a broken repository was read as none: %v", got)
	}
}

// The tests below set the package variable relationsMaxAge, so they do not
// run in parallel.
func TestRelationsMemoFollowsTheRef(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	old := relationsMaxAge
	relationsMaxAge = 0 // revalidate on every call
	t.Cleanup(func() { relationsMaxAge = old })
	k := newTestKB(t)
	if got, _ := k.Relations(); len(got) != 0 {
		t.Fatalf("Relations = %v", got)
	}
	commitSettingsHere(t, k, relSettings)
	if got, err := k.Relations(); err != nil || len(got) != 1 {
		t.Fatalf("after the ref moved: %v, %v", got, err)
	}
}

func TestRelationsGitReadsBounded(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	old := relationsMaxAge
	relationsMaxAge = time.Hour
	t.Cleanup(func() { relationsMaxAge = old })
	k := newTestKB(t)
	if _, err := k.Relations(); err != nil {
		t.Fatal(err)
	}
	k.NormalizedOrigin()
	base := relationsGitReads.Load()
	for i := 0; i < 1000; i++ {
		if _, err := k.Relations(); err != nil {
			t.Fatal(err)
		}
		k.NormalizedOrigin()
	}
	if n := relationsGitReads.Load() - base; n != 0 {
		t.Fatalf("%d git reads within relationsMaxAge, want 0", n)
	}
	// A write through the server forgets the validation.
	if err := k.WithGitLock(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Relations(); err != nil {
		t.Fatal(err)
	}
	if relationsGitReads.Load() == base {
		t.Fatal("a write did not make the next read look at git")
	}
}

func TestNormalizedOrigin(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := newTestKB(t)
	if o, ok := k.NormalizedOrigin(); ok {
		t.Fatalf("a KB with no remote has origin %q", o)
	}
	gitHere(t, k.Root, "remote", "add", "origin", "https://user:secret@Git.Example.com/org/core.git")
	k.forgetRelationsValidation()
	if o, ok := k.NormalizedOrigin(); !ok || o != "git.example.com/org/core" {
		t.Fatalf("origin = %q, %v", o, ok)
	}
}
