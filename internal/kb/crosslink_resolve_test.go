package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// AC-cross-kb-links-5
func TestCrossTarget(t *testing.T) {
	a, b := mountedPair(t, map[string]string{"old-b": "kb-b"})
	writeConcept(t, b, "ops/x", "x\n")
	for _, c := range []struct {
		l    CrossLink
		want CrossTarget
	}{
		{CrossLink{KB: "kb-b", ID: "ops/x"}, CrossTarget{Mounted: true, Exists: true}},
		{CrossLink{KB: "old-b", ID: "ops/x"}, CrossTarget{Mounted: true, Exists: true}},
		{CrossLink{KB: "kb-b", ID: "ops/gone"}, CrossTarget{Mounted: true}},
		{CrossLink{KB: "nope", ID: "ops/x"}, CrossTarget{}},
		{CrossLink{Server: "host", KB: "kb-b", ID: "ops/x"}, CrossTarget{}},
		{CrossLink{KB: "kb-b", ID: "ops/x", Malformed: true}, CrossTarget{}},
	} {
		if got := a.CrossTarget(c.l); got != c.want {
			t.Errorf("%+v: got %+v, want %+v", c.l, got, c.want)
		}
	}
	if got := mustInitKB(t).CrossTarget(CrossLink{KB: "kb-b", ID: "ops/x"}); got.Mounted {
		t.Errorf("nil mounts must resolve nowhere: %+v", got)
	}
}

// AC-cross-kb-links-5
func TestCrossTargetTakesNoGitLock(t *testing.T) {
	a, b := mountedPair(t, nil)
	writeConcept(t, b, "x", "x\n")
	held, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- b.WithGitLock(func() error { close(held); <-release; return nil })
	}()
	<-held
	res := make(chan CrossTarget, 1)
	go func() { res <- a.CrossTarget(CrossLink{KB: "kb-b", ID: "x"}) }()
	select {
	case got := <-res:
		if !got.Exists {
			t.Errorf("got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Error("CrossTarget waited for the other KB's git lock")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// AC-cross-kb-links-5
func TestCrossIncomingSeesDiskChanges(t *testing.T) {
	setCrossMaxAge(t, 0)
	a, b := mountedPair(t, map[string]string{"old-a": "kb-a"})
	if got := a.CrossIncoming("x"); len(got) != 0 {
		t.Fatalf("no link yet: %v", got)
	}
	writeConcept(t, b, "p1", "[[kb:kb-a/x]]\n")
	writeConcept(t, b, "p2", "[[kb:old-a/x]] [[kb:kb-a/y]]\n")
	writeConcept(t, a, "q", "[[kb:kb-a/x]]\n") // a link of a KB to itself is no inbound edge
	want := []CrossRef{{KB: "kb-b", ID: "p1"}, {KB: "kb-b", ID: "p2"}}
	if got := a.CrossIncoming("x"); !reflect.DeepEqual(got, want) {
		t.Errorf("CrossIncoming = %v, want %v", got, want)
	}
	if err := os.Remove(filepath.Join(b.DataRoot(), "p1.md")); err != nil {
		t.Fatal(err)
	}
	if got := a.CrossIncoming("x"); !reflect.DeepEqual(got, want[1:]) {
		t.Errorf("after a deletion on disk: %v", got)
	}
}

func setCrossMaxAge(t *testing.T, d time.Duration) {
	t.Helper()
	old := crossMaxAge
	crossMaxAge = d
	t.Cleanup(func() { crossMaxAge = old })
}

// AC-cross-kb-links-7
func TestCrossInboundDigest(t *testing.T) {
	setCrossMaxAge(t, 0)
	a, b := mountedPair(t, nil)
	if d := a.CrossInboundDigest(); d != "" {
		t.Errorf("no inbound link: digest = %q", d)
	}
	writeConcept(t, b, "p", "[[kb:kb-a/x]]\n")
	d1 := a.CrossInboundDigest()
	if d1 == "" {
		t.Fatal("a link from kb-b must change the digest")
	}
	writeConcept(t, b, "other", "unrelated\n")
	if d := a.CrossInboundDigest(); d != d1 {
		t.Error("an unrelated page changed the digest")
	}
	writeConcept(t, b, "p", "no more\n")
	if d := a.CrossInboundDigest(); d != "" {
		t.Errorf("the link is gone: digest = %q", d)
	}
}

// AC-cross-kb-links-7
func TestCrossInboundDigestIsMemoized(t *testing.T) {
	setCrossMaxAge(t, time.Hour)
	names, byName := []string{"kb-0"}, map[string]*KB{}
	for i := 1; i < 30; i++ {
		names = append(names, fmt.Sprintf("kb-%d", i))
	}
	for _, n := range names {
		byName[n] = mustInitKB(t)
	}
	m := NewMounts(byName, names, nil)
	for _, k := range byName {
		k.SetMounts(m)
	}
	writeConcept(t, byName["kb-1"], "p", "[[kb:kb-0/x]]\n")
	a := byName["kb-0"]
	first := a.CrossInboundDigest()
	builds := crossDigestBuilds.Load()
	rebuilds := map[string]int{}
	for _, n := range names {
		rebuilds[n] = byName[n].graphStatsValidations()
	}
	for i := 0; i < 1000; i++ {
		if a.CrossInboundDigest() != first {
			t.Fatal("digest moved with nothing changed")
		}
	}
	if n := crossDigestBuilds.Load() - builds; n != 0 {
		t.Errorf("%d recomputes with nothing changed", n)
	}
	for _, n := range names {
		if got := byName[n].graphStatsValidations(); got != rebuilds[n] {
			t.Errorf("%s: %d graph validations on a warm digest", n, got-rebuilds[n])
		}
	}
}

func (kb *KB) graphStatsValidations() int {
	kb.graphMu.Lock()
	defer kb.graphMu.Unlock()
	if kb.graph == nil {
		return 0
	}
	return kb.graph.stats.validations
}

// Resolution beside two-KB git locking in both orders, as concept_transfer
// takes them, must neither deadlock nor race (run with -race).
func TestCrossResolveBesideTransferLocking(t *testing.T) {
	a, b := mountedPair(t, nil)
	writeConcept(t, a, "p", "[[kb:kb-b/x]]\n")
	writeConcept(t, b, "x", "[[kb:kb-a/p]]\n")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = a.WithGitLock(func() error {
					a.CrossTarget(CrossLink{KB: "kb-b", ID: "x"})
					return b.WithGitLock(func() error { return nil })
				})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				b.CrossIncoming("x")
				b.CrossInboundDigest()
				b.CrossTarget(CrossLink{KB: "kb-a", ID: "p"})
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock")
	}
}
