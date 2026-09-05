package serve

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// ErrNoPoolBounds reports a pool whose limits were not declared.
//
// ⚠ There is no default. An unbounded pool and an unconfigured one are
// indistinguishable from outside, and the unbounded one is a file-descriptor
// leak that appears only under load — which is the worst time to discover a
// configuration question nobody answered.
var ErrNoPoolBounds = errors.New("serve: a maximum idle count and an idle lifetime must both be declared and positive")

// PoolBounds is what a pool is allowed to keep.
type PoolBounds struct {
	// MaxIdlePerNode caps idle connections held for ONE node. Per node rather
	// than in total, because a client that has learned many routes should not
	// let one busy node evict every other node's warm connection.
	MaxIdlePerNode int

	// IdleTimeout is how long an unused connection may be kept.
	//
	// ★ A peer, a load balancer or a firewall will close an idle connection
	// eventually and say nothing. Keeping one past that turns a saved handshake
	// into a failed write, so this should be shorter than whatever the network
	// in front of the cluster does.
	IdleTimeout time.Duration
}

// idle is a connection waiting to be used again, and when it stopped being used.
type idle struct {
	conn net.Conn
	at   time.Time
}

// Pool keeps connections that are known to be at a frame boundary.
//
// ★★ THE INVARIANT IS THE WHOLE TYPE: a connection is in this pool only if the
// last thing that happened on it was a COMPLETE, successfully decoded exchange.
// Anything else — a failed write, a short read, a frame error, a decode error, a
// deadline — closes it instead.
//
// ⚠ The temptation is to keep a connection after a DECODE error, because the
// transport read succeeded and it reads like a caller-side problem. It is not: a
// decode failure means the bytes were not what was expected, so where the next
// frame begins is unknown. Reading from an unknown offset means taking a length
// prefix out of the middle of somebody's payload, which is the one input ADR-045
// bounds precisely because a stranger chooses it.
//
// It is safe for concurrent use.
type Pool struct {
	bounds PoolBounds
	// now is injectable so an idle-lifetime test does not have to sleep.
	now func() time.Time

	mu   sync.Mutex
	idle map[string][]idle
}

// NewPool refuses undeclared bounds and returns a pool.
func NewPool(bounds PoolBounds, now func() time.Time) (*Pool, error) {
	if bounds.MaxIdlePerNode <= 0 || bounds.IdleTimeout <= 0 {
		return nil, fmt.Errorf("%w: max idle %d, idle timeout %v",
			ErrNoPoolBounds, bounds.MaxIdlePerNode, bounds.IdleTimeout)
	}
	if now == nil {
		now = time.Now
	}
	return &Pool{bounds: bounds, now: now, idle: map[string][]idle{}}, nil
}

// Get returns a connection for node, or nil when there is none worth reusing.
//
// ⚠ Nil is an ordinary answer and not an error: the caller dials. Returning an
// error for "the pool is empty" would make a cold client look broken.
func (p *Pool) Get(node string) net.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()

	waiting := p.idle[node]
	for len(waiting) > 0 {
		// Newest first: the most recently used connection is the one least
		// likely to have been closed by the far end while nobody was looking.
		last := len(waiting) - 1
		candidate := waiting[last]
		waiting = waiting[:last]

		if p.now().Sub(candidate.at) >= p.bounds.IdleTimeout {
			_ = candidate.conn.Close()
			continue
		}
		p.idle[node] = waiting
		return candidate.conn
	}
	delete(p.idle, node)
	return nil
}

// Put offers a connection back.
//
// ⚠ CALL IT ONLY AFTER A COMPLETE, SUCCESSFULLY DECODED EXCHANGE. There is no
// way for the pool to check that, which is why the rule lives at the one call
// site rather than here — see [exchange.roundTrip].
func (p *Pool) Put(node string, conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.idle[node]) >= p.bounds.MaxIdlePerNode {
		// At the bound, so this one is closed rather than kept. ★ The NEW
		// connection is the one closed, which keeps the already-warm ones warm.
		defer func() { _ = conn.Close() }()
		return
	}
	p.idle[node] = append(p.idle[node], idle{conn: conn, at: p.now()})
}

// Close closes every connection the pool holds.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for node, waiting := range p.idle {
		for _, c := range waiting {
			_ = c.conn.Close()
		}
		delete(p.idle, node)
	}
	return nil
}

// Len is how many idle connections are held for a node, for a test.
func (p *Pool) Len(node string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle[node])
}
