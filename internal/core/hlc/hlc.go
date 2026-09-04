package hlc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// EncodedSize is the width of a [Timestamp] in its byte-comparable form: eight
// bytes of wall reading and four of logical counter.
const EncodedSize = 12

// ErrShortEncoding reports a buffer that is not [EncodedSize] bytes.
var ErrShortEncoding = errors.New("hlc: encoded timestamp is the wrong size")

// Timestamp is one reading: a wall-clock component in Unix nanoseconds and a
// logical counter that breaks ties when the wall does not advance.
//
// The wall component is an input to the algorithm rather than the ordering
// itself. Comparing timestamps is what orders events; comparing wall readings
// is not.
type Timestamp struct {
	Wall    int64
	Logical uint32
}

// Compare orders by wall reading, then by logical counter. It returns a
// negative number when t precedes other, zero when they are identical, and a
// positive number otherwise.
func (t Timestamp) Compare(other Timestamp) int {
	switch {
	case t.Wall != other.Wall:
		if t.Wall < other.Wall {
			return -1
		}
		return 1
	case t.Logical != other.Logical:
		if t.Logical < other.Logical {
			return -1
		}
		return 1
	default:
		return 0
	}
}

// String renders a timestamp for a diagnostic.
func (t Timestamp) String() string {
	return fmt.Sprintf("%d.%d", t.Wall, t.Logical)
}

// Encode returns the fixed-width, byte-comparable form.
//
// Both fields are big-endian so that comparing two encodings with bytes.Compare
// yields the same order as [Timestamp.Compare]. That is what lets a segment
// index sort on the bytes without decoding them.
func (t Timestamp) Encode() [EncodedSize]byte {
	var b [EncodedSize]byte
	binary.BigEndian.PutUint64(b[0:8], uint64(t.Wall))
	binary.BigEndian.PutUint32(b[8:12], t.Logical)
	return b
}

// Decode reverses [Timestamp.Encode].
func Decode(b [EncodedSize]byte) (Timestamp, error) {
	return Timestamp{
		Wall:    int64(binary.BigEndian.Uint64(b[0:8])),
		Logical: binary.BigEndian.Uint32(b[8:12]),
	}, nil
}

// DecodeSlice reverses [Timestamp.Encode] for a slice of unknown length.
func DecodeSlice(b []byte) (Timestamp, error) {
	if len(b) != EncodedSize {
		return Timestamp{}, fmt.Errorf("%w: got %d bytes, want %d", ErrShortEncoding, len(b), EncodedSize)
	}
	var fixed [EncodedSize]byte
	copy(fixed[:], b)
	return Decode(fixed)
}

// Clock issues hybrid logical timestamps.
//
// It is safe for concurrent use, and it must be: every commit on a node passes
// through one, so a race here would produce two events claiming the same
// position in a log that is permanent.
type Clock struct {
	mu   sync.Mutex
	last Timestamp
	now  func() int64
}

// NewClock returns a Clock reading the wall from the supplied function.
//
// The reading is injected rather than taken directly from the standard library
// because every interesting property of this package concerns a misbehaving
// wall clock, and a test cannot make the real one jump backwards. Production
// callers use [NewSystemClock].
func NewClock(now func() int64) *Clock {
	return &Clock{now: now}
}

// NewSystemClock returns a Clock reading the host's wall clock.
func NewSystemClock() *Clock {
	return NewClock(func() int64 { return time.Now().UnixNano() })
}

// Now returns a reading strictly greater than every reading this Clock has
// already returned.
//
// When the wall clock has advanced past the last reading, the wall half moves
// and the logical counter resets. When it has not — because it is frozen, or
// because it moved backwards — the wall half stays pinned and the logical
// counter advances instead. That second branch is the whole reason this type
// exists.
func (c *Clock) Now() Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()

	if pt := c.now(); pt > c.last.Wall {
		c.last = Timestamp{Wall: pt}
		return c.last
	}
	c.last.Logical++
	return c.last
}

// Merge absorbs a timestamp received from another node and returns a reading
// strictly greater than both the local state and the remote value.
//
// This is what establishes causality: after merging, everything this node does
// is ordered after the remote event that carried the timestamp.
//
// ⚠ It is also the path by which a badly-skewed remote clock propagates. A
// remote wall reading far in the future is adopted and, being monotonic, is
// never given back. Refusing such a remote is a cluster-level policy and is not
// decided here.
func (c *Clock) Merge(remote Timestamp) Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()

	pt := c.now()
	high := pt
	if c.last.Wall > high {
		high = c.last.Wall
	}
	if remote.Wall > high {
		high = remote.Wall
	}

	switch {
	case high == c.last.Wall && high == remote.Wall:
		// Both sides already sit at the winning wall reading, so the counter
		// must clear both.
		next := c.last.Logical
		if remote.Logical > next {
			next = remote.Logical
		}
		c.last.Logical = next + 1
	case high == c.last.Wall:
		c.last.Logical++
	case high == remote.Wall:
		c.last.Logical = remote.Logical + 1
	default:
		// The physical clock overtook both sides, so the counter restarts.
		c.last.Logical = 0
	}
	c.last.Wall = high
	return c.last
}

// Last returns the most recent reading without issuing a new one. It exists for
// a diagnostic; ordering an event requires [Clock.Now].
func (c *Clock) Last() Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}
