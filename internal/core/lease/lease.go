package lease

import (
	"errors"
	"fmt"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
)

// Epoch is a fencing token: a per-leaf counter that only ever goes up.
//
// It orders CLAIMS, not identities. A resource comparing two epochs learns which
// claim is more recent, which is the only thing it can decide without knowing
// anything about who is alive.
type Epoch uint64

// NoEpoch is the zero value, which no grant ever returns. A resource that has
// seen only NoEpoch has seen nothing.
const NoEpoch Epoch = 0

var (
	// ErrStaleEpoch reports a claim that is not the newest.
	ErrStaleEpoch = errors.New("lease: the epoch is older than one already seen")

	// ErrNoLease reports a leaf nobody has been granted.
	//
	// ★ It is returned instead of a zero lease, which would be epoch zero held
	// by nobody — a valid-looking value that compares as older than everything
	// and silently means "unowned".
	ErrNoLease = errors.New("lease: no lease has been granted for that leaf")
)

// Lease is one grant: a leaf, who holds it, and how recent the claim is.
type Lease struct {
	Leaf   addr.LeafID
	Holder string
	Epoch  Epoch
}

// Registry grants leases.
//
// ⚠ It is in-process and it is named for what it is. WHO should hold a leaf in a
// real cluster is a consensus question that needs a transport; this is the half
// that makes a handover safe once somebody has decided to make one.
//
// There is no Release and no expiry. See the package comment for why: neither
// can distinguish a dead holder from a slow one, and both therefore permit two
// live writers.
type Registry struct {
	mu      sync.Mutex
	granted map[addr.LeafID]Lease
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{granted: map[addr.LeafID]Lease{}}
}

// Grant issues a lease on a leaf to a holder.
//
// ★ It does not consult, notify or wait for the previous holder. Waiting is what
// would make a dead writer a permanent outage; the epoch is what makes not
// waiting safe. The previous holder finds out at its next write, and until then
// it can do no harm.
//
// The epoch is STRICTLY greater than every epoch granted before it for this leaf.
// Strictly, because an equal epoch orders nothing and two holders would be
// indistinguishable to the resource that has to choose between them.
func (r *Registry) Grant(leaf addr.LeafID, holder string) (Lease, error) {
	if holder == "" {
		return Lease{}, fmt.Errorf("lease: a lease needs a holder")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	next := Epoch(1)
	if held, ok := r.granted[leaf]; ok {
		next = held.Epoch + 1
	}
	l := Lease{Leaf: leaf, Holder: holder, Epoch: next}
	r.granted[leaf] = l
	return l, nil
}

// Current reports the most recent grant for a leaf.
func (r *Registry) Current(leaf addr.LeafID) (Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.granted[leaf]
	if !ok {
		return Lease{}, fmt.Errorf("%w: %s", ErrNoLease, leaf)
	}
	return l, nil
}
