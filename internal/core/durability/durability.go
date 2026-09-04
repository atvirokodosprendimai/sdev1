package durability

import (
	"errors"
	"fmt"
)

// MinimumFloor is the lowest floor any policy may declare. It is a constant
// rather than a default because the moment an operator would relax it is the
// moment nobody is reading warnings.
const MinimumFloor = 2

// Tier names which half of the storage design a policy governs. The two halves
// have incompatible requirements, so a policy must say which it is for.
type Tier uint8

const (
	// TierUnset is the zero value and is not a valid tier. A policy that
	// defaulted into a tier would default into the wrong half of the design.
	TierUnset Tier = iota

	// Live is the mutable tail of the log, replicated whole because consensus
	// needs replicas that can serve a read and cast a vote alone.
	Live

	// Sealed is immutable segments, erasure-coded because nothing votes on them.
	Sealed
)

// String renders a tier for a diagnostic.
func (t Tier) String() string {
	switch t {
	case Live:
		return "live"
	case Sealed:
		return "sealed"
	default:
		return "unset"
	}
}

// ErrInvalidPolicy reports a policy shape that is refused at construction.
var ErrInvalidPolicy = errors.New("durability: invalid policy")

// Policy declares how durable data must be and across what it must be spread.
//
// It is constructed through [Replicated] or [Coded], never by literal, so an
// unsafe shape is not reachable.
type Policy struct {
	Tier        Tier
	Size        int
	MinSize     int
	DomainLevel string

	// DataShards and ParityShards are zero for a replicated policy.
	DataShards   int
	ParityShards int
}

// Replicated returns a live-tier policy holding whole copies.
func Replicated(size, minSize int, domainLevel string) (Policy, error) {
	p := Policy{Tier: Live, Size: size, MinSize: minSize, DomainLevel: domainLevel}
	if err := p.check(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Coded returns a sealed-tier policy holding erasure-coded fragments.
//
// Its target size is data+parity, because that is how many distinct places a
// stripe occupies and therefore how many failure domains it needs.
func Coded(data, parity, minSize int, domainLevel string) (Policy, error) {
	if data < 1 {
		return Policy{}, fmt.Errorf("%w: a coded policy needs at least one data shard, got %d",
			ErrInvalidPolicy, data)
	}
	if parity < 1 {
		return Policy{}, fmt.Errorf("%w: a coded policy needs at least one parity shard, got %d — "+
			"a code with no parity tolerates no loss and is replication wearing a coding label",
			ErrInvalidPolicy, parity)
	}
	p := Policy{
		Tier:         Sealed,
		Size:         data + parity,
		MinSize:      minSize,
		DomainLevel:  domainLevel,
		DataShards:   data,
		ParityShards: parity,
	}
	if err := p.check(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// check applies the refusals every policy shares.
func (p Policy) check() error {
	if p.Tier == TierUnset {
		return fmt.Errorf("%w: no tier named", ErrInvalidPolicy)
	}
	if p.DomainLevel == "" {
		return fmt.Errorf("%w: no failure domain level named; a policy that spreads across "+
			"nothing is not a durability policy", ErrInvalidPolicy)
	}
	if p.Size < 1 {
		return fmt.Errorf("%w: target size %d is not a number of copies", ErrInvalidPolicy, p.Size)
	}
	if p.MinSize < MinimumFloor {
		return fmt.Errorf("%w: floor %d is below the minimum of %d — data held once is data "+
			"one failure from gone, and a floor that permits it will eventually be set to it",
			ErrInvalidPolicy, p.MinSize, MinimumFloor)
	}
	if p.MinSize > p.Size {
		return fmt.Errorf("%w: floor %d exceeds target %d, so every write would be refused "+
			"on a healthy cluster", ErrInvalidPolicy, p.MinSize, p.Size)
	}
	return nil
}

// IsCoded reports whether this policy erasure-codes rather than replicating.
func (p Policy) IsCoded() bool { return p.ParityShards > 0 }

// DomainsNeeded is how many distinct failure domains the policy requires: one
// per copy when replicating, and data+parity when coding.
//
// This is the number a cluster's declared topology is checked against, and it is
// where the coding arithmetic bites — a (8,2) code needs ten domains at its
// level, so declaring it over three racks is refused rather than silently
// degraded.
func (p Policy) DomainsNeeded() int { return p.Size }

// String renders a policy for a diagnostic.
func (p Policy) String() string {
	if p.IsCoded() {
		return fmt.Sprintf("%s coded(%d,%d) min %d across %s",
			p.Tier, p.DataShards, p.ParityShards, p.MinSize, p.DomainLevel)
	}
	return fmt.Sprintf("%s replicated x%d min %d across %s",
		p.Tier, p.Size, p.MinSize, p.DomainLevel)
}
