package durability

import (
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/sdev1/internal/core/topology"
)

// mapWithRacks builds a topology declaring n racks, each holding one server.
func mapWithRacks(t *testing.T, n int) topology.Map {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"version":1,"depth":1,"levels":["datacenter","rack","server"],`)
	b.WriteString(`"root":{"level":"datacenter","name":"dc","children":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmtRack(&b, i)
	}
	b.WriteString("]}}")

	m, err := topology.Load(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("Load a %d-rack map: %v", n, err)
	}
	return m
}

func fmtRack(b *strings.Builder, i int) {
	name := string(rune('a' + i%26))
	if i >= 26 {
		name += string(rune('a' + i/26))
	}
	b.WriteString(`{"level":"rack","name":"rack-` + name + `","children":[`)
	b.WriteString(`{"level":"server","name":"srv-` + name + `"}]}`)
}

// TestPolicyBelowMinSizeIsRefused is the falsifier ADR-004 names in its
// Enforced-by header.
//
// A floor of one permits data held once. The refusal is at construction rather
// than at use because the moment an operator would relax it — mid-incident, to
// get writes flowing — is the moment nobody is reading warnings.
func TestPolicyBelowMinSizeIsRefused(t *testing.T) {
	for _, floor := range []int{-1, 0, 1} {
		if _, err := Replicated(3, floor, "rack"); !errors.Is(err, ErrInvalidPolicy) {
			t.Errorf("Replicated with floor %d: error = %v, want ErrInvalidPolicy", floor, err)
		}
		if _, err := Coded(8, 2, floor, "rack"); !errors.Is(err, ErrInvalidPolicy) {
			t.Errorf("Coded with floor %d: error = %v, want ErrInvalidPolicy", floor, err)
		}
	}
	// And the minimum itself is accepted, or the check would be refusing
	// everything rather than refusing what is unsafe.
	if _, err := Replicated(3, MinimumFloor, "rack"); err != nil {
		t.Errorf("Replicated at the minimum floor was refused: %v", err)
	}
}

// TestPolicyFloorCannotExceedTarget checks a floor above the target is refused,
// since it would fail every write on a perfectly healthy cluster.
func TestPolicyFloorCannotExceedTarget(t *testing.T) {
	if _, err := Replicated(2, 3, "rack"); !errors.Is(err, ErrInvalidPolicy) {
		t.Errorf("floor above target: error = %v, want ErrInvalidPolicy", err)
	}
	if _, err := Replicated(3, 3, "rack"); err != nil {
		t.Errorf("floor equal to target was refused: %v", err)
	}
}

// TestReplicatedNeedsOneDomainPerCopy checks a replicated policy needs as many
// distinct domains as it holds copies.
func TestReplicatedNeedsOneDomainPerCopy(t *testing.T) {
	p, err := Replicated(3, 2, "rack")
	if err != nil {
		t.Fatalf("Replicated: %v", err)
	}
	if got := p.DomainsNeeded(); got != 3 {
		t.Errorf("DomainsNeeded = %d, want 3", got)
	}
	if p.IsCoded() {
		t.Error("a replicated policy reports itself as coded")
	}
	if p.Tier != Live {
		t.Errorf("Tier = %v, want live", p.Tier)
	}
}

// TestCodedNeedsDataPlusParityDomains is the arithmetic that makes coding and
// survival trade against each other: a (8,2) stripe occupies ten places, so it
// needs ten failure domains at the level it means to survive. Across three
// servers it survives nothing at the server level, however its fragments are
// arranged.
func TestCodedNeedsDataPlusParityDomains(t *testing.T) {
	p, err := Coded(8, 2, 2, "rack")
	if err != nil {
		t.Fatalf("Coded: %v", err)
	}
	if got := p.DomainsNeeded(); got != 10 {
		t.Errorf("DomainsNeeded = %d, want 10 (8 data + 2 parity)", got)
	}
	if !p.IsCoded() {
		t.Error("a coded policy does not report itself as coded")
	}
	if p.Tier != Sealed {
		t.Errorf("Tier = %v, want sealed", p.Tier)
	}
}

// TestCodedRefusesZeroParity checks a code tolerating no loss is refused.
func TestCodedRefusesZeroParity(t *testing.T) {
	if _, err := Coded(8, 0, 2, "rack"); !errors.Is(err, ErrInvalidPolicy) {
		t.Errorf("Coded with zero parity: error = %v, want ErrInvalidPolicy", err)
	}
	if _, err := Coded(0, 2, 2, "rack"); !errors.Is(err, ErrInvalidPolicy) {
		t.Errorf("Coded with zero data shards: error = %v, want ErrInvalidPolicy", err)
	}
}

// TestTierIsExplicit checks a policy names its tier and cannot default into one,
// and that the policy carries both knobs plus the domain level.
func TestTierIsExplicit(t *testing.T) {
	if TierUnset != 0 {
		t.Error("the zero value of Tier is not TierUnset, so a zero policy would name a real tier")
	}
	// A policy assembled by literal with no tier is refused by check().
	bare := Policy{Size: 3, MinSize: 2, DomainLevel: "rack"}
	if err := bare.check(); !errors.Is(err, ErrInvalidPolicy) {
		t.Errorf("a policy with no tier: error = %v, want ErrInvalidPolicy", err)
	}
	// And a policy with no domain level is refused, since spreading across
	// nothing is not a durability policy.
	noLevel := Policy{Tier: Live, Size: 3, MinSize: 2}
	if err := noLevel.check(); !errors.Is(err, ErrInvalidPolicy) {
		t.Errorf("a policy with no domain level: error = %v, want ErrInvalidPolicy", err)
	}

	p, err := Replicated(3, 2, "rack")
	if err != nil {
		t.Fatalf("Replicated: %v", err)
	}
	if p.Size != 3 || p.MinSize != 2 || p.DomainLevel != "rack" {
		t.Errorf("policy did not carry its knobs: %+v", p)
	}
}

// TestValidateRefusesTooFewDomains checks a policy the cluster could never
// satisfy is refused when it is loaded, with both numbers named — "not enough
// domains" without them tells an operator nothing actionable.
func TestValidateRefusesTooFewDomains(t *testing.T) {
	p, err := Coded(8, 2, 2, "rack")
	if err != nil {
		t.Fatalf("Coded: %v", err)
	}
	err = p.Validate(mapWithRacks(t, 3))
	if !errors.Is(err, ErrInsufficientDomains) {
		t.Fatalf("a (8,2) policy over 3 racks: error = %v, want ErrInsufficientDomains", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "10") || !strings.Contains(msg, "3") {
		t.Errorf("error %q does not name both the requirement and what the map offers", msg)
	}
}

// TestValidateAcceptsASufficientMap checks the same policy is accepted when the
// map offers enough domains, so the check is not merely always-refusing.
func TestValidateAcceptsASufficientMap(t *testing.T) {
	p, err := Coded(8, 2, 2, "rack")
	if err != nil {
		t.Fatalf("Coded: %v", err)
	}
	if err := p.Validate(mapWithRacks(t, 10)); err != nil {
		t.Errorf("a (8,2) policy over 10 racks was refused: %v", err)
	}
	if err := p.Validate(mapWithRacks(t, 12)); err != nil {
		t.Errorf("a (8,2) policy over 12 racks was refused: %v", err)
	}
}

// TestValidateRefusesAnUndeclaredLevel checks a typo cannot silently disable the
// spread requirement by naming a level nobody declared.
func TestValidateRefusesAnUndeclaredLevel(t *testing.T) {
	p, err := Replicated(3, 2, "raxk")
	if err != nil {
		t.Fatalf("Replicated: %v", err)
	}
	if err := p.Validate(mapWithRacks(t, 10)); !errors.Is(err, ErrUndeclaredLevel) {
		t.Fatalf("an undeclared domain level: error = %v, want ErrUndeclaredLevel — "+
			"an unknown level must not be read as no constraint", err)
	}
}

// TestSatisfiedRefusesBelowFloor checks a degraded cluster stops accepting
// writes rather than accepting them at a durability nobody has.
func TestSatisfiedRefusesBelowFloor(t *testing.T) {
	p, err := Replicated(3, 2, "rack")
	if err != nil {
		t.Fatalf("Replicated: %v", err)
	}
	if err := p.Satisfied([]string{"rack-a"}); !errors.Is(err, ErrBelowFloor) {
		t.Errorf("one domain against a floor of 2: error = %v, want ErrBelowFloor", err)
	}
	if err := p.Satisfied(nil); !errors.Is(err, ErrBelowFloor) {
		t.Errorf("no domains: error = %v, want ErrBelowFloor", err)
	}
	if err := p.Satisfied([]string{"rack-a", "rack-b"}); err != nil {
		t.Errorf("two domains against a floor of 2 was refused: %v", err)
	}
}

// TestSatisfiedCountsDistinctDomains is the assertion that separates spread from
// replica count. Three copies in one rack are ONE domain, and a rack failure
// takes all three — counting replicas would report that cluster healthy.
func TestSatisfiedCountsDistinctDomains(t *testing.T) {
	p, err := Replicated(3, 2, "rack")
	if err != nil {
		t.Fatalf("Replicated: %v", err)
	}
	err = p.Satisfied([]string{"rack-a", "rack-a", "rack-a"})
	if !errors.Is(err, ErrBelowFloor) {
		t.Fatalf("three copies in ONE rack: error = %v, want ErrBelowFloor — "+
			"counting replicas instead of domains reports a healthy cluster with "+
			"every copy in the same failure domain", err)
	}
	if err := p.Satisfied([]string{"rack-a", "rack-a", "rack-b"}); err != nil {
		t.Errorf("two distinct domains among three copies was refused: %v", err)
	}
}

// TestValidateAndSatisfiedAreIndependent checks neither answers the other's
// question: a policy can be perfectly feasible and currently unsatisfied.
func TestValidateAndSatisfiedAreIndependent(t *testing.T) {
	p, err := Replicated(3, 2, "rack")
	if err != nil {
		t.Fatalf("Replicated: %v", err)
	}
	m := mapWithRacks(t, 10)

	if err := p.Validate(m); err != nil {
		t.Fatalf("the policy is not feasible against a 10-rack map: %v", err)
	}
	if err := p.Satisfied([]string{"rack-a"}); !errors.Is(err, ErrBelowFloor) {
		t.Fatalf("feasible AND satisfied by one domain: error = %v, want ErrBelowFloor — "+
			"the two checks have collapsed into one", err)
	}

	// And the converse: infeasible while the domains currently present would
	// clear the floor.
	big, err := Coded(8, 2, 2, "rack")
	if err != nil {
		t.Fatalf("Coded: %v", err)
	}
	if err := big.Validate(mapWithRacks(t, 3)); !errors.Is(err, ErrInsufficientDomains) {
		t.Fatalf("expected the big policy to be infeasible over 3 racks, got %v", err)
	}
	if err := big.Satisfied([]string{"rack-a", "rack-b"}); err != nil {
		t.Errorf("the floor check consulted the topology: %v", err)
	}
}
