package serve

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/datom"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/wire"
)

// ⚠ The compile-time assertion is the contract. [Client] implements
// [routing.Cluster] and nothing else about resolution: the redirect following,
// the epoch rule and the hop budget all live in that package, and a second copy
// here would be a second place to be wrong.
var _ routing.Cluster = (*Client)(nil)

// ErrRefused reports that the node holding the leaf declined to serve the
// statement.
//
// ⚠ It is an error and NOT an empty result. A refusal that arrived as zero rows
// would tell a caller its statement ran and matched nothing — the same lie an
// empty answer to a write would tell, one layer up.
var ErrRefused = errors.New("serve: the node that holds the leaf refused the statement")

// ErrNoAnswer reports a resolution that ended without one.
//
// ★ It should be unreachable: [routing.Resolve] returns a destination only when a
// node reported the leaf served, and this client reports that only when it is
// holding the response. It exists so that if the two ever disagree the client
// says so rather than returning an empty run.
var ErrNoAnswer = errors.New("serve: resolution succeeded but no node answered")

// probeStatement is what a bare [Client.Serve] sends.
//
// ★ There is no ping in this protocol, and adding one would be a fourth outcome
// through the back door. "Do you hold this key" is already answerable from the
// three that exist: a node that holds the leaf answers or refuses, and a node
// that does not redirects — so a read of an entity that carries nothing settles
// the question at the cost of one empty load.
const probeStatement = "READ * FROM _probe"

// ClientOptions is what a client needs, and it has no usable zero value.
type ClientOptions struct {
	// Seed is the one route a client starts from — typically a frontdoor.
	//
	// ★ There is no bulk load, deliberately: a client's map grows only by the
	// redirects it actually followed, so its size tracks what the client used
	// rather than how large the cluster is.
	Seed routing.Route

	// HopBudget bounds a redirect chain. Zero takes [routing.DefaultHopBudget].
	HopBudget int

	// DialTimeout bounds reaching a node; ReadTimeout and WriteTimeout bound the
	// one exchange after that.
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// MaxFrame bounds a frame in either direction. See [wire.MaxFrame].
	MaxFrame int

	// TLS is how this caller proves who it is and checks the node it reached.
	//
	// ★ The certificate's Common Name is the PRINCIPAL a node reads the grant
	// set for, so this is not merely confidentiality — it is the caller's
	// identity, and there is no other way to supply one.
	TLS TLSConfig
}

// Client is one caller's view of a cluster: a route cache and a socket.
// Client is one caller's view of a cluster: a route cache and a socket.
type Client struct {
	cache  *routing.Cache
	budget int
	opts   ClientOptions
	// tls is built once at construction rather than per dial, because a pool of
	// certificates and a parsed key pair are the expensive parts and neither
	// varies per connection.
	tls *tls.Config
}

// NewClient seeds a cache and validates the options.
//
// ⚠ It refuses missing timeouts and a missing frame bound for the same reason
// [NewServer] does: an unbounded dial is a caller that hangs, and a defaulted
// bound makes "not configured" indistinguishable from "chosen".
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.DialTimeout <= 0 || opts.ReadTimeout <= 0 || opts.WriteTimeout <= 0 || opts.MaxFrame <= 0 {
		return nil, fmt.Errorf("%w: dial %v, read %v, write %v, frame %d",
			ErrNoTimeout, opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout, opts.MaxFrame)
	}
	config, err := opts.TLS.Client()
	if err != nil {
		return nil, err
	}
	cache, err := routing.NewCache(opts.Seed)
	if err != nil {
		return nil, fmt.Errorf("serve: seeding the route cache: %w", err)
	}
	budget := opts.HopBudget
	if budget < 1 {
		budget = routing.DefaultHopBudget
	}
	return &Client{cache: cache, budget: budget, opts: opts, tls: config}, nil
}

// Route is what the client currently believes about a key, which is how a caller
// can see that a redirect repaired it.
func (c *Client) Route(k addr.Key) (routing.Route, error) { return c.cache.Lookup(k) }

// Serve reports whether node holds the leaf for k, and where it says the leaf
// went when it does not.
//
// ★ This method IS [routing.Cluster], and it is the entire contract between the
// transport and the routing package. It follows nothing, installs nothing and
// counts nothing.
//
// It sends [probeStatement]. [Client.Read] takes the same path with the caller's
// own statement, so an answer arrives on the hop that resolved the key rather
// than on one more.
func (c *Client) Serve(node string, k addr.Key) (routing.Redirect, bool) {
	return (&exchange{client: c, statement: probeStatement}).Serve(node, k)
}

// Read resolves a key and returns what the node holding it answered.
//
// ★ It is a thin driver. [routing.Resolve] owns the redirect chain, the epoch
// rule and the hop budget; this function contributes the socket and nothing else.
// A redirect loop written here would duplicate all three, and a duplicate that is
// wrong still redirects — so nothing would fail visibly.
func (c *Client) Read(k addr.Key, statement string, now int64) ([]ports.Datom, error) {
	ex := &exchange{client: c, statement: statement, now: now}

	dest, err := routing.Resolve(c.cache, ex, k, c.budget)
	if err != nil {
		// ⚠ The routing error is what the caller gets, unwrappable. A transport
		// failure is added as context because "no route resolved" and "the node
		// refused the connection" are different problems for whoever is paged.
		if ex.transport != nil {
			return nil, fmt.Errorf("%w (last transport error: %v)", err, ex.transport)
		}
		return nil, err
	}

	switch {
	case ex.refusal != nil:
		return nil, fmt.Errorf("%w: %s said %q", ErrRefused, dest.Node, ex.refusal.Reason)
	case ex.answer == nil:
		return nil, fmt.Errorf("%w: %s", ErrNoAnswer, dest.Node)
	}

	// ★ Decoded from what the RESOLVING exchange already carried. Asking the
	// destination again would cost one more round trip for an answer already in
	// hand, and would let the leaf move between the two.
	run, err := datom.Decode(ex.answer.Datoms)
	if err != nil {
		return nil, fmt.Errorf("serve: decoding the answer from %s: %w", dest.Node, err)
	}
	return run, nil
}

// exchange is one in-flight statement: what to send, and what came back.
//
// ★ It exists so [Client.Read] needs no lock and no per-client mutable state. The
// statement travels with the resolution rather than beside it, which is what lets
// the answer arrive on the resolving hop.
type exchange struct {
	client    *Client
	statement string
	now       int64

	answer  *wire.Answer
	refusal *wire.Refusal
	// transport records why a hop could not be reached. ⚠ routing.Cluster has
	// nowhere to return an error, and a hop that failed for a network reason is
	// otherwise indistinguishable from one that answered "not mine".
	transport error
}

// Serve performs one exchange and translates the response.
//
// An answer or a refusal both mean the node HELD the leaf: it either produced a
// result or declined to, and neither is "go elsewhere". Only a redirect is
// not-served.
func (e *exchange) Serve(node string, k addr.Key) (routing.Redirect, bool) {
	e.answer, e.refusal = nil, nil

	resp, err := e.roundTrip(node, k)
	if err != nil {
		e.transport = fmt.Errorf("%s: %w", node, err)
		// ⚠ Not served, and a ZERO route. routing.Cache.Install refuses a route
		// with no next hops, so an unreachable node ends the resolution with
		// routing's own error naming the chain rather than being retried here —
		// retry is a cluster policy and Resolve owns the only one that exists.
		return routing.Redirect{}, false
	}

	switch v := resp.(type) {
	case *wire.Answer:
		e.answer = v
		return routing.Redirect{}, true
	case *wire.Refusal:
		e.refusal = v
		return routing.Redirect{}, true
	case *wire.Redirect:
		return routing.Redirect{Route: v.Route}, false
	default:
		e.transport = fmt.Errorf("%s: unknown outcome %s", node, resp.Outcome())
		return routing.Redirect{}, false
	}
}

// roundTrip is the whole of the wire protocol on the client side: dial, one
// framed request, one framed response, close.
func (e *exchange) roundTrip(node string, k addr.Key) (wire.Response, error) {
	opts := e.client.opts

	// ⚠ A TLS dialler, so the handshake is inside the dial timeout. Dialling in
	// the clear and upgrading afterwards would leave the handshake unbounded,
	// which is a stranger holding a goroutine again by a different route.
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: opts.DialTimeout},
		Config:    e.client.tls,
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.DialTimeout)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", node)
	if err != nil {
		return nil, fmt.Errorf("dialling: %w", err)
	}
	defer func() { _ = conn.Close() }()

	payload, err := wire.EncodeRequest(wire.Request{Key: k, Statement: e.statement, Now: e.now})
	if err != nil {
		return nil, err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(opts.WriteTimeout)); err != nil {
		return nil, err
	}
	if err := wire.WriteFrame(conn, payload, opts.MaxFrame); err != nil {
		return nil, err
	}

	if err := conn.SetReadDeadline(time.Now().Add(opts.ReadTimeout)); err != nil {
		return nil, err
	}
	body, err := wire.ReadFrame(conn, opts.MaxFrame)
	if err != nil {
		return nil, err
	}
	return wire.Decode(body)
}
