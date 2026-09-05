package serve_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/authz"
	"github.com/atvirokodosprendimai/sdev1/internal/core/certs"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/serve"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
	"github.com/atvirokodosprendimai/sdev1/internal/core/wire"
)

// grantLeaf is a real leaf holding the reserved tenant's grant datoms.
//
// ★ A real `leafstore`, not a fake reader: grants are datoms, and the point of
// ADR-033 rule 1 is that they travel the same path as everything else. A fake
// here would prove the wiring against something the wiring does not use.
type grantLeaf struct {
	store *leafstore.Store
	seq   uint32
}

func newGrantLeaf(t *testing.T) *grantLeaf {
	t.Helper()

	store, err := leafstore.Open(t.TempDir(), authz.SystemTenant.TenantSubtree())
	if err != nil {
		t.Fatalf("opening the grant leaf: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &grantLeaf{store: store}
}

// grant files a grant and seals it, so it is readable.
func (g *grantLeaf) grant(t *testing.T, principal string, tn addr.TenantID, at int64) {
	t.Helper()
	g.write(t, authz.GrantDatom, principal, tn, at)
}

// revoke retracts one. ★ ADR-033: revocation is a RETRACTION, not a delete.
func (g *grantLeaf) revoke(t *testing.T, principal string, tn addr.TenantID, at int64) {
	t.Helper()
	g.write(t, authz.RevokeDatom, principal, tn, at)
}

// deny files a certificate denial into the same reserved-tenant leaf.
//
// ★ The same leaf as the grants, and a different entity space: a grant's entity
// is a principal, a denial's is `cert:<serial>`. They are different statements
// about different things and they share the machinery, which is the whole reason
// a denial is a datom rather than a CRL.
func (g *grantLeaf) deny(t *testing.T, serial string, until time.Time, reason string, at int64) {
	t.Helper()

	g.seq++
	d, err := certs.DenyDatom(serial, until, reason,
		tx.TxID{HLC: hlc.Timestamp{Wall: at}, Seq: g.seq}, at)
	if err != nil {
		t.Fatalf("DenyDatom: %v", err)
	}

	ctx := context.Background()
	if err := g.store.Append(ctx, d); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := g.store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}
}

func (g *grantLeaf) write(t *testing.T,
	make func(string, addr.TenantID, authz.Capability, tx.TxID, int64) (ports.Datom, error),
	principal string, tn addr.TenantID, at int64) {
	t.Helper()

	g.seq++
	id := tx.TxID{HLC: hlc.Timestamp{Wall: at}, Seq: g.seq}
	d, err := make(principal, tn, authz.Read, id, at)
	if err != nil {
		t.Fatalf("building a grant datom: %v", err)
	}
	ctx := context.Background()
	if err := g.store.Append(ctx, d); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := g.store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}
}

// authorizedNode is a node holding planet-7's leaf, reading grants from g at a
// clock the test controls.
func authorizedNode(t *testing.T, g *grantLeaf, now func() int64, seed ...ports.Datom) (*serve.Server, addr.LeafID) {
	t.Helper()

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
	if len(seed) > 0 {
		if err := store.Append(ctx, seed...); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := store.Seal(ctx); err != nil {
			t.Fatalf("Seal: %v", err)
		}
	}

	srv, err := serve.NewServer(serve.Options{
		Addr: "127.0.0.1:0", Leaf: held, Store: store, Grants: g.store,
		Table: routing.NewTable(), ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, MaxFrame: wire.MaxFrame,
		TLS: sharedCA.issue(t, "node"), Now: now,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve(ctx) }()
	return srv, held
}

// clockAt returns a node clock a test can move.
func clockAt(start int64) (now func() int64, set func(int64)) {
	var mu sync.Mutex
	at := start
	return func() int64 {
			mu.Lock()
			defer mu.Unlock()
			return at
		}, func(to int64) {
			mu.Lock()
			defer mu.Unlock()
			at = to
		}
}

// TestRevocationReachesALiveCertificate is ADR-046's falsifier.
//
// ★★ THE CONNECTION IS NEVER CLOSED AND THE CERTIFICATE NEVER CHANGES. A read
// succeeds, the grant is retracted, and the SAME client reading over the SAME
// pooled connection is refused.
//
// ⚠ Revoke-then-redial would pass against a design that established authority at
// handshake time — which is exactly the design being ruled out, and exactly what
// a capability carried in an X.509 extension would give. A certificate is valid
// until it expires; only reading the grant set per request makes a retraction
// reach a caller who is already connected.
func TestRevocationReachesALiveCertificate(t *testing.T) {
	const (
		granted = int64(1_700_000_000)
		revoked = granted + 500
	)

	g := newGrantLeaf(t)
	g.grant(t, "reader-1", tenant(), granted)

	// The NODE's clock, which the caller cannot touch.
	now, setNow := clockAt(revoked - 1)

	id := tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1}
	srv, held := authorizedNode(t, g, now, ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), TxID: id, Assert: true,
	})

	key := addr.KeyOf(tenant(), "planet-7")
	c := clientWithTLS(t, routing.Route{Prefix: held, NextHops: []string{srv.Addr()}, Epoch: 1},
		0, sharedCA.issue(t, "reader-1"))

	// Granted: the read works.
	run, err := c.Read(key, "READ name FROM planet-7", revoked-1)
	if err != nil {
		t.Fatalf("a granted principal could not read: %v", err)
	}
	if len(run) != 1 || string(run[0].Value) != "Kepler" {
		t.Fatalf("the granted read returned %v", run)
	}

	// ⚠ The connection is now WARM and stays warm across the revocation.
	if c.Idle(srv.Addr()) != 1 {
		t.Fatalf("the client holds %d idle connections, so the second read would "+
			"redial and this test would prove nothing", c.Idle(srv.Addr()))
	}
	before := srv.Accepted()

	g.revoke(t, "reader-1", tenant(), revoked)
	setNow(revoked + 1)

	// ★ The same certificate, the same connection, and now refused.
	if _, err := c.Read(key, "READ name FROM planet-7", revoked+1); err == nil {
		t.Fatal("a revoked principal still read, over a connection that was never closed.\n" +
			"Authority must be read from the grant set per request. If it were established " +
			"at handshake time — or carried in the certificate — a retraction would report " +
			"success and change nothing until the certificate expired.")
	} else if !strings.Contains(err.Error(), authz.ErrNotGranted.Error()) {
		t.Errorf("the refusal was %v, which does not name ErrNotGranted", err)
	}

	// ★★ AND THE CALLER CANNOT LIE ITS WAY BACK IN. `wire.Request.Now` is chosen
	// by the client; if the node loaded the grant set at THAT instant, sending a
	// second before the revocation would make the retraction datom not yet valid,
	// the grant still carried, and the read authorized.
	//
	// ⚠ This is not hypothetical — it is what this code did until the node's own
	// clock replaced the request's. Every other assertion in this test passed
	// while it was true, because the test's client was honest.
	if _, err := c.Read(key, "READ name FROM planet-7", granted+1); err == nil {
		t.Fatal("a revoked principal read by naming an instant before its revocation.\n" +
			"The caller may choose which moment of the DATA to ask about. It must not " +
			"choose which moment of the GRANTS it is judged by.")
	}

	// ⚠ And it really was the same connection: no new handshake happened, so the
	// refusal cannot be explained by anything the TLS layer re-checked.
	if got := srv.Accepted(); got != before {
		t.Errorf("%d new connection(s) were accepted between the two reads, so the "+
			"revocation may have been noticed at handshake time rather than per request",
			got-before)
	}
}

// TestAnUngrantedPrincipalIsRefused checks the shape of the refusal.
func TestAnUngrantedPrincipalIsRefused(t *testing.T) {
	const granted = int64(1_700_000_000)

	g := newGrantLeaf(t)
	g.grant(t, "reader-1", tenant(), granted)

	id := tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1}
	nodeNow, _ := clockAt(granted + 1)
	srv, held := authorizedNode(t, g, nodeNow, ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), TxID: id, Assert: true,
	})

	key := addr.KeyOf(tenant(), "planet-7")
	route := routing.Route{Prefix: held, NextHops: []string{srv.Addr()}, Epoch: 1}

	// A perfectly valid certificate from the declared authority, naming somebody
	// with no grant at all.
	stranger := clientWithTLS(t, route, 0, sharedCA.issue(t, "reader-2"))
	_, err := stranger.Read(key, "READ name FROM planet-7", granted+1)
	if err == nil {
		t.Fatal("a principal with no grant was served")
	}
	// ⚠ A REFUSAL, never an empty answer. An empty answer would tell the caller
	// its statement ran and matched nothing, which is a different and wrong fact.
	if !errors.Is(err, serve.ErrRefused) {
		t.Errorf("an ungranted read failed with %v, want serve.ErrRefused — an empty "+
			"answer would read as 'permitted, nothing matched'", err)
	}

	// The positive control: the granted principal, same node, same statement.
	allowed := clientWithTLS(t, route, 0, sharedCA.issue(t, "reader-1"))
	if _, err := allowed.Read(key, "READ name FROM planet-7", granted+1); err != nil {
		t.Fatalf("the granted principal was refused by the same node: %v.\n"+
			"The refusal above therefore proves nothing about the grant.", err)
	}
}

// TestANodeWithoutGrantsRefusesEveryRead is ADR-033 rule 5, at construction.
//
// ★ There is no running node in this state to test the request path of, and that
// is the design: a node that cannot read grants never starts.
func TestANodeWithoutGrantsRefusesEveryRead(t *testing.T) {
	srv, err := serve.NewServer(serve.Options{
		Addr: "127.0.0.1:0", Leaf: addr.LeafID{Depth: 1}, Store: emptyReader{},
		Table: routing.NewTable(), ReadTimeout: time.Second, WriteTimeout: time.Second,
		MaxFrame: wire.MaxFrame, TLS: sharedCA.issue(t, "node"),
	})
	if !errors.Is(err, serve.ErrNoGrants) {
		if srv != nil {
			_ = srv.Close()
		}
		t.Fatalf("a node with no grant source = %v, want ErrNoGrants.\n"+
			"An unconfigured grant store is not a special case — it is exactly when a "+
			"system fails open, because the thing that would say no is what is missing.", err)
	}
}

// TestTheSystemTenantIsNotReadableOverTheWire is ADR-046 rule 6.
//
// ⚠ The node in this test HOLDS the grant leaf and the caller is granted
// everything it could be granted. If the fixture did not place that leaf here,
// the read would fail because there was nothing to read and the test would pass
// for the wrong reason.
func TestTheSystemTenantIsNotReadableOverTheWire(t *testing.T) {
	const granted = int64(1_700_000_000)

	g := newGrantLeaf(t)
	g.grant(t, "reader-1", tenant(), granted)

	// ★ The node's OWN leaf is the system tenant's subtree, and its store IS the
	// grant leaf — so every grant datom is locally readable by construction.
	system := authz.SystemTenant.TenantSubtree()
	srv, err := serve.NewServer(serve.Options{
		Addr: "127.0.0.1:0", Leaf: system, Store: g.store, Grants: g.store,
		Table: routing.NewTable(), ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, MaxFrame: wire.MaxFrame,
		TLS: sharedCA.issue(t, "node"),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve(context.Background()) }()

	// A key in tenant 0000 — the grants' own tenant.
	key := addr.KeyOf(authz.SystemTenant, "reader-1")
	c := clientWithTLS(t, routing.Route{Prefix: system, NextHops: []string{srv.Addr()}, Epoch: 1},
		0, sharedCA.issue(t, "reader-1"))

	_, err = c.Read(key, "READ * FROM reader-1", granted+1)
	if err == nil {
		t.Fatal("the grant table was served over the wire.\n" +
			"Set.Allow refusing tenant 0000 does not cover this: reading the grant leaf is " +
			"an ordinary read, so a node holding it would serve every principal's authority.")
	}
	if !strings.Contains(err.Error(), serve.ErrSystemTenant.Error()) {
		t.Errorf("the refusal was %v, which does not name ErrSystemTenant", err)
	}
}

// TestAPastQueryIsAuthorizedByThePresent is ADR-033 rule 3, over the wire.
//
// ★ The tempting symmetry is to authorize a historical question against the
// grants in force at that instant — the data is historical, so why not the
// permissions. It makes revocation unable to reach backwards, so revoking access
// leaves the revoked party reading last year forever, and the system reports the
// revocation as successful.
func TestAPastQueryIsAuthorizedByThePresent(t *testing.T) {
	const (
		granted = int64(1_700_000_000)
		revoked = granted + 500
	)

	g := newGrantLeaf(t)
	g.grant(t, "reader-1", tenant(), granted)

	id := tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1}
	// ★ The node's clock is AFTER the revocation, which is what "the present"
	// means here. The statement's `AS OF` reaches back before it.
	nodeNow, _ := clockAt(revoked + 1)
	srv, held := authorizedNode(t, g, nodeNow, ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), TxID: id, Assert: true,
	})

	key := addr.KeyOf(tenant(), "planet-7")
	c := clientWithTLS(t, routing.Route{Prefix: held, NextHops: []string{srv.Addr()}, Epoch: 1},
		0, sharedCA.issue(t, "reader-1"))

	g.revoke(t, "reader-1", tenant(), revoked)

	// ⚠ `AS OF` an instant when the grant WAS live. The request's own instant
	// must reach the evaluator and never the decision.
	_, err := c.Read(key, "READ name FROM planet-7 AS OF "+strconv.FormatInt(revoked-100, 10), revoked+1)
	if err == nil {
		t.Fatal("a revoked principal read the past.\n" +
			"Authorizing a historical query against historical grants means revocation " +
			"never reaches backwards: revoking access today would leave the revoked party " +
			"able to read everything up to the moment of revocation, forever.")
	}
	if !strings.Contains(err.Error(), authz.ErrNotGranted.Error()) {
		t.Errorf("the refusal was %v, which does not name ErrNotGranted", err)
	}
}
