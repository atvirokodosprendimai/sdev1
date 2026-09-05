package watch

import (
	"errors"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/observe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/subscribe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tail"
)

const day = int64(24 * time.Hour)

func purgeObligation(subject string) Obligation {
	return Obligation{
		Kind:    observe.KindPurgeIncomplete,
		Subject: subject,
		Detail:  map[string]string{"outstanding": "archive"},
	}
}

// TestAMonthOldObligationIsStillReported is ADR-038's falsifier.
//
// ⚠ The retention horizon is CONSTRUCTED here on purpose, and then not passed to
// anything. A test that merely aged an obligation and found it present would
// prove the horizon is unused only by omission — which is not a proof, because
// omission is exactly what a later "consistency" change would undo.
func TestAMonthOldObligationIsStillReported(t *testing.T) {
	l := NewLedger()

	raised := int64(0)
	if err := l.Raise(purgeObligation("subject-7"), raised); err != nil {
		t.Fatalf("Raise: %v", err)
	}

	// ADR-010's retention horizon: thirty days. The obligation is thirty-one days
	// old. If the horizon reached the obligation set, this is exactly where the
	// system would start answering "nothing is outstanding".
	horizon := subscribe.Horizon{Nanos: 30 * day}
	now := raised + 31*day
	if now-raised <= horizon.Nanos {
		t.Fatal("the fixture is not past the horizon, so it tests nothing")
	}

	out := l.Outstanding(now)
	if len(out) != 1 {
		t.Fatalf("a %d-day-old obligation under a %d-day horizon is reported %d time(s), want 1.\n"+
			"Retention bounds the LOG and never the obligation set — otherwise an old problem "+
			"stops being reported BECAUSE it is old, and the system answers \"nothing is "+
			"outstanding\" precisely when that answer is most wrong.",
			(now-raised)/day, horizon.Nanos/day, len(out))
	}

	// ★ And its age is TRUE, not clamped to the horizon.
	if got := out[0].Age; got != 31*day {
		t.Errorf("age = %d days, want 31", got/day)
	}
	if out[0].Subject != "subject-7" || out[0].Kind != observe.KindPurgeIncomplete {
		t.Errorf("outstanding = %+v, want the purge obligation for subject-7", out[0])
	}
}

// TestARetryDoesNotResetTheAge is ADR-038 rule 6.
func TestARetryDoesNotResetTheAge(t *testing.T) {
	l := NewLedger()

	first := int64(1000)
	if err := l.Raise(purgeObligation("subject-7"), first); err != nil {
		t.Fatalf("Raise: %v", err)
	}

	// A daily retry that keeps failing, for a month.
	for n := int64(1); n <= 30; n++ {
		o := purgeObligation("subject-7")
		o.Detail = map[string]string{"outstanding": "archive", "attempt": "retry"}
		if err := l.Raise(o, first+n*day); err != nil {
			t.Fatalf("Raise %d: %v", n, err)
		}
	}

	if got := l.Len(); got != 1 {
		t.Fatalf("31 raises of one condition produced %d obligations, want 1 — the same "+
			"condition about the same subject is ONE obligation, not a stream of them", got)
	}

	out := l.Outstanding(first + 30*day)
	if got := out[0].Age; got != 30*day {
		t.Errorf("age = %d days, want 30.\n"+
			"A purge that retries daily and fails daily must not look one day old forever: "+
			"age is the whole signal, so resetting it disables the mechanism while leaving it "+
			"apparently working.", got/day)
	}

	// ★ But the DETAIL is current. Without this assertion, "keeps the first
	// raised time" would also be satisfied by ignoring the re-raise entirely,
	// which loses the up-to-date list of who is still outstanding.
	if got := out[0].Detail["attempt"]; got != "retry" {
		t.Errorf("detail = %v, want the latest raise's detail — the current list of "+
			"outstanding sinks is news even though the age is not", out[0].Detail)
	}
}

// TestOnlyAnAcknowledgementClearsIt is ADR-038 rule 2.
func TestOnlyAnAcknowledgementClearsIt(t *testing.T) {
	l := NewLedger()
	if err := l.Raise(purgeObligation("subject-7"), 0); err != nil {
		t.Fatalf("Raise: %v", err)
	}

	// A year passes. Nothing changes.
	if got := len(l.Outstanding(365 * day)); got != 1 {
		t.Errorf("after a year, %d outstanding, want 1 — time does not resolve anything", got)
	}

	// ⚠ Acknowledging something that is not outstanding ERRORS. A silent success
	// would let a mistyped subject read as "dealt with", which is worse than the
	// obligation simply remaining.
	err := l.Acknowledge(observe.KindPurgeIncomplete, "subject-8", "operator", 365*day)
	if !errors.Is(err, ErrNotOutstanding) {
		t.Errorf("acknowledging an unknown subject = %v, want ErrNotOutstanding", err)
	}
	if got := l.Len(); got != 1 {
		t.Errorf("a failed acknowledgement changed the ledger: %d outstanding", got)
	}

	// An acknowledgement must name WHO.
	if err := l.Acknowledge(observe.KindPurgeIncomplete, "subject-7", "", 365*day); err == nil {
		t.Error("an anonymous acknowledgement was accepted; it must name who dealt with it")
	}

	// And the real thing clears it.
	if err := l.Acknowledge(observe.KindPurgeIncomplete, "subject-7", "operator", 365*day); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if got := l.Len(); got != 0 {
		t.Errorf("%d outstanding after acknowledgement, want 0", got)
	}

	// Acknowledging twice is refused, so a second operator is told it was already
	// handled rather than silently succeeding.
	if err := l.Acknowledge(observe.KindPurgeIncomplete, "subject-7", "operator", 365*day); !errors.Is(err, ErrNotOutstanding) {
		t.Errorf("a second acknowledgement = %v, want ErrNotOutstanding", err)
	}
}

// TestTheOldestUnansweredThingIsFirst is ADR-038 rule 4.
func TestTheOldestUnansweredThingIsFirst(t *testing.T) {
	l := NewLedger()

	// ⚠ Raised OUT OF chronological order. Raising them in order would make
	// insertion order and age order the same, and the assertion would pass with
	// no sorting at all.
	for _, c := range []struct {
		subject string
		at      int64
	}{
		{"middle", 10 * day},
		{"newest", 20 * day},
		{"oldest", 1 * day},
	} {
		if err := l.Raise(purgeObligation(c.subject), c.at); err != nil {
			t.Fatalf("Raise(%s): %v", c.subject, err)
		}
	}

	out := l.Outstanding(30 * day)
	want := []string{"oldest", "middle", "newest"}
	for i, w := range want {
		if out[i].Subject != w {
			t.Fatalf("outstanding[%d] = %q, want %q (order: %v).\n"+
				"The question is whether an OLD unanswered thing reaches somebody, and "+
				"newest-first buries it further every day.",
				i, out[i].Subject, w, subjects(out))
		}
	}
	if out[0].Age != 29*day {
		t.Errorf("the oldest reports age %d days, want 29", out[0].Age/day)
	}

	// Acknowledging the middle one leaves the order intact.
	if err := l.Acknowledge(observe.KindPurgeIncomplete, "middle", "operator", 30*day); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	out = l.Outstanding(30 * day)
	if len(out) != 2 || out[0].Subject != "oldest" || out[1].Subject != "newest" {
		t.Errorf("after acknowledging the middle one, got %v, want [oldest newest]", subjects(out))
	}
}

// TestAnIncompletePurgeBecomesAnObligation is ADR-038 rule 7.
//
// ★ Built from a REAL purge over a real registry rather than a hand-made struct,
// so the field ADR-010 actually populates is the field this reads.
func TestAnIncompletePurgeBecomesAnObligation(t *testing.T) {
	reg := subscribe.NewRegistry()

	// A sink that CAN forget, and one that cannot. ADR-010 makes the second leave
	// every purge incomplete, which is the condition being watched.
	if _, err := reg.Register(forgetful{name: "replica"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := reg.Register(forgetless{name: "archive"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result := subscribe.Mark(reg, "subject-7")
	if result.State != subscribe.PurgeIncomplete {
		t.Fatalf("the purge is %v, want incomplete — the fixture does not produce the "+
			"condition being watched", result.State)
	}

	o, ok := FromPurge(result)
	if !ok {
		t.Fatal("an incomplete purge produced no obligation")
	}
	if o.Kind != observe.KindPurgeIncomplete || o.Subject != "subject-7" {
		t.Errorf("obligation = %+v, want a purge-incomplete about subject-7", o)
	}
	// ★ It names WHO to chase. ADR-010 computed this and it was being discarded.
	if got := o.Detail["outstanding"]; got != "archive" {
		t.Errorf("detail[outstanding] = %q, want \"archive\" — an obligation an operator "+
			"cannot act on is a notification, not an obligation", got)
	}
	if got := o.Detail["verb"]; got != "mark" {
		t.Errorf("detail[verb] = %q, want \"mark\"", got)
	}

	l := NewLedger()
	if err := l.Raise(o, 0); err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if got := len(l.Outstanding(31 * day)); got != 1 {
		t.Errorf("%d outstanding a month later, want 1", got)
	}

	// A COMPLETE purge owes nobody anything.
	complete := subscribe.NewRegistry()
	if _, err := complete.Register(forgetful{name: "replica"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	done := subscribe.Mark(complete, "subject-9")
	if done.State != subscribe.PurgeDone {
		t.Fatalf("the second purge is %v, want done", done.State)
	}
	if _, ok := FromPurge(done); ok {
		t.Error("a completed purge produced an obligation; there is nobody to chase")
	}
}

func subjects(out []Outstanding) []string {
	names := make([]string, len(out))
	for i, o := range out {
		names[i] = o.Subject
	}
	return names
}

// forgetful acknowledges a purge.
type forgetful struct{ name string }

func (f forgetful) Name() string                { return f.name }
func (f forgetful) Consume(e []tail.Entry) int  { return len(e) }
func (f forgetful) Forget(subject string) error { return nil }

// forgetless cannot acknowledge, so it leaves every purge incomplete — which
// ADR-010 says is deliberate and correct.
type forgetless struct{ name string }

func (f forgetless) Name() string               { return f.name }
func (f forgetless) Consume(e []tail.Entry) int { return len(e) }
