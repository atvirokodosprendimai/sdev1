package serve_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/datom"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/serve"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
	"github.com/atvirokodosprendimai/sdev1/internal/core/wire"
)

// leafDepth is deeper than the tenant subtree, so two entities of one tenant can
// live on different leaves — which is what a redirect needs to be possible at all.
const leafDepth = uint8(3)

const registered = int64(1_700_000_000)

func tenant() addr.TenantID { return addr.TenantFromUint(11) }

func forever(from int64) temporal.Interval {
	return temporal.Interval{From: from, To: temporal.Forever}
}

// elsewhere returns a leaf that is NOT the one given, by moving its last prefix
// byte.
//
// ★ It is derived rather than searched for: a hash is deterministic, so a fixture
// that hunts for a non-colliding name is a loop that either always runs once or
// always fails, dressed up as a search.
func elsewhere(leaf addr.LeafID) addr.LeafID {
	other := leaf
	other.Prefix[leaf.Depth-1]++
	return other
}

// node starts a server on a free loopback port over a real leaf store, and
// returns its address.
//
// ⚠ `t.TempDir` and a real store, not an in-memory reader: a server over a fake
// would not exercise the path an operator runs.
func node(t *testing.T, holds addr.LeafID, table *routing.Table, seed ...ports.Datom) string {
	t.Helper()

	store, err := leafstore.Open(t.TempDir(), holds)
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if len(seed) > 0 {
		if err := store.Append(ctx, seed...); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := store.Seal(ctx); err != nil {
			t.Fatalf("Seal: %v", err)
		}
	}

	srv, err := serve.NewServer(serve.Options{
		Addr:         "127.0.0.1:0",
		Leaf:         holds,
		Store:        store,
		Table:        table,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		MaxFrame:     wire.MaxFrame,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// ⚠ Closed by cleanup so a failing test does not leak a listener into the
	// next one.
	t.Cleanup(func() { _ = srv.Close() })

	go func() { _ = srv.Serve(ctx) }()
	return srv.Addr()
}

// ask runs one exchange the way ADR-045 says a connection is used: dial, one
// framed request, one framed response, close.
func ask(t *testing.T, address string, req wire.Request) wire.Response {
	t.Helper()

	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", address, err)
	}
	defer func() { _ = conn.Close() }()

	payload, err := wire.EncodeRequest(req)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if err := wire.WriteFrame(conn, payload, wire.MaxFrame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	body, err := wire.ReadFrame(conn, wire.MaxFrame)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	resp, err := wire.Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return resp
}

// TestANodeRedirectsForAKeyItDoesNotHold is ADR-045 rule 2, and the server half
// of the record's falsifier.
//
// ★★ The server is given ONLY the request. It has never heard of this client and
// holds no record of what the client believed — it descends the key it was handed
// against its own leaf, finds the key is not its, and looks the route up in its
// own table. A request naming a leaf would leave it holding a name it could not
// invert, with nothing to send back but an error.
func TestANodeRedirectsForAKeyItDoesNotHold(t *testing.T) {
	key := addr.KeyOf(tenant(), "planet-7")
	wanted, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	held := elsewhere(wanted)
	if held == wanted {
		t.Fatal("the fixture put both leaves at the same address, so nothing would be redirected")
	}

	table := routing.NewTable()
	if err := table.Insert(routing.Route{
		Prefix: wanted, NextHops: []string{"127.0.0.1:9", "127.0.0.1:10"}, Epoch: 42,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	address := node(t, held, table)
	resp := ask(t, address, wire.Request{
		Key: key, Statement: "READ name FROM planet-7", Now: registered + 1,
	})

	redirect, ok := resp.(*wire.Redirect)
	if !ok {
		t.Fatalf("a node holding %s, asked for a key on %s, answered %T (%s).\n"+
			"ADR-008 rule 4: a stale route is answered with a redirect, never with an error "+
			"and never with data.", held, wanted, resp, resp.Outcome())
	}
	if redirect.Route.Prefix != wanted {
		t.Errorf("redirected to prefix %s, want %s", redirect.Route.Prefix, wanted)
	}
	if redirect.Route.Epoch != 42 {
		t.Errorf("epoch %d, want 42. ⚠ Without it a redirect cannot be ordered, and "+
			"two stale nodes can bounce a client between them forever.", redirect.Route.Epoch)
	}
	if got := redirect.Route.NextHops; len(got) != 2 || got[0] != "127.0.0.1:9" {
		t.Errorf("next hops %v, want the table's two in order", got)
	}
}

// TestAReadIsServedFromARealLeaf is the ordinary case, end to end over a socket.
func TestAReadIsServedFromARealLeaf(t *testing.T) {
	key := addr.KeyOf(tenant(), "planet-7")
	held, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}

	id := tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1}
	fact := ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), TxID: id, Assert: true,
	}
	orbits := ports.Datom{
		Entity: "planet-7", Attribute: "orbits", Value: []byte("star-1"),
		Valid: forever(registered), TxID: id, Assert: true, IsReference: true,
	}

	address := node(t, held, routing.NewTable(), fact, orbits)
	resp := ask(t, address, wire.Request{
		Key: key, Statement: "READ * FROM planet-7", Now: registered + 1,
	})

	answer, ok := resp.(*wire.Answer)
	if !ok {
		t.Fatalf("a node holding %s, asked for a key on it, answered %T (%s)", held, resp, resp.Outcome())
	}
	run, err := datom.Decode(answer.Datoms)
	if err != nil {
		t.Fatalf("decoding the answer as an ADR-025 datom run: %v", err)
	}
	if len(run) != 2 {
		t.Fatalf("the answer carries %d datom(s), want 2", len(run))
	}

	by := map[string]ports.Datom{}
	for _, d := range run {
		by[d.Attribute] = d
	}
	name, ok := by["name"]
	if !ok {
		t.Fatalf("the answer carries %v, not the name", by)
	}
	if name.Entity != "planet-7" || string(name.Value) != "Kepler" {
		t.Errorf("name datom = %s/%s, want planet-7/Kepler", name.Entity, name.Value)
	}
	// ★ Both temporal coordinates survive the crossing. A zero interval would be
	// a fact that acquired an end at the epoch with nothing about it looking
	// unusual — which is exactly why datom.Encode writes both endpoints in full.
	if name.Valid != forever(registered) {
		t.Errorf("valid interval %s, want %s. A row that dropped it would arrive as a "+
			"fact true only at the epoch.", name.Valid, forever(registered))
	}
	if name.TxID != id {
		t.Errorf("TxID %+v, want %+v", name.TxID, id)
	}
	if !name.Assert {
		t.Error("the datom arrived retracted; a projection carries only asserted facts")
	}
	// ⚠ "star-1" as a name and "star-1" as a link are the same six bytes, and
	// only this flag tells them apart.
	if name.IsReference {
		t.Error("a plain value arrived as a reference")
	}
	if !by["orbits"].IsReference {
		t.Error("a reference arrived as a plain value, so an edge became data in transit")
	}
}

// TestAWriteOverTheWireIsRefused is ADR-045 rule 5.
//
// ⚠ The shape matters more than the refusal. An empty ANSWER would be read as
// "the write ran and matched nothing", which is a client believing a fact was
// recorded that was not.
func TestAWriteOverTheWireIsRefused(t *testing.T) {
	key := addr.KeyOf(tenant(), "planet-7")
	held, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	address := node(t, held, routing.NewTable())

	for _, statement := range []string{
		"ASSERT planet-7 mass = 5972",
		"RETRACT planet-7 mass = 5972",
	} {
		resp := ask(t, address, wire.Request{Key: key, Statement: statement, Now: registered + 1})

		refusal, ok := resp.(*wire.Refusal)
		if !ok {
			if answer, isAnswer := resp.(*wire.Answer); isAnswer {
				t.Fatalf("%q was ANSWERED with %d payload bytes.\n"+
					"An answer to a write is the dangerous shape: a client reads no rows and "+
					"concludes the write landed. There is no leader, so it would have been "+
					"unfenced (ADR-009) and committed at a durability nobody has (ADR-020).",
					statement, len(answer.Datoms))
			}
			t.Fatalf("%q answered %T (%s), want a refusal", statement, resp, resp.Outcome())
		}
		// Named, so a caller can tell "I will not" from "I could not parse that".
		if !strings.Contains(refusal.Reason, serve.ErrWriteNotServed.Error()) {
			t.Errorf("%q refused with %q, which does not name ErrWriteNotServed", statement, refusal.Reason)
		}
	}
}

// TestAServerNeedsDeclaredTimeouts is ADR-045 rule 6.
//
// ⚠ At construction, where a caller sees it — not at the first connection that
// hangs. A connection with no deadline is a goroutine a stranger can pin forever,
// and nothing about the process looks wrong while it happens.
func TestAServerNeedsDeclaredTimeouts(t *testing.T) {
	good := serve.Options{
		Addr:         "127.0.0.1:0",
		Leaf:         addr.LeafID{Depth: 1},
		Store:        emptyReader{},
		Table:        routing.NewTable(),
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		MaxFrame:     wire.MaxFrame,
	}

	for _, c := range []struct {
		name   string
		break_ func(*serve.Options)
	}{
		{"no read timeout", func(o *serve.Options) { o.ReadTimeout = 0 }},
		{"negative read timeout", func(o *serve.Options) { o.ReadTimeout = -time.Second }},
		{"no write timeout", func(o *serve.Options) { o.WriteTimeout = 0 }},
		{"negative write timeout", func(o *serve.Options) { o.WriteTimeout = -time.Second }},
		{"no frame bound", func(o *serve.Options) { o.MaxFrame = 0 }},
		{"negative frame bound", func(o *serve.Options) { o.MaxFrame = -1 }},
	} {
		opts := good
		c.break_(&opts)
		srv, err := serve.NewServer(opts)
		if !errors.Is(err, serve.ErrNoTimeout) {
			if srv != nil {
				_ = srv.Close()
			}
			t.Errorf("%s = %v, want ErrNoTimeout", c.name, err)
		}
	}

	// ★ And a node must say which leaf it holds. Depth 0 is the ROOT, which
	// contains every key — a node claiming it would serve every request from one
	// leaf's store and never redirect, and would look correctly placed until a
	// second node existed to be redirected to.
	rootish := good
	rootish.Leaf = addr.LeafID{}
	if srv, err := serve.NewServer(rootish); !errors.Is(err, serve.ErrNoLeaf) {
		if srv != nil {
			_ = srv.Close()
		}
		t.Errorf("a server claiming the root leaf = %v, want ErrNoLeaf", err)
	}

	// The valid options still bind, so the refusals above are about what they name.
	srv, err := serve.NewServer(good)
	if err != nil {
		t.Fatalf("fully declared options were refused: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// emptyReader is a store with nothing in it, for the construction checks that
// never read.
type emptyReader struct{}

func (emptyReader) Load(context.Context, string, ports.Snapshot) ([]ports.Datom, error) {
	return nil, nil
}

func (emptyReader) Attributes(context.Context, string, ports.Snapshot) ([]string, error) {
	return nil, nil
}
