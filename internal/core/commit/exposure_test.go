package commit

import (
	"errors"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

const (
	aSecond = int64(time.Second)
	aMinute = int64(time.Minute)
)

func entryAt(wall int64) tx.TxID {
	return tx.TxID{HLC: hlc.Timestamp{Wall: wall}, Seq: 1}
}

func meter(t *testing.T) *Meter {
	t.Helper()
	m, err := NewMeter(Bound{MaxAge: 10 * aSecond, MaxBytes: 1000})
	if err != nil {
		t.Fatalf("NewMeter: %v", err)
	}
	return m
}

// TestTheReportedExposureIsThePeakNotTheCalmAfterIt is ADR-041's falsifier.
//
// ⚠ BOTH numbers are asserted at the same instant. A peak alone could be a
// maximum that never falls; a current alone is exactly the defect being guarded
// against. Only the pair — peak high, current low, read together — shows the rule.
func TestTheReportedExposureIsThePeakNotTheCalmAfterIt(t *testing.T) {
	m := meter(t)

	// A burst: twenty entries of fifty bytes.
	const t0 = int64(1000)
	for i := int64(0); i < 20; i++ {
		m.Committed(entryAt(t0+i), 50, t0+i)
	}
	if got := m.Current(t0 + 20).Entries; got != 20 {
		t.Fatalf("current entries = %d during the burst, want 20", got)
	}

	// ★ A PARTIAL flush: most of the burst reaches stable storage, and the
	// entries committed while it ran do not. This is the case that separates a
	// peak from a running total — without it the window only ever grows until it
	// hits zero, and the two numbers are the same number.
	m.Flushed(entryAt(t0+16), t0+21)

	now := t0 + 23
	current := m.Current(now)
	peak := m.Peak(now)

	// The calm afterwards: three entries survived the flush.
	if current.Entries != 3 || current.Bytes != 150 {
		t.Fatalf("current after a partial flush = %+v, want 3 entries of 150 bytes", current)
	}

	// ⚠ And the peak still reports the burst. This is the whole rule: asked
	// after the burst has passed, the present value reports the calm.
	if peak.Entries != 20 || peak.Bytes != 1000 {
		t.Errorf("peak = %+v, want the burst's 20 entries and 1000 bytes.\n"+
			"The exposure correlates with load, so it is largest exactly when a correlated "+
			"failure is most likely — reporting what is true when somebody happens to look "+
			"hides that completely.", peak)
	}
	if peak.Entries <= current.Entries {
		t.Error("the peak is not above the current value, so the two are indistinguishable " +
			"and the peak proves nothing")
	}

	// The window still has not emptied, so the peak still stands.
	m.Committed(entryAt(t0+30), 10, t0+30)
	if got := m.Peak(t0 + 31); got.Entries != 20 {
		t.Errorf("peak = %+v after a further commit, want the burst's 20 — a partial flush "+
			"does not close the window, and what it left behind is still at risk", got)
	}

	// ★ The peak's AGE grows while the oldest unflushed entry waits.
	later := t0 + 5*aMinute
	if got := m.Peak(later).Age; got < 4*aMinute {
		t.Errorf("peak age = %d, want at least four minutes — the oldest surviving entry is "+
			"still unflushed and still at risk", got)
	}

	// Only emptying the window resets it.
	m.Flushed(entryAt(t0+30), later)
	if got := m.Peak(later); got.Entries != 0 {
		t.Errorf("peak = %+v after the window emptied, want empty", got)
	}
}

// TestABoundNeedsBothHalves is ADR-041 rule 3.
//
// ★ It shows WHY, not only that: a refusal test proves the guard fires, and the
// second half proves the guard is right.
func TestABoundNeedsBothHalves(t *testing.T) {
	for _, c := range []struct {
		name  string
		bound Bound
	}{
		{"neither", Bound{}},
		{"age only", Bound{MaxAge: aSecond}},
		{"size only", Bound{MaxBytes: 100}},
		{"negative age", Bound{MaxAge: -1, MaxBytes: 100}},
		{"negative size", Bound{MaxAge: aSecond, MaxBytes: -1}},
	} {
		if _, err := NewMeter(c.bound); !errors.Is(err, ErrIncompleteBound) {
			t.Errorf("NewMeter(%s) = %v, want ErrIncompleteBound", c.name, err)
		}
	}
	if _, err := NewMeter(Bound{MaxAge: aSecond, MaxBytes: 100}); err != nil {
		t.Fatalf("a complete bound was refused: %v", err)
	}

	// ⚠ WHY size alone is not a bound: a QUIET tenant commits one small entry and
	// it sits forever, never reaching the size.
	quiet, err := NewMeter(Bound{MaxAge: 1 << 62, MaxBytes: 1000})
	if err != nil {
		t.Fatalf("NewMeter: %v", err)
	}
	quiet.Committed(entryAt(0), 1, 0)
	if quiet.Exceeds(int64(365 * 24 * time.Hour)) {
		t.Error("a size-only bound is exceeded by one byte after a year; the fixture is wrong")
	}
	// The same entry against a real age bound IS due, which is the half a
	// size-only policy is missing.
	both, err := NewMeter(Bound{MaxAge: aSecond, MaxBytes: 1000})
	if err != nil {
		t.Fatalf("NewMeter: %v", err)
	}
	both.Committed(entryAt(0), 1, 0)
	if !both.Exceeds(2 * aSecond) {
		t.Error("one byte unflushed for two seconds does not exceed a one-second age bound; " +
			"a quiet tenant is unbounded in TIME without it")
	}

	// ⚠ And WHY age alone is not a bound: a BUSY tenant commits an arbitrary
	// volume inside the interval.
	busy, err := NewMeter(Bound{MaxAge: aMinute, MaxBytes: 1 << 62})
	if err != nil {
		t.Fatalf("NewMeter: %v", err)
	}
	for i := int64(0); i < 1000; i++ {
		busy.Committed(entryAt(i), 1_000_000, i)
	}
	if busy.Exceeds(aSecond) {
		t.Error("an age-only bound is exceeded within its interval; the fixture is wrong")
	}
	if !both2(t).Exceeds(aSecond) {
		t.Error("a gigabyte unflushed does not exceed a size bound; a busy tenant is " +
			"unbounded in BYTES without it")
	}
}

// both2 is a meter with both halves, loaded with the same busy volume.
func both2(t *testing.T) *Meter {
	t.Helper()
	m, err := NewMeter(Bound{MaxAge: aMinute, MaxBytes: 1000})
	if err != nil {
		t.Fatalf("NewMeter: %v", err)
	}
	for i := int64(0); i < 1000; i++ {
		m.Committed(entryAt(i), 1_000_000, i)
	}
	return m
}

// TestThePeakResetsOnAFlushAndNothingElse is ADR-041 rule 5.
func TestThePeakResetsOnAFlushAndNothingElse(t *testing.T) {
	m := meter(t)

	const t0 = int64(0)
	for i := int64(0); i < 5; i++ {
		m.Committed(entryAt(i), 100, t0+i)
	}

	// ⚠ READ TWICE. Clearing on read is invisible to a single reader and is
	// exactly what fools the second one — they are reassured because somebody
	// looked before them.
	first := m.Peak(t0 + 5)
	second := m.Peak(t0 + 5)
	if first != second {
		t.Errorf("two reads of Peak gave %+v then %+v; a gauge that clears when somebody "+
			"looks makes two operators disagree about the same window", first, second)
	}
	if first.Entries != 5 || first.Bytes != 500 {
		t.Fatalf("peak = %+v, want 5 entries of 500 bytes", first)
	}

	// Current does not reset it either.
	_ = m.Current(t0 + 5)
	if got := m.Peak(t0 + 5); got != first {
		t.Errorf("reading Current changed the peak to %+v", got)
	}

	// Exceeds does not reset it.
	_ = m.Exceeds(t0 + 5)
	if got := m.Peak(t0 + 5); got != first {
		t.Errorf("reading Exceeds changed the peak to %+v", got)
	}

	// A flush does.
	m.Flushed(entryAt(5), t0+6)
	if got := m.Peak(t0 + 6); got.Entries != 0 || got.Bytes != 0 {
		t.Errorf("peak after a flush = %+v, want empty", got)
	}
	if got := m.Current(t0 + 6); got.Entries != 0 {
		t.Errorf("current after a flush = %+v, want empty", got)
	}
}

// TestAnExceededBoundAsksForAFlush is ADR-041 rule 4.
func TestAnExceededBoundAsksForAFlush(t *testing.T) {
	m := meter(t) // 10s, 1000 bytes

	// Nothing unflushed: nothing is due.
	if m.Exceeds(0) {
		t.Error("an empty window is exceeded")
	}

	// Past the SIZE half.
	m.Committed(entryAt(0), 1500, 0)
	if !m.Exceeds(1) {
		t.Fatal("1500 bytes does not exceed a 1000-byte bound")
	}

	// ⚠ And it still accepts. Nothing refuses, nothing blocks — the node is
	// behind, not unsafe, and refusing would convert a durability exposure into
	// an availability outage.
	m.Committed(entryAt(1), 500, 1)
	if got := m.Current(2); got.Entries != 2 || got.Bytes != 2000 {
		t.Errorf("after exceeding the bound the meter holds %+v; it must keep accepting", got)
	}

	// Past the AGE half, with a trivial size.
	aged := meter(t)
	aged.Committed(entryAt(0), 1, 0)
	if aged.Exceeds(5 * aSecond) {
		t.Error("five seconds exceeds a ten-second bound")
	}
	if !aged.Exceeds(11 * aSecond) {
		t.Error("eleven seconds does not exceed a ten-second bound")
	}

	// A flush clears the request.
	aged.Flushed(entryAt(0), 12*aSecond)
	if aged.Exceeds(12 * aSecond) {
		t.Error("a flush did not clear the request for one")
	}
}
