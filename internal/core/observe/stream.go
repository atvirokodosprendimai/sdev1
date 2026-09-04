package observe

import (
	"fmt"
	"sync/atomic"
)

// Stream carries events to a sink without ever making the caller wait.
//
// ⚠ Observability that can stall the thing it observes is worse than none, and a
// blocking emit is the failure that actually happens rather than a hypothetical
// one. So a full buffer DROPS, and every drop is counted — a stream that loses
// events silently is lying exactly under the load that made it lose them.
type Stream struct {
	events chan Event
	// dropped is this stream's own count, alongside the package-wide one, so a
	// caller can attribute losses to a stream rather than only to the process.
	dropped atomic.Uint64
}

// NewStream returns a stream with a bounded buffer.
func NewStream(capacity int) (*Stream, error) {
	if capacity < 1 {
		return nil, fmt.Errorf("observe: a stream needs a buffer, got capacity %d", capacity)
	}
	return &Stream{events: make(chan Event, capacity)}, nil
}

// Emit offers an event to the stream.
//
// ★ It never blocks and never returns an error. There is deliberately nothing a
// caller can do wrong here and nothing it must handle, because the alternative —
// a caller that must decide what to do when observability is busy — is how the
// observability path acquires the ability to fail a request.
func (s *Stream) Emit(e Event) {
	select {
	case s.events <- e:
	default:
		s.dropped.Add(1)
		droppedEvents.Add(1)
	}
}

// Receive takes one event if there is one, without blocking.
func (s *Stream) Receive() (Event, bool) {
	select {
	case e := <-s.events:
		return e, true
	default:
		return Event{}, false
	}
}

// Dropped is how many events this stream could not take.
func (s *Stream) Dropped() uint64 { return s.dropped.Load() }

// Buffered is how many events are waiting for a sink.
func (s *Stream) Buffered() int { return len(s.events) }

// Capacity is the buffer's size.
func (s *Stream) Capacity() int { return cap(s.events) }
