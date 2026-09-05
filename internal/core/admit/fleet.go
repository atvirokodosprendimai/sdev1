package admit

import (
	"sort"
	"strconv"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/observe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/watch"
)

// Fleet is what a group of replicas has reported about itself.
//
// ⚠ It holds STATES and never [Controller]s, and that is the mechanism rather
// than a tidiness preference. `BACKLOG.md` §22 names the trap: *a node that
// refuses to withdraw because its peers already have is a node that keeps taking
// work it cannot serve* — the error-returning behaviour ADR-015 rejected, reached
// by a different route. Holding values a node already reported means there is
// nothing here to reach back through.
//
// ★ "Should I withdraw" and "what do we do when everyone has" are two questions.
// Conflating them is the only way to write that trap, so they are two types.
type Fleet struct {
	mu     sync.Mutex
	group  string
	states map[string]State
}

// NewFleet returns an empty view of one replica group.
func NewFleet(group string) *Fleet {
	return &Fleet{group: group, states: make(map[string]State)}
}

// Observe records what one replica reported.
func (f *Fleet) Observe(node string, state State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[node] = state
}

// AllWithdrawn reports whether every known replica has withdrawn, so read work
// has nowhere left to go.
//
// ⚠ An EMPTY fleet is not all-withdrawn. Zero replicas is a group nobody has told
// us about, not a saturated one, and a vacuous truth here would raise an
// obligation about a cluster that may be perfectly healthy.
func (f *Fleet) AllWithdrawn() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.states) == 0 {
		return false
	}
	for _, s := range f.states {
		if s != StateWithdrawn {
			return false
		}
	}
	return true
}

// Report raises an obligation when every replica has withdrawn, and reports
// whether it raised one.
//
// ★ It REPORTS and does not resolve. `BACKLOG.md` §22 says the candidate
// responses — a withdrawal floor, group-aware admission, or letting the queue
// back up — differ sharply and need a cluster to choose between; and the one that
// would reach a node's own decision is the trap above. So this makes the
// condition visible and answers nothing.
//
// ⚠ It offers no floor, no override and no way to keep a node joined. There is
// deliberately no method here that a node's [Controller.Decide] could consult.
//
// ★ The condition is an ADR-038 obligation rather than a new notion of "somebody
// should look": it is a state, it matters, and nobody has dealt with it. That
// also means a fleet which RECOVERS does not clear it — only an acknowledgement
// does, because a cluster that shed everything and came back is exactly what an
// operator should still see.
func (f *Fleet) Report(l *watch.Ledger, at int64) (bool, error) {
	if !f.AllWithdrawn() {
		return false, nil
	}

	f.mu.Lock()
	nodes := make([]string, 0, len(f.states))
	for name := range f.states {
		nodes = append(nodes, name)
	}
	group := f.group
	f.mu.Unlock()
	sort.Strings(nodes)

	o := watch.Obligation{
		Kind:    observe.KindFleetWithdrawn,
		Subject: group,
		Detail: map[string]string{
			"group":     group,
			"replicas":  strconv.Itoa(len(nodes)),
			"withdrawn": joinNames(nodes),
		},
	}
	if err := l.Raise(o, at); err != nil {
		return false, err
	}
	return true, nil
}

// joinNames renders the replica names for an operator to read.
func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
