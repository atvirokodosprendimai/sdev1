package serve_test

import (
	"errors"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/serve"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
	"github.com/atvirokodosprendimai/sdev1/internal/core/wire"
)

// client builds a client whose cache holds exactly the seed route given, so a
// test can make it deliberately WRONG.
func client(t *testing.T, seed routing.Route, budget int) *serve.Client {
	t.Helper()

	c, err := serve.NewClient(serve.ClientOptions{
		Seed:         seed,
		HopBudget:    budget,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		MaxFrame:     wire.MaxFrame,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestAStaleClientIsRedirectedAndRepaired is ADR-045's falsifier.
//
// ★★ Two real servers. The client's cache points at the WRONG one, and the read
// still succeeds — because the wrong node was able to compute, FROM THE KEY IT WAS
// GIVEN, which leaf was wanted and where that leaf now lives. Afterwards the
// client's cache names the right node.
//
// ⚠ The cache assertion is the half that matters. A client that succeeded by
// trying every node it knows has not been repaired and would pay the same cost on
// the next read; only a cache that MOVED shows the redirect did its job.
func TestAStaleClientIsRedirectedAndRepaired(t *testing.T) {
	key := addr.KeyOf(tenant(), "planet-7")
	wanted, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	other := elsewhere(wanted)

	// The node that HOLDS the key, with the data on it.
	id := tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1}
	right := node(t, wanted, routing.NewTable(), ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), TxID: id, Assert: true,
	})

	// The node that does NOT, and whose table knows where the key went.
	wrongTable := routing.NewTable()
	if err := wrongTable.Insert(routing.Route{
		Prefix: wanted, NextHops: []string{right}, Epoch: 42,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	wrong := node(t, other, wrongTable)

	// ⚠ The client believes the key lives on the wrong node, at an older epoch.
	c := client(t, routing.Route{Prefix: wanted, NextHops: []string{wrong}, Epoch: 1}, 0)

	run, err := c.Read(key, "READ name FROM planet-7", registered+1)
	if err != nil {
		t.Fatalf("a stale client could not read: %v.\n"+
			"ADR-008 rule 4: a stale route is answered with a redirect, never with an error "+
			"and never with data.", err)
	}
	if len(run) != 1 || run[0].Entity != "planet-7" || string(run[0].Value) != "Kepler" {
		t.Fatalf("the answer is %v, want one planet-7/name/Kepler datom", run)
	}

	// ★ The repair. The node that was WRONG is the one that supplied it.
	route, err := c.Route(key)
	if err != nil {
		t.Fatalf("the client has no route for the key it just read: %v", err)
	}
	if route.NextHops[0] != right {
		t.Errorf("the client still points at %v after a successful read, want %s.\n"+
			"Succeeding is not being repaired: an unrepaired client pays the same redirect "+
			"on every read forever.", route.NextHops, right)
	}
	if route.Epoch != 42 {
		t.Errorf("the client's epoch is %d, want 42 — the redirect's own", route.Epoch)
	}
}

// TestTheClientImplementsClusterAndNotRouting checks the seam is the whole
// contract.
//
// ⚠ The budget assertion is the interesting half. Writing a redirect loop in the
// client is the natural thing to do — the redirect is right there in the response
// — and it would duplicate the epoch rule and the hop budget. A duplicate that is
// WRONG still redirects, so nothing fails visibly. The error therefore has to come
// from `routing`, which is what shows the budget is that package's and not this
// one's.
func TestTheClientImplementsClusterAndNotRouting(t *testing.T) {
	// The compile-time assertion, restated here so the test names the property.
	var _ routing.Cluster = (*serve.Client)(nil)

	key := addr.KeyOf(tenant(), "planet-7")
	wanted, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	other := elsewhere(wanted)

	right := node(t, wanted, routing.NewTable())

	wrongTable := routing.NewTable()
	if err := wrongTable.Insert(routing.Route{
		Prefix: wanted, NextHops: []string{right}, Epoch: 42,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	wrong := node(t, other, wrongTable)

	// A budget of ONE: the first hop is spent on the wrong node, so the redirect
	// it learns can never be followed.
	c := client(t, routing.Route{Prefix: wanted, NextHops: []string{wrong}, Epoch: 1}, 1)

	_, err = c.Read(key, "READ name FROM planet-7", registered+1)
	if !errors.Is(err, routing.ErrTooManyRedirects) {
		t.Fatalf("a budget of one hop against a stale cache = %v, want routing.ErrTooManyRedirects.\n"+
			"An error of the client's own would mean the client is counting hops, and a "+
			"second counter is a second place to be wrong.", err)
	}

	// ★ Resolve installed what it learned before the budget ran out, so even the
	// failed read left the client better off. That is `routing`'s doing, not the
	// client's.
	if route, err := c.Route(key); err != nil {
		t.Fatalf("Route: %v", err)
	} else if route.Epoch != 42 {
		t.Errorf("after an exhausted budget the client holds epoch %d, want the 42 it "+
			"was redirected to on the hop it did spend", route.Epoch)
	}

	// The bare seam works on its own: `Serve` answers the one question
	// routing.Resolve asks, and does nothing else. ⚠ A FRESH client, because the
	// one above has already been repaired and could not show a cache standing
	// still.
	bare := client(t, routing.Route{Prefix: wanted, NextHops: []string{wrong}, Epoch: 1}, 1)

	if redirect, served := bare.Serve(right, key); !served {
		t.Errorf("Serve(the node that holds the key) reported not-served, redirecting to %v",
			redirect.Route.NextHops)
	}
	redirect, served := bare.Serve(wrong, key)
	if served {
		t.Fatal("Serve(a node that does not hold the key) reported served")
	}
	if redirect.Route.Prefix != wanted || redirect.Route.Epoch != 42 {
		t.Errorf("Serve returned route %+v, want prefix %s at epoch 42", redirect.Route, wanted)
	}
	// ⚠ And it installed NOTHING. The cache is the resolution's to move, and a
	// client that installed here would be keeping a second copy of the epoch rule.
	if route, err := bare.Route(key); err != nil {
		t.Fatalf("Route: %v", err)
	} else if route.Epoch != 1 || route.NextHops[0] != wrong {
		t.Errorf("a bare Serve moved the cache to %+v; installing is routing.Resolve's job", route)
	}
}

// TestAnOlderRouteIsNotInstalled is ADR-008 rule 5, driven through the real
// client.
//
// ⚠ Through the client on purpose: `routing.Cache` already refuses an older
// route, and the property worth checking is that the transport does not work
// around it — which is only visible where it will actually be used.
func TestAnOlderRouteIsNotInstalled(t *testing.T) {
	key := addr.KeyOf(tenant(), "planet-7")
	wanted, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	other := elsewhere(wanted)

	// A node that does not hold the key and advertises an OLDER epoch for the
	// very prefix the client already holds a newer route for.
	staleTable := routing.NewTable()
	if err := staleTable.Insert(routing.Route{
		Prefix: wanted, NextHops: []string{"127.0.0.1:9"}, Epoch: 5,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	backwards := node(t, other, staleTable)

	c := client(t, routing.Route{Prefix: wanted, NextHops: []string{backwards}, Epoch: 10}, 0)

	_, err = c.Read(key, "READ name FROM planet-7", registered+1)
	if !errors.Is(err, routing.ErrTooManyRedirects) {
		t.Fatalf("a redirect to an older epoch = %v, want routing.ErrTooManyRedirects.\n"+
			"A redirect no newer than what is held cannot move the client forward, so "+
			"following it is a loop with extra steps.", err)
	}

	// ★ The cache did not move backwards, which is the whole of rule 5: a stale
	// redirect must not drag a client to a route it already knows is old.
	route, err := c.Route(key)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if route.Epoch != 10 {
		t.Errorf("the client's epoch is %d after an older redirect, want 10", route.Epoch)
	}
	if route.NextHops[0] != backwards {
		t.Errorf("the client now points at %v, want the route it started with", route.NextHops)
	}
}
