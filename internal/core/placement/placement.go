package placement

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
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

// ⚠ There is deliberately NO SEED VARIABLE HERE, and re-introducing one is the
// mistake this comment exists to prevent.
//
// This scoring must be identical in every process, on every machine, forever —
// two clients that disagree place the same leaf on different servers, and the
// data written by one is then looked for by the other. [weightedScore] therefore
// uses an unseeded hash whose output is a pure function of its input bytes.
//
// A seeded hash is the natural reach here and it is wrong: `maphash.MakeSeed`
// returns a NEW RANDOM seed per process, so placement would agree with itself
// all day inside one binary and disagree with every other binary in the cluster.
// It cannot be fixed by seeding from a constant either — a `maphash.Seed` can
// only be produced by `MakeSeed`, so there is no constant to use.
//
// ★ Found 2026-09-04 by running cmd/sdev1-addr twice and getting two different
// orders. Every in-process test passed, because a Go test binary is ONE process
// and the seed is constant within it. The invariant is cross-process, so only a
// check on VALUES can hold it — see TestResolveOrderIsPinnedAcrossProcesses.

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
	// SHA-256, truncated: unseeded, so the same bytes give the same score in
	// every process, and well-distributed, so the ranking does not correlate with
	// anything about the name.
	//
	// ⚠ FNV-1a was tried here first and is NOT adequate, for a reason that is
	// invisible unless you look at the resulting order. Its avalanche is weak
	// enough that on a fixture of four targets the ranking came out in exact name
	// order — srv-3, srv-2, srv-1-d1, srv-1-d0 — and stayed in that order when
	// the hash input was perturbed. Deterministic, and useless: a target's score
	// tracked its name, so placement would systematically favour whichever
	// servers sort late, and rendezvous hashing's whole point is spread.
	//
	// ★ The requirement here is determinism AND distribution. It is easy to
	// satisfy the first alone and not notice the second, because every
	// determinism test still passes.
	sum := sha256.Sum256(scoringInput(leaf, n.Name))
	raw := binary.BigEndian.Uint64(sum[:8])
	if n.Weight <= 0 {
		return 0
	}
	// Scale into the high bits so weight dominates and the hash orders within a
	// weight class. Kept deliberately simple: this is a ranking, not a
	// probability model.
	return (raw >> 16) * uint64(n.Weight)
}

// scoringInput is the exact byte sequence a score is taken over.
//
// ★ It is a named function so the scoring input has one definition rather than
// being spelled out at each call site. The depth is included because two leaves
// with the same prefix bytes at different depths are different leaves, and
// hashing only the prefix would score them identically.
func scoringInput(leaf addr.LeafID, name string) []byte {
	buf := make([]byte, 0, 1+len(leaf.Bytes())+len(name))
	buf = append(buf, leaf.Depth)
	buf = append(buf, leaf.Bytes()...)
	buf = append(buf, name...)
	return buf
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
