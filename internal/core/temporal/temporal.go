package temporal

import (
	"fmt"
	"math"

	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// Forever is the upper bound of a validity interval that has not ended. A datom
// asserted with no known end is valid from its start until something retracts
// or replaces it.
const Forever = int64(math.MaxInt64)

// Interval is a half-open business-time window, [From, To).
//
// Half-open is deliberate: adjacent intervals neither overlap nor leave a gap,
// so exactly one is visible at any instant. A closed interval produces the
// off-by-one where a value appears twice at the boundary between two versions.
type Interval struct {
	From int64
	To   int64
}

// Contains reports whether an instant falls inside the interval.
func (i Interval) Contains(instant int64) bool {
	return i.From <= instant && instant < i.To
}

// String renders an interval for a diagnostic.
func (i Interval) String() string {
	if i.To == Forever {
		return fmt.Sprintf("[%d, ∞)", i.From)
	}
	return fmt.Sprintf("[%d, %d)", i.From, i.To)
}

// Visible reports whether a datom is visible to a resolved query.
//
// It is the ONLY place in this module where a validity bound and a transaction
// identifier are both compared. That concentration is the point: the defect
// this package prevents is a caller passing one instant into two parameters,
// and one comparison site makes that reviewable in one file rather than at
// every call site.
//
// The two conditions are independent and neither is derived from the other:
//
//   - the datom's business interval must contain the query's ValidAt, and
//   - the datom's transaction identifier must not exceed the query's AsOf,
//     which an open AsOf never does.
//
// Pass a query that has been through [ResolveQualifiers]; an unresolved ValidAt
// is treated as unbound on the business axis, which is a caller error rather
// than a meaningful question.
func Visible(validFrom, validTo int64, id tx.TxID, q Query) bool {
	if q.ValidAt != nil && !(Interval{From: validFrom, To: validTo}).Contains(*q.ValidAt) {
		return false
	}
	if q.AsOf != nil && id.Compare(*q.AsOf) > 0 {
		return false
	}
	return true
}
