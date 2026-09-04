package routing

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

// fakeCluster answers Serve from a map of which node actually holds which
// prefix, plus what each node believes about leaves it does not hold.
type fakeCluster struct {
	// holder maps a prefix to the node that really serves it.
	holder map[addr.LeafID]string
	// belief maps a node to the routes it will redirect with.
	belief map[string][]Route
	// served counts how many times each node was asked, so a test can show the
	// client actually visited the nodes it claims.
	served map[string]int
}

func newCluster() *fakeCluster {
	return &fakeCluster{
		holder: map[addr.LeafID]string{},
		belief: map[string][]Route{},
		served: map[string]int{},
	}
}

func (f *fakeCluster) Serve(node string, k addr.Key) (Redirect, bool) {
	f.served[node]++

	// Does this node really hold the leaf? Deepest holder wins, same rule as a
	// table lookup.
	for depth := int(addr.MaxDepth); depth >= 1; depth-- {
		prefix, err := addr.Descend(k, uint8(depth))
		if err != nil {
			continue
		}
		if who, ok := f.holder[prefix]; ok {
			if who == node {
				return Redirect{}, true
			}
			break
		}
	}

	// It does not. Redirect with whatever it believes covers the key.
	for _, r := range f.belief[node] {
		leaf, err := addr.Descend(k, r.Prefix.Depth)
		if err != nil {
			continue
		}
		if r.Prefix.Contains(leaf) {
			return Redirect{Route: r}, false
		}
	}
	return Redirect{}, false
}

func seedCache(t *testing.T, frontdoor string) *Cache {
	t.Helper()
	c, err := NewCache(Route{Prefix: RootPrefix, NextHops: []string{frontdoor}, Epoch: 1})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return c
}

// TestStaleRouteRedirectsRatherThanFailing is the falsifier ADR-008 names in its
// Enforced-by header.
//
// A client holding an out-of-date route must reach the right node, learn the new
// route, and neither error nor be served by the node that no longer holds the
// leaf. Refusing would make every topology change an outage; answering anyway
// would be silently wrong.
func TestStaleRouteRedirectsRatherThanFailing(t *testing.T) {
	key := keyWith(0x2A, 0x01)
	leaf := leafAt(0x2A, 0x01)

	cl := newCluster()
	cl.holder[leaf] = "node-new"
	// The frontdoor knows the leaf moved.
	cl.belief["node-old"] = []Route{{Prefix: leaf, NextHops: []string{"node-new"}, Epoch: 5}}

	// The client's route is stale: it still points at node-old.
	c := seedCache(t, "node-old")
	if got := mustLookup(t, c.table, key).NextHops; !slices.Equal(got, []string{"node-old"}) {
		t.Fatalf("the client starts believing %v, want node-old", got)
	}

	dst, err := Resolve(c, cl, key, DefaultHopBudget)
	if err != nil {
		t.Fatalf("a stale route must redirect, not fail: %v", err)
	}
	if dst.Node != "node-new" {
		t.Errorf("resolved to %q, want node-new — the stale node must not serve the request", dst.Node)
	}
	if cl.served["node-old"] != 1 {
		t.Errorf("node-old was asked %d times, want 1: the client should have tried it and been "+
			"redirected exactly once", cl.served["node-old"])
	}

	// The client LEARNED. A second resolution goes straight there.
	before := cl.served["node-old"]
	dst2, err := Resolve(c, cl, key, DefaultHopBudget)
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if dst2.Node != "node-new" {
		t.Errorf("second resolution went to %q, want node-new", dst2.Node)
	}
	if cl.served["node-old"] != before {
		t.Errorf("node-old was asked again after the client learned; the redirect did not stick")
	}
}

// TestOlderEpochNeverReplacesNewer checks a client cannot be dragged backwards.
//
// Equal is covered as well as older: accepting an equal epoch would let two
// nodes flap a client between two routes forever with neither being newer.
func TestOlderEpochNeverReplacesNewer(t *testing.T) {
	prefix := leafAt(0x11)
	c := seedCache(t, "frontdoor")

	if !c.Install(Route{Prefix: prefix, NextHops: []string{"node-b"}, Epoch: 5}) {
		t.Fatal("installing a route for a new prefix was refused")
	}
	held := mustLookup(t, c.table, keyWith(0x11))
	if held.Epoch != 5 {
		t.Fatalf("held epoch %d, want 5", held.Epoch)
	}

	for _, older := range []uint64{0, 1, 4, 5} {
		if c.Install(Route{Prefix: prefix, NextHops: []string{"node-stale"}, Epoch: older}) {
			t.Errorf("a route at epoch %d replaced one at epoch 5", older)
		}
		if got := mustLookup(t, c.table, keyWith(0x11)); got.Epoch != 5 ||
			!slices.Equal(got.NextHops, []string{"node-b"}) {
			t.Fatalf("after an epoch-%d install the client holds %v at epoch %d, want node-b at 5",
				older, got.NextHops, got.Epoch)
		}
	}

	if !c.Install(Route{Prefix: prefix, NextHops: []string{"node-c"}, Epoch: 6}) {
		t.Error("a strictly newer route was refused")
	}
	if got := mustLookup(t, c.table, keyWith(0x11)); !slices.Equal(got.NextHops, []string{"node-c"}) {
		t.Errorf("after a newer install the client holds %v, want node-c", got.NextHops)
	}

	// A route with no next hops is never installed, whatever its epoch.
	if c.Install(Route{Prefix: prefix, Epoch: 99}) {
		t.Error("a route with no next hops was installed")
	}
}

// TestRedirectChainIsBounded checks a cycle is refused rather than followed
// forever.
//
// ⚠ The cycle's epochs INCREASE. A cycle at one epoch is stopped by the epoch
// rule alone, so testing that would say nothing about the budget — and the budget
// exists precisely for the case the epoch rule cannot catch.
func TestRedirectChainIsBounded(t *testing.T) {
	key := keyWith(0x30)
	prefix := leafAt(0x30)

	cl := newCluster()
	// Nobody holds the leaf, and two nodes point at each other with ever-newer
	// epochs — a node that is simply wrong.
	epoch := uint64(10)
	cl.belief["node-a"] = []Route{{Prefix: prefix, NextHops: []string{"node-b"}, Epoch: epoch}}
	cl.belief["node-b"] = []Route{{Prefix: prefix, NextHops: []string{"node-a"}, Epoch: epoch}}

	// Rebuild beliefs on each Serve so the epochs keep rising.
	rising := &risingCluster{inner: cl, prefix: prefix, epoch: epoch}

	c := seedCache(t, "node-a")
	_, err := Resolve(c, rising, key, 6)
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("a rising-epoch cycle: error = %v, want ErrTooManyRedirects", err)
	}
	if !strings.Contains(err.Error(), "node-a") || !strings.Contains(err.Error(), "node-b") {
		t.Errorf("the error does not name the chain: %v — an operator cannot find the lying "+
			"node without it", err)
	}

	// A cycle at a FLAT epoch is stopped by the epoch rule, sooner.
	flat := newCluster()
	flat.belief["node-a"] = []Route{{Prefix: prefix, NextHops: []string{"node-b"}, Epoch: 3}}
	flat.belief["node-b"] = []Route{{Prefix: prefix, NextHops: []string{"node-a"}, Epoch: 3}}
	c2 := seedCache(t, "node-a")
	if _, err := Resolve(c2, flat, key, 100); !errors.Is(err, ErrTooManyRedirects) {
		t.Errorf("a flat-epoch cycle: error = %v, want ErrTooManyRedirects", err)
	}
}

// risingCluster makes every redirect carry a newer epoch than the last, which is
// the case a hop budget exists for.
type risingCluster struct {
	inner  *fakeCluster
	prefix addr.LeafID
	epoch  uint64
}

func (r *risingCluster) Serve(node string, k addr.Key) (Redirect, bool) {
	r.epoch++
	next := "node-b"
	if node == "node-b" {
		next = "node-a"
	}
	r.inner.served[node]++
	return Redirect{Route: Route{Prefix: r.prefix, NextHops: []string{next}, Epoch: r.epoch}}, false
}

// TestClientLearnsFromOneFrontdoor checks a client reaches the whole key space
// from a single seed route, and that its cache grows only by what it used.
func TestClientLearnsFromOneFrontdoor(t *testing.T) {
	cl := newCluster()
	const regions = 12
	for i := 0; i < regions; i++ {
		leaf := leafAt(byte(i))
		node := "node-" + string(rune('a'+i))
		cl.holder[leaf] = node
		cl.belief["frontdoor"] = append(cl.belief["frontdoor"],
			Route{Prefix: leaf, NextHops: []string{node}, Epoch: uint64(10 + i)})
	}

	c := seedCache(t, "frontdoor")
	if c.Len() != 1 {
		t.Fatalf("a seeded cache holds %d routes, want 1 — a client starts with a frontdoor "+
			"and nothing else", c.Len())
	}

	// Use four of the twelve regions.
	used := []int{0, 3, 7, 11}
	for _, i := range used {
		dst, err := Resolve(c, cl, keyWith(byte(i)), DefaultHopBudget)
		if err != nil {
			t.Fatalf("resolving region %d: %v", i, err)
		}
		want := "node-" + string(rune('a'+i))
		if dst.Node != want {
			t.Errorf("region %d resolved to %q, want %q", i, dst.Node, want)
		}
	}

	// The cache holds the seed plus exactly what was used — not the cluster.
	if got, want := c.Len(), 1+len(used); got != want {
		t.Errorf("after using %d regions the cache holds %d routes, want %d; a client's memory "+
			"must track what it used, not how large the cluster is", len(used), got, want)
	}

	// And there is no way to load the rest: the cache exposes no bulk load.
	typ := reflect.TypeOf(c)
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if strings.Contains(name, "Load") || strings.Contains(name, "All") ||
			strings.Contains(name, "Sync") {
			t.Errorf("Cache exposes %q; a client that can ask for the map will hold the map, "+
				"which is what this record exists to avoid", name)
		}
	}
}

// TestRedirectIsNotMistakableForAnAnswer checks the type system carries rule 4.
//
// A redirect that can be read as an answer means a stale route can serve one,
// which is the exact failure redirecting exists to prevent.
func TestRedirectIsNotMistakableForAnAnswer(t *testing.T) {
	rd := reflect.TypeOf(Redirect{})
	dst := reflect.TypeOf(Destination{})

	if rd == dst {
		t.Fatal("Redirect and Destination are the same type")
	}
	if rd.ConvertibleTo(dst) || dst.ConvertibleTo(rd) {
		t.Error("Redirect and Destination are convertible to each other; 'go elsewhere' and " +
			"'here is your node' must not be interchangeable")
	}

	// A Destination carries no redirect, and a Redirect carries no payload.
	for i := 0; i < dst.NumField(); i++ {
		if dst.Field(i).Type == rd {
			t.Errorf("Destination field %q holds a Redirect", dst.Field(i).Name)
		}
	}
	wantRedirectFields := map[string]bool{"Route": true}
	for i := 0; i < rd.NumField(); i++ {
		f := rd.Field(i)
		if !wantRedirectFields[f.Name] {
			t.Errorf("Redirect has field %q of type %s — a redirect that can carry data means a "+
				"stale route can serve an answer", f.Name, f.Type)
		}
	}

	// Resolve's own signature returns a Destination, never a Redirect.
	fn := reflect.TypeOf(Resolve)
	for i := 0; i < fn.NumOut(); i++ {
		if fn.Out(i) == rd {
			t.Error("Resolve returns a Redirect to its caller; a redirect is internal to " +
				"resolution and a caller could mistake it for an answer")
		}
	}
	if fn.Out(0) != dst {
		t.Errorf("Resolve returns %s first, want Destination", fn.Out(0))
	}
}
