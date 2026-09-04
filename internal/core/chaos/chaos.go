package chaos

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
)

// Disposition is what happened to the system under a fault.
//
// ⚠ There are exactly three, and there is deliberately no fourth. A fourth value
// is how "we are looking into it" enters a catalogue, after which nothing in it
// is countable again.
type Disposition int

const (
	// DispositionUnset is the zero value and is never a valid answer.
	DispositionUnset Disposition = iota

	// Recovers: the system returned correct results, or refused correctly,
	// and nothing was lost.
	Recovers

	// UnrecoverableByDesign: the data is gone and that is the intended
	// behaviour, because the information was not present. This is a correct
	// answer, not a bug — a system that produced something anyway would be
	// inventing.
	UnrecoverableByDesign

	// UnrecoverableAndOpen: the system does not recover and it should. Every
	// entry with this disposition carries a fix or a written reason there is
	// none.
	UnrecoverableAndOpen
)

// String renders a disposition exactly as the catalogue spells it, because the
// catalogue check compares these strings.
func (d Disposition) String() string {
	switch d {
	case Recovers:
		return "recovers"
	case UnrecoverableByDesign:
		return "unrecoverable by design"
	case UnrecoverableAndOpen:
		return "unrecoverable and open"
	default:
		return "unset"
	}
}

// Dispositions returns the three valid values, for a test that wants to assert
// there are no others.
func Dispositions() []Disposition {
	return []Disposition{Recovers, UnrecoverableByDesign, UnrecoverableAndOpen}
}

// Outcome is what one injection observed.
type Outcome struct {
	// Disposition is what actually happened.
	Disposition Disposition
	// Detail says what was observed, including the evidence that the fault
	// actually LANDED. A fault that did not land produces a passing test about
	// nothing.
	Detail string
}

// Fault is one named, injectable way to break the system.
type Fault struct {
	// Name is the catalogue key.
	Name string
	// Record is the decision whose promise this fault tests, e.g. "ADR-006".
	// It is here so an assertion can point at the document that made the claim
	// rather than restating it.
	Record string
	// Expected is what the owning record says should happen.
	Expected Disposition
	// Inject performs the fault and reports what happened. An error means the
	// injection itself failed — an environment problem, never a finding.
	Inject func(rng *rand.Rand) (Outcome, error)
}

// ErrDuplicateFault reports two registrations of one name.
var ErrDuplicateFault = errors.New("chaos: a fault with that name is already registered")

// ErrPreconditionNotMet reports that a fault did not actually land, so whatever
// followed proves nothing.
var ErrPreconditionNotMet = errors.New("chaos: the fault did not land, so the result is about nothing")

var (
	registryMu sync.RWMutex
	registry   = map[string]Fault{}
)

// Register makes a fault runnable.
//
// ★ Registration is what SELECTS a fault. A fault that is written but never
// registered does not run, and the catalogue check fails on it — which is the
// difference between this and a package full of unreachable test helpers.
func Register(f Fault) error {
	if f.Name == "" || f.Record == "" || f.Inject == nil {
		return fmt.Errorf("chaos: a fault needs a name, the record it tests, and an injection (got %q, %q, %v)",
			f.Name, f.Record, f.Inject != nil)
	}
	if f.Expected == DispositionUnset {
		return fmt.Errorf("chaos: fault %q declares no expected disposition; "+
			"a fault whose expected outcome is unstated cannot fail", f.Name)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[f.Name]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateFault, f.Name)
	}
	registry[f.Name] = f
	return nil
}

// Registered returns every registered fault, ordered by name.
//
// ⚠ The ordering is not cosmetic. A schedule must be a pure function of its
// seed, and Go randomises map iteration deliberately — drawing straight from the
// map would make a "reproducible" schedule reproduce nothing.
func Registered() []Fault {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Fault, 0, len(registry))
	for _, f := range registry {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns one fault by name.
func Lookup(name string) (Fault, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	return f, ok
}

// Schedule is a replayable sequence of faults.
type Schedule struct {
	// Seed is everything needed to reproduce this run. Print it on failure.
	Seed int64
	// Faults are the names drawn, in order.
	Faults []string
}

// NewSchedule draws n faults from the registry.
//
// ★ It is a pure function of its seed. That is the decision that makes this
// whole exercise worth doing: a failure found here is replayable, so finding one
// and fixing one are the same activity rather than two separated by days of
// re-running.
func NewSchedule(seed int64, n int) Schedule {
	available := Registered()
	s := Schedule{Seed: seed, Faults: make([]string, 0, n)}
	if len(available) == 0 {
		return s
	}
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < n; i++ {
		s.Faults = append(s.Faults, available[rng.Intn(len(available))].Name)
	}
	return s
}

// Rand returns the generator for one step of a schedule, so an injection's own
// randomness is seeded from the schedule rather than from the clock.
func (s Schedule) Rand(step int) *rand.Rand {
	return rand.New(rand.NewSource(s.Seed + int64(step)*7919))
}
