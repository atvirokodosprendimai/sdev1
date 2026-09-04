package observe

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

func mustEmit(t *testing.T, k Kind, fields map[string]string) Event {
	t.Helper()
	e, err := Emit(k, testLeaf(), tx.TxID{}, fields)
	if err != nil {
		t.Fatalf("Emit(%s): %v", k, err)
	}
	return e
}

// promptly runs fn and fails if it does not return quickly.
//
// ⚠ EVERY emission into a possibly-full buffer in this file goes through it, and
// that is a deliberate correction. Written the obvious way — emitting
// synchronously into a full stream — a blocking implementation does not fail the
// test, it HANGS it: the suite then burns Go's full ten-minute timeout, buries
// the reason in a goroutine dump, and a mutation run comes back inconclusive
// instead of killed. A test for "this never blocks" has to be able to observe
// blocking without doing it.
func promptly(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not return within 3s — emission must never block the caller, because "+
			"observability that can stall the served path is worse than none", what)
	}
}

// TestCounterWithoutAQuestionIsRefused checks the rule that closes the drift
// towards a dashboard of unread numbers, at the point it starts.
func TestCounterWithoutAQuestionIsRefused(t *testing.T) {
	_, err := RegisterCounter("test.no_question", "")
	if !errors.Is(err, ErrNoQuestion) {
		t.Fatalf("a counter with no question: error = %v, want ErrNoQuestion", err)
	}
	if _, ok := CounterNamed("test.no_question"); ok {
		t.Error("the refused counter was registered anyway")
	}

	c, err := RegisterCounter("test.with_question",
		"is the repair backlog growing faster than repairs complete?")
	if err != nil {
		t.Fatalf("a counter with a question was refused: %v", err)
	}
	if c.Question == "" {
		t.Error("the registered counter carries no question")
	}

	// A counter with no name is refused, since nothing could read it.
	if _, err := RegisterCounter("", "a question"); err == nil {
		t.Error("a counter with no name was registered")
	}

	// A duplicate is refused rather than replacing, so two components cannot
	// disagree about what a number means.
	if _, err := RegisterCounter("test.with_question", "a different question"); !errors.Is(err, ErrDuplicateCounter) {
		t.Errorf("a duplicate counter: error = %v, want ErrDuplicateCounter", err)
	}

	// Counting works and is readable.
	c.Add(3)
	c.Add(4)
	if got := c.Value(); got != 7 {
		t.Errorf("counter value = %d, want 7", got)
	}
}

// TestEmissionNeverBlocksTheCaller is the property that keeps observability from
// being able to stall the served path.
//
// ⚠ The buffer is FILLED first and nothing drains it. Emitting fewer events than
// the buffer holds would prove nothing, because a blocking implementation would
// not block either.
func TestEmissionNeverBlocksTheCaller(t *testing.T) {
	s, err := NewStream(4)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	e := mustEmit(t, KindRedirect, map[string]string{"from": "a", "to": "b"})

	// Fill it. No consumer exists.
	promptly(t, "filling the buffer", func() {
		for i := 0; i < s.Capacity(); i++ {
			s.Emit(e)
		}
	})
	if s.Buffered() != s.Capacity() {
		t.Fatalf("the buffer holds %d of %d; it is not full, so this test would prove nothing",
			s.Buffered(), s.Capacity())
	}

	// Now emit hard, concurrently, with nothing draining.
	const writers, each = 8, 500
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < each; i++ {
					s.Emit(e)
				}
			}()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("emission blocked with a full buffer and no consumer — observability that can " +
			"stall the served path is worse than none, and this is the failure that actually happens")
	}

	// Everything past the buffer was dropped, not queued.
	if got, want := s.Dropped(), uint64(writers*each); got != want {
		t.Errorf("dropped %d events, want %d — the buffer must not have grown", got, want)
	}
}

// TestDroppedEventsAreCounted checks the count is exact, so a stream under load
// reports what it lost.
func TestDroppedEventsAreCounted(t *testing.T) {
	s, err := NewStream(3)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	e := mustEmit(t, KindStripeReconstructed, map[string]string{"lost": "2", "scheme": "RS(4,2)"})

	// Nothing is dropped while there is room. A drop count that started
	// incrementing early would look the same on a full buffer.
	for i := 0; i < 3; i++ {
		s.Emit(e)
		if got := s.Dropped(); got != 0 {
			t.Fatalf("after %d emissions into a buffer of 3, dropped = %d, want 0", i+1, got)
		}
	}

	const over = 17
	promptly(t, "emitting past a full buffer", func() {
		for i := 0; i < over; i++ {
			s.Emit(e)
		}
	})
	if got := s.Dropped(); got != over {
		t.Errorf("dropped = %d, want exactly %d", got, over)
	}

	// Draining makes room, and the next emission is not dropped.
	if _, ok := s.Receive(); !ok {
		t.Fatal("a buffered event could not be received")
	}
	before := s.Dropped()
	promptly(t, "emitting into a drained buffer", func() { s.Emit(e) })
	if got := s.Dropped(); got != before {
		t.Errorf("an emission into a drained buffer was dropped: %d then %d", before, got)
	}

	// An empty stream yields nothing rather than blocking.
	empty, err := NewStream(1)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if _, ok := empty.Receive(); ok {
		t.Error("an empty stream returned an event")
	}

	// A stream with no buffer at all is refused, since it could only ever drop.
	if _, err := NewStream(0); err == nil {
		t.Error("a stream with a zero buffer was created; it could only ever drop")
	}
}

// TestDropCounterIsItselfDeclared checks the count that reveals lost events is
// not another unread number.
func TestDropCounterIsItselfDeclared(t *testing.T) {
	c, ok := CounterNamed("observe.events_dropped")
	if !ok {
		t.Fatal("the drop counter is not registered; a stream that loses events would then " +
			"lose them silently, which is lying exactly under the load being investigated")
	}
	if c.Question == "" {
		t.Error("the drop counter states no question, so it is exactly the unread number this " +
			"package refuses to allow")
	}

	// It moves when a stream drops.
	before := c.Value()
	s, err := NewStream(1)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	e := mustEmit(t, KindWriterFencedOut, map[string]string{"holder": "node-a"})
	promptly(t, "emitting three events into a buffer of one", func() {
		s.Emit(e)
		s.Emit(e)
		s.Emit(e)
	})
	if got := c.Value(); got != before+2 {
		t.Errorf("the package drop counter moved by %d, want 2", got-before)
	}
	if s.Dropped() != 2 {
		t.Errorf("the stream's own count is %d, want 2", s.Dropped())
	}
}

// TestCountersAreStableAndOrdered checks a consumer and a test see the same set
// each run.
func TestCountersAreStableAndOrdered(t *testing.T) {
	if _, err := RegisterCounter("test.zzz_last", "does ordering hold at the end?"); err != nil &&
		!errors.Is(err, ErrDuplicateCounter) {
		t.Fatalf("RegisterCounter: %v", err)
	}
	if _, err := RegisterCounter("test.aaa_first", "does ordering hold at the start?"); err != nil &&
		!errors.Is(err, ErrDuplicateCounter) {
		t.Fatalf("RegisterCounter: %v", err)
	}

	first := Counters()
	if len(first) < 3 {
		t.Fatalf("only %d counters registered; ordering cannot be observed", len(first))
	}
	names := make([]string, len(first))
	for i, c := range first {
		names[i] = c.Name
		if c.Question == "" {
			t.Errorf("counter %q states no question", c.Name)
		}
	}
	if !slices.IsSorted(names) {
		t.Fatalf("counters are not ordered: %v", names)
	}

	// Repeatedly, because Go randomises map iteration and an unordered answer
	// would differ only sometimes.
	for i := 0; i < 20; i++ {
		again := Counters()
		if len(again) != len(first) {
			t.Fatalf("run %d returned %d counters, first returned %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j].Name != first[j].Name {
				t.Fatalf("run %d diverges at %d: %q vs %q", i, j, again[j].Name, first[j].Name)
			}
		}
	}
}
