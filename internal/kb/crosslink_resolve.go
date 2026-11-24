package kb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// crossMaxAge is how old a validation of another KB's graph may be when a
// cross-KB read trusts it: the Atlas poller's value (a write through the
// server forgets it, an out-of-band edit is seen within it). A variable so a
// test can shorten it.
var crossMaxAge = 3 * time.Second

// crossDigestBuilds counts CrossInboundDigest recomputes, for tests.
var crossDigestBuilds atomic.Int64

// CrossTarget is what is true of a link's target, whoever asks: it says
// nothing about what a caller may see. Err is set when the target KB's graph
// could not be validated: the answer is then unknown, neither Exists nor not.
type CrossTarget struct {
	Mounted bool
	Exists  bool
	Err     error
}

// HasConcepts returns a test of which concepts exist in the KB, from its
// graph snapshot validated within maxAge. It takes the graph cache mutex only,
// a leaf: never a git lock, so it is safe under another KB's git lock
// (internal/mcpserver/AGENTS.md).
func (kb *KB) HasConcepts(maxAge time.Duration) (func(okf.ConceptID) bool, error) {
	kb.graphMu.Lock()
	defer kb.graphMu.Unlock()
	v := (*graphView)(nil)
	if c := kb.graph; c != nil && c.view != nil && !c.stale && time.Since(c.validatedAt) < maxAge {
		v = c.view
	} else {
		var err error
		if v, err = kb.graphViewLocked(nil); err != nil {
			return nil, err
		}
	}
	exists := v.exists
	return func(id okf.ConceptID) bool { _, ok := exists[id]; return ok }, nil
}

// CrossTarget resolves one link against the mounts.
func (kb *KB) CrossTarget(l CrossLink) CrossTarget { return kb.CrossTargets([]CrossLink{l})[0] }

// CrossTargets resolves links in order, reading each distinct target KB once.
// A server-qualified or malformed link, no mounts, or a name that is not
// mounted (an alias is followed) is not Mounted. This KB's own graph is never
// held while another's is read.
func (kb *KB) CrossTargets(links []CrossLink) []CrossTarget {
	out := make([]CrossTarget, len(links))
	m := kb.Mounts()
	has := map[string]func(okf.ConceptID) bool{}
	errs := map[string]error{}
	for i, l := range links {
		if m == nil || l.Server != "" || l.Malformed {
			continue
		}
		target, ok := m.Lookup(l.KB)
		if !ok {
			continue
		}
		out[i].Mounted = true
		name := m.Canonical(l.KB)
		if _, done := has[name]; !done && errs[name] == nil {
			has[name], errs[name] = target.HasConcepts(crossMaxAge)
		}
		if errs[name] != nil {
			out[i].Err = errs[name]
		} else {
			out[i].Exists = has[name](l.ID)
		}
	}
	return out
}

func indexCrossByTarget(cross map[okf.ConceptID][]CrossLink) map[string]map[okf.ConceptID][]okf.ConceptID {
	idx := map[string]map[okf.ConceptID][]okf.ConceptID{}
	for src, ls := range cross {
		for _, l := range ls {
			if l.Malformed || l.Server != "" {
				continue
			}
			if idx[l.KB] == nil {
				idx[l.KB] = map[okf.ConceptID][]okf.ConceptID{}
			}
			idx[l.KB][l.ID] = append(idx[l.KB][l.ID], src)
		}
	}
	return idx
}

// CrossRef is a page of another KB that links here.
type CrossRef struct {
	KB string
	ID okf.ConceptID
}

// inbound is one link of another mounted KB toward this one.
type inbound struct {
	kb          string
	target, src okf.ConceptID
}

// inbounds lists, sorted, the links of the other mounted KBs that name this
// KB (by its name or an alias) and, when target is non-empty, that id only.
// Each KB's view is read in turn, never with another's mutex held.
func (kb *KB) inbounds(target okf.ConceptID) []inbound {
	m, self := kb.Mounts(), kb.MountName()
	if m == nil || self == "" {
		return nil
	}
	names := m.NamesFor(self)
	var out []inbound
	for _, x := range m.Names() {
		other, ok := m.Lookup(x)
		if !ok || other == kb {
			continue
		}
		v, err := other.graphView()
		if err != nil {
			continue
		}
		for _, n := range names {
			for tid, srcs := range v.crossByTarget[n] {
				if target != "" && tid != target {
					continue
				}
				for _, s := range srcs {
					out = append(out, inbound{x, tid, s})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.kb != b.kb {
			return a.kb < b.kb
		}
		if a.target != b.target {
			return a.target < b.target
		}
		return a.src < b.src
	})
	return out
}

// CrossIncoming lists the pages of the other mounted KBs whose links name id
// of this KB, by this KB's name or one of its aliases.
func (kb *KB) CrossIncoming(id okf.ConceptID) []CrossRef {
	var out []CrossRef
	for _, in := range kb.inbounds(id) {
		out = append(out, CrossRef{KB: in.kb, ID: in.src})
	}
	return out
}

// crossDigestMemo remembers the digest for the vector of the other KBs'
// graph generations it was computed on.
type crossDigestMemo struct {
	mu     sync.Mutex
	gens   map[string]uint64
	digest string
	ok     bool
}

// CrossInboundDigest is a sha of the links the other mounted KBs hold toward
// this one, empty when there are none: a link from another KB appearing or
// disappearing changes it. It sits in the lint-cache key, so it is memoized on
// the other KBs' graph generations, each read within crossMaxAge: while none
// moved it costs a clock read per KB, no git call and no walk.
func (kb *KB) CrossInboundDigest() string {
	m, self := kb.Mounts(), kb.MountName()
	if m == nil || self == "" {
		return ""
	}
	gens := map[string]uint64{}
	for _, x := range m.Names() {
		if other, ok := m.Lookup(x); ok && other != kb {
			if g, err := other.GraphGenerationWithin(crossMaxAge); err == nil {
				gens[x] = g
			}
		}
	}
	memo := &kb.crossDigest
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if memo.ok && sameGens(memo.gens, gens) {
		return memo.digest
	}
	crossDigestBuilds.Add(1)
	digest := ""
	if ins := kb.inbounds(""); len(ins) > 0 {
		h := sha256.New()
		for _, in := range ins {
			fmt.Fprintf(h, "%s\x00%s\x00%s\n", in.kb, in.target, in.src)
		}
		digest = hex.EncodeToString(h.Sum(nil))
	}
	memo.gens, memo.digest, memo.ok = gens, digest, true
	return digest
}

func sameGens(a, b map[string]uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
