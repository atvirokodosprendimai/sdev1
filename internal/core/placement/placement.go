package placement

import (
	"errors"
	"fmt"
	"hash/maphash"
	"sort"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/topology"
)

// ErrDepthMismatch reports a leaf identifier recorded at a depth the map does
// not declare. It is refused rather than re-placed, because silently resolving
// a depth-1 leaf against a depth-2 map routes it somewhere nobody asked for.
var ErrDepthMismatch = errors.New("placement: leaf depth does not match the map")

// ErrNoTargets reports a map that declares no placement targets.
var ErrNoTargets = errors.New("placement: map declares no targets")

// seed is fixed rather than random so that scoring is identical in every process
// and every run. A per-process seed would make placement disagree between
// clients, which is the one thing this package may not do.
var seed = maphash.MakeSeed()

// Resolve returns the canonical, ordered set of targets for a leaf.
//
// A target is a node at the map's deepest declared level — the level that
// actually holds data. Ordering is by weighted rendezvous score over the leaf
// identifier and the target's name, so:
//
//   - it is a pure function of the leaf and the map, and takes no caller
//     identity, so every client computes the same answer without coordinating;
//   - adding or removing an unrelated target does not reorder the others
//     relative to each other, which is rendezvous hashing's defining property
//     and what keeps a topology change from reshuffling the whole cluster.
//
// How many of the returned targets actually hold a copy is a durability policy
// and is deliberately not decided here. Callers take a prefix.
func Resolve(leaf addr.LeafID, m topology.Map) ([]string, error) {
	if leaf.Depth != m.Depth {
		return nil, fmt.Errorf("%w: leaf is depth %d, map declares %d",
			ErrDepthMismatch, leaf.Depth, m.Depth)
	}
	targetLevel := len(m.Levels) - 1
	type scored struct {
		name  string
		score uint64
	}
	candidates := make([]scored, 0, len(m.Nodes))
	for _, n := range m.Nodes {
		if n.LevelIdx != targetLevel {
			continue
		}
		candidates = append(candidates, scored{name: n.Name, score: weightedScore(leaf, n)})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: no node at level %q", ErrNoTargets, m.Levels[targetLevel])
	}
	// Highest score first; name breaks a tie so the order is total and stable.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.name
	}
	return out, nil
}

// weightedScore is the rendezvous score of one target for one leaf.
//
// A zero or negative weight scores zero, which sorts the target last without
// removing it: a target an operator has drained should stop attracting new
// placements, not vanish from an answer other replicas are computed against.
func weightedScore(leaf addr.LeafID, n topology.Node) uint64 {
	var h maphash.Hash
	h.SetSeed(seed)
	var depth [1]byte
	depth[0] = leaf.Depth
	_, _ = h.Write(depth[:])
	_, _ = h.Write(leaf.Bytes())
	_, _ = h.WriteString(n.Name)
	raw := h.Sum64()
	if n.Weight <= 0 {
		return 0
	}
	// Scale into the high bits so weight dominates and the hash orders within a
	// weight class. Kept deliberately simple: this is a ranking, not a
	// probability model.
	return (raw >> 16) * uint64(n.Weight)
}

// Spread reorders a canonical set so that consecutive entries fall in distinct
// failure domains at the given level, wherever the map offers them.
//
// It is separate from [Resolve] because they answer different questions:
// Resolve decides which targets are candidates at all, Spread decides the order
// a durability policy should consume them in so that taking a prefix yields
// distinct domains. Membership is unchanged — Spread is a permutation.
//
// Targets with no ancestor at the level are treated as their own domain, since
// a node outside every rack shares a rack with nothing.
func Spread(order []string, m topology.Map, levelIdx int) []string {
	domainOf := make(map[string]string, len(order))
	for _, name := range order {
		if anc, err := m.AncestorAtLevel(name, levelIdx); err == nil {
			domainOf[name] = anc.Name
		} else {
			domainOf[name] = "\x00self:" + name
		}
	}
	remaining := append([]string(nil), order...)
	out := make([]string, 0, len(order))
	for len(remaining) > 0 {
		used := make(map[string]bool)
		kept := remaining[:0]
		for _, name := range remaining {
			d := domainOf[name]
			if used[d] {
				kept = append(kept, name)
				continue
			}
			used[d] = true
			out = append(out, name)
		}
		remaining = kept
	}
	return out
}

// Nearest returns the given set ordered by topology distance from one node,
// nearest first, preserving the canonical order among equals.
//
// This is the READ-PREFERENCE question and it is deliberately not [Resolve]'s.
// Resolve answers who holds a leaf and must be identical for every caller;
// Nearest answers who this particular caller should read from first, and is
// necessarily different for each of them. Folding the two together would make
// placement depend on who is asking, which is the one property the whole design
// rests on not having.
//
// Nearest is a permutation: it reorders and never adds or drops a target.
func Nearest(set []string, from string, m topology.Map) []string {
	type ranked struct {
		name string
		dist int
		ord  int
	}
	rs := make([]ranked, len(set))
	for i, name := range set {
		d, err := m.Distance(from, name)
		if err != nil {
			// An unknown or unreachable target sorts last rather than being
			// dropped: Nearest may not change membership.
			d = len(m.Levels) + 1
		}
		rs[i] = ranked{name: name, dist: d, ord: i}
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].dist != rs[j].dist {
			return rs[i].dist < rs[j].dist
		}
		return rs[i].ord < rs[j].ord
	})
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.name
	}
	return out
}
