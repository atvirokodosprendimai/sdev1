package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/command"
	"github.com/atvirokodosprendimai/sdev1/internal/core/eval"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// The fixtures below are REAL records from juridiniai.jsonl — 548,547 Lithuanian
// public-procurement legal entities, 178MB, examined 2026-09-05. The corpus is
// git-ignored and never committed; these are copied in so the test is hermetic.
//
// ★ They are real on purpose. The value of this test is that the DOMAIN produced
// a multi-entity operation nobody here predicted — "Dalyvaujantis" means
// "participating", and a status whose entire content is "I am one party to an act
// involving others" is the registry telling us ADR-003's falsifier is armed.
// Inventing the case would prove only that the author could imagine it.
type registryRecord struct {
	id           string
	name         string
	registration string
	legalForm    string
	regStatus    string
	legalStatus  string // sparse: 0.4% of the corpus
	evrk         string // sparse: 14%
	employees    string // sparse: 14%
	county       string // sparse: 14%
	municipality string // sparse: 14%
}

var (
	// Reorganizuojamas — the company BEING reorganised.
	fifaa = registryRecord{
		id: "111756039", name: `UAB "FIFAA BALTIC"`, registration: "2001-12-12",
		legalForm: "Uždaroji akcinė bendrovė", regStatus: "registered",
		legalStatus: "Reorganizuojamas",
		evrk:        "477200", employees: "1",
		county: "Vilniaus apskr.", municipality: "Vilniaus m. sav.",
	}
	// Dalyvaujantis reorganizavime — PARTICIPATING in a reorganisation.
	rivona = registryRecord{
		id: "110512039", name: `Uždaroji akcinė bendrovė "RIVONA"`, registration: "1993-06-23",
		legalForm: "Uždaroji akcinė bendrovė", regStatus: "registered",
		legalStatus: "Dalyvaujantis reorganizavime",
		evrk:        "463900", employees: "1340",
		county: "Vilniaus apskr.", municipality: "Vilniaus m. sav.",
	}
	// Dalyvaujantis atskyrime — participating in a separation.
	ergolain = registryRecord{
		id: "110861884", name: `Uždaroji akcinė bendrovė "ERGOLAIN"`, registration: "2001-04-05",
		legalForm: "Uždaroji akcinė bendrovė", regStatus: "registered",
		legalStatus: "Dalyvaujantis atskyrime",
		evrk:        "702000", employees: "1",
		county: "Klaipėdos apskr.", municipality: "Klaipėdos m. sav.",
	}
)

const registryTenantDepth = uint8(2)

func registryTenant() addr.TenantID { return addr.TenantFromUint(11) }

// forever is the business interval of a fact with no stated end.
func forever(from int64) temporal.Interval {
	return temporal.Interval{From: from, To: temporal.Forever}
}

// TestARegistryRecordIsOneEntity checks the ordinary case: everything the
// registry says about one company is one entity.
func TestARegistryRecordIsOneEntity(t *testing.T) {
	txn, err := command.New(registryTenant(), fifaa.id, registryTenantDepth)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// All twelve attributes — five always present, seven sparse.
	for _, a := range []struct{ name, value string }{
		{"name", fifaa.name},
		{"registrationDate", fifaa.registration},
		{"legalForm", fifaa.legalForm},
		{"registrationStatus", fifaa.regStatus},
		{"legalStatus", fifaa.legalStatus},
		{"evrk", fifaa.evrk},
		{"employees", fifaa.employees},
		{"county", fifaa.county},
		{"municipality", fifaa.municipality},
	} {
		if err := txn.Assert(fifaa.id, a.name, []byte(a.value), forever(0)); err != nil {
			t.Fatalf("Assert(%s): %v", a.name, err)
		}
	}
	if got := len(txn.Datoms()); got != 9 {
		t.Errorf("transaction holds %d datoms, want 9", got)
	}

	// ⚠ And a datom about a DIFFERENT company is refused. The boundary is in
	// force, not merely unexercised.
	err = txn.Assert(rivona.id, "name", []byte(rivona.name), forever(0))
	if !errors.Is(err, command.ErrCrossEntity) {
		t.Errorf("a second company in one transaction = %v, want ErrCrossEntity", err)
	}
	if got := len(txn.Datoms()); got != 9 {
		t.Errorf("a refused Assert changed the transaction: %d datoms", got)
	}
}

// TestARealMultiEntityActFitsTheBoundary is ADR-044's falsifier, and ADR-003's.
//
// ★ A reorganisation genuinely spans several companies — the registry has a word
// for being one of them. The boundary holds because the ACT is an entity: it has
// a date, a kind and participants, so registering it is a single-entity write.
func TestARealMultiEntityActFitsTheBoundary(t *testing.T) {
	const act = "reorg-2026-0417"
	const registered = int64(1_700_000_000)

	txn, err := command.New(registryTenant(), act, registryTenantDepth)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The act's own facts.
	if err := txn.Assert(act, "kind", []byte("reorganizavimas"), forever(registered)); err != nil {
		t.Fatalf("Assert(kind): %v", err)
	}
	if err := txn.Assert(act, "registered", []byte("2026-04-17"), forever(registered)); err != nil {
		t.Fatalf("Assert(registered): %v", err)
	}

	// The participants, as REFERENCES. One being reorganised, two participating —
	// which is what the registry's own statuses describe.
	for _, p := range []registryRecord{fifaa, rivona, ergolain} {
		if err := txn.Assert(act, "participant:"+p.id, []byte(p.id), forever(registered)); err != nil {
			t.Fatalf("Assert(participant %s): %v", p.id, err)
		}
	}

	// ★ THE POINT: a legal act involving three companies committed as ONE
	// transaction, on one entity, resolving to one leaf.
	if got := len(txn.Datoms()); got != 5 {
		t.Fatalf("the act holds %d datoms, want 5", got)
	}
	if txn.Entity() != act {
		t.Errorf("transaction entity = %q, want the act", txn.Entity())
	}

	// ⚠ And the boundary is still in force — the act did not circumvent it. A
	// datom about a PARTICIPANT cannot ride along in the act's transaction.
	err = txn.Assert(rivona.id, "legalStatus", []byte(rivona.legalStatus), forever(registered))
	if !errors.Is(err, command.ErrCrossEntity) {
		t.Fatalf("a participant's own status rode along in the act's transaction = %v, "+
			"want ErrCrossEntity.\nThe act fitting the boundary must not mean the boundary "+
			"stopped applying.", err)
	}
}

// TestTheActsParticipantsAreReadable checks the model is usable and not merely
// storable.
//
// ⚠ This is where ADR-003's liveability turns out to rest on ADR-035. With the
// act as the entity, "which companies are in this reorganisation" is an inbound
// read. Before that record existed the normalised model could be written and then
// only reached by already knowing every participant's identifier.
func TestTheActsParticipantsAreReadable(t *testing.T) {
	ctx := context.Background()
	const act = "reorg-2026-0417"
	const registered = int64(1_700_000_000)

	store, err := leafstore.Open(t.TempDir(), registryTenant().TenantSubtree())
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Each company points AT the act — the inbound direction ADR-035 reads.
	// ⚠ One Append per entity: the transaction boundary is one entity, and these
	// are three separate writes by construction.
	for i, p := range []registryRecord{fifaa, rivona, ergolain} {
		id := tx.TxID{HLC: hlc.Timestamp{Wall: registered + int64(i)}, Seq: 1}
		member := ports.Datom{
			Entity: p.id, Attribute: "reorganisation", Value: []byte(act),
			Valid: forever(registered), TxID: id, Assert: true, IsReference: true,
		}
		named := ports.Datom{
			Entity: p.id, Attribute: "name", Value: []byte(p.name),
			Valid: forever(registered), TxID: id, Assert: true,
		}
		if err := store.Append(ctx, member, named); err != nil {
			t.Fatalf("Append(%s): %v", p.id, err)
		}
	}
	if err := store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	stmt, err := ql.Parse("READ ->name FROM [" + act + "]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rows, err := eval.Read(ctx, store, stmt.(*ql.Read), registered+1000)
	if err != nil {
		t.Fatalf("inbound read: %v", err)
	}

	if len(rows) != 3 {
		t.Fatalf("the act has %d readable participants, want 3.\n"+
			"Without the inbound read this model is storable and unqueryable — which is why "+
			"ADR-003's liveability rests on ADR-035.", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[string(r.Value)] = true
	}
	for _, p := range []registryRecord{fifaa, rivona, ergolain} {
		if !seen[p.name] {
			t.Errorf("participant %q is not readable from the act", p.name)
		}
	}
}

// TestTheDenormalisedShapeAgreesOnTheValidAxis checks what the boundary COSTS.
//
// ★ The registry's own shape puts legalStatus on each participant. Reproducing it
// takes two transactions, which are not atomic on the transaction axis — and
// bitemporality is what pays for that: both carry the act's REAL-WORLD date, so a
// reader on the valid axis sees a consistent world however the writes interleaved.
func TestTheDenormalisedShapeAgreesOnTheValidAxis(t *testing.T) {
	ctx := context.Background()
	const actDate = int64(1_700_000_000)

	store, err := leafstore.Open(t.TempDir(), registryTenant().TenantSubtree())
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	// ⚠ TWO transactions, deliberately far apart on the TRANSACTION axis — as
	// they would be if the second write happened minutes later, or after a crash.
	first := tx.TxID{HLC: hlc.Timestamp{Wall: actDate}, Seq: 1}
	second := tx.TxID{HLC: hlc.Timestamp{Wall: actDate + 999_999}, Seq: 1}

	// ★ But ONE valid-from: the date the legal act took effect. That shared
	// instant is what makes the pair consistent on the axis a reader asks about.
	if err := store.Append(ctx, ports.Datom{
		Entity: fifaa.id, Attribute: "legalStatus", Value: []byte(fifaa.legalStatus),
		Valid: forever(actDate), TxID: first, Assert: true,
	}); err != nil {
		t.Fatalf("Append(first): %v", err)
	}
	if err := store.Append(ctx, ports.Datom{
		Entity: rivona.id, Attribute: "legalStatus", Value: []byte(rivona.legalStatus),
		Valid: forever(actDate), TxID: second, Assert: true,
	}); err != nil {
		t.Fatalf("Append(second): %v", err)
	}
	if err := store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// ⚠ A REAL FINDING FROM THE REAL CORPUS: every registry identifier is
	// all-numeric — "111756039" — and a bare numeric cannot be an entity name,
	// because it lexes as a number. ★ The language already has the escape ADR-021
	// added for keywords, and it turns out to cover this too: backticks make any
	// token an identifier. Nobody anticipated needing it for IDENTIFIERS, and a
	// domain whose primary keys are integers is not unusual.
	statusAt := func(entity string, instant int64) string {
		t.Helper()
		stmt, err := ql.Parse("READ legalStatus FROM `" + entity + "` AS OF " + itoa(instant))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		rows, err := eval.Read(ctx, store, stmt.(*ql.Read), instant)
		if err != nil {
			t.Fatalf("Read(%s): %v", entity, err)
		}
		if len(rows) == 0 {
			return ""
		}
		return string(rows[0].Value)
	}

	// ★ At the act's date BOTH statuses are visible, although the writes were
	// almost a second apart on the transaction axis.
	if got := statusAt(fifaa.id, actDate+1); got != fifaa.legalStatus {
		t.Errorf("%s at the act date = %q, want %q", fifaa.id, got, fifaa.legalStatus)
	}
	if got := statusAt(rivona.id, actDate+1); got != rivona.legalStatus {
		t.Errorf("%s at the act date = %q, want %q.\n"+
			"Both facts carry the act's real-world date as Valid.From, so a reader on the "+
			"VALID axis sees a consistent world however the two writes interleaved. The "+
			"tearing exists only on the transaction axis, which is the audit axis.",
			rivona.id, got, rivona.legalStatus)
	}

	// And BEFORE the act neither status holds — so the shared valid-from is doing
	// the work, rather than the facts simply being true always.
	if got := statusAt(fifaa.id, actDate-1); got != "" {
		t.Errorf("%s before the act = %q, want nothing", fifaa.id, got)
	}
	if got := statusAt(rivona.id, actDate-1); got != "" {
		t.Errorf("%s before the act = %q, want nothing", rivona.id, got)
	}
}

// itoa renders an instant for a statement, without pulling in strconv for one call.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
