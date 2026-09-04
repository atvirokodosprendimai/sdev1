package session

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// newDurableSession returns a session backed by a leaf in dir, on a clock the
// test advances by hand.
func newDurableSession(t *testing.T, dir string, from int64) (*Session, func(int64)) {
	t.Helper()
	tenant := addr.TenantFromUint(7)
	store, err := leafstore.Open(dir, tenant.TenantSubtree())
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	wall := from
	s, err := Open(tenant, func() int64 { return wall }, store)
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, func(to int64) { wall = to }
}

func TestASessionRehydratesFromItsStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	first, _ := newDurableSession(t, dir, 1000)
	mustRun(t, first, `ASSERT planet-3 mass = "5.97e24"`)
	mustRun(t, first, `ASSERT planet-3 radius = "6371"`)
	if err := first.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A different session, on the same directory, with nothing carried over in
	// memory.
	second, _ := newDurableSession(t, dir, 5000)
	got := mustRun(t, second, `SELECT * FROM planet-3`)
	if len(got.Rows) != 2 {
		t.Fatalf("SELECT after reopening returned %d rows, want 2: %+v", len(got.Rows), got.Rows)
	}

	found := map[string]string{}
	for _, r := range got.Rows {
		found[r.Attribute] = r.Value
	}
	if found["mass"] != "5.97e24" {
		t.Errorf("mass came back as %q", found["mass"])
	}
	if found["radius"] != "6371" {
		t.Errorf("radius came back as %q", found["radius"])
	}
}

func TestRehydrationRestoresTheSearchIndexToo(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	first, _ := newDurableSession(t, dir, 1000)
	mustRun(t, first, `ASSERT planet-3 codename = "blue marble"`)
	mustRun(t, first, `ASSERT planet-3 orbits = ->star-1`)
	mustRun(t, first, `ASSERT star-1 codename = "yellow dwarf"`)
	if err := first.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, _ := newDurableSession(t, dir, 5000)

	// ⚠ This is the quiet one. A rehydration that restored the datom map and
	// forgot the index leaves SELECT working, the restart looking successful, and
	// SEARCH answering nothing at all with no error anywhere.
	hits := mustRun(t, second, `SEARCH "marble" IN codename LIMIT 10`)
	if len(hits.Hits) == 0 {
		t.Errorf("SEARCH found nothing after a restart: the datoms were rehydrated and the " +
			"search index was not, which is invisible to every other statement")
	}

	// And the link resolver, for the same reason.
	walked := mustRun(t, second, `TRAVERSE planet-3 DEPTH 2`)
	if len(walked.Reached) == 0 {
		t.Errorf("TRAVERSE reached nothing after a restart: a rehydrated reference is not being " +
			"followed")
	}
}

func TestRehydrationAdvancesTheClockPastWhatItLoaded(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	first, _ := newDurableSession(t, dir, 9000)
	old := mustRun(t, first, `ASSERT planet-3 mass = "old"`).Wrote.TxID
	if err := first.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// ⚠ A LOWER clock than the run that wrote the fact. Without observing what it
	// loaded, the session mints identifiers that sort BEFORE the rehydrated datom
	// and a new assertion quietly loses to an old one.
	second, _ := newDurableSession(t, dir, 1000)
	fresh := mustRun(t, second, `ASSERT planet-3 mass = "new"`).Wrote.TxID

	if fresh.Compare(old) <= 0 {
		t.Errorf("a write after rehydration minted %s, which does not sort after the rehydrated "+
			"%s — the clock did not observe what it loaded", fresh, old)
	}

	// ★ Asserted on the identifiers rather than through a SELECT, on purpose. The
	// two axes are independent: winding the clock back to 1000 also winds BUSINESS
	// time back, and a fact valid from 9000 is legitimately not true at 1000. A
	// SELECT here would be measuring the business axis while claiming to measure
	// the system one, and it would fail for a reason that is correct behaviour.
}

// TestTheSessionReaderHonoursItsSnapshot exercises the reader DIRECTLY, because
// nothing else can see whether it does.
//
// ⚠ [eval.Select] filters again with the query the parser resolved, which is the
// authoritative form — so a reader that ignored its snapshot entirely would still
// produce the right rows through a statement, and a mutant that removed this
// filter survived every other test in this package. The obligation is real
// nonetheless: `ports.Reader.Load` promises datoms visible at a snapshot, and any
// other consumer would be relying on it.
func TestTheSessionReaderHonoursItsSnapshot(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestSession()
	mustRun(t, s, `ASSERT planet-3 mass = "5" VALID FROM 500 TO 900`)

	// Bounded well past anything the session minted, so only the business axis
	// decides these two answers.
	future := tx.TxID{HLC: hlc.Timestamp{Wall: 1 << 62}}
	r := memoryReader{s}

	inside, err := r.Load(ctx, "planet-3", ports.Snapshot{At: future, ValidAt: 600})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(inside) != 1 {
		t.Errorf("Load at an instant inside the validity returned %d datoms, want 1", len(inside))
	}

	outside, err := r.Load(ctx, "planet-3", ports.Snapshot{At: future, ValidAt: 1500})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(outside) != 0 {
		t.Errorf("Load at an instant after the validity ended returned %d datoms, want none — "+
			"the reader is ignoring the snapshot it was handed", len(outside))
	}
}

func TestASelectWithAStoreReadsThroughTheStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tenant := addr.TenantFromUint(7)

	store, err := leafstore.Open(dir, tenant.TenantSubtree())
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	s, err := Open(tenant, func() int64 { return 1000 }, store)
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// ⚠ Appended to the STORE behind the session's back, so this fact exists in
	// the store and NOT in the session's rehydrated map. It is the only
	// observation that tells the two read paths apart: with both populated they
	// give the same answer, and a session that quietly answered from its own map
	// would look correct forever.
	//
	// It is deliberately not a supported topology — one leaf has one fenced
	// writer — but it is the read path stated as something a test can see.
	if err := store.Append(ctx, ports.Datom{
		Entity: "planet-9", Attribute: "mass", Value: []byte("1.9e27"),
		Valid: temporal.Interval{From: 0, To: temporal.Forever},
		TxID:  tx.TxID{HLC: hlc.Timestamp{Wall: 500}, Seq: 1}, Assert: true,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got := mustRun(t, s, `SELECT * FROM planet-9`)
	if len(got.Rows) != 1 {
		t.Fatalf("SELECT returned %d rows for a fact only the store holds; the session is "+
			"answering from its own map rather than through the store", len(got.Rows))
	}
	if got.Rows[0].Value != "1.9e27" {
		t.Errorf("value came back as %q", got.Rows[0].Value)
	}
}

func TestAWriteReachesTheStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	tenant := addr.TenantFromUint(7)
	store, err := leafstore.Open(dir, tenant.TenantSubtree())
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	wall := int64(1000)
	s, err := Open(tenant, func() int64 { return wall }, store)
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	mustRun(t, s, `ASSERT planet-3 mass = "5.97e24"`)

	if store.Pending() != 1 {
		t.Fatalf("the store's tail holds %d datoms after one ASSERT, want 1", store.Pending())
	}
	if store.Segments() != 0 {
		t.Errorf("a write produced %d segments; ADR-020 puts the commit point in memory, so "+
			"nothing should have reached a disk yet", store.Segments())
	}

	if err := s.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if store.Pending() != 0 || store.Segments() != 1 {
		t.Errorf("after Seal: %d pending, %d segments; want 0 and 1", store.Pending(), store.Segments())
	}

	history, err := store.History("planet-3")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 || string(history[0].Value) != "5.97e24" {
		t.Errorf("the store holds %+v, want the one asserted fact", history)
	}
}

func TestASessionWithNoStoreIsUnchanged(t *testing.T) {
	s, _ := newTestSession()

	mustRun(t, s, `ASSERT planet-3 mass = "5.97e24"`)
	got := mustRun(t, s, `SELECT * FROM planet-3`)
	if len(got.Rows) != 1 {
		t.Fatalf("SELECT returned %d rows, want 1", len(got.Rows))
	}
	hits := mustRun(t, s, `SEARCH "5.97e24" IN mass LIMIT 5`)
	if len(hits.Hits) == 0 {
		t.Errorf("SEARCH found nothing in a session with no store")
	}

	// ⚠ No-ops rather than refusals, so a caller need not ask which kind of
	// session it is holding.
	if err := s.Seal(context.Background()); err != nil {
		t.Errorf("Seal on a session with no store = %v, want nil", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close on a session with no store = %v, want nil", err)
	}
}
