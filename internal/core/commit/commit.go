package commit

import (
	"errors"
	"fmt"
	"sort"

	"github.com/atvirokodosprendimai/sdev1/internal/core/durability"
	"github.com/atvirokodosprendimai/sdev1/internal/core/lease"
)

// The three ways a commit fails.
//
// ★ Three, not one, because they need three different responses: restore
// capacity, fix placement, or stop writing. Collapsing them loses the operator's
// next action rather than any behaviour, which is why it is easy to do and worth
// resisting.
var (
	// ErrBelowFloor: not enough distinct domains acknowledged. Restore capacity.
	ErrBelowFloor = errors.New("commit: fewer distinct failure domains than the floor requires")

	// ErrOneDomain: enough acknowledgements, but they collapse to too few
	// domains. Fix placement — the count looks right and is worthless.
	ErrOneDomain = errors.New("commit: the acknowledgements do not span enough distinct failure domains")

	// ErrStaleEpoch: acknowledgements were made to a writer that has since been
	// superseded. Stop writing; this node no longer holds the leaf.
	ErrStaleEpoch = errors.New("commit: acknowledged under an epoch that has been superseded")
)

// Ack is one replica's acknowledgement that it holds an entry in memory.
type Ack struct {
	// Node is who acknowledged.
	Node string
	// Domain is the failure domain it sits in, at the level the condition is
	// judged at. Two acks with one domain are one failure.
	Domain string
	// Epoch is the writer's lease epoch at the time. An ack made to a
	// superseded writer is an ack made to nobody.
	Epoch lease.Epoch
}

// Condition is what must be true for a write to be committed.
type Condition struct {
	// Policy supplies the floor. This record adds no second count.
	Policy durability.Policy
	// DomainLevel is the level distinctness is judged at.
	//
	// ⚠ It is explicit rather than taken from the policy, because a memory
	// commit and a sealed-segment placement guard against different failures:
	// power for the first, a machine or a disk for the second.
	DomainLevel string
}

// NewCondition builds a condition, refusing one that could not mean anything.
func NewCondition(p durability.Policy, domainLevel string) (Condition, error) {
	if domainLevel == "" {
		return Condition{}, fmt.Errorf("commit: a condition needs a domain level; for a memory " +
			"commit the failure guarded against is power, and leaving it unstated would silently " +
			"judge distinctness at whatever the durability policy happens to use")
	}
	if p.MinSize < durability.MinimumFloor {
		return Condition{}, fmt.Errorf("commit: floor %d is below the minimum of %d",
			p.MinSize, durability.MinimumFloor)
	}
	return Condition{Policy: p, DomainLevel: domainLevel}, nil
}

// Satisfied reports whether a set of acknowledgements commits a write under the
// given current epoch.
//
// ★ It counts DISTINCT DOMAINS, never acknowledgements. Three replies from three
// processes on one power feed is one failure domain wearing three names, and the
// count looks identical to real triple durability.
//
// ⚠ It returns an error naming WHICH of the three failures occurred, because
// they call for different actions.
func (c Condition) Satisfied(acks []Ack, current lease.Epoch) error {
	// An acknowledgement made to a superseded writer is an acknowledgement made
	// to nobody, so it is discarded before anything is counted.
	live := make([]Ack, 0, len(acks))
	stale := 0
	for _, a := range acks {
		if a.Epoch < current {
			stale++
			continue
		}
		live = append(live, a)
	}

	domains := DistinctDomains(live)

	if len(domains) >= c.Policy.MinSize {
		return nil
	}

	// Enough replies but too few domains is a DIFFERENT problem from too few
	// replies, and an operator fixes them differently.
	if len(live) >= c.Policy.MinSize {
		return fmt.Errorf("%w: %d acknowledgement(s) span only %d domain(s) at level %q, and the "+
			"floor is %d — the count looks sufficient and is one failure from nothing",
			ErrOneDomain, len(live), len(domains), c.DomainLevel, c.Policy.MinSize)
	}

	if stale > 0 && len(live)+stale >= c.Policy.MinSize {
		return fmt.Errorf("%w: %d of %d acknowledgement(s) were made below epoch %d",
			ErrStaleEpoch, stale, len(acks), current)
	}

	return fmt.Errorf("%w: %d distinct domain(s) at level %q, and the floor is %d",
		ErrBelowFloor, len(domains), c.DomainLevel, c.Policy.MinSize)
}

// DistinctDomains returns the failure domains represented, ordered.
//
// ★ This is the whole arithmetic of the record, exposed so a caller can show an
// operator what was actually spanned rather than only that it was insufficient.
func DistinctDomains(acks []Ack) []string {
	seen := map[string]bool{}
	for _, a := range acks {
		if a.Domain == "" {
			// A domain nobody declared cannot be shown to be distinct from any
			// other, so it counts as no domain at all rather than as its own.
			continue
		}
		seen[a.Domain] = true
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
