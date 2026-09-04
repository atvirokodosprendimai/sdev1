package subscribe

import (
	"errors"
	"fmt"
	"sort"

	"github.com/atvirokodosprendimai/sdev1/internal/core/crypt"
)

// PurgeState is what happened to a removal request.
//
// ⚠ There are exactly three, and the middle one is the reason. Two states would
// force an unacknowledged sink to be reported as one of them, and both readings
// are wrong: DONE is a lie that surfaces at the next restore, REFUSED suggests
// nothing happened when the primary copy is already gone.
type PurgeState int

const (
	// PurgeStateUnset is the zero value and is never a valid answer.
	PurgeStateUnset PurgeState = iota

	// PurgeDone: every registered sink acknowledged.
	PurgeDone

	// PurgeIncomplete: the primary action happened and at least one registered
	// sink has not acknowledged. This is the only one of the three an operator
	// can act on — it says what is gone AND names who to chase.
	PurgeIncomplete

	// PurgeRefused: nothing happened at all.
	PurgeRefused
)

func (s PurgeState) String() string {
	switch s {
	case PurgeDone:
		return "done"
	case PurgeIncomplete:
		return "incomplete"
	case PurgeRefused:
		return "refused"
	default:
		return "unset"
	}
}

// PurgeStates returns the three valid values.
func PurgeStates() []PurgeState { return []PurgeState{PurgeDone, PurgeIncomplete, PurgeRefused} }

// ErrNotErasure reports that an operation did not make the subject unreadable.
//
// ★ It exists so a caller can DEMAND erasure and be told no. Marking and
// sweeping are routinely reported as deletion, and this is the mechanism that
// makes that claim fail rather than pass quietly.
var ErrNotErasure = errors.New("subscribe: that operation does not make the subject unreadable")

// Horizon bounds a sweep: how long reclaimable space stays reclaimable.
//
// ⚠ It bounds the SWEEP and nothing else. It does not bound marking, which is
// immediate, and it must never be mistaken for bounding erasure, which reaches
// everywhere at once and has no horizon.
type Horizon struct {
	Nanos int64
}

// Forgetter is a sink that can confirm it has forgotten a subject.
//
// ⚠ A registered sink that is NOT a Forgetter cannot acknowledge, so it leaves
// every purge incomplete. That is deliberate and correct: a sink that has no way
// to forget has no way to say it did, and reporting otherwise would be inventing
// an acknowledgement on its behalf.
type Forgetter interface {
	Forget(subject string) error
}

// PurgeResult says what happened and to whom.
type PurgeResult struct {
	Subject string
	// Verb is which of the three was performed: mark, shred or sweep.
	Verb  string
	State PurgeState
	// Acknowledged and Outstanding name sinks, so an operator has the one to
	// chase rather than a verdict about all of them.
	Acknowledged []string
	Outstanding  []string

	erases bool
}

// Erases reports whether this operation made the subject unreadable.
func (r PurgeResult) Erases() bool { return r.erases }

// AssertErased returns nil only when the subject is genuinely unreadable
// everywhere: the key was destroyed AND every registered sink acknowledged.
//
// ★ This is what stops a mark being reported as an erasure. A caller that needs
// to say "this person's data is gone" calls it, and a mark or a sweep fails
// here rather than passing.
func (r PurgeResult) AssertErased() error {
	if !r.erases {
		return fmt.Errorf("%w: %s leaves the bytes readable to anyone holding them", ErrNotErasure, r.Verb)
	}
	if r.State != PurgeDone {
		return fmt.Errorf("%w: the key is destroyed but %d sink(s) have not acknowledged: %v",
			ErrNotErasure, len(r.Outstanding), r.Outstanding)
	}
	return nil
}

// fanOut tells every registered sink to forget the subject and collects who
// confirmed.
//
// ★ It enumerates the REGISTRY, which is the whole mechanism: a sink wired up
// outside it is invisible here, and nothing can see what nothing told it about.
func fanOut(reg *Registry, subject string) (acknowledged, outstanding []string) {
	for _, name := range reg.Sinks() {
		sub, err := reg.Lookup(name)
		if err != nil {
			outstanding = append(outstanding, name)
			continue
		}
		f, ok := sub.Sink.(Forgetter)
		if !ok {
			// It cannot confirm, so it has not confirmed.
			outstanding = append(outstanding, name)
			continue
		}
		if err := f.Forget(subject); err != nil {
			outstanding = append(outstanding, name)
			continue
		}
		acknowledged = append(acknowledged, name)
	}
	sort.Strings(acknowledged)
	sort.Strings(outstanding)
	return acknowledged, outstanding
}

// stateFor turns the two lists into the answer.
func stateFor(outstanding []string) PurgeState {
	if len(outstanding) == 0 {
		return PurgeDone
	}
	return PurgeIncomplete
}

// Mark makes a subject invisible to queries.
//
// ⚠ It changes no bytes. Anyone holding them still has them, and this is NOT
// erasure however it is reported upstream.
func Mark(reg *Registry, subject string) PurgeResult {
	ack, out := fanOut(reg, subject)
	return PurgeResult{
		Subject:      subject,
		Verb:         "mark",
		State:        stateFor(out),
		Acknowledged: ack,
		Outstanding:  out,
		erases:       false,
	}
}

// Shred destroys the subject's key and tells every registered sink.
//
// ★ This is the only one of the three that is erasure. Destroying the key
// reaches coded stripes, offline replicas and backups without visiting any of
// them — but a sink holding PLAINTEXT is not reached by it, which is why the
// fan-out matters and why an unacknowledged sink leaves the result incomplete.
func Shred(reg *Registry, ks crypt.Keystore, subject, request string) (PurgeResult, error) {
	id, ok := ks.Resolve(subject)
	if !ok {
		return PurgeResult{
			Subject: subject, Verb: "shred", State: PurgeRefused,
		}, fmt.Errorf("subscribe: no key is held for %q, so there is nothing to destroy", subject)
	}
	if err := ks.Destroy(id); err != nil {
		return PurgeResult{
			Subject: subject, Verb: "shred", State: PurgeRefused,
		}, fmt.Errorf("subscribe: destroying the key for %q: %w", subject, err)
	}

	ack, out := fanOut(reg, subject)
	return PurgeResult{
		Subject:      subject,
		Verb:         "shred",
		State:        stateFor(out),
		Acknowledged: ack,
		Outstanding:  out,
		erases:       true,
	}, nil
}

// Sweep reclaims space eventually, bounded by a horizon.
//
// ⚠ It reaches neither a backup nor a coded stripe already written elsewhere,
// and it makes nothing unreadable. A caller that needs erasure gets
// [ErrNotErasure] from [PurgeResult.AssertErased].
func Sweep(reg *Registry, subject string, h Horizon) (PurgeResult, error) {
	if h.Nanos <= 0 {
		return PurgeResult{
			Subject: subject, Verb: "sweep", State: PurgeRefused,
		}, fmt.Errorf("subscribe: a sweep needs a retention horizon, got %d", h.Nanos)
	}
	// A sweep is local reclamation. It does not fan out, because it is not
	// telling anyone to forget anything — it is reclaiming space nobody
	// references.
	return PurgeResult{
		Subject: subject,
		Verb:    "sweep",
		State:   PurgeDone,
		erases:  false,
	}, nil
}
