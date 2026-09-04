package subscribe

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/lease"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tail"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// filledTail returns a tail holding n entries, sequenced 1..n.
func filledTail(t *testing.T, n int) *tail.Tail {
	t.Helper()
	tl := tail.New()
	var leaf addr.LeafID
	leaf.Prefix[0] = 0x21
	leaf.Depth = 1
	l, err := lease.NewRegistry().Grant(leaf, "writer")
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	for seq := 1; seq <= n; seq++ {
		id := tx.TxID{HLC: hlc.Timestamp{Wall: int64(seq) * 1000, Logical: uint32(seq)}, Seq: uint32(seq)}
		d := []ports.Datom{{Entity: fmt.Sprintf("e%d", seq), Attribute: "a", Assert: true}}
		if _, err := tl.Append(l.Epoch, id, d); err != nil {
			t.Fatalf("Append %d: %v", seq, err)
		}
	}
	return tl
}

// countingSink acknowledges at most `accept` entries per delivery and records
// everything it was shown, so a test can prove nothing was skipped.
type countingSink struct {
	name   string
	accept int
	seen   []uint32
	calls  int
}

func (s *countingSink) Name() string { return s.name }

func (s *countingSink) Consume(entries []tail.Entry) int {
	s.calls++
	take := len(entries)
	if s.accept >= 0 && s.accept < take {
		take = s.accept
	}
	for _, e := range entries {
		s.seen = append(s.seen, e.TxID.Seq)
	}
	return take
}

// TestCursorAdvancesOnlyPastAcknowledged checks the cursor stops where the sink
// stopped.
//
// ⚠ The sink acknowledges a PREFIX and refuses the rest. A sink that
// acknowledged everything would leave the cursor's stopping point untested, and
// the assertion would hold for a cursor that simply jumped to the watermark.
func TestCursorAdvancesOnlyPastAcknowledged(t *testing.T) {
	tl := filledTail(t, 10)
	reg := NewRegistry()
	sink := &countingSink{name: "backup", accept: 4}
	sub, err := reg.Register(sink)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	took := sub.Deliver(tl, tl.Watermark())
	if took != 4 {
		t.Fatalf("the sink acknowledged %d entries, want 4", took)
	}
	if !sub.Cursor.Started {
		t.Fatal("the cursor did not start after a delivery")
	}
	if sub.Cursor.At.Seq != 4 {
		t.Errorf("the cursor is at %d, want 4 — it must not move past what the sink "+
			"acknowledged, or a crashed sink resumes beyond what it processed", sub.Cursor.At.Seq)
	}

	// The next delivery starts at 5, not at 1 and not at the watermark.
	sink.seen = nil
	sink.accept = 3
	if took := sub.Deliver(tl, tl.Watermark()); took != 3 {
		t.Fatalf("the second delivery acknowledged %d, want 3", took)
	}
	if len(sink.seen) == 0 || sink.seen[0] != 5 {
		t.Errorf("the second delivery started at %v, want it to begin at 5", sink.seen)
	}
	if sub.Cursor.At.Seq != 7 {
		t.Errorf("the cursor is at %d after two deliveries, want 7", sub.Cursor.At.Seq)
	}

	// A sink that acknowledges nothing leaves the cursor exactly where it was.
	sink.accept = 0
	before := sub.Cursor
	if took := sub.Deliver(tl, tl.Watermark()); took != 0 {
		t.Errorf("a sink acknowledging nothing reported %d taken", took)
	}
	if sub.Cursor != before {
		t.Errorf("the cursor moved from %+v to %+v with nothing acknowledged", before, sub.Cursor)
	}
}

// TestCrashedSinkResumesWithoutSkipping checks the union of what a sink saw
// across a crash covers the whole range with no gap.
//
// ⚠ Redelivery starts from the CURSOR, not from the beginning. Redelivering
// everything would make the sink see the whole range regardless of whether the
// cursor skipped, and the test would prove nothing.
func TestCrashedSinkResumesWithoutSkipping(t *testing.T) {
	const total = 25
	tl := filledTail(t, total)
	reg := NewRegistry()

	sink := &countingSink{name: "backup", accept: 9}
	sub, err := reg.Register(sink)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// First pass: the sink takes 9 and then "crashes".
	sub.Deliver(tl, tl.Watermark())
	firstPass := append([]uint32(nil), sink.seen...)
	if len(firstPass) != total {
		t.Fatalf("the first delivery showed %d entries, want %d", len(firstPass), total)
	}
	acknowledged := sub.Cursor.At.Seq
	if acknowledged != 9 {
		t.Fatalf("the cursor is at %d after the crash, want 9", acknowledged)
	}

	// It comes back and takes the rest.
	sink.seen = nil
	sink.accept = -1
	sub.Deliver(tl, tl.Watermark())
	secondPass := sink.seen

	// Every entry past the cursor was redelivered, and the two passes together
	// cover 1..total with no gap.
	covered := map[uint32]bool{}
	for _, s := range firstPass {
		covered[s] = true
	}
	for _, s := range secondPass {
		covered[s] = true
	}
	for seq := uint32(1); seq <= total; seq++ {
		if !covered[seq] {
			t.Fatalf("entry %d was never delivered; a backup missing entries looks exactly "+
				"like a complete one", seq)
		}
	}
	if len(secondPass) == 0 || secondPass[0] != acknowledged+1 {
		t.Errorf("the resumed delivery began at %v, want it to begin at %d — resuming from the "+
			"start would prove nothing about the cursor", secondPass, acknowledged+1)
	}
	if sub.Cursor.At.Seq != total {
		t.Errorf("the cursor is at %d after catching up, want %d", sub.Cursor.At.Seq, total)
	}

	// Caught up: nothing more is delivered.
	sink.seen = nil
	if took := sub.Deliver(tl, tl.Watermark()); took != 0 || len(sink.seen) != 0 {
		t.Errorf("a caught-up sink was given %d entries and took %d", len(sink.seen), took)
	}
}

// TestCursorIsATransactionIdentifier checks the position is expressed in the
// order the rest of the system uses.
func TestCursorIsATransactionIdentifier(t *testing.T) {
	typ := reflect.TypeOf(Cursor{})
	at, ok := typ.FieldByName("At")
	if !ok {
		t.Fatal("Cursor has no At field")
	}
	if at.Type != reflect.TypeOf(tx.TxID{}) {
		t.Errorf("Cursor.At is %s, want tx.TxID — an offset is meaningless after compaction "+
			"and cannot be compared with anything else the system orders", at.Type)
	}

	// "Nothing consumed yet" is distinct from position zero, because the zero
	// identifier is a valid position.
	var fresh Cursor
	zeroID := tx.TxID{}
	if !fresh.After(zeroID) {
		t.Error("a fresh cursor treats the zero identifier as already consumed; " +
			"'nothing yet' and 'at position zero' must be different states")
	}

	started := Cursor{At: tx.TxID{HLC: hlc.Timestamp{Wall: 5000, Logical: 5}, Seq: 5}, Started: true}
	older := tx.TxID{HLC: hlc.Timestamp{Wall: 4000, Logical: 4}, Seq: 4}
	newer := tx.TxID{HLC: hlc.Timestamp{Wall: 6000, Logical: 6}, Seq: 6}
	if started.After(older) {
		t.Error("an entry older than the cursor is reported as undelivered")
	}
	if started.After(started.At) {
		t.Error("the cursor's own entry is reported as undelivered; it was acknowledged")
	}
	if !started.After(newer) {
		t.Error("an entry newer than the cursor is reported as delivered")
	}
}

// TestUnregisteredSinkIsUnreachable checks registration is what makes a sink
// visible to a purge.
func TestUnregisteredSinkIsUnreachable(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Register(&countingSink{name: "backup", accept: -1}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := reg.Lookup("console"); !errors.Is(err, ErrUnknownSink) {
		t.Errorf("an unregistered sink: error = %v, want ErrUnknownSink", err)
	}
	sinks := reg.Sinks()
	if len(sinks) != 1 || sinks[0] != "backup" {
		t.Errorf("Sinks() = %v, want only the registered one", sinks)
	}

	// Registering it makes it appear in the set a purge enumerates.
	if _, err := reg.Register(&countingSink{name: "console", accept: -1}); err != nil {
		t.Fatalf("Register console: %v", err)
	}
	if got := reg.Sinks(); len(got) != 2 {
		t.Errorf("Sinks() = %v, want both", got)
	}

	// A nameless sink cannot be registered, since a purge could not report on it.
	if _, err := reg.Register(&countingSink{name: "", accept: -1}); err == nil {
		t.Error("a sink with no name was registered")
	}
}

// TestDuplicateRegistrationIsRefused checks a second sink cannot inherit the
// first's cursor.
func TestDuplicateRegistrationIsRefused(t *testing.T) {
	tl := filledTail(t, 10)
	reg := NewRegistry()

	first := &countingSink{name: "backup", accept: -1}
	sub, err := reg.Register(first)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	sub.Deliver(tl, tl.Watermark())
	if sub.Cursor.At.Seq != 10 {
		t.Fatalf("the first sink is at %d, want 10", sub.Cursor.At.Seq)
	}

	second := &countingSink{name: "backup", accept: -1}
	if _, err := reg.Register(second); !errors.Is(err, ErrDuplicateSink) {
		t.Fatalf("a duplicate name: error = %v, want ErrDuplicateSink — a second sink "+
			"inheriting the first's cursor would skip everything before it and its backup "+
			"would look complete", err)
	}
	if second.calls != 0 {
		t.Error("the refused sink was delivered to")
	}
	if got := reg.Sinks(); len(got) != 1 {
		t.Errorf("Sinks() = %v after a refused duplicate, want one", got)
	}
}
