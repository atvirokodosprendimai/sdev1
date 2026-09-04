package temporal

import "github.com/atvirokodosprendimai/sdev1/internal/core/tx"

// Query carries a caller's two time qualifiers, each independently optional.
//
// A nil AsOf means the transaction axis is OPEN: no datom is excluded for
// having been recorded late. A nil ValidAt is resolved to the current instant
// by [ResolveQualifiers], after which it is always bound.
//
// Both are pointers so that "not supplied" is representable and cannot be
// confused with a zero value — an instant of zero is a legitimate question.
//
// ★ The two fields also have DIFFERENT TYPES, and that is deliberate rather
// than incidental. The defect this package exists to prevent is a caller
// passing one value into both parameters; when both axes are plain instants
// that is a one-character mistake, and when one is an instant and the other a
// transaction identifier the compiler refuses it. The type system carries part
// of the rule that would otherwise rest entirely on review.
type Query struct {
	AsOf    *tx.TxID
	ValidAt *int64
}

// ResolveQualifiers applies the defaults.
//
// This is ADR-002 rule 6's table, written as a table rather than as branching
// prose so that it can be read against the record it implements:
//
//	the caller wrote        AsOf becomes    ValidAt becomes
//	nothing                 open            now
//	AS OF t                 open            t
//	AS OF t TRANSACTION u   u               t
//	TRANSACTION u           u               now
//
// ★ The load-bearing row is the second. A lone instant binds BUSINESS time and
// leaves the transaction axis OPEN. Binding it to both is the defect described
// in this package's documentation, and it is the behaviour a reasonable
// implementer writes by default — which is why it is a stated rule rather than
// a default nobody wrote down.
func ResolveQualifiers(q Query, now int64) Query {
	resolved := Query{AsOf: q.AsOf}
	if q.ValidAt != nil {
		instant := *q.ValidAt
		resolved.ValidAt = &instant
	} else {
		instant := now
		resolved.ValidAt = &instant
	}
	return resolved
}
