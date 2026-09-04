package hlc

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"
)

// fakeClock is a wall-clock reading under the test's control. Every interesting
// property of this package is about what happens when the wall clock
// misbehaves, and a test cannot make the real clock jump backwards.
type fakeClock struct {
	mu  sync.Mutex
	now int64
}

func (f *fakeClock) read() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) set(v int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = v
}

// TestNowIsStrictlyMonotonic checks successive readings strictly increase, and
// keeps holding under concurrent callers — a clock shared by every commit on a
// node makes a data race here a correctness defect, not a performance note.
func TestNowIsStrictlyMonotonic(t *testing.T) {
	f := &fakeClock{now: 1000}
	c := NewClock(f.read)

	prev := c.Now()
	for i := 0; i < 500; i++ {
		got := c.Now()
		if got.Compare(prev) <= 0 {
			t.Fatalf("iteration %d: Now() = %v, which does not exceed the previous %v", i, got, prev)
		}
		prev = got
	}

	// Concurrent callers must all receive distinct, increasing values.
	c2 := NewClock(f.read)
	const goroutines, each = 8, 200
	seen := make([][]Timestamp, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			out := make([]Timestamp, each)
			for i := range out {
				out[i] = c2.Now()
			}
			seen[g] = out
		}(g)
	}
	wg.Wait()

	all := make(map[Timestamp]bool)
	for _, batch := range seen {
		for _, ts := range batch {
			if all[ts] {
				t.Fatalf("two concurrent callers received the same timestamp %v", ts)
			}
			all[ts] = true
		}
	}
	if len(all) != goroutines*each {
		t.Fatalf("got %d distinct timestamps, want %d", len(all), goroutines*each)
	}
}

// TestNowSurvivesBackwardsWallClock is the property a plain wall clock cannot
// give: a correction that moves time backwards must not move the clock
// backwards, because the event log records an order permanently.
func TestNowSurvivesBackwardsWallClock(t *testing.T) {
	f := &fakeClock{now: 10_000}
	c := NewClock(f.read)

	before := c.Now()
	f.set(5_000) // the wall clock jumps an hour into the past
	after := c.Now()

	if after.Compare(before) <= 0 {
		t.Fatalf("after a backwards wall-clock jump Now() = %v, which does not exceed %v", after, before)
	}
	if after.Wall != before.Wall {
		t.Errorf("wall moved to %d on a backwards jump, want it pinned at %d", after.Wall, before.Wall)
	}
	if after.Logical != before.Logical+1 {
		t.Errorf("logical = %d, want %d: a frozen or backwards wall must advance the logical counter",
			after.Logical, before.Logical+1)
	}
}

// TestLogicalIncrementsWhenWallDoesNotAdvance checks a frozen wall clock still
// yields distinct increasing timestamps, and that the logical counter resets
// once the wall does move.
func TestLogicalIncrementsWhenWallDoesNotAdvance(t *testing.T) {
	f := &fakeClock{now: 42}
	c := NewClock(f.read)

	first := c.Now()
	if first.Logical != 0 {
		t.Fatalf("first reading has logical %d, want 0", first.Logical)
	}
	for i := uint32(1); i <= 5; i++ {
		got := c.Now()
		if got.Wall != 42 {
			t.Fatalf("wall = %d, want 42 while the clock is frozen", got.Wall)
		}
		if got.Logical != i {
			t.Fatalf("logical = %d, want %d", got.Logical, i)
		}
	}
	f.set(43)
	moved := c.Now()
	if moved.Wall != 43 || moved.Logical != 0 {
		t.Errorf("after the wall advanced: %v, want wall 43 and logical reset to 0", moved)
	}
}

// TestMergeAdvancesPastRemote checks the causality guarantee: after absorbing a
// remote timestamp, the next local reading strictly exceeds it. This is what
// makes a message carrying a timestamp establish a happens-before relation.
func TestMergeAdvancesPastRemote(t *testing.T) {
	f := &fakeClock{now: 100}
	c := NewClock(f.read)

	// A remote node far ahead of us.
	remote := Timestamp{Wall: 5_000, Logical: 7}
	merged := c.Merge(remote)
	if merged.Compare(remote) <= 0 {
		t.Fatalf("Merge(%v) = %v, which does not exceed the remote", remote, merged)
	}
	next := c.Now()
	if next.Compare(remote) <= 0 {
		t.Fatalf("after Merge, Now() = %v, which does not exceed the remote %v", next, remote)
	}

	// A remote behind us must not drag the clock backwards.
	before := c.Now()
	stale := Timestamp{Wall: 1, Logical: 0}
	if got := c.Merge(stale); got.Compare(before) <= 0 {
		t.Fatalf("Merge(stale %v) = %v, which does not exceed the prior %v", stale, got, before)
	}

	// An exactly-tied remote must still produce something strictly greater.
	tied := c.Now()
	if got := c.Merge(tied); got.Compare(tied) <= 0 {
		t.Fatalf("Merge(tied %v) = %v, want strictly greater", tied, got)
	}
}

// TestTimestampOrdersLexicographically checks the encoding sorts as bytes in
// exactly the order Compare gives, so an index can order on it without decoding.
func TestTimestampOrdersLexicographically(t *testing.T) {
	rng := rand.New(rand.NewSource(20260904))
	gen := func() Timestamp {
		return Timestamp{Wall: rng.Int63n(1 << 40), Logical: uint32(rng.Intn(1 << 16))}
	}
	for i := 0; i < 2000; i++ {
		a, b := gen(), gen()
		ea, eb := a.Encode(), b.Encode()
		if len(ea) != EncodedSize || len(eb) != EncodedSize {
			t.Fatalf("encoding is %d and %d bytes, want %d for both", len(ea), len(eb), EncodedSize)
		}
		if got, want := sign(bytes.Compare(ea[:], eb[:])), sign(a.Compare(b)); got != want {
			t.Fatalf("case %d: bytes.Compare sign %d, Compare sign %d for %v vs %v", i, got, want, a, b)
		}
	}
}

// TestDecodeRoundTrips checks a timestamp survives the encoding it is indexed by.
func TestDecodeRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		want := Timestamp{Wall: rng.Int63n(1 << 40), Logical: uint32(rng.Intn(1 << 20))}
		got, err := Decode(want.Encode())
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got != want {
			t.Fatalf("round trip: got %v, want %v", got, want)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
