package routing

import (
	"errors"
	"slices"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

// leafAt builds a prefix directly, so a test controls the trie position rather
// than hashing its way to one.
func leafAt(prefix ...byte) addr.LeafID {
	var l addr.LeafID
	copy(l.Prefix[:], prefix)
	l.Depth = uint8(len(prefix))
	return l
}

// keyWith builds a key whose leading bytes are given; the rest is a fixed
// filler, so two keys with the same prefix are otherwise identical.
func keyWith(prefix ...byte) addr.Key {
	var k addr.Key
	copy(k[:], prefix)
	for i := len(prefix); i < len(k); i++ {
		k[i] = 0x77
	}
	return k
}

func mustInsert(t *testing.T, tbl *Table, prefix addr.LeafID, hops []string, epoch uint64) {
	t.Helper()
	if err := tbl.Insert(Route{Prefix: prefix, NextHops: hops, Epoch: epoch}); err != nil {
		t.Fatalf("Insert(%s): %v", prefix, err)
	}
}

func mustLookup(t *testing.T, tbl *Table, k addr.Key) Route {
	t.Helper()
	r, err := tbl.Lookup(k)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	return r
}

// TestLongestPrefixWins checks a deeper prefix beats a shallower one containing
// the same key.
//
// That is what lets a subtree be carved out of a larger route by advertising a
// deeper one, without withdrawing the parent — so a repair is a local change
// rather than a rewrite of what covers it.
func TestLongestPrefixWins(t *testing.T) {
	tbl := NewTable()
	mustInsert(t, tbl, leafAt(0x2A), []string{"shallow"}, 1)
	mustInsert(t, tbl, leafAt(0x2A, 0x01, 0x02), []string{"deep"}, 1)

	if got := mustLookup(t, tbl, keyWith(0x2A, 0x01, 0x02)).NextHops; !slices.Equal(got, []string{"deep"}) {
		t.Errorf("a key inside the carved-out subtree routed to %v, want the deeper route", got)
	}
	// One byte different at the third level: still inside the shallow route, not
	// the deep one.
	if got := mustLookup(t, tbl, keyWith(0x2A, 0x01, 0x99)).NextHops; !slices.Equal(got, []string{"shallow"}) {
		t.Errorf("a key outside the carved-out subtree routed to %v, want the shallower route", got)
	}
	// Different at the first level: outside both.
	if _, err := tbl.Lookup(keyWith(0x2B)); !errors.Is(err, ErrNoRoute) {
		t.Errorf("a key outside every route: error = %v, want ErrNoRoute", err)
	}

	// Depth ordering holds when a middle route is added later, so it is a
	// property of the lookup rather than of insertion order.
	mustInsert(t, tbl, leafAt(0x2A, 0x01), []string{"middle"}, 1)
	if got := mustLookup(t, tbl, keyWith(0x2A, 0x01, 0x99)).NextHops; !slices.Equal(got, []string{"middle"}) {
		t.Errorf("after inserting a middle route, the key routed to %v, want the middle route", got)
	}
	if got := mustLookup(t, tbl, keyWith(0x2A, 0x01, 0x02)).NextHops; !slices.Equal(got, []string{"deep"}) {
		t.Errorf("the deepest route stopped winning after a middle one was added: %v", got)
	}
}

// sampleAnswers records where a spread of keys routes, so aggregation can be
// checked to have preserved them.
//
// ⚠ Every aggregation test uses this. Asserting only that the table SHRANK would
// pass for an aggregation that simply dropped routes, which is the failure mode
// that matters.
func sampleAnswers(t *testing.T, tbl *Table, keys []addr.Key) [][]string {
	t.Helper()
	out := make([][]string, len(keys))
	for i, k := range keys {
		r, err := tbl.Lookup(k)
		if err != nil {
			t.Fatalf("sampling key %d: %v", i, err)
		}
		out[i] = r.NextHops
	}
	return out
}

func firstLevelKeys() []addr.Key {
	keys := make([]addr.Key, 0, addr.FanOut)
	for b := 0; b < addr.FanOut; b++ {
		keys = append(keys, keyWith(byte(b)))
	}
	return keys
}

// TestAggregationCollapsesIdenticalChildren checks a complete set of children
// sharing next hops is replaced by the parent, and that nothing about the
// answers changes.
func TestAggregationCollapsesIdenticalChildren(t *testing.T) {
	tbl := NewTable()
	hops := []string{"node-a", "node-b"}
	for b := 0; b < addr.FanOut; b++ {
		mustInsert(t, tbl, leafAt(byte(b)), hops, uint64(b))
	}
	if tbl.Len() != addr.FanOut {
		t.Fatalf("built %d routes, want %d", tbl.Len(), addr.FanOut)
	}

	keys := firstLevelKeys()
	before := sampleAnswers(t, tbl, keys)

	removed := tbl.Aggregate()
	if removed != addr.FanOut-1 {
		t.Errorf("Aggregate removed %d routes, want %d", removed, addr.FanOut-1)
	}
	if tbl.Len() != 1 {
		t.Fatalf("after aggregating a uniform level the table holds %d routes, want 1", tbl.Len())
	}
	if got := tbl.Routes()[0].Prefix; got != RootPrefix {
		t.Errorf("the surviving route covers %s, want the root prefix", got)
	}
	// The newest child epoch is carried forward, so an older redirect cannot
	// overwrite the aggregate.
	if got, want := tbl.Routes()[0].Epoch, uint64(addr.FanOut-1); got != want {
		t.Errorf("the aggregate carries epoch %d, want the newest child's %d", got, want)
	}

	after := sampleAnswers(t, tbl, keys)
	for i := range before {
		if !slices.Equal(before[i], after[i]) {
			t.Fatalf("key %d routed to %v before aggregation and %v after; aggregation "+
				"changed an answer", i, before[i], after[i])
		}
	}
}

// TestAggregationKeepsAnOddChildOut checks one differing child prevents the
// collapse entirely, which is what makes aggregation safe to run at any time.
func TestAggregationKeepsAnOddChildOut(t *testing.T) {
	tbl := NewTable()
	hops := []string{"node-a"}
	for b := 0; b < addr.FanOut; b++ {
		mustInsert(t, tbl, leafAt(byte(b)), hops, 1)
	}
	// One child moved somewhere else — a repair in flight.
	mustInsert(t, tbl, leafAt(0x42), []string{"node-z"}, 2)

	keys := firstLevelKeys()
	before := sampleAnswers(t, tbl, keys)

	if removed := tbl.Aggregate(); removed != 0 {
		t.Errorf("Aggregate removed %d routes with one child differing, want 0", removed)
	}
	if tbl.Len() != addr.FanOut {
		t.Errorf("the table holds %d routes, want all %d kept", tbl.Len(), addr.FanOut)
	}

	after := sampleAnswers(t, tbl, keys)
	for i := range before {
		if !slices.Equal(before[i], after[i]) {
			t.Fatalf("key %d changed from %v to %v", i, before[i], after[i])
		}
	}
	if got := mustLookup(t, tbl, keyWith(0x42)).NextHops; !slices.Equal(got, []string{"node-z"}) {
		t.Errorf("the odd child routed to %v, want node-z", got)
	}

	// Once it agrees again, the level collapses — so the refusal was about the
	// disagreement rather than about anything permanent.
	mustInsert(t, tbl, leafAt(0x42), hops, 3)
	if removed := tbl.Aggregate(); removed != addr.FanOut-1 {
		t.Errorf("after the odd child agreed, Aggregate removed %d, want %d", removed, addr.FanOut-1)
	}
}

// TestTableWithoutADefaultRefuses checks a lookup matching nothing says so.
func TestTableWithoutADefaultRefuses(t *testing.T) {
	empty := NewTable()
	if _, err := empty.Lookup(keyWith(0x01)); !errors.Is(err, ErrNoRoute) {
		t.Errorf("an empty table: error = %v, want ErrNoRoute", err)
	}

	tbl := NewTable()
	mustInsert(t, tbl, leafAt(0x2A, 0x01), []string{"somewhere"}, 1)
	if _, err := tbl.Lookup(keyWith(0x2A, 0x02)); !errors.Is(err, ErrNoRoute) {
		t.Errorf("a key outside the only route: error = %v, want ErrNoRoute", err)
	}

	// A default route is what lets a client bootstrap from one frontdoor.
	mustInsert(t, tbl, RootPrefix, []string{"frontdoor"}, 1)
	if got := mustLookup(t, tbl, keyWith(0x2A, 0x02)).NextHops; !slices.Equal(got, []string{"frontdoor"}) {
		t.Errorf("with a default route the key routed to %v, want the frontdoor", got)
	}
	if got := mustLookup(t, tbl, keyWith(0x2A, 0x01)).NextHops; !slices.Equal(got, []string{"somewhere"}) {
		t.Errorf("the specific route stopped winning over the default: %v", got)
	}

	// A route with no next hops is not a route.
	if err := tbl.Insert(Route{Prefix: leafAt(0x99)}); !errors.Is(err, ErrEmptyRoute) {
		t.Errorf("inserting a route with no hops: error = %v, want ErrEmptyRoute", err)
	}
}

// TestTableSizeIsBoundedByVariety is the claim the record's rule 2 rests on: the
// table's size follows how much placement DIFFERS, not how many leaves exist.
func TestTableSizeIsBoundedByVariety(t *testing.T) {
	keys := make([]addr.Key, 0, addr.FanOut)
	for b := 0; b < addr.FanOut; b++ {
		keys = append(keys, keyWith(0x10, byte(b)))
	}

	// Uniform: every child of one node agrees, so the whole level becomes one
	// route however many leaves are under it.
	uniform := NewTable()
	for b := 0; b < addr.FanOut; b++ {
		mustInsert(t, uniform, leafAt(0x10, byte(b)), []string{"node-a"}, 1)
	}
	before := sampleAnswers(t, uniform, keys)
	uniform.Aggregate()
	if uniform.Len() != 1 {
		t.Errorf("a uniform level of %d routes aggregated to %d, want 1", addr.FanOut, uniform.Len())
	}
	after := sampleAnswers(t, uniform, keys)
	for i := range before {
		if !slices.Equal(before[i], after[i]) {
			t.Fatalf("uniform: key %d changed from %v to %v", i, before[i], after[i])
		}
	}

	// Varied: half the children sit elsewhere, so nothing collapses and the
	// table stays the size of the variety.
	varied := NewTable()
	for b := 0; b < addr.FanOut; b++ {
		hop := "node-a"
		if b%2 == 1 {
			hop = "node-b"
		}
		mustInsert(t, varied, leafAt(0x10, byte(b)), []string{hop}, 1)
	}
	variedBefore := sampleAnswers(t, varied, keys)
	if removed := varied.Aggregate(); removed != 0 {
		t.Errorf("a varied level aggregated away %d routes; the bound is on variety, "+
			"and collapsing here would change answers", removed)
	}
	if varied.Len() != addr.FanOut {
		t.Errorf("a varied level holds %d routes, want %d", varied.Len(), addr.FanOut)
	}
	variedAfter := sampleAnswers(t, varied, keys)
	for i := range variedBefore {
		if !slices.Equal(variedBefore[i], variedAfter[i]) {
			t.Fatalf("varied: key %d changed from %v to %v", i, variedBefore[i], variedAfter[i])
		}
	}

	// The two tables cover the same leaves and differ only in placement variety,
	// which is the whole claim.
	if uniform.Len() >= varied.Len() {
		t.Errorf("uniform holds %d routes and varied holds %d; the uniform table must be "+
			"smaller for the same number of leaves", uniform.Len(), varied.Len())
	}
}
