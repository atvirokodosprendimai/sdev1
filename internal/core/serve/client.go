package serve

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
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

	// Pool bounds what may be kept between reads.
	//
	// ★ Required, because TLS made a connection expensive enough that whether one
	// is kept is a decision rather than a detail — and an unbounded pool looks
	// exactly like an unconfigured one until it runs a process out of
	// descriptors.
	Pool PoolBounds

	// Now is injectable so a test can age an idle connection without sleeping.
	// Nil takes the wall clock.
	Now func() time.Time
}

// Client is one caller's view of a cluster: a route cache, a pool and a socket.
type Client struct {
	cache  *routing.Cache
	budget int
	opts   ClientOptions
	// certs is the certificate material, RE-READ PER DIAL.
	//
	// ⚠ It used to be a `*tls.Config` built once, which meant a rotated
	// certificate was never picked up. The cost of rebuilding is a file read
	// against a handshake that already does asymmetric crypto, and the
	// alternative is a client that quietly presents an expired certificate
	// forever.
	certs *certs.Source
	pool  *Pool
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
	source, err := opts.TLS.Source()
	if err != nil {
		return nil, err
	}
	pool, err := NewPool(opts.Pool, opts.Now)
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
	return &Client{cache: cache, budget: budget, opts: opts, certs: source, pool: pool}, nil
}

// Idle is how many warm connections this client holds for a node.
//
// ★ An ordinary pool gauge, and the only way to see the difference between a
// connection that was DISCARDED after a failed exchange and one that was quietly
// put back. A returned-but-broken connection is invisible to every assertion
// about content: it sits in the pool looking exactly like a good one until some
// later read draws it.
func (c *Client) Idle(node string) int { return c.pool.Len(node) }

// Close releases every pooled connection.
//
// ⚠ A client that is discarded without this leaks one descriptor per warm
// connection, and the leak is invisible until a process runs out.
func (c *Client) Close() error { return c.pool.Close() }

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
	return (&exchange{client: c, statement: probeStatement, now: c.instant()}).Serve(node, k)
}

// instant is the business moment a probe carries.
//
// ⚠ It must be a REAL instant. A probe sent with zero was harmless while nothing
// authorized — and became a refusal for every caller the moment ADR-046 landed,
// because the node reads the grant set at the instant it is given and no grant is
// valid at the epoch. A bare `Serve` then reported every node as "served", since
// a refusal means the node HELD the leaf.
func (c *Client) instant() int64 {
	if c.opts.Now != nil {
		return c.opts.Now().Unix()
	}
	return time.Now().Unix()
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

// roundTrip performs one exchange, reusing a pooled connection when there is one.
//
// ★★ THE CONNECTION IS RETURNED TO THE POOL AT EXACTLY ONE PLACE: after the
// response has been fully read AND successfully decoded. Every other exit closes
// it. A connection whose stream position is unknown cannot be resynchronised —
// the next thing read from it would be a length prefix taken from the middle of
// something else, and that is the one number ADR-045 bounds precisely because a
// stranger chooses it.
//
// ⚠ A decode failure closes the connection even though the transport read
// succeeded. That is the case worth stating: it reads like a caller-side problem
// and it is not, because "these bytes were not what was expected" is exactly the
// same information as "where the next frame starts is unknown".
func (e *exchange) roundTrip(node string, k addr.Key) (wire.Response, error) {
	payload, err := wire.EncodeRequest(wire.Request{Key: k, Statement: e.statement, Now: e.now})
	if err != nil {
		return nil, err
	}

	// ⚠ ONE retry, and only here. A pooled connection may have been closed by
	// the far end while it was idle, with no signal until the write — so a
	// failure at the FIRST WRITE on a REUSED connection is the pool admitting
	// its own cache went stale, not a cluster policy. It must never extend to a
	// failure after the request was sent: that request may already have been
	// served, and re-sending it would be this transport inventing a retry
	// policy that `routing.Resolve` alone is allowed to own.
	conn, reused, err := e.connect(node)
	if err != nil {
		return nil, err
	}
	resp, err := e.on(conn, node, payload)
	if err != nil && reused && errors.Is(err, errWriteFailed) {
		conn, _, err = e.dial(node)
		if err != nil {
			return nil, err
		}
		resp, err = e.on(conn, node, payload)
	}
	return resp, err
}

// errWriteFailed marks the one failure a stale pooled connection is allowed to
// be retried after, because nothing can have happened yet.
var errWriteFailed = errors.New("serve: the request could not be sent")

// connect returns a pooled connection when one is waiting, and dials otherwise.
func (e *exchange) connect(node string) (net.Conn, bool, error) {
	if conn := e.client.pool.Get(node); conn != nil {
		return conn, true, nil
	}
	return e.dial(node)
}

// dial opens a new authenticated connection.
//
// ⚠ A TLS dialler, so the handshake is inside the dial timeout. Dialling in the
// clear and upgrading afterwards would leave the handshake unbounded, which is a
// stranger holding a goroutine again by a different route.
func (e *exchange) dial(node string) (net.Conn, bool, error) {
	opts := e.client.opts

	// ⚠ The config is rebuilt HERE, per dial, so a replaced certificate and a
	// replaced authority pool are both picked up without a restart. Building it
	// once at construction is what ADR-046 did and what ADR-047 rule 4 changes.
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: opts.DialTimeout},
		Config:    e.client.certs.Client(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.DialTimeout)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", node)
	if err != nil {
		return nil, false, fmt.Errorf("dialling: %w", err)
	}
	return conn, false, nil
}

// on runs one exchange over a connection it either keeps or closes.
func (e *exchange) on(conn net.Conn, node string, payload []byte) (wire.Response, error) {
	opts := e.client.opts
	kept := false
	defer func() {
		if !kept {
			_ = conn.Close()
		}
	}()

	if err := conn.SetWriteDeadline(time.Now().Add(opts.WriteTimeout)); err != nil {
		return nil, fmt.Errorf("%w: %v", errWriteFailed, err)
	}
	if err := wire.WriteFrame(conn, payload, opts.MaxFrame); err != nil {
		return nil, fmt.Errorf("%w: %v", errWriteFailed, err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(opts.ReadTimeout)); err != nil {
		return nil, err
	}
	body, err := wire.ReadFrame(conn, opts.MaxFrame)
	if err != nil {
		return nil, err
	}
	resp, err := wire.Decode(body)
	if err != nil {
		return nil, err
	}

	// ★ HERE, and nowhere else. A complete frame, fully decoded, so the stream
	// is known to be at a boundary.
	kept = true
	e.client.pool.Put(node, conn)
	return resp, nil
}
