package durability

import (
	"errors"
	"fmt"
)

// ErrBelowFloor reports that fewer distinct failure domains currently hold
// copies than the policy's floor requires.
//
// A write receiving this is refused. That is an availability cost taken
// deliberately: a cluster that has lost too many copies becomes read-only for
// the affected leaves rather than accepting data at a durability nobody has.
var ErrBelowFloor = errors.New("durability: below the policy floor")

// Satisfied reports whether the given domains currently meet the policy's floor.
//
// ★ It counts DISTINCT domains, not copies, and that is the whole point. Three
// copies in one rack are ONE domain, and a policy spreading across racks is not
// satisfied by them — a rack failure would take all three. Counting replicas
// instead would report a healthy cluster with every copy in the same place.
//
// ⚠ It answers a different question from [Policy.Validate]. A policy can be
// perfectly feasible against the declared topology and unsatisfied right now,
// which is not a contradiction: the first is about configuration, this is about
// current state.
func (p Policy) Satisfied(domains []string) error {
	distinct := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		if d == "" {
			continue
		}
		distinct[d] = struct{}{}
	}
	if len(distinct) < p.MinSize {
		return fmt.Errorf("%w: %d distinct %s domain(s) hold copies, policy %s requires at least %d",
			ErrBelowFloor, len(distinct), p.DomainLevel, p, p.MinSize)
	}
	return nil
}
