// Package authz decides whether a principal may do something in a tenant.
//
// # The trap this package exists to make unwritable
//
// This is a bitemporal store, so a caller can ask what was true last March. The
// obvious extension is to authorize that question against the grants that were in
// force last March — the data is historical, so why not the permissions.
//
// ⚠ Revoking access today would then leave the revoked party able to read last
// year, FOREVER. Their grant was live then, so a query about then is permitted,
// and the revocation accomplishes nothing except for the present — while
// reporting success.
//
// ★ So [Set.Allow] TAKES NO INSTANT. The signature is the enforcement: a caller
// cannot authorize against the past because there is nothing to ask with. That is
// worth more than a rule people remember, because the tempting version looks more
// principled rather than less.
//
// The audit question stays answerable. [History] returns RECORDS — not a decision
// — so "who had access in March" can be asked without becoming a way to
// authorize.
//
// # What a grant is
//
// A datom, in the reserved tenant. It costs nothing and buys everything: a grant
// is bitemporal, retractable (so revocation is a retraction), and inside the
// transaction boundary. A separate permission store would have to re-decide all
// of that.
//
// ⚠ No grant means REFUSED. A default of permission fails open exactly when the
// thing that would say no is unreachable.
//
// See docs/adr/ADR-033-grants-and-tenant-allocation.md.
package authz

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/temporal"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// SystemTenant holds the grants, and can never be allocated to anybody.
//
// ⚠ A tenant able to hold the grants could grant itself anything.
var SystemTenant = addr.TenantFromUint(0)

var (
	// ErrNotGranted reports that a principal holds no grant for what it asked.
	//
	// ⚠ It is the answer for an absent grant, an absent grant SET, a grant for a
	// different tenant, and a grant for a different capability. All four are "no".
	ErrNotGranted = errors.New("authz: no grant permits that")

	// ErrReservedTenant reports a grant naming the tenant that holds the grants.
	ErrReservedTenant = errors.New("authz: the system tenant cannot be granted")
)

// Capability is what a grant permits.
type Capability string

const (
	// Read permits reading a tenant's facts.
	Read Capability = "read"
	// Write permits asserting and retracting them.
	Write Capability = "write"
)

// attributePrefix is how a grant is named as an attribute.
//
// The tenant and capability live in the attribute rather than the value so that
// one principal's grants are distinct attributes — which makes each independently
// retractable, and makes "latest per attribute" mean "the current state of THIS
// grant".
const attributePrefix = "grant:"

func attributeFor(tenant addr.TenantID, can Capability) string {
	return attributePrefix + tenant.String() + ":" + string(can)
}

// GrantDatom returns the datom that grants a capability.
//
// ★ It is an ordinary datom: bitemporal, ordered by transaction, inside the
// entity transaction boundary. Nothing about a permission needed a new mechanism.
func GrantDatom(principal string, tenant addr.TenantID, can Capability, id tx.TxID, from int64) (ports.Datom, error) {
	if tenant == SystemTenant {
		return ports.Datom{}, fmt.Errorf("%w: %s", ErrReservedTenant, tenant)
	}
	return ports.Datom{
		Entity:    principal,
		Attribute: attributeFor(tenant, can),
		Value:     []byte(can),
		Valid:     temporal.Interval{From: from, To: temporal.Forever},
		TxID:      id,
		Assert:    true,
	}, nil
}

// RevokeDatom returns the datom that revokes a capability.
//
// ★ A RETRACTION, not a deletion. "This grant stopped applying" and "this grant
// never existed" are different facts, and only the first is what a revocation
// means — which is also what keeps [History] able to answer honestly.
func RevokeDatom(principal string, tenant addr.TenantID, can Capability, id tx.TxID, from int64) (ports.Datom, error) {
	d, err := GrantDatom(principal, tenant, can, id, from)
	if err != nil {
		return ports.Datom{}, err
	}
	d.Assert = false
	return d, nil
}

// Set is the CURRENT grant set for one principal.
//
// ⚠ Current is the only kind there is. There is deliberately no way to build the
// set as it stood at some past instant, because a decision must never be made
// against one.
type Set struct {
	principal string
	held      map[string]bool
}

// Load builds the current grant set for a principal, at the given instant.
//
// ⚠ `now` selects WHICH SET IS CURRENT — it is the present, not a question about
// the past. A caller passing an old instant here is building a stale set, which is
// why [Set.Allow] cannot be given one at all: the instant belongs to loading the
// present, and never to deciding.
func Load(ctx context.Context, r ports.Reader, principal string, at ports.Snapshot) (Set, error) {
	datoms, err := r.Load(ctx, principal, at)
	if err != nil {
		return Set{}, fmt.Errorf("authz: reading grants for %q: %w", principal, err)
	}

	s := Set{principal: principal, held: make(map[string]bool)}
	// ports.Carried is the shared reduction: latest per attribute, retractions
	// suppressed. A revoked grant is therefore ABSENT rather than present and
	// withdrawn.
	for name := range ports.Carried(datoms) {
		if !strings.HasPrefix(name, attributePrefix) {
			continue
		}
		if _, ok := tenantOf(name); !ok {
			continue
		}
		// ⚠ A grant naming the reserved tenant is NOT filtered here. It would be
		// unreachable code: [Set.Allow] refuses that tenant unconditionally,
		// whatever the set holds, so a forged datom can enter the set and still
		// permit nothing. One guard at the decision beats two, and a mutant
		// proved the second one bound to nothing.
		s.held[name] = true
	}
	return s, nil
}

// tenantOf reads the tenant back out of a grant attribute.
func tenantOf(attribute string) (addr.TenantID, bool) {
	parts := strings.Split(attribute, ":")
	if len(parts) != 3 {
		return addr.TenantID{}, false
	}
	raw, err := hex.DecodeString(parts[1])
	if err != nil || len(raw) != addr.TenantBytes {
		return addr.TenantID{}, false
	}
	var tenant addr.TenantID
	copy(tenant[:], raw)
	return tenant, true
}

// Allow reports whether the principal may exercise a capability in a tenant.
//
// ⚠ IT TAKES NO INSTANT, and that is the whole point. Whatever moment a query
// asks about, the decision is made against the CURRENT grant set — so revoking
// access today reaches every question asked from today, including questions about
// last year. A parameter here would make the leak expressible, and it would look
// natural at any call site that already holds a snapshot.
func (s Set) Allow(tenant addr.TenantID, can Capability) error {
	if tenant == SystemTenant {
		return fmt.Errorf("%w: %s", ErrReservedTenant, tenant)
	}
	if !s.held[attributeFor(tenant, can)] {
		return fmt.Errorf("%w: %q has no %s grant for tenant %s",
			ErrNotGranted, s.principal, can, tenant)
	}
	return nil
}

// Record is one grant or revocation, for AUDIT.
type Record struct {
	Principal  string
	Tenant     addr.TenantID
	Capability Capability
	// Granted is false for a revocation.
	Granted bool
	// From is the instant the record took effect on the business axis.
	From int64
	// TxID is when it was recorded.
	TxID tx.TxID
}

// History returns every grant and revocation for a principal, in transaction
// order.
//
// ⚠ It answers "who had access in March" and CANNOT authorize: it returns
// records, not a decision. That separation is deliberate — the audit question is
// legitimate, and answering it through the same function that grants permission is
// how the instant leaks back into the decision.
func History(ctx context.Context, r ports.Reader, principal string, at ports.Snapshot) ([]Record, error) {
	datoms, err := r.Load(ctx, principal, at)
	if err != nil {
		return nil, fmt.Errorf("authz: reading grant history for %q: %w", principal, err)
	}

	out := make([]Record, 0, len(datoms))
	for _, d := range datoms {
		if !strings.HasPrefix(d.Attribute, attributePrefix) {
			continue
		}
		tenant, ok := tenantOf(d.Attribute)
		if !ok {
			continue
		}
		parts := strings.Split(d.Attribute, ":")
		out = append(out, Record{
			Principal:  d.Entity,
			Tenant:     tenant,
			Capability: Capability(parts[2]),
			Granted:    d.Assert,
			From:       d.Valid.From,
			TxID:       d.TxID,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TxID.Compare(out[j].TxID) < 0 })
	return out, nil
}
