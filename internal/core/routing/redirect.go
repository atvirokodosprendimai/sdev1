package routing

import (
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

// DefaultHopBudget bounds a redirect chain.
//
// Epochs make a loop impossible in a correct cluster, so this is not the primary
// defence — it is what keeps a client from spinning forever when a node is
// wrong, and the number is small because a correct cluster never needs more than
// one or two.
const DefaultHopBudget = 8

// ErrTooManyRedirects reports a chain that did not resolve.
//
// It names the chain, because the useful question is which node is lying and
// that is answerable only from the sequence.
var ErrTooManyRedirects = errors.New("routing: the redirect chain did not resolve")

// Redirect is what a node returns when it does not hold the leaf.
//
// ⚠ It carries a route and NOTHING that could be read as data. The moment a
// redirect can carry an answer, a stale route can serve one — which is the exact
// failure redirecting exists to prevent.
type Redirect struct {
	// Route is where the receiving node believes the leaf now is.
	Route Route
}

// Destination is a resolved answer: the node to talk to, and the route that
// chose it.
//
// ★ It is a different type from [Redirect] on purpose. "Go elsewhere" and "here
// is your node" must not be interchangeable, and the type system is the cheapest
// place to say so.
type Destination struct {
	Node  string
	Route Route
}

// Cluster is what a resolution asks: does this node hold the leaf, and if not,
// where does it say the leaf went.
//
// It is declared here, where it is consumed, and implemented by whatever
// actually talks to nodes.
type Cluster interface {
	// Serve reports whether node holds the leaf for k. When it does not, the
	// returned Redirect says where the node believes the leaf is.
	Serve(node string, k addr.Key) (Redirect, bool)
}

// Cache is a client's partial route table.
//
// ★ It has no bulk load, and that absence is the design. A client cannot obtain
// the cluster's map because nothing here offers one: the cache starts with a
// single frontdoor route and grows only by the redirects it actually followed.
// Its size therefore tracks what the client used, not how large the cluster is.
type Cache struct {
	table *Table
}

// NewCache returns a cache seeded with one route — typically the default route
// naming a frontdoor, which is all a client needs to start.
func NewCache(seed Route) (*Cache, error) {
	t := NewTable()
	if err := t.Insert(seed); err != nil {
		return nil, err
	}
	return &Cache{table: t}, nil
}

// Install adds a learned route, and reports whether it was taken.
//
// ⚠ A route is taken only if its epoch is STRICTLY newer than one already held
// for the same prefix. Accepting an equal epoch would let two nodes flap a
// client between two routes forever with neither being newer; accepting an older
// one would let a stale redirect drag a client backwards and leave it wrong
// until something unrelated corrected it.
func (c *Cache) Install(r Route) bool {
	if len(r.NextHops) == 0 {
		return false
	}
	if held, ok := c.table.routes[r.Prefix]; ok && r.Epoch <= held.Epoch {
		return false
	}
	if err := c.table.Insert(r); err != nil {
		return false
	}
	return true
}

// Lookup returns the route the client currently believes.
func (c *Cache) Lookup(k addr.Key) (Route, error) { return c.table.Lookup(k) }

// Len is how many routes the client has learned.
func (c *Cache) Len() int { return c.table.Len() }

// Resolve finds the node to talk to, following redirects and learning as it
// goes.
//
// ★ A stale route yields a redirect, never an error and never data. Refusing
// would make every topology change an outage for every client that had not yet
// heard; answering anyway would be silently wrong, served by a node that no
// longer holds the leaf.
//
// ⚠ It never returns a [Redirect]. A redirect is internal to resolution, and a
// caller that could receive one could mistake it for an answer.
func Resolve(c *Cache, cl Cluster, k addr.Key, budget int) (Destination, error) {
	if budget < 1 {
		budget = DefaultHopBudget
	}

	chain := make([]string, 0, budget)
	for hop := 0; hop < budget; hop++ {
		route, err := c.Lookup(k)
		if err != nil {
			return Destination{}, err
		}
		node := route.NextHops[0]
		chain = append(chain, node)

		redirect, served := cl.Serve(node, k)
		if served {
			return Destination{Node: node, Route: route}, nil
		}

		// A redirect that is not newer than what we already hold cannot move us
		// forward, so following it is a loop with extra steps. Stop here rather
		// than spending the whole budget proving it.
		if !c.Install(redirect.Route) {
			return Destination{}, fmt.Errorf(
				"%w: %s redirected to a route no newer than the one already held (epoch %d), "+
					"after chain %v", ErrTooManyRedirects, node, redirect.Route.Epoch, chain)
		}
	}

	return Destination{}, fmt.Errorf("%w: %d hops without resolving, chain %v",
		ErrTooManyRedirects, budget, chain)
}
