package durability

import (
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/topology"
)

// ErrInsufficientDomains reports a topology that offers fewer failure domains
// than a policy requires. It is returned when the policy is loaded, not when a
// disk fails.
var ErrInsufficientDomains = errors.New("durability: topology offers too few failure domains")

// ErrUndeclaredLevel reports a domain level the map does not declare.
var ErrUndeclaredLevel = errors.New("durability: topology declares no such level")

// Validate reports whether a cluster with this topology could EVER satisfy the
// policy.
//
// It counts DISTINCT nodes at the policy's domain level and compares that with
// [Policy.DomainsNeeded]. A (8,2) code needs ten domains, so declaring it over a
// three-rack map is a configuration error, and this is where it surfaces —
// rather than during a repair, which is when an operator has least attention to
// spare.
//
// ⚠ It answers a different question from [Policy.Satisfied] and neither
// substitutes for the other. This one catches a policy that could never work;
// that one catches a cluster that has stopped working.
//
// ⚠ It checks a DECLARATION. A map claiming ten racks that share one power feed
// declares ten domains and has one, and nothing here can tell the difference.
func (p Policy) Validate(m topology.Map) error {
	idx := m.LevelIndex(p.DomainLevel)
	if idx < 0 {
		return fmt.Errorf("%w: %q (declared levels: %v) — an unknown level is refused rather "+
			"than treated as no constraint, so a typo cannot silently disable the spread",
			ErrUndeclaredLevel, p.DomainLevel, m.Levels)
	}

	domains := 0
	for _, n := range m.Nodes {
		if n.LevelIdx == idx {
			domains++
		}
	}
	if need := p.DomainsNeeded(); domains < need {
		return fmt.Errorf("%w: policy %s needs %d distinct %s domains, the map declares %d",
			ErrInsufficientDomains, p, need, p.DomainLevel, domains)
	}
	return nil
}
