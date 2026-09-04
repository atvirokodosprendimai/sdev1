package admit

import (
	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/observe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// State is whether a node is pulling read work.
//
// There are exactly two. A third — "draining", say — would need a rule about
// what it does with new work, and every answer to that is one of these two.
type State int

const (
	// StateUnset is the zero value and is never valid.
	StateUnset State = iota
	// StateJoined: the node is in the read queue and taking work.
	StateJoined
	// StateWithdrawn: the node has stopped pulling. It still serves writes,
	// still completes what it already accepted, and still emits.
	StateWithdrawn
)

func (s State) String() string {
	switch s {
	case StateJoined:
		return "joined"
	case StateWithdrawn:
		return "withdrawn"
	default:
		return "unset"
	}
}

// States returns the two states.
func States() []State { return []State{StateJoined, StateWithdrawn} }

// State reports the node's current queue membership.
func (c *Controller) State() State { return c.state }

// Decide re-evaluates queue membership from the read budget's utilisation, and
// reports the state along with whether it CHANGED.
//
// ★ The hysteresis is the band between the two thresholds: above the withdraw
// fraction the node leaves, below the rejoin fraction it returns, and BETWEEN
// THEM it keeps whatever it was. A decision that consulted one threshold would
// rejoin at exactly the load that made it leave, take a burst, and leave again —
// and that flapping costs more than the load did.
//
// ⚠ It touches only the pull of NEW read work. Work already accepted is
// unaffected, and the write budget is not consulted at all: a shed write has
// nowhere to go, so shedding one would be an outage rather than a re-route.
func (c *Controller) Decide() (state State, changed bool) {
	u := c.read.Utilisation()
	was := c.state

	switch {
	case u > c.read.Ceiling.Withdraw:
		c.state = StateWithdrawn
	case u < c.read.Ceiling.Rejoin:
		c.state = StateJoined
	default:
		// Inside the band. Keep what we had — this is the hysteresis.
	}

	return c.state, c.state != was
}

// Observed is what a state change is reported as.
//
// ★ An event is produced on a TRANSITION and never on a re-evaluation that
// changed nothing. Emitting per evaluation would bury the handful of transitions
// an operator is looking for in a stream of non-events.
func (c *Controller) Observed(leaf addr.LeafID, at tx.TxID, node string) (observe.Event, bool, error) {
	state, changed := c.Decide()
	if !changed {
		return observe.Event{}, false, nil
	}

	kind := observe.KindQueueRejoined
	if state == StateWithdrawn {
		kind = observe.KindQueueWithdrawn
	}
	ev, err := observe.Emit(kind, leaf, at, map[string]string{
		"node":        node,
		"utilisation": formatFraction(c.read.Utilisation()),
		"threshold":   formatFraction(thresholdFor(c.read.Ceiling, state)),
	})
	if err != nil {
		return observe.Event{}, false, err
	}
	return ev, true, nil
}

// thresholdFor is the threshold that was crossed to reach a state, so the event
// says what the node was measured against rather than leaving a reader to guess
// which of the two applied.
func thresholdFor(c Ceiling, s State) float64 {
	if s == StateWithdrawn {
		return c.Withdraw
	}
	return c.Rejoin
}

func formatFraction(f float64) string {
	// Three decimals is enough to distinguish thresholds without implying a
	// precision the measurement does not have.
	const scale = 1000
	whole := int(f * scale)
	return itoa(whole/scale) + "." + pad3(whole%scale)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func pad3(n int) string {
	if n < 0 {
		n = -n
	}
	s := itoa(n)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}
