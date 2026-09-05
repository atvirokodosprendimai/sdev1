package ql

import (
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// Statement is anything the language can express.
type Statement interface{ statement() }

// Read reads attributes of an entity, optionally filtered and optionally
// qualified in time.
//
// ★ The verb is READ and the type is named for it (ADR-034). The old spelling
// was SELECT, borrowed from a family — INSERT, UPDATE, DELETE — that this store
// will never have, because it appends. Renaming the keyword and leaving the type
// called `Select` would be a name that says one thing and means another, which is
// the trap ADR-032 removed one record earlier.
type Read struct {
	// Attributes is the projection; empty means every attribute.
	Attributes []string
	// Entity names what is being read.
	Entity string
	// Where is the optional filter.
	Where *Predicate
	// Time is the qualifier as WRITTEN, before defaults.
	Time TimeClause
}

func (*Read) statement() {}

// Predicate is one comparison.
type Predicate struct {
	Attribute string
	Op        string
	Value     string
	// ValueIsNumber records whether the literal was written as a number, so a
	// later evaluator does not have to guess.
	ValueIsNumber bool
}

// TimeClause holds the time qualifiers exactly as the caller wrote them, before
// any default is applied.
//
// ⚠ Keeping "as written" separate from "resolved" is what makes the defaults
// checkable. A clause that applied defaults during parsing would leave nothing
// to compare against ADR-002's table.
type TimeClause struct {
	// ValidAt is the instant from `AS OF t`, or nil.
	ValidAt *int64
	// AsOf is the transaction from `TRANSACTION u`, or nil.
	AsOf *tx.TxID
}

// Resolve applies the defaults and returns the query the storage layer takes.
//
// ★ It calls [temporal.ResolveQualifiers] and decides nothing itself. The
// defaults table has exactly ONE implementation, in the package that owns what
// time means — two would drift, and the drift is invisible until a query returns
// the wrong history.
//
// ⚠ This function contains no branch, deliberately. A four-row table cannot be
// implemented without one, so its absence is the property a guard can check.
func (c TimeClause) Resolve(now int64) temporal.Query {
	return temporal.ResolveQualifiers(temporal.Query{AsOf: c.AsOf, ValidAt: c.ValidAt}, now)
}
