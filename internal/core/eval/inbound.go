package eval

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ql"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
)

// ErrNoInboundIndex reports that the reader cannot say what points at an entity.
//
// ⚠ It is a REFUSAL and never an empty result. "Nothing points at this" and "I
// cannot tell you what points at this" are different answers, and returning the
// first for the second is ADR-027's discarded-`WHERE` defect wearing a different
// hat: a narrow question getting a confident wrong answer with no error.
var ErrNoInboundIndex = errors.New("eval: this reader cannot say what points at an entity")

// readInbound answers `READ ->a FROM [e]`: the entities that point at e.
//
// The order of operations is the decision, and each step is one of ADR-035's
// rules:
//
//  1. Candidates are PROPOSED by [ports.Inbound] — never trusted (rule 6).
//  2. Each candidate is loaded and CONFIRMED to still carry an asserted
//     reference to the target at this snapshot (rule 6).
//  3. A member missing any attribute the statement names, or failing the
//     comparison, is DROPPED entirely (rule 4).
//  4. Survivors are ordered by entity name, then paged (rule 5).
//
// ⚠ It costs one scan plus one load per candidate. That N+1 is the price of
// step 2 and is recorded on ADR-035 rather than optimised away here.
func readInbound(ctx context.Context, r ports.Reader, sel *ql.Read,
	at ports.Snapshot, resolved temporal.Query) ([]Row, error) {

	scan, ok := r.(ports.Inbound)
	if !ok {
		return nil, fmt.Errorf("%w: reading FROM [%s] needs one, and %T is not one",
			ErrNoInboundIndex, sel.Entity, r)
	}

	candidates, err := scan.Referrers(ctx, sel.Entity, at)
	if err != nil {
		return nil, fmt.Errorf("eval: finding what points at %q: %w", sel.Entity, err)
	}

	// ⚠ Sorted and de-duplicated HERE rather than trusted from the source. A
	// candidate list may legitimately repeat an entity or arrive in whatever
	// order a scan produced, and paging an unordered list is not paging — it
	// repeats and skips while still looking like a page.
	sort.Strings(candidates)

	var members []member
	var previous string
	for i, name := range candidates {
		if i > 0 && name == previous {
			continue
		}
		previous = name

		kept, held, err := memberOf(ctx, r, sel, name, at, resolved)
		if err != nil {
			return nil, err
		}
		if held {
			members = append(members, kept)
		}
	}

	// ★ Paged AFTER the drop, and over MEMBERS. Paging before it gives
	// unpredictable page sizes; paging over rows cuts a member in half.
	return rowsOf(paged(members, sel.Page)), nil
}

// member is one entity that survived, with the attributes it contributes.
type member struct {
	entity    string
	projected map[string]ports.Datom
}

// memberOf confirms one candidate and applies the drop rule, reporting whether
// the member contributes anything at all.
func memberOf(ctx context.Context, r ports.Reader, sel *ql.Read, name string,
	at ports.Snapshot, resolved temporal.Query) (member, bool, error) {

	datoms, err := r.Load(ctx, name, at)
	if err != nil {
		return member{}, false, fmt.Errorf("eval: reading %q: %w", name, err)
	}
	carried := latestVisible(datoms, resolved)

	// ⚠ CONFIRMATION, and it is not optional. [ports.Carried] has already
	// dropped retracted attributes, so a member whose reference was withdrawn
	// no longer points anywhere — which is exactly the case an index that only
	// appends keeps proposing.
	if !pointsAt(carried, sel.Entity) {
		return member{}, false, nil
	}

	// Rule 4, the predicate half. `satisfies` answers false with no error when
	// the attribute is absent, which is the drop rather than a failure.
	if sel.Where != nil {
		qualifies, err := satisfies(carried, sel.Where)
		if err != nil {
			return member{}, false, err
		}
		if !qualifies {
			return member{}, false, nil
		}
	}

	// ADR-036: the absence clause. ⚠ Applied BEFORE the drop below and never
	// through it — a WITHOUT attribute is named in order to be absent, so
	// requiring its presence would make the clause unsatisfiable.
	if carries(carried, sel.Without) {
		return member{}, false, nil
	}

	// Rule 4, the projection half. ⚠ Checked NAME BY NAME rather than by
	// comparing counts: a projection that names one attribute twice would make
	// a count comparison drop a member that carries it.
	//
	// ⚠ It iterates sel.Attributes ONLY. Extending it over sel.Without is the
	// defect ADR-036 rule 4 names: it would drop every subject for lacking
	// exactly what the caller asked them to lack, and would do it by returning
	// nothing — which is indistinguishable from a correct empty answer.
	for _, want := range sel.Attributes {
		if _, held := carried[want]; !held {
			return member{}, false, nil
		}
	}

	projected := project(carried, sel.Attributes)
	if len(projected) == 0 {
		// `READ *` over an entity carrying nothing. It cannot happen for a
		// confirmed member, which carries at least its reference, but a member
		// with nothing to say contributes no rows rather than an empty one.
		return member{}, false, nil
	}
	return member{entity: name, projected: projected}, true, nil
}

// carries reports whether any of the named attributes is present.
//
// ★ Absence is the NEGATION OF THIS, and this is `ports.Carried`'s answer rather
// than a second one. That already gets three histories right: an attribute never
// asserted, one asserted and later RETRACTED, and one whose validity interval
// does not cover the instant. A second definition would drift from the first on
// exactly the retracted case, which is the one this clause exists to find.
//
// ⚠ It is therefore SNAPSHOT-RELATIVE. "Does not carry `thirdname`" means at this
// instant, never "never had one".
func carries(carried map[string]ports.Datom, names []string) bool {
	for _, name := range names {
		if _, held := carried[name]; held {
			return true
		}
	}
	return false
}

// pointsAt reports whether an entity's carried attributes include a reference to
// target.
//
// ⚠ It takes CARRIED attributes, so a retracted reference is already absent. A
// version scanning raw datoms would confirm a member on the strength of an edge
// that has since been withdrawn.
func pointsAt(carried map[string]ports.Datom, target string) bool {
	for _, d := range carried {
		if d.IsReference && string(d.Value) == target {
			return true
		}
	}
	return false
}

// paged applies the LIMIT and OFFSET clause over MEMBERS.
//
// ⚠ An absent clause returns everything; `LIMIT 0` returns nothing. They are
// opposite requests, which is why [ql.Page] carries an explicit flag rather than
// relying on a zero value.
func paged(members []member, p ql.Page) []member {
	if !p.Has {
		return members
	}
	if p.Offset >= int64(len(members)) {
		return nil
	}
	members = members[p.Offset:]
	if p.Limit < int64(len(members)) {
		members = members[:p.Limit]
	}
	return members
}

// rowsOf flattens members to rows: members in the order given, and each
// member's attributes sorted so two runs agree.
func rowsOf(members []member) []Row {
	var rows []Row
	for _, m := range members {
		names := make([]string, 0, len(m.projected))
		for name := range m.projected {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			d := m.projected[name]
			rows = append(rows, Row{
				Entity:      d.Entity,
				Attribute:   d.Attribute,
				Value:       d.Value,
				Valid:       d.Valid,
				TxID:        d.TxID,
				IsReference: d.IsReference,
			})
		}
	}
	return rows
}
