package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// crossRevisionSuffix is the part of a KB's Atlas revision that follows the KBs
// it is cross-linked with, in both directions. It is "-x<12 hex>", a digest of
// the graph generations of the linked KBs the caller reads whole, and empty when
// there is none (the token is then the one a KB without cross links has).
//
// Only a KB the caller reads whole counts: /revision answers 404 for any other,
// so a token that moved for a KB the caller cannot poll would tell it that KB
// changed. The digest never carries a name. One KB at a time, never under
// another KB's lock. A link the relation guard leaves unresolved can still name
// its KB here: the token may then move for nothing, which discloses nothing.
func crossRevisionSuffix(ctx context.Context, k *kb.KB) string {
	set := map[string]bool{}
	if linked, err := k.CrossLinkedKBsWithin(uiRevisionMaxAge); err == nil {
		for _, n := range linked {
			set[n] = true
		}
	}
	for _, n := range k.CrossLinkedFromWithin(uiRevisionMaxAge) {
		set[n] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	any := false
	for _, n := range names {
		target, canonical, mounted := crossMounted(k, n)
		if !mounted || !crossReadable(ctx, k, canonical) {
			continue
		}
		gen, err := target.GraphGenerationWithin(uiRevisionMaxAge)
		if err != nil {
			continue
		}
		any = true
		h.Write([]byte(canonical))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatUint(gen, 10)))
		h.Write([]byte{0})
	}
	if !any {
		return ""
	}
	return "-x" + hex.EncodeToString(h.Sum(nil))[:12]
}
