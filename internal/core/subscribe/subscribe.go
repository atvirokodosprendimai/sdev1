package subscribe

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/tail"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

var (
	// ErrUnknownSink reports a sink that was never registered.
	//
	// ★ Registration is what makes a sink reachable by a purge, so an unknown
	// sink is not merely undeliverable — it is invisible to the mechanism that
	// would otherwise tell it to forget something.
	ErrUnknownSink = errors.New("subscribe: no sink registered under that name")

	// ErrDuplicateSink reports two registrations of one name.
	//
	// Refusing rather than replacing matters: a second sink inheriting the
	// first's cursor would silently skip everything before it, and its backup
	// would look complete.
	ErrDuplicateSink = errors.New("subscribe: a sink is already registered under that name")
)

// Cursor is a resumable position in the log.
//
// ★ It is a transaction identifier rather than an offset. An offset is
// meaningless after compaction or renumbering, and two subscribers holding
// offsets cannot be compared with anything else in the system; a `tx.TxID` is
// ordered by the same rule everything else is.
type Cursor struct {
	// At is the last entry the sink acknowledged.
	At tx.TxID
	// Started is false until something has been consumed, because the zero
	// identifier is a valid position and "nothing yet" is not.
	Started bool
}

// After reports whether an entry lies beyond the cursor and so is undelivered.
func (c Cursor) After(id tx.TxID) bool {
	if !c.Started {
		return true
	}
	return id.Compare(c.At) > 0
}

// Sink consumes entries. It returns the number it ACKNOWLEDGED, in order, from
// the start of what it was given.
//
// ⚠ Delivery is at-least-once. A sink that crashes after processing an entry but
// before acknowledging it will see that entry again, and it must tolerate the
// repeat. Exactly-once would require the sink's own writes to be transactional
// with the cursor advance, which is the sink's property and cannot be provided
// here.
type Sink interface {
	// Name identifies the sink to a purge.
	Name() string
	// Consume returns how many of the given entries it accepted, counting from
	// the first. Returning fewer than it was given is how a sink reports that it
	// stopped, and everything past that point is redelivered.
	Consume(entries []tail.Entry) int
}

// Subscription is a sink and its position.
type Subscription struct {
	Sink   Sink
	Cursor Cursor
}

// Deliver walks the tail from the subscription's cursor to the watermark, hands
// the entries to the sink, and advances the cursor past what was acknowledged.
//
// ★ ONLY past what was acknowledged. A cursor that moved further would let a
// crashed sink resume beyond what it processed, and a backup missing entries
// looks exactly like a complete one.
func (s *Subscription) Deliver(t *tail.Tail, upTo tail.Watermark) int {
	var pending []tail.Entry
	t.Walk(upTo, func(e tail.Entry) bool {
		if s.Cursor.After(e.TxID) {
			pending = append(pending, e)
		}
		return true
	})
	if len(pending) == 0 {
		return 0
	}

	took := s.Sink.Consume(pending)
	if took < 0 {
		took = 0
	}
	if took > len(pending) {
		took = len(pending)
	}
	if took > 0 {
		s.Cursor = Cursor{At: pending[took-1].TxID, Started: true}
	}
	return took
}

// Registry holds the sinks a purge must reach.
type Registry struct {
	mu    sync.RWMutex
	subs  map[string]*Subscription
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{subs: map[string]*Subscription{}}
}

// Register makes a sink known, and therefore reachable by a purge.
func (r *Registry) Register(s Sink) (*Subscription, error) {
	if s == nil || s.Name() == "" {
		return nil, fmt.Errorf("subscribe: a sink needs a name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[s.Name()]; ok {
		return nil, fmt.Errorf("%w: %s", ErrDuplicateSink, s.Name())
	}
	sub := &Subscription{Sink: s}
	r.subs[s.Name()] = sub
	r.order = append(r.order, s.Name())
	return sub, nil
}

// Lookup returns a registered subscription.
func (r *Registry) Lookup(name string) (*Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sub, ok := r.subs[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSink, name)
	}
	return sub, nil
}

// Sinks names every registered sink, ordered, which is exactly the set a purge
// enumerates.
func (r *Registry) Sinks() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	sort.Strings(out)
	return out
}
