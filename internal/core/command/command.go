package command

import (
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

// ErrCrossEntity reports an operation naming an entity other than the one the
// transaction is bound to.
//
// It is a named sentinel so a caller can match on it, and so the first genuine
// case of a domain needing a multi-entity transaction surfaces as a specific
// refusal rather than as a confusing failure somewhere downstream.
var ErrCrossEntity = errors.New("command: a transaction touches exactly one entity")

// ErrNoEntity reports a transaction constructed without an entity.
var ErrNoEntity = errors.New("command: a transaction must name an entity")

// Transaction is a set of datoms about one entity, ready to commit.
//
// The entity and the leaf it resolves to are fixed at construction. No method
// changes either, which is what makes "one entity per transaction" a property of
// the type rather than a habit of its callers.
type Transaction struct {
	entity string
	leaf   addr.LeafID
	datoms []ports.Datom
}

// New returns a transaction bound to one entity, resolving it to a leaf at the
// cluster's declared depth.
func New(entity string, depth uint8) (*Transaction, error) {
	if entity == "" {
		return nil, ErrNoEntity
	}
	leaf, err := addr.Descend(addr.KeyOf(entity), depth)
	if err != nil {
		return nil, fmt.Errorf("command: resolving %q: %w", entity, err)
	}
	return &Transaction{entity: entity, leaf: leaf}, nil
}

// Entity returns the entity this transaction is bound to.
func (t *Transaction) Entity() string { return t.entity }

// Leaf returns the leaf every datom in this transaction resolves to. It is what
// makes the commit single-leaf, and therefore local.
func (t *Transaction) Leaf() addr.LeafID { return t.leaf }

// Datoms returns the datoms recorded so far.
func (t *Transaction) Datoms() []ports.Datom {
	out := make([]ports.Datom, len(t.datoms))
	copy(out, t.datoms)
	return out
}

// Assert records that an entity had an attribute with a value over a business
// interval.
//
// The entity is a parameter rather than implied, so a caller building datoms in
// a loop is refused rather than silently writing to the wrong transaction.
func (t *Transaction) Assert(entity, attribute string, value []byte, valid temporal.Interval) error {
	return t.add(entity, attribute, value, valid, true)
}

// Retract records that an entity's attribute stopped being true over a business
// interval.
//
// It appends a datom with the assert flag cleared; it never removes one.
func (t *Transaction) Retract(entity, attribute string, value []byte, valid temporal.Interval) error {
	return t.add(entity, attribute, value, valid, false)
}

// add is the single place the boundary is enforced.
//
// ⚠ The check happens BEFORE the datom is appended, so a refused operation
// leaves the transaction exactly as it was. A caller can discard a rejected
// transaction without cleanup, and a partially-applied transaction is not a
// state this type can be in.
func (t *Transaction) add(entity, attribute string, value []byte, valid temporal.Interval, assert bool) error {
	if entity != t.entity {
		return fmt.Errorf("%w: this transaction is bound to %q and cannot touch %q",
			ErrCrossEntity, t.entity, entity)
	}
	t.datoms = append(t.datoms, ports.Datom{
		Entity:    entity,
		Attribute: attribute,
		Value:     value,
		Valid:     valid,
		Assert:    assert,
	})
	return nil
}
