package serve_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/serve"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
	"github.com/atvirokodosprendimai/sdev1/internal/core/wire"
)

// pooledNode is a node holding planet-7's leaf, with its accept count exposed.
//
// ★★ Counting ACCEPTS is the only honest measure of reuse, and it is the
// server's own count rather than anything the client reports. Asserting the
// pool's length instead would pass for a pool that stores a connection and hands
// it out to nobody: the numbers would look right and every read would still
// dial. One accept is one TLS handshake, which is the cost being paid down.
func pooledNode(t *testing.T, seed ...ports.Datom) (address string, accepts func() int64) {
	t.Helper()

	key := addr.KeyOf(tenant(), "planet-7")
	held, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}

	at, srv := serverWithTLS(t, held, routing.NewTable(), sharedCA.issue(t, "node"), seed...)
	return at, srv.Accepted
}

// TestAConnectionIsReusedAcrossExchanges is ADR-046 rule 7's positive half.
func TestAConnectionIsReusedAcrossExchanges(t *testing.T) {
	id := tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1}
	address, accepts := pooledNode(t, ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), TxID: id, Assert: true,
	})

	key := addr.KeyOf(tenant(), "planet-7")
	held, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	c := client(t, routing.Route{Prefix: held, NextHops: []string{address}, Epoch: 1}, 0)

	for i := range 3 {
		run, err := c.Read(key, "READ name FROM planet-7", registered+1)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if len(run) != 1 {
			t.Fatalf("read %d returned %d datoms, want 1", i, len(run))
		}
	}

	if got := accepts(); got != 1 {
		t.Errorf("three reads cost %d accepted connections, want 1.\n"+
			"A TLS handshake is asymmetric crypto on both sides, which is the cost pooling "+
			"exists to pay once.", got)
	}
}

// TestAFailedExchangeDiscardsItsConnection is rule 7's important half, over the
// failure that actually happens: a pooled connection the far end has quietly
// dropped.
//
// ⚠ **A REFUSAL IS NOT A FAILED EXCHANGE**, and the first version of this test
// got that wrong. A node refusing a write sends a complete, well-formed response;
// the stream is still exactly at a frame boundary, so keeping that connection is
// correct and the pool does. What breaks a connection is the frame not arriving —
// here, because the server's read deadline expired while the connection sat idle
// in the pool, which is what a firewall or a load balancer does in production and
// says nothing about.
//
// ★ The assertion is that the NEXT read SUCCEEDS. "The pool is empty" would be
// the wrong assertion: a connection stored with an unknown stream position does
// not announce itself, it corrupts what is read from it afterwards.
func TestAFailedExchangeDiscardsItsConnection(t *testing.T) {
	key := addr.KeyOf(tenant(), "planet-7")
	held, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}

	store, err := leafstore.Open(t.TempDir(), held)
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	id := tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1}
	if err := store.Append(ctx, ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), TxID: id, Assert: true,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// ⚠ A SHORT server read deadline, so an idle pooled connection is dropped by
	// the far end within the test rather than in twenty minutes. This is the
	// production case reproduced, not a special one invented for the test.
	const idleDrop = 150 * time.Millisecond
	srv, err := serve.NewServer(serve.Options{
		Addr: "127.0.0.1:0", Leaf: held, Store: store, Table: routing.NewTable(),
		ReadTimeout: idleDrop, WriteTimeout: 5 * time.Second,
		MaxFrame: wire.MaxFrame, TLS: sharedCA.issue(t, "node"), Grants: sharedGrants,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve(ctx) }()

	c := client(t, routing.Route{Prefix: held, NextHops: []string{srv.Addr()}, Epoch: 1}, 0)

	// One good read, so there is a warm connection in the pool.
	if _, err := c.Read(key, "READ name FROM planet-7", registered+1); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	if got := srv.Accepted(); got != 1 {
		t.Fatalf("the first read cost %d connections, want 1", got)
	}

	// Let the far end drop it. The client is told nothing.
	time.Sleep(idleDrop * 3)

	// ★ The read still succeeds: the stale connection is discarded and redialled
	// (rule 7 plus S5's single pre-send retry), rather than being written to and
	// then read from at an unknown offset.
	run, err := c.Read(key, "READ name FROM planet-7", registered+2)
	if err != nil {
		t.Fatalf("the read after the peer dropped a pooled connection: %v.\n"+
			"A connection whose stream position is unknown must be discarded, and a write "+
			"that never left is the one failure it is safe to retry.", err)
	}
	if len(run) != 1 || string(run[0].Value) != "Kepler" {
		t.Errorf("the read after a dropped connection returned %v", run)
	}
	if got := srv.Accepted(); got < 2 {
		t.Errorf("only %d connection(s) were accepted, so the dead one was reused "+
			"rather than replaced", got)
	}

	// ★★ THE ASSERTION THAT ACTUALLY BINDS. Succeeding is not enough: a client
	// that put the dead connection BACK and then dialled a fresh one would pass
	// every check above — the read works, the answer is right, and two
	// connections were accepted. The pool would simply hold a corpse, invisible
	// until some later read drew it.
	//
	// ⚠ This test passed without this check, and a mutant that pooled the
	// connection on every exit survived it.
	if got := c.Idle(srv.Addr()); got != 1 {
		t.Errorf("the client holds %d idle connections, want 1 — the failed one was "+
			"returned to the pool instead of being closed", got)
	}
}

// TestPoolBoundsAreRequired is ADR-046 rule 9.
func TestPoolBoundsAreRequired(t *testing.T) {
	conf := sharedCA.issue(t, "reader")
	base := serve.ClientOptions{
		Seed:         routing.Route{Prefix: addr.LeafID{Depth: 1}, NextHops: []string{"127.0.0.1:9"}, Epoch: 1},
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		MaxFrame:     wire.MaxFrame,
		TLS:          conf,
		Pool:         PoolBoundsForTest,
	}

	for _, c := range []struct {
		name   string
		bounds serve.PoolBounds
	}{
		{"nothing declared", serve.PoolBounds{}},
		{"no idle count", serve.PoolBounds{IdleTimeout: time.Minute}},
		{"negative idle count", serve.PoolBounds{MaxIdlePerNode: -1, IdleTimeout: time.Minute}},
		{"no idle timeout", serve.PoolBounds{MaxIdlePerNode: 4}},
		{"negative idle timeout", serve.PoolBounds{MaxIdlePerNode: 4, IdleTimeout: -time.Second}},
	} {
		opts := base
		opts.Pool = c.bounds
		if cl, err := serve.NewClient(opts); !errors.Is(err, serve.ErrNoPoolBounds) {
			if cl != nil {
				_ = cl.Close()
			}
			t.Errorf("NewClient with %s = %v, want ErrNoPoolBounds", c.name, err)
		}
		if _, err := serve.NewPool(c.bounds, nil); !errors.Is(err, serve.ErrNoPoolBounds) {
			t.Errorf("NewPool with %s = %v, want ErrNoPoolBounds", c.name, err)
		}
	}

	// The declared bounds still build, so the refusals are about what they name.
	cl, err := serve.NewClient(base)
	if err != nil {
		t.Fatalf("fully declared bounds were refused: %v", err)
	}
	if err := cl.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestAnIdleConnectionIsClosedAfterItsLifetime drives the clock rather than the
// wall.
//
// ⚠ A test that sleeps for a timeout is slow always and flaky on a loaded
// machine. The clock is injected for exactly this.
func TestAnIdleConnectionIsClosedAfterItsLifetime(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	pool, err := serve.NewPool(serve.PoolBounds{MaxIdlePerNode: 4, IdleTimeout: time.Minute}, clock)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })

	pool.Put("node-a", left)
	if pool.Len("node-a") != 1 {
		t.Fatalf("the pool holds %d, want 1", pool.Len("node-a"))
	}
	if got := pool.Get("node-a"); got == nil {
		t.Fatal("a fresh connection was not handed out")
	}

	// Put it back, then age it past the bound.
	pool.Put("node-a", left)
	advance(time.Minute + time.Second)

	if got := pool.Get("node-a"); got != nil {
		t.Error("a connection idle past its lifetime was handed out.\n" +
			"A peer or a firewall closes an idle connection silently, so keeping one past " +
			"that turns a saved handshake into a failed write.")
	}
	if pool.Len("node-a") != 0 {
		t.Errorf("the expired connection is still held (%d)", pool.Len("node-a"))
	}

	// ⚠ And it was CLOSED rather than merely dropped, or the descriptor leaks.
	if _, err := left.Write([]byte{0}); err == nil {
		t.Error("the expired connection was dropped without being closed")
	}
}
