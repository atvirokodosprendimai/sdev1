package serve

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/authz"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

var (
	// ErrNoGrants reports a node that was not told where to read grants.
	//
	// ⚠ It refuses at CONSTRUCTION, and a node in this state serves nothing.
	// ADR-033 rule 5 says no grant means refused, and the dangerous reading is
	// that an unconfigured or unreachable grant store is a special case. It is
	// not — it is exactly the case where a system fails open, because the thing
	// that would say no is the thing that is missing.
	ErrNoGrants = errors.New("serve: a node must be told where to read grants")

	// ErrSystemTenant reports a request for the tenant that holds the grants.
	//
	// ⚠ Refused BY NAME and before any store is touched. [authz.Set.Allow]
	// refusing that tenant does not cover this: reading the grant leaf is an
	// ordinary read, so a node that happened to hold it would serve the whole
	// grant table — every principal's authority — through the ordinary path.
	ErrSystemTenant = errors.New("serve: the reserved system tenant is not readable over the wire")

	// ErrNotPermitted reports a caller with no grant for what it asked.
	ErrNotPermitted = errors.New("serve: no grant permits that")
)

// permits decides whether principal may read the tenant this key belongs to.
//
// ★★ THE GRANT SET IS LOADED AT THE NODE'S OWN CLOCK, NEVER AT THE CALLER'S.
// This function takes no instant from the request, and that is not tidiness — it
// is the difference between a working revocation and a decorative one.
//
// ⚠ `wire.Request.Now` is chosen by the CLIENT. It is the right input for the
// evaluator, which resolves a time clause against a stated moment so that one
// statement cannot span two. It is the wrong input here, and the failure is total:
// a principal whose grant was retracted at T need only send `Now` a second before
// T, and the retraction datom is not yet valid, so the grant is still carried and
// the read is authorized. The revocation would report success and stop nothing,
// for anyone willing to lie about the time.
//
// ★ So the caller may choose which moment of the DATA to ask about, and may not
// choose which moment of the GRANTS to be judged by. [authz.Set.Allow] then takes
// no instant at all, which is ADR-033 rule 3 closing the same door one level up.
func (s *Server) permits(ctx context.Context, principal string, k addr.Key) error {
	tenant := addr.TenantOf(k)
	if tenant == authz.SystemTenant {
		return fmt.Errorf("%w: %s", ErrSystemTenant, tenant)
	}

	// The present, on both axes.
	//
	// ★ The transaction bound is the MAXIMUM clock reading, which is the same
	// spelling `temporal.Query.Bounds` uses for an unbound transaction axis:
	// nothing can exceed a maximum, so this means "every transaction that has
	// committed". A grant written a millisecond ago is therefore in force.
	at := ports.Snapshot{
		At:      tx.TxID{HLC: hlc.Timestamp{Wall: math.MaxInt64, Logical: math.MaxUint32}},
		ValidAt: s.now(),
	}
	set, err := authz.Load(ctx, s.opts.Grants, principal, at)
	if err != nil {
		// ⚠ An unreadable grant store is a REFUSAL, never a pass. This is rule 5
		// at run time rather than at construction.
		return fmt.Errorf("%w: reading grants for %q: %v", ErrNotPermitted, principal, err)
	}
	if err := set.Allow(tenant, authz.Read); err != nil {
		return fmt.Errorf("%w: %v", ErrNotPermitted, err)
	}
	return nil
}

// now is the node's own business instant, in Unix seconds.
//
// ⚠ The NODE's, never the caller's. See [Server.permits]. It is injectable only
// so a test can grant and revoke at chosen moments; nothing on the wire reaches
// it.
func (s *Server) now() int64 {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now().Unix()
}
