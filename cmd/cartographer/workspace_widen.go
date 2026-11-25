package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// workspace_widen.go — what one sync widened: the lineage KBs it pulls, and the
// lines and the sum that announce a change of derived or lineage bindings.

// merge adds the pulls of o to p (a nil p is o). The `relations` of o are not
// read: a closure is already transitive, a lineage KB is never a seed.
func (p *prefetched) merge(o *prefetched) *prefetched {
	if p == nil {
		return o
	}
	for _, m := range []func(){
		func() { maps.Copy(p.cs.Named, o.cs.Named) },
		func() { maps.Copy(p.cs.Placeholders, o.cs.Placeholders) },
		func() { maps.Copy(p.cs.PathRegistries, o.cs.PathRegistries) },
		func() { maps.Copy(p.cs.Issues, o.cs.Issues) },
		func() { maps.Copy(p.cs.FeedbackModes, o.cs.FeedbackModes) },
		func() { maps.Copy(p.kbs, o.kbs) },
	} {
		m()
	}
	p.cs.HasBare = p.cs.HasBare || o.cs.HasBare
	p.failed = append(p.failed, o.failed...)
	return p
}

// lineageKBsToPull is the KBs the effective bindings and provider sets of
// targets hold by lineage that the pulls in pre do not cover.
func lineageKBsToPull(cfg *clientconfig.Config, targets []string, pre *prefetched) []string {
	want := map[string]bool{}
	for _, p := range targets {
		for _, k := range cfg.ProviderLineage(p) {
			want[k] = true
		}
		if cfg.DerivesBindings(p) {
			for _, w := range cfg.WorkspaceBindings(p) {
				for k := range w.Lineage {
					want[k] = true
				}
			}
		}
	}
	var out []string
	for k := range want {
		if !pre.covers([]string{k}) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// restoredLineage is lineage with the stored entry put back for every seed
// whose closure holds a KB that could not be pulled (no stored entry: it goes).
// It copies the map: cfg keeps the new closure for the projections.
func restoredLineage(prev clientconfig.DerivedState, lineage map[string]clientconfig.DerivedLineage, failed []kbPullFailure, errw io.Writer) map[string]clientconfig.DerivedLineage {
	if len(failed) == 0 {
		return lineage
	}
	out := maps.Clone(lineage)
	for seed, l := range lineage {
		for _, f := range failed {
			if !slices.Contains(l.Closure, f.KB) {
				continue
			}
			fmt.Fprintf(errw, "lineage of %s left as it was: %s could not be pulled\n", escapeControl(seed), escapeControl(f.KB))
			if old, ok := prev.Lineage[seed]; ok {
				out[seed] = old
			} else {
				delete(out, seed)
			}
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// widening is the difference between two snapshots of the bindings.
type widening struct {
	Added, Removed []clientconfig.DerivedChangeItem
}

// count is the number of added items that came by via.
func (w widening) count(via string) int {
	n := 0
	for _, it := range w.Added {
		if it.Via == via {
			n++
		}
	}
	return n
}

// record is the LastChange the run stores, capped as the loader requires.
func (w widening) record(now time.Time) *clientconfig.DerivedChange {
	c := &clientconfig.DerivedChange{At: now.UTC().Format(time.RFC3339), Added: w.Added, Removed: w.Removed}
	if room := clientconfig.MaxLastChangeItems; len(c.Added) > room {
		c.Added = c.Added[:room]
	}
	if room := clientconfig.MaxLastChangeItems - len(c.Added); len(c.Removed) > room {
		c.Removed = c.Removed[:room]
	}
	return c
}

// bindingSnapshot is every KB the targeted providers hold through a derived
// binding (paths.yaml) or through lineage, per path or provider.
func bindingSnapshot(cfg *clientconfig.Config, targets []string) map[clientconfig.DerivedChangeItem]bool {
	out := map[clientconfig.DerivedChangeItem]bool{}
	for _, p := range targets {
		if cfg.DerivesBindings(p) {
			for _, b := range cfg.Derived().Bindings[p] {
				for _, kb := range b.KBs {
					out[clientconfig.DerivedChangeItem{KB: kb, Path: b.Path, Via: clientconfig.LastChangeViaPaths}] = true
				}
			}
			for _, w := range cfg.WorkspaceBindings(p) {
				for kb, seeds := range w.Lineage {
					out[clientconfig.DerivedChangeItem{KB: kb, Path: w.Path, Via: clientconfig.LastChangeViaLineage, From: slices.Min(seeds)}] = true
				}
			}
		}
		own, _ := cfg.BoundOwnKBs(p)
		for _, kb := range cfg.ProviderLineage(p) {
			from := ""
			for _, seed := range own {
				if slices.Contains(cfg.Lineage(seed).Closure, kb) {
					from = seed
					break
				}
			}
			out[clientconfig.DerivedChangeItem{KB: kb, Via: clientconfig.LastChangeViaLineage, From: from, Provider: p}] = true
		}
	}
	return out
}

// diffSnapshots lists what appeared and went; a KB whose pull failed is not
// announced (its holders are skipped and the failure is reported).
func diffSnapshots(before, after map[clientconfig.DerivedChangeItem]bool, pre *prefetched) widening {
	var w widening
	for it := range after {
		if !before[it] && len(pre.failedFor([]string{it.KB})) == 0 {
			w.Added = append(w.Added, it)
		}
	}
	for it := range before {
		if !after[it] {
			w.Removed = append(w.Removed, it)
		}
	}
	less := func(a, b clientconfig.DerivedChangeItem) int {
		return strings.Compare(fmt.Sprint(a.Provider, "\x00", a.Path, "\x00", a.KB, "\x00", a.Via), fmt.Sprint(b.Provider, "\x00", b.Path, "\x00", b.KB, "\x00", b.Via))
	}
	slices.SortFunc(w.Added, less)
	slices.SortFunc(w.Removed, less)
	return w
}

// printWidenedBindings announces each change of a derived or lineage binding,
// before the apply, under the dry-run prefix when nothing is written.
func printWidenedBindings(out io.Writer, w widening, dryRun bool) {
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	line := func(it clientconfig.DerivedChangeItem, added bool) string {
		kb, path := escapeControl(it.KB), escapeControl(it.Path)
		switch {
		case it.Provider != "" && added:
			return fmt.Sprintf("%s: %s bound by lineage (from %s)", escapeControl(it.Provider), kb, escapeControl(it.From))
		case it.Provider != "":
			return fmt.Sprintf("%s: %s no longer bound by lineage", escapeControl(it.Provider), kb)
		case it.Via == clientconfig.LastChangeViaLineage && added:
			return fmt.Sprintf("bound by lineage: %s (from %s) in %s", kb, escapeControl(it.From), path)
		case added:
			return fmt.Sprintf("bound by %s: %s in %s", it.Via, kb, path)
		}
		return fmt.Sprintf("no longer bound by %s: %s in %s", it.Via, kb, path)
	}
	for _, it := range w.Added {
		fmt.Fprintln(out, prefix+line(it, true))
	}
	for _, it := range w.Removed {
		fmt.Fprintln(out, prefix+line(it, false))
	}
}

// syncWidening is what one sync added to what is bound, by stage.
type syncWidening struct{ Mounted, Derived, Lineage int }

// printSyncWidening is the closing line: nothing when no count is set.
func printSyncWidening(out io.Writer, s syncWidening, dryRun bool) {
	var parts []string
	if s.Mounted > 0 {
		parts = append(parts, fmt.Sprintf("%d KB(s) mounted", s.Mounted))
	}
	if s.Derived > 0 {
		parts = append(parts, fmt.Sprintf("%d repository binding(s) derived", s.Derived))
	}
	if s.Lineage > 0 {
		parts = append(parts, fmt.Sprintf("%d KB(s) brought by lineage", s.Lineage))
	}
	if len(parts) == 0 {
		return
	}
	verb, prefix := "widened", ""
	if dryRun {
		verb, prefix = "would widen", "[dry-run] "
	}
	fmt.Fprintf(out, "%sthis sync %s what is bound: %s; review with cartographer status\n", prefix, verb, strings.Join(parts, ", "))
}
