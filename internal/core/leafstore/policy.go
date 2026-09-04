package leafstore

import (
	"errors"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/datom"
)

// ErrNoBound reports a policy that would never seal.
//
// ⚠ "Never seal" is a legitimate choice — a test, an import, a leaf being rebuilt
// — but it has to be said out loud rather than being what a caller gets by
// configuring nothing. A zero value that silently meant never would leave nothing
// durable while everything reported success.
var ErrNoBound = errors.New("leafstore: a policy with neither bound would never seal")

// Policy is when a leaf's tail should be sealed.
//
// ★ It is a PAIR, and the first bound to trip wins. Neither alone is enough, and
// that is why this is one decision rather than two constants:
//
//   - With SIZE only, a quiet tenant never reaches the threshold, so its
//     acknowledged writes stay in memory without bound — and it is the tenant
//     generating no load whose exposure nobody notices.
//   - With AGE only, segment sizes track the clock rather than the data: a burst
//     produces one enormous segment and a lull produces empty ones.
//
// ⚠ The sealing trigger IS the flush bound. Data is exposed from the moment it is
// acknowledged (ADR-020: N memory replicas, not a disk) until the moment it is
// sealed, so this pair decides how long that window can be.
type Policy struct {
	// MaxBytes seals once the tail holds at least this many encoded bytes. Zero
	// disables the size bound.
	MaxBytes int64

	// MaxAge seals once the OLDEST unsealed datom is at least this old. Zero
	// disables the age bound.
	//
	// ⚠ It wins over any minimum segment size. The two conflict on a quiet leaf,
	// and durability beats layout: a small segment costs space, an unbounded
	// exposure costs data.
	MaxAge time.Duration

	// MaxSegments compacts once the leaf holds at least this many segments. Zero
	// disables compaction.
	//
	// ★ A COUNT rather than a size, because the cost being paid is one block
	// lookup per segment per read — and that is counted in segments however large
	// each one is. See [Store.ShouldCompact] and ADR-029.
	MaxSegments int
}

// Valid reports whether a policy would ever seal.
func (p Policy) Valid() error {
	if p.MaxBytes <= 0 && p.MaxAge <= 0 {
		return ErrNoBound
	}
	return nil
}

// Exposure is what a leaf has acknowledged and not yet written to a disk.
//
// ⚠ This is the number an operator wants DURING a power event, not after one.
type Exposure struct {
	// Datoms is how many are unsealed.
	Datoms int

	// Bytes is what they will cost when written, counted through ADR-025's
	// encoding rather than estimated from the values.
	Bytes int64

	// Oldest is the age of the OLDEST unsealed datom, and it is deliberately not
	// a mean.
	//
	// ⚠ An average is smallest when the tail is fullest, because a burst of
	// recent writes drags it down at exactly the moment the worst case is worst —
	// so the number looks best when the risk is highest. The age of the NEWEST is
	// worse still and sounds more natural: it approaches zero as writes continue,
	// so a leaf holding one acknowledged fact for an hour reports near-perfect
	// safety as long as anything else is moving.
	Oldest time.Duration
}

// Exposure measures the unsealed tail against a wall reading.
//
// now is an instant in the same units the transaction identifiers carry, which is
// where the ages come from — so the answer cannot disagree with the datoms.
func (s *Store) Exposure(now int64) Exposure {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var e Exposure
	e.Datoms = len(s.tail)
	if e.Datoms == 0 {
		return e
	}

	oldest := int64(-1)
	for _, d := range s.tail {
		e.Bytes += int64(datom.SizeOf(d))
		if wall := d.TxID.HLC.Wall; oldest < 0 || wall < oldest {
			oldest = wall
		}
	}
	if age := now - oldest; age > 0 {
		e.Oldest = time.Duration(age)
	}
	return e
}

// ShouldSeal reports whether a seal is due under a policy.
//
// ⚠ It DECIDES and does not seal. ADR-020 fixed the commit point at N memory
// replicas in distinct failure domains; a store that sealed itself here — or
// inside Append — would put a flush on the write path, and the acknowledged
// latency would change without the record that fixed it changing.
//
// Who calls this, and how often, is a deployment decision this package
// deliberately does not take (`BACKLOG.md` §15).
func (s *Store) ShouldSeal(p Policy, now int64) (bool, error) {
	if err := p.Valid(); err != nil {
		return false, err
	}
	e := s.Exposure(now)
	if e.Datoms == 0 {
		return false, nil
	}
	// The FIRST bound to trip. Either alone is enough, which is the whole point
	// of the pair.
	if p.MaxBytes > 0 && e.Bytes >= p.MaxBytes {
		return true, nil
	}
	if p.MaxAge > 0 && e.Oldest >= p.MaxAge {
		return true, nil
	}
	return false, nil
}
