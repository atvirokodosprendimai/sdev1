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
	// Entity names what is being read: one entity, or — when Inbound is set —
	// the entity whose referrers are being read.
	Entity string
	// Inbound reports that the source was written `FROM [Entity]`, so the
	// statement reads the SET of entities carrying a reference to Entity rather
	// than Entity itself (ADR-035).
	//
	// ★ One identifier, two questions. The brackets are a property of the source
	// rather than part of the name, so the same entity is addressed either way.
	Inbound bool
	// Where is the optional filter.
	Where *Predicate
	// Without names attributes the subject must NOT carry (ADR-036).
	//
	// ★ It is a CLAUSE and not a predicate, so `WHERE a = 'x' WITHOUT b` needs no
	// `AND`: two clauses conjoin by being two clauses. ADR-011 has no boolean
	// composition, and folding absence into `WHERE` would have forced some in.
	//
	// ⚠ These attributes are named in order to be ABSENT, so they are never
	// subject to ADR-035's rule that a member missing a named attribute is
	// dropped. Applying it here would make the clause unsatisfiable.
	Without []string
	// Page is the paging clause as WRITTEN. Only an inbound read may carry one.
	Page Page
	// Time is the qualifier as WRITTEN, before defaults.
	Time TimeClause
}

func (*Read) statement() {}

// Page is a `LIMIT n OFFSET m` clause exactly as written.
//
// ⚠ Has is not decoration. Without it the zero value reads as `LIMIT 0`, and
// "no paging clause" and "no rows" are opposite answers — so conflating them
// would make the emptier one the default for every statement that omits the
// clause.
//
// ⚠ A page is over MEMBERS and not over rows, and it is applied AFTER a member
// is dropped for missing an attribute the statement names. See ADR-035 rule 5:
// paging before the drop gives unpredictable page sizes, and paging over rows
// cuts a member in half.
type Page struct {
	// Limit is the maximum number of members returned.
	Limit int64
	// Offset is how many surviving members to skip first.
	Offset int64
	// Has reports that a clause was written at all.
	Has bool
}

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
