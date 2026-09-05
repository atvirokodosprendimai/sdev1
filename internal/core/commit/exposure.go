package commit

import (
	"errors"
	"fmt"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// ErrIncompleteBound reports a bound missing either half.
//
// ⚠ Both halves are required, and this differs from ADR-028's sealing policy
// deliberately. There, any one bound still causes a seal. Here each single-bound
// policy leaves a whole class of tenant unbounded:
//
//   - Size only: a quiet tenant's single committed entry sits unflushed forever,
//     because the size is never reached. Unbounded in TIME.
//   - Age only: a busy tenant commits an arbitrary volume inside the interval.
//     Unbounded in BYTES.
//
// ★ So the pair is the decision rather than two constants, which is what
// docs/adr/BACKLOG.md §23 says.
var ErrIncompleteBound = errors.New("commit: a flush bound needs both a maximum age and a maximum size")

// Exposure is what has been acknowledged and is not yet on stable storage.
//
// ⚠ It is entries COMMITTED and not yet FLUSHED — NOT [Gate.Pending], which is
// written-and-not-committed. The first is a promise that could still be broken;
// the second is data nobody was promised. Reporting one as the other says a busy
// node is exposed, or that an exposed one is calm.
type Exposure struct {
	// Entries is how many committed entries are unflushed.
	Entries int
	// Bytes is how much they hold.
	Bytes int64
	// Age is how long the OLDEST of them has been waiting, in nanoseconds.
	Age int64
}

// Bound is how large the unflushed window may get before a flush is due.
//
// Both halves are required; see [ErrIncompleteBound].
type Bound struct {
	// MaxAge is how long the oldest unflushed commit may wait, in nanoseconds.
	MaxAge int64
	// MaxBytes is how much unflushed data may accumulate.
	MaxBytes int64
}

// Valid reports whether both halves are declared.
func (b Bound) Valid() error {
	if b.MaxAge <= 0 || b.MaxBytes <= 0 {
		return fmt.Errorf("%w: got age %d and size %d", ErrIncompleteBound, b.MaxAge, b.MaxBytes)
	}
	return nil
}

// Meter measures the window between acknowledgement and flush.
//
// ★ It reports the PEAK as well as the present value. docs/adr/BACKLOG.md §23
// names the trap — stating the window as an average — because the exposure
// correlates with load, so it is largest exactly when a correlated failure is
// most likely and an average hides that completely. ⚠ The instantaneous reading
// has the same defect one step removed: asked after a burst it reports the calm,
// and an operator budgeting for a power event needs what the exposure REACHED.
//
// ⚠ It refuses nothing and blocks nothing. An exceeded bound means a flush is
// due, not that a write must stop — the node is behind, not unsafe, and refusing
// would convert a durability exposure into an availability outage.
// unflushed is one committed entry that is not yet on stable storage.
type unflushed struct {
	id    tx.TxID
	bytes int64
	at    int64
}

type Meter struct {
	mu    sync.Mutex
	bound Bound

	// held is every committed-and-unflushed entry, in commit order.
	//
	// ⚠ Individually rather than as a running total, because a flush is PARTIAL:
	// it writes what has accumulated, and entries committed while it runs are
	// still unflushed when it finishes. A total could only ever be reset to zero,
	// which would make the window monotonic and [Meter.Peak] identical to
	// [Meter.Current] — a redundant number that looks like a safeguard.
	held  []unflushed
	bytes int64

	// peak is the worst this window has been since it last EMPTIED.
	//
	// ★ Since it emptied, not since the last flush. A partial flush does not
	// close the window, so the exposure it leaves behind is still at risk and the
	// worst moment still stands.
	peak Exposure
}

// NewMeter returns a meter bounded by both halves of b.
func NewMeter(b Bound) (*Meter, error) {
	if err := b.Valid(); err != nil {
		return nil, err
	}
	return &Meter{bound: b}, nil
}

// Bound is the declared bound.
func (m *Meter) Bound() Bound { return m.bound }

// Committed records that an entry has been acknowledged and is not yet flushed.
//
// ⚠ It never refuses. See [Meter].
func (m *Meter) Committed(id tx.TxID, bytes int64, at int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.held = append(m.held, unflushed{id: id, bytes: bytes, at: at})
	m.bytes += bytes

	// ★ The peak is taken HERE, on every commit, rather than sampled when
	// somebody reads. A sampled peak misses the burst that happened between two
	// reads, which is the burst that matters.
	if n := len(m.held); n > m.peak.Entries {
		m.peak.Entries = n
	}
	if m.bytes > m.peak.Bytes {
		m.peak.Bytes = m.bytes
	}
	if age := at - m.held[0].at; age > m.peak.Age {
		m.peak.Age = age
	}
}

// Flushed records that everything up to and including upTo is now on stable
// storage.
//
// ⚠ It is PARTIAL. Entries committed while a flush ran are still unflushed when
// it finishes, and they are exactly what keeps the window from being monotonic —
// without that, the exposure would only ever grow until it hit zero, and the peak
// would be indistinguishable from the present value.
//
// ★ The peak resets only when the window EMPTIES, and on nothing else. Not on a
// partial flush, because the exposure left behind is still at risk. And not on a
// read: a gauge that clears when somebody looks gives the second reader a
// different answer about the same window, and reassures them precisely because
// the first one looked.
func (m *Meter) Flushed(upTo tx.TxID, at int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Take the age one last time, so a window that was long but small still
	// leaves its duration in the peak.
	if len(m.held) > 0 {
		if age := at - m.held[0].at; age > m.peak.Age {
			m.peak.Age = age
		}
	}

	kept := m.held[:0]
	var bytes int64
	for _, u := range m.held {
		if u.id.Compare(upTo) <= 0 {
			continue
		}
		kept = append(kept, u)
		bytes += u.bytes
	}
	m.held, m.bytes = kept, bytes

	if len(m.held) == 0 {
		m.peak = Exposure{}
	}
}

// Current is the window right now.
func (m *Meter) Current(now int64) Exposure {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentLocked(now)
}

func (m *Meter) currentLocked(now int64) Exposure {
	e := Exposure{Entries: len(m.held), Bytes: m.bytes}
	if len(m.held) > 0 {
		e.Age = now - m.held[0].at
	}
	return e
}

// Peak is the worst this window has been since the last flush.
//
// ⚠ Reading it does not reset it, and reading it twice gives the same answer.
func (m *Meter) Peak(now int64) Exposure {
	m.mu.Lock()
	defer m.mu.Unlock()

	// The stored peak plus whatever the CURRENT window has reached since the last
	// commit — an old window that nothing has added to still ages.
	peak := m.peak
	if len(m.held) > 0 {
		if age := now - m.held[0].at; age > peak.Age {
			peak.Age = age
		}
	}
	return peak
}

// Exceeds reports that a flush is DUE: either half of the bound is past.
//
// ⚠ It is a request, not a refusal. Nothing here stops a commit.
func (m *Meter) Exceeds(now int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.currentLocked(now)
	if e.Entries == 0 {
		return false
	}
	return e.Age > m.bound.MaxAge || e.Bytes > m.bound.MaxBytes
}
