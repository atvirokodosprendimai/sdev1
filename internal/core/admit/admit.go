package admit

import (
	"errors"
	"fmt"
)

var (
	// ErrNoCeiling reports a ceiling that was not declared.
	//
	// ★ There is no default. A node that measures its own ceiling discovers it
	// by exceeding it, so the ceiling is stated by an operator and its absence is
	// an error rather than something to guess.
	ErrNoCeiling = errors.New("admit: a ceiling must be declared and positive")

	// ErrThresholdsInverted reports a rejoin threshold that is not strictly
	// below the withdraw threshold.
	//
	// ⚠ Equal is refused too, and equal is the case people write. It oscillates
	// by construction: the node rejoins at exactly the load that made it leave,
	// takes a burst, and leaves again.
	ErrThresholdsInverted = errors.New("admit: the rejoin threshold must be strictly below the withdraw threshold")
)

// Kind is which budget.
//
// ⚠ There are exactly two and there is deliberately nowhere to put a third. A
// shared or total budget is the thing this package exists to prevent, and it is
// prevented by there being no way to express one.
type Kind int

const (
	// KindUnset is the zero value and is never valid.
	KindUnset Kind = iota
	// KindRead is elastic: any replica can serve a read, so read work can be
	// shed to a peer.
	KindRead
	// KindWrite is not: a leaf has one writer, so a shed write is an outage
	// rather than a re-route.
	KindWrite
)

func (k Kind) String() string {
	switch k {
	case KindRead:
		return "read"
	case KindWrite:
		return "write"
	default:
		return "unset"
	}
}

// Kinds returns the two budget kinds.
func Kinds() []Kind { return []Kind{KindRead, KindWrite} }

// Ceiling is what a node has declared about its capacity.
type Ceiling struct {
	// BitsPerSecond is the link, as stated by an operator.
	BitsPerSecond float64
	// Withdraw is the utilisation fraction at which a node stops pulling read
	// work.
	Withdraw float64
	// Rejoin is the fraction at which it starts again. Strictly below Withdraw;
	// the gap between them is the hysteresis.
	Rejoin float64
}

// NewCeiling declares a ceiling, refusing one that cannot work.
func NewCeiling(bitsPerSecond, withdraw, rejoin float64) (Ceiling, error) {
	if bitsPerSecond <= 0 {
		return Ceiling{}, fmt.Errorf("%w: got %v bits per second", ErrNoCeiling, bitsPerSecond)
	}
	if withdraw <= 0 || withdraw > 1 {
		return Ceiling{}, fmt.Errorf("%w: a withdraw fraction of %v is not in (0, 1]",
			ErrNoCeiling, withdraw)
	}
	if rejoin < 0 || rejoin >= withdraw {
		return Ceiling{}, fmt.Errorf("%w: rejoin %v against withdraw %v", ErrThresholdsInverted, rejoin, withdraw)
	}
	return Ceiling{BitsPerSecond: bitsPerSecond, Withdraw: withdraw, Rejoin: rejoin}, nil
}

// Budget is one kind's capacity and its current load.
//
// ⚠ It holds its OWN ceiling and its own load. Two budgets sharing either would
// let read load consume write capacity, which is the failure this package exists
// to prevent.
type Budget struct {
	Kind    Kind
	Ceiling Ceiling
	// bitsPerSecond is the observed rate. Nothing here measures it; it is
	// supplied, because the counters that carry it belong elsewhere.
	bitsPerSecond float64
}

// Observe records the current rate for this budget.
func (b *Budget) Observe(bitsPerSecond float64) { b.bitsPerSecond = bitsPerSecond }

// Rate is the observed rate.
func (b *Budget) Rate() float64 { return b.bitsPerSecond }

// Utilisation is the observed rate as a fraction of the declared ceiling.
//
// ★ A fraction rather than an absolute, so one threshold means the same thing on
// a node with a ten-gigabit link and one with a gigabit link.
func (b *Budget) Utilisation() float64 {
	if b.Ceiling.BitsPerSecond <= 0 {
		return 0
	}
	return b.bitsPerSecond / b.Ceiling.BitsPerSecond
}

// Controller holds a node's budgets.
type Controller struct {
	read  Budget
	write Budget
	state State
}

// NewController declares a node's capacity for both kinds.
//
// The two ceilings are separate arguments rather than one, because a single
// ceiling would be a shared budget wearing two names.
func NewController(readCeiling, writeCeiling Ceiling) (*Controller, error) {
	if readCeiling.BitsPerSecond <= 0 || writeCeiling.BitsPerSecond <= 0 {
		return nil, fmt.Errorf("%w: both kinds need a declared ceiling", ErrNoCeiling)
	}
	return &Controller{
		read:  Budget{Kind: KindRead, Ceiling: readCeiling},
		write: Budget{Kind: KindWrite, Ceiling: writeCeiling},
		state: StateJoined,
	}, nil
}

// Budget returns one kind's budget.
func (c *Controller) Budget(k Kind) *Budget {
	switch k {
	case KindRead:
		return &c.read
	case KindWrite:
		return &c.write
	default:
		return nil
	}
}

// Observe records a rate against one kind.
func (c *Controller) Observe(k Kind, bitsPerSecond float64) error {
	b := c.Budget(k)
	if b == nil {
		return fmt.Errorf("admit: %v is not a budget kind", k)
	}
	b.Observe(bitsPerSecond)
	return nil
}

// Admits reports whether this kind is currently taking work.
//
// ★ A write ALWAYS admits. A leaf has one writer, so refusing a write here would
// be an outage rather than a re-route — and a read burst must never be able to
// cause one.
func (c *Controller) Admits(k Kind) bool {
	if k == KindWrite {
		return true
	}
	return c.state == StateJoined
}
