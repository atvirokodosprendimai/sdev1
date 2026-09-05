package eval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

var (
	// ErrNotComparable reports a comparison whose operands cannot be compared —
	// a numeric literal against a value that is not a number.
	//
	// ⚠ It is a refusal rather than a false. "This value is not a number" and
	// "this value is not greater than five" are different answers, and returning
	// the second for the first hides a type error inside an ordinary-looking
	// empty result.
	ErrNotComparable = errors.New("eval: those operands cannot be compared")

	// ErrUnboundInstant reports a query that reached evaluation without a business
	// instant.
	//
	// ⚠ A store's snapshot takes a bound and cannot express "unbound", so the
	// alternative is rendering it as zero — the epoch, which is a different
	// question rather than an absent one.
	ErrUnboundInstant = errors.New("eval: the query has no business instant")

	// ErrUnknownOperator reports a comparison operator the evaluator does not
	// implement.
	//
	// ⚠ It exists so that adding one to the grammar and forgetting it here is a
	// refusal rather than a predicate that quietly matches everything — which is
	// the defect this package was written to remove, one operator at a time.
	ErrUnknownOperator = errors.New("eval: unknown comparison operator")
)

// Row is one attribute of one entity, as evaluated.
type Row struct {
	Entity    string
	Attribute string
	Value     []byte
	TxID      tx.TxID
}

// Read evaluates a READ against a reader, at one instant.
//
// ⚠ now is an INSTANT, not a clock. A clock could be read twice, and two readings
// inside one statement is a query that spans two moments — which is the defect
// ADR-023 fixed for traversal.
//
// It performs exactly one [ports.Reader.Load], for the entity the statement
// names.
func Read(ctx context.Context, r ports.Reader, sel *ql.Read, now int64) ([]Row, error) {
	// Resolved ONCE, here, by the one implementation of ADR-002 rule 6's table.
	// Everything below uses this value and re-derives nothing.
	resolved := sel.Time.Resolve(now)

	id, instant, ok := resolved.Bounds()
	if !ok {
		return nil, ErrUnboundInstant
	}

	datoms, err := r.Load(ctx, sel.Entity, ports.Snapshot{At: id, ValidAt: instant})
	if err != nil {
		return nil, fmt.Errorf("eval: reading %q: %w", sel.Entity, err)
	}

	carried := latestVisible(datoms, resolved)
	projected := project(carried, sel.Attributes)

	if sel.Where != nil {
		// ⚠ Tested against CARRIED — the entity's whole attribute set — and never
		// against `projected`. The published guide has
		// `READ name FROM planet-7 WHERE class = 'terrestrial'`, so a predicate
		// must be able to name an attribute the projection does not return.
		// Narrowing first leaves nothing to test against and the query silently
		// returns nothing, on data where it should return a row.
		qualifies, err := satisfies(carried, sel.Where)
		if err != nil {
			return nil, err
		}
		if !qualifies {
			return nil, nil
		}
	}

	names := make([]string, 0, len(projected))
	for name := range projected {
		names = append(names, name)
	}
	// Sorted so two runs agree; a map would order this differently each time.
	sort.Strings(names)

	rows := make([]Row, 0, len(names))
	for _, name := range names {
		d := projected[name]
		rows = append(rows, Row{
			Entity:    d.Entity,
			Attribute: d.Attribute,
			Value:     d.Value,
			TxID:      d.TxID,
		})
	}
	return rows, nil
}

// project narrows an entity's carried attributes to the ones a statement asked
// for. An empty list is `READ *` and returns them all.
func project(carried map[string]ports.Datom, attributes []string) map[string]ports.Datom {
	if len(attributes) == 0 {
		return carried
	}
	out := make(map[string]ports.Datom, len(attributes))
	for _, name := range attributes {
		if d, held := carried[name]; held {
			out[name] = d
		}
	}
	return out
}

// latestVisible reduces an entity's datoms to what it CARRIES at the snapshot:
// the latest visible datom per attribute, with retractions removed.
//
// ⚠ A retraction SUPPRESSES its attribute rather than being reported as a value —
// it is a datom, not an absence, and the attribute it withdraws is absent from
// the entity's shape rather than present with a withdrawn value.
//
// ★ This filters even though [ports.Reader.Load] already filtered, and the two
// are not the same filter. The store was handed a SNAPSHOT, which is a lossy
// rendering of the query: [temporal.Query.Bounds] turns an open system axis into
// the largest identifier, because a snapshot cannot say "open". This one uses the
// query the parser RESOLVED, which is the authoritative form. The store's filter
// is an optimisation — it avoids shipping datoms nobody wants — and this one is
// the meaning.
func latestVisible(datoms []ports.Datom, resolved temporal.Query) map[string]ports.Datom {
	visible := make([]ports.Datom, 0, len(datoms))
	for _, d := range datoms {
		if temporal.Visible(d.Valid.From, d.Valid.To, d.TxID, resolved) {
			visible = append(visible, d)
		}
	}
	// ★ The reduction itself is [ports.Carried], shared with the store and with
	// search's confirmation. Three copies of "latest per attribute, retractions
	// suppressed" is three chances to get the second half wrong.
	return ports.Carried(visible)
}

// satisfies reports whether an entity's carried attributes satisfy the predicate.
//
// ⚠ An attribute the predicate names and the entity does not carry does not
// qualify, with no error: that is an answer about the DATA. A comparison that
// cannot be MADE is an error: that is an answer about the QUERY.
func satisfies(carried map[string]ports.Datom, p *ql.Predicate) (bool, error) {
	d, held := carried[p.Attribute]
	if !held {
		return false, nil
	}
	return compare(string(d.Value), p)
}

// compare applies one operator.
//
// ⚠ The comparison is NUMERIC only when the literal was written as a number. It
// is a property of the query text, readable where it was written — deciding it
// from the stored value would make the same statement change meaning when the
// data changes, since "10" < "9" is true as text and false as numbers.
func compare(stored string, p *ql.Predicate) (bool, error) {
	if p.ValueIsNumber {
		left, err := strconv.ParseFloat(stored, 64)
		if err != nil {
			return false, fmt.Errorf("%w: %s holds %q, and the query compares it with the number %s",
				ErrNotComparable, p.Attribute, stored, p.Value)
		}
		right, err := strconv.ParseFloat(p.Value, 64)
		if err != nil {
			return false, fmt.Errorf("%w: %q was lexed as a number and does not parse as one",
				ErrNotComparable, p.Value)
		}
		return ordered(sign(left, right), p)
	}
	return ordered(strings.Compare(stored, p.Value), p)
}

// sign returns -1, 0 or 1 for two numbers, so both comparisons reduce to one
// three-way result and no operator is implemented twice.
func sign(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// ordered turns a three-way comparison into the operator's answer.
//
// ⚠ An unrecognised operator is a refusal. The grammar and this switch can drift,
// and a default that returned false would make a new operator match nothing while
// looking like it worked.
func ordered(c int, p *ql.Predicate) (bool, error) {
	switch p.Op {
	case "=", "==":
		return c == 0, nil
	case "!=":
		return c != 0, nil
	case "<":
		return c < 0, nil
	case "<=":
		return c <= 0, nil
	case ">":
		return c > 0, nil
	case ">=":
		return c >= 0, nil
	default:
		return false, fmt.Errorf("%w: %q", ErrUnknownOperator, p.Op)
	}
}
