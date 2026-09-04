package topology

import (
	"fmt"
	"sort"
)

// Distance reports how many levels must be climbed from the deeper of two nodes
// to reach their common ancestor. Smaller is nearer, and a node is distance 0
// from itself.
//
// Two servers in one rack are 1 apart; two in different racks of one datacenter
// are 2. This is the primitive a caller uses to prefer a near replica, and it is
// symmetric.
//
// It reads intervals only — no traversal, no parent pointers.
func (m Map) Distance(a, b string) (int, error) {
	na, err := m.Node(a)
	if err != nil {
		return 0, err
	}
	nb, err := m.Node(b)
	if err != nil {
		return 0, err
	}
	anc, ok := m.commonAncestor(na, nb)
	if !ok {
		// Unreachable for a map loaded by Load, which always has a single root
		// containing everything, but reported rather than assumed.
		return 0, fmt.Errorf("%w: %q and %q share no ancestor", ErrNoAncestorAtLevel, a, b)
	}
	deeper := na.LevelIdx
	if nb.LevelIdx > deeper {
		deeper = nb.LevelIdx
	}
	return deeper - anc.LevelIdx, nil
}

// CommonAncestor returns the deepest node containing both named nodes.
//
// This is the failure-domain question in its general form: two replicas share a
// failure domain at every level from this node's outward.
func (m Map) CommonAncestor(a, b string) (Node, error) {
	na, err := m.Node(a)
	if err != nil {
		return Node{}, err
	}
	nb, err := m.Node(b)
	if err != nil {
		return Node{}, err
	}
	anc, ok := m.commonAncestor(na, nb)
	if !ok {
		return Node{}, fmt.Errorf("%w: %q and %q share no ancestor", ErrNoAncestorAtLevel, a, b)
	}
	return anc, nil
}

// commonAncestor finds the deepest node whose interval encloses both.
func (m Map) commonAncestor(a, b Node) (Node, bool) {
	lo, hi := a.Lft, a.Rgt
	if b.Lft < lo {
		lo = b.Lft
	}
	if b.Rgt > hi {
		hi = b.Rgt
	}
	best := -1
	for i, n := range m.Nodes {
		if n.Lft <= lo && hi <= n.Rgt {
			if best < 0 || n.LevelIdx > m.Nodes[best].LevelIdx {
				best = i
			}
		}
	}
	if best < 0 {
		return Node{}, false
	}
	return m.Nodes[best], true
}

// AncestorAtLevel returns the node at the given level whose subtree contains the
// named node. A node is its own ancestor at its own level.
//
// This is what a durability rule uses to ask "which rack is this replica in",
// and it is how a failure domain is expressed: two replicas are in distinct
// domains at level L when their AncestorAtLevel(L) differ.
//
// The lookup is a binary search among the nodes at that level, which are held in
// ascending Lft order, followed by one interval comparison.
func (m Map) AncestorAtLevel(name string, levelIdx int) (Node, error) {
	n, err := m.Node(name)
	if err != nil {
		return Node{}, err
	}
	if levelIdx < 0 || levelIdx >= len(m.Levels) {
		return Node{}, fmt.Errorf("%w: level index %d not in 0..%d",
			ErrNoAncestorAtLevel, levelIdx, len(m.Levels)-1)
	}
	if levelIdx > n.LevelIdx {
		return Node{}, fmt.Errorf("%w: %q is at level %q, which is shallower than %q",
			ErrNoAncestorAtLevel, name, m.Levels[n.LevelIdx], m.Levels[levelIdx])
	}
	candidates := m.byLevel[levelIdx]
	// The last node at this level whose interval starts at or before n's is the
	// only one that can enclose it, because intervals at one level are disjoint.
	i := sort.Search(len(candidates), func(k int) bool {
		return m.Nodes[candidates[k]].Lft > n.Lft
	}) - 1
	if i < 0 {
		return Node{}, fmt.Errorf("%w: %q has no ancestor at %q",
			ErrNoAncestorAtLevel, name, m.Levels[levelIdx])
	}
	cand := m.Nodes[candidates[i]]
	if !cand.Contains(n) {
		return Node{}, fmt.Errorf("%w: %q has no ancestor at %q",
			ErrNoAncestorAtLevel, name, m.Levels[levelIdx])
	}
	return cand, nil
}
