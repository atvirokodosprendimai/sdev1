package hlc

import (
	"errors"
	"fmt"
)

var (
	// ErrSkewTooLarge reports a remote timestamp further from this node's own
	// wall reading than the declared bound allows.
	//
	// ⚠ The MESSAGE is refused, not the node. A node with a skewed clock is
	// otherwise healthy — its data is correct and its storage is fine, and only
	// its timestamps are wrong. Refusing its messages already stops the skew
	// spreading; evicting it additionally loses a working replica over a clock.
	ErrSkewTooLarge = errors.New("hlc: the remote clock is further ahead than the bound allows")

	// ErrNoBound reports an undeclared skew bound.
	//
	// ★ There is deliberately no default. A datacentre and a wide-area link
	// tolerate different skew, so any constant chosen here is wrong somewhere and
	// is a number nobody picked.
	ErrNoBound = errors.New("hlc: a skew bound must be declared and positive")
)

// Bound is how far ahead of this node's own wall reading a remote timestamp may
// be before it is refused.
type Bound struct {
	// MaxSkew is the tolerance, in nanoseconds.
	MaxSkew int64
}

// Valid reports whether a bound was declared.
func (b Bound) Valid() error {
	if b.MaxSkew <= 0 {
		return fmt.Errorf("%w: got %d nanoseconds", ErrNoBound, b.MaxSkew)
	}
	return nil
}

// Skew is how far a remote reading sits from the receiver's own.
//
// ★ It is the RECEIVER's measurement. A node whose clock is wrong is exactly the
// node whose self-assessment is wrong, so a self-reported skew is the suspect
// testifying.
//
// ⚠ And it measures the DIFFERENCE between two clocks, not either one's error.
// A receiver whose own clock is wrong will refuse correct peers, confidently.
// That is a limit of the approach rather than of this implementation: two clocks
// can only ever establish that they disagree, and saying which is wrong needs a
// third party this system does not have.
type Skew struct {
	// Ahead is how far the remote reading is beyond the receiver's, in
	// nanoseconds. It is negative when the remote is behind.
	Ahead int64
}

// Exceeds reports whether this skew is past the bound.
//
// ⚠ Only a remote AHEAD of the receiver is bounded. A remote behind is harmless
// to monotonicity — merging it cannot drag this clock forward — and refusing it
// would reject a node whose only fault is being slow.
//
// A remote exactly AT the bound is accepted. An off-by-one in the other
// direction refuses honest peers at precisely the tolerance an operator declared
// as acceptable.
func (s Skew) Exceeds(b Bound) bool { return s.Ahead > b.MaxSkew }

// SkewOf measures a remote reading against a local wall reading.
func SkewOf(remote Timestamp, localWall int64) Skew {
	return Skew{Ahead: remote.Wall - localWall}
}

// Admit checks a remote timestamp against the bound and merges it only if it
// passes.
//
// ⚠ THE CHECK PRECEDES THE MERGE, AND A REFUSAL LEAVES THE CLOCK UNTOUCHED.
// ★ That ordering is the whole decision. [Clock.Merge] is irreversible by
// construction — monotonicity is the property that forbids coming back — so a
// check performed afterwards is not a gate, it is a report of damage already
// done. A caller cannot tell the two apart from the error alone, which is why the
// test asserts on the clock.
//
// ⚠ Use this at a NETWORK boundary. For a timestamp read back from durable
// storage use [Clock.Merge]: that is already-accepted history, whatever skew it
// carries has already happened, and refusing it would make committed data
// unreadable — a clock problem turned into data loss.
func (c *Clock) Admit(remote Timestamp, b Bound) (Timestamp, error) {
	if err := b.Valid(); err != nil {
		return Timestamp{}, err
	}

	c.mu.Lock()
	skew := SkewOf(remote, c.now())
	c.mu.Unlock()

	if skew.Exceeds(b) {
		// ⚠ Return BEFORE merging. Nothing above this line touched c.last.
		return Timestamp{}, fmt.Errorf("%w: remote is %dns ahead, bound is %dns",
			ErrSkewTooLarge, skew.Ahead, b.MaxSkew)
	}
	return c.Merge(remote), nil
}
