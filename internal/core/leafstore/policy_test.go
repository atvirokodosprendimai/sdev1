package leafstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/datom"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
)

// second is the wall unit the transaction identifiers here carry: nanoseconds,
// as time.Duration counts them.
const second = int64(time.Second)

func openLeaf(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), testLeaf())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestExposureReportsTheOldestNotTheAverage(t *testing.T) {
	ctx := context.Background()
	s := openLeaf(t)

	// ⚠ ONE old datom among many recent ones. The fixture is arranged so the three
	// candidate answers are far apart: the oldest is 3600s, the newest is 1s, and
	// the mean is under 200s. An implementation that computes either wrong answer
	// fails with a message naming which one it looks like.
	now := 10_000 * second
	if err := s.Append(ctx, assertion("planet-3", "mass", "old", (10_000-3600)*second)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	for i := 1; i <= 19; i++ {
		if err := s.Append(ctx, assertion("planet-3", "note", "recent", (10_000-int64(i))*second)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got := s.Exposure(now)
	if got.Datoms != 20 {
		t.Fatalf("Exposure counted %d datoms, want 20", got.Datoms)
	}

	want := 3600 * time.Second
	if got.Oldest == want {
		return
	}
	switch {
	case got.Oldest < 2*time.Second:
		t.Fatalf("Oldest = %v, want %v — this is the age of the NEWEST datom. It approaches zero "+
			"as writes continue, so a leaf holding one acknowledged fact for an hour would report "+
			"near-perfect safety as long as anything else is moving", got.Oldest, want)
	case got.Oldest < 30*time.Minute:
		t.Fatalf("Oldest = %v, want %v — this is a MEAN. An average is smallest when the tail is "+
			"fullest, because recent writes drag it down at exactly the moment the worst case is "+
			"worst, so the number looks best when the risk is highest", got.Oldest, want)
	default:
		t.Fatalf("Oldest = %v, want %v", got.Oldest, want)
	}
}

func TestAPolicyWithNoBoundsIsRefused(t *testing.T) {
	ctx := context.Background()
	s := openLeaf(t)
	if err := s.Append(ctx, assertion("planet-3", "mass", "5", 100*second)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if _, err := s.ShouldSeal(Policy{}, 200*second); !errors.Is(err, ErrNoBound) {
		t.Errorf("a policy with neither bound = %v, want ErrNoBound; a zero value that silently "+
			"meant never would leave nothing durable while everything reported success", err)
	}

	// ⚠ The refusal is about having NO bound, not about having only one. Each
	// alone is a complete policy.
	if _, err := s.ShouldSeal(Policy{MaxBytes: 1 << 20}, 200*second); err != nil {
		t.Errorf("a size-only policy was refused: %v", err)
	}
	if _, err := s.ShouldSeal(Policy{MaxAge: time.Hour}, 200*second); err != nil {
		t.Errorf("an age-only policy was refused: %v", err)
	}
}

func TestEitherBoundTripsASeal(t *testing.T) {
	ctx := context.Background()

	// ⚠ Each case trips ONE bound and is comfortably inside the other. A fixture
	// that tripped both would prove neither.
	t.Run("size alone", func(t *testing.T) {
		s := openLeaf(t)
		big := make([]byte, 4096)
		if err := s.Append(ctx, ports.Datom{
			Entity: "planet-3", Attribute: "atlas", Value: big,
			TxID: at(9_999 * second), Assert: true,
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		// One second old, against an hour's age bound: only size can trip this.
		due, err := s.ShouldSeal(Policy{MaxBytes: 1024, MaxAge: time.Hour}, 10_000*second)
		if err != nil {
			t.Fatalf("ShouldSeal: %v", err)
		}
		if !due {
			t.Error("a tail over the size bound is not due; the size bound is being ignored")
		}
	})

	t.Run("age alone", func(t *testing.T) {
		s := openLeaf(t)
		if err := s.Append(ctx, assertion("planet-3", "mass", "5", 1*second)); err != nil {
			t.Fatalf("Append: %v", err)
		}
		// A handful of bytes against a megabyte: only age can trip this.
		due, err := s.ShouldSeal(Policy{MaxBytes: 1 << 20, MaxAge: time.Minute}, 10_000*second)
		if err != nil {
			t.Fatalf("ShouldSeal: %v", err)
		}
		if !due {
			t.Error("a tail past the age bound is not due; the age bound is being ignored")
		}
	})

	t.Run("neither", func(t *testing.T) {
		s := openLeaf(t)
		if err := s.Append(ctx, assertion("planet-3", "mass", "5", 9_999*second)); err != nil {
			t.Fatalf("Append: %v", err)
		}
		due, err := s.ShouldSeal(Policy{MaxBytes: 1 << 20, MaxAge: time.Hour}, 10_000*second)
		if err != nil {
			t.Fatalf("ShouldSeal: %v", err)
		}
		if due {
			t.Error("a tail inside both bounds is due; something trips unconditionally")
		}
	})
}

func TestExposureCountsEncodedBytes(t *testing.T) {
	ctx := context.Background()
	s := openLeaf(t)

	// Many small facts: the shape a byte count that ignored the fixed part would
	// under-report by an order of magnitude.
	tail := []ports.Datom{
		assertion("planet-3", "a", "1", 100*second),
		assertion("planet-3", "b", "2", 101*second),
		assertion("planet-3", "c", "3", 102*second),
		assertion("star-1", "codename", "yellow dwarf", 103*second),
	}
	if err := s.Append(ctx, tail...); err != nil {
		t.Fatalf("Append: %v", err)
	}

	encoded, err := datom.Encode(tail)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// ⚠ Compared against the ENCODER, not against a hand-computed constant: a
	// constant agrees with whatever the layout was the day it was written.
	want := int64(len(encoded) - datom.HeaderSize)

	if got := s.Exposure(200 * second).Bytes; got != want {
		t.Errorf("Exposure reported %d bytes, the encoder produces %d for the same tail", got, want)
	}
}

func TestShouldSealDoesNotSeal(t *testing.T) {
	ctx := context.Background()
	s := openLeaf(t)
	if err := s.Append(ctx, assertion("planet-3", "mass", "5", 1*second)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	segments, pending := s.Segments(), s.Pending()

	// Well past both bounds, asked several times.
	for i := 0; i < 3; i++ {
		due, err := s.ShouldSeal(Policy{MaxBytes: 1, MaxAge: time.Nanosecond}, 10_000*second)
		if err != nil {
			t.Fatalf("ShouldSeal: %v", err)
		}
		if !due {
			t.Fatal("a tail past both bounds is not due")
		}
	}

	// ⚠ ADR-020 fixed the commit point at memory. A ShouldSeal that sealed would
	// put a flush wherever it was called from, and the acknowledged latency would
	// change without the record that fixed it changing.
	if s.Segments() != segments {
		t.Errorf("ShouldSeal wrote a segment: %d became %d", segments, s.Segments())
	}
	if s.Pending() != pending {
		t.Errorf("ShouldSeal emptied the tail: %d became %d", pending, s.Pending())
	}
}
