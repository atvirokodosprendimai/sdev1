package routing

import (
	"errors"
	"fmt"
	"slices"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

// RootPrefix is the depth-zero leaf, which contains every key.
//
// It is the default route. A table holding one can always answer, which is what
// lets a client bootstrap from a single frontdoor address.
var RootPrefix = addr.LeafID{}

// ErrNoRoute reports that nothing matched, not even a default.
//
// ★ It is returned instead of a zero route on purpose. A zero route is a next
// hop of nowhere, and a client handed one would send a request into the dark
// rather than reporting that it does not know where to go.
var ErrNoRoute = errors.New("routing: no route matches that key")

// ErrEmptyRoute reports a route with no next hops, which is not a route.
var ErrEmptyRoute = errors.New("routing: a route needs at least one next hop")

// Route says where requests for a prefix should go.
type Route struct {
	// Prefix is the subtree this route covers. A deeper prefix beats a
	// shallower one containing the same key.
	Prefix addr.LeafID
	// NextHops are the nodes to try, in preference order. The order is part of
	// the route: two routes with the same hops in a different order are
	// different routes, because a client tries them in sequence.
	NextHops []string
	// Epoch orders two claims about one prefix. A client installs a route only
	// if its epoch is strictly newer than what it holds, which is what keeps a
	// stale redirect from dragging it backwards.
	Epoch uint64
}

// Table maps prefixes to routes.
//
// It is not safe for concurrent modification; a table is built and then read, or
// wrapped by something that owns its mutation.
type Table struct {
	routes map[addr.LeafID]Route
}

// NewTable returns an empty table.
func NewTable() *Table { return &Table{routes: map[addr.LeafID]Route{}} }

// Insert adds or replaces the route for a prefix.
func (t *Table) Insert(r Route) error {
	if len(r.NextHops) == 0 {
		return fmt.Errorf("%w: prefix %s", ErrEmptyRoute, r.Prefix)
	}
	hops := make([]string, len(r.NextHops))
	copy(hops, r.NextHops)
	r.NextHops = hops
	t.routes[r.Prefix] = r
	return nil
}

// Lookup returns the route for a key, matching the deepest prefix that contains
// it.
//
// It walks from the most specific depth to the least, so a carved-out subtree
// wins over the parent it was carved from. The walk is bounded by the trie's
// maximum depth rather than by the size of the table, which is why a table of
// any size answers in the same time.
func (t *Table) Lookup(k addr.Key) (Route, error) {
	for depth := int(addr.MaxDepth); depth >= 1; depth-- {
		prefix, err := addr.Descend(k, uint8(depth))
		if err != nil {
			continue
		}
		if r, ok := t.routes[prefix]; ok {
			return r, nil
		}
	}
	if r, ok := t.routes[RootPrefix]; ok {
		return r, nil
	}
	return Route{}, ErrNoRoute
}

// Routes returns every route, ordered by prefix depth then bytes, for a
// diagnostic and for tests that need a stable order.
func (t *Table) Routes() []Route {
	out := make([]Route, 0, len(t.routes))
	for _, r := range t.routes {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Route) int {
		if a.Prefix.Depth != b.Prefix.Depth {
			return int(a.Prefix.Depth) - int(b.Prefix.Depth)
		}
		return slices.Compare(a.Prefix.Prefix[:], b.Prefix.Prefix[:])
	})
	return out
}

// Len is how many routes the table holds.
func (t *Table) Len() int { return len(t.routes) }

// Aggregate replaces every complete set of children that share their next hops
// with their parent, and reports how many routes were removed.
//
// ★ This is what bounds the table by placement VARIETY rather than by leaf
// count. A node holding a whole subtree advertises one prefix; only where
// placement actually differs does the table have to say so.
//
// ⚠ It never changes what a lookup answers. A collapse happens only when ALL
// [addr.FanOut] children are present and agree, so every key that reached a
// child reaches the parent with the same next hops. One differing child prevents
// the collapse entirely — which is why aggregation is safe to run at any time.
func (t *Table) Aggregate() int {
	removed := 0
	// Deepest first, so a collapsed level can itself be collapsed into its own
	// parent on the next iteration.
	for depth := int(addr.MaxDepth); depth >= 1; depth-- {
		for parent, children := range t.childrenByParent(depth) {
			if len(children) != addr.FanOut {
				continue
			}
			hops, epoch, agreed := t.commonHops(children)
			if !agreed {
				continue
			}
			for _, c := range children {
				delete(t.routes, c)
			}
			t.routes[parent] = Route{Prefix: parent, NextHops: hops, Epoch: epoch}
			removed += len(children) - 1
		}
	}
	return removed
}

// childrenByParent groups the routes at one depth by the parent prefix they
// would collapse into.
func (t *Table) childrenByParent(depth int) map[addr.LeafID][]addr.LeafID {
	groups := map[addr.LeafID][]addr.LeafID{}
	for prefix := range t.routes {
		if int(prefix.Depth) != depth {
			continue
		}
		parent := prefix
		// The parent is one level shallower with this level's byte cleared, so
		// that only the first Depth bytes stay meaningful.
		parent.Prefix[depth-1] = 0
		parent.Depth = uint8(depth - 1)
		groups[parent] = append(groups[parent], prefix)
	}
	return groups
}

// commonHops reports the next hops every child shares, and the newest epoch
// among them.
//
// The epoch carried forward is the NEWEST, because the aggregate asserts what is
// true of all of them and the freshest claim is the one that must not be
// overwritten by an older redirect.
func (t *Table) commonHops(children []addr.LeafID) (hops []string, epoch uint64, agreed bool) {
	first := t.routes[children[0]]
	for _, c := range children {
		r := t.routes[c]
		if !slices.Equal(r.NextHops, first.NextHops) {
			return nil, 0, false
		}
		if r.Epoch > epoch {
			epoch = r.Epoch
		}
	}
	hops = make([]string, len(first.NextHops))
	copy(hops, first.NextHops)
	return hops, epoch, true
}
