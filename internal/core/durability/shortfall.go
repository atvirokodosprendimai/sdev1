package durability

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/observe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/watch"
)

// ErrNoGrace reports a watchdog with no declared grace.
//
// ⚠ The grace is not a tuning knob on this mechanism — it IS the mechanism.
// Instantaneously a rolling restart and a genuine shortfall are the same
// observation, so how long the condition lasts is the only thing that separates
// them. A watchdog without one is not a watchdog with a default; it is one that
// cannot answer the question it exists for.
//
// ★ There is deliberately no default. What a restart costs is a property of a
// deployment, and a constant here would page the wrong deployments, stay silent
// for the others, and be a number nobody wrote down.
var ErrNoGrace = errors.New("durability: a watchdog needs a declared grace")

// Shortfall is a leaf below its policy floor, and since when.
type Shortfall struct {
	// Leaf is which leaf is short.
	Leaf addr.LeafID
	// Policy is what it was measured against.
	Policy Policy
	// Domains is how many DISTINCT failure domains currently hold copies.
	Domains int
	// Since is when it first went below the floor, in nanoseconds.
	Since int64
	// Age is how long it has been short, at the instant it was asked about.
	Age int64
}

// Reportable says whether this shortfall has lasted longer than the grace, and
// is therefore something somebody must answer for rather than watch.
func (s Shortfall) Reportable(grace int64) bool { return s.Age > grace }

// Watchdog tracks which leaves are below their floor and for how long.
//
// ★ It answers docs/adr/BACKLOG.md §10's hardest question — telling "briefly
// degraded during a restart" from "genuinely short of copies" — by admitting that
// no instantaneous measurement can. A leaf holding two of three domains is
// holding two of three, whatever the reason. The difference is entirely in what
// happens next, so only TIME reveals it.
//
// ⚠ It takes no leaf out of service, and offers no method that could. A
// below-floor leaf is degraded, not wrong: its data is readable and correct, so
// evicting it would trade a durability risk for a certain outage — and it would
// remove exactly the copies that still exist.
type Watchdog struct {
	mu    sync.Mutex
	grace int64
	since map[addr.LeafID]*Shortfall
}

// NewWatchdog returns a watchdog whose grace is the given number of nanoseconds.
func NewWatchdog(graceNanos int64) (*Watchdog, error) {
	if graceNanos <= 0 {
		return nil, fmt.Errorf("%w: got %d nanoseconds", ErrNoGrace, graceNanos)
	}
	return &Watchdog{grace: graceNanos, since: make(map[addr.LeafID]*Shortfall)}, nil
}

// Grace is the declared threshold, in nanoseconds.
func (w *Watchdog) Grace() int64 { return w.grace }

// Observe records a leaf's current domain membership against its policy.
//
// ⚠ Re-observing a leaf that is STILL short does not restart its clock. A
// watchdog polled every second would otherwise report every leaf as one second
// old forever — the mechanism disabled while it continues to produce output,
// which is ADR-038 rule 6 arriving in a different package.
//
// A leaf that has returned above its floor is forgotten HERE, so the status
// reflects the present. ⚠ Any obligation already raised for it is untouched:
// only an acknowledgement clears one, and a leaf that fell below its floor and
// came back is exactly what an operator should still see.
func (w *Watchdog) Observe(leaf addr.LeafID, p Policy, domains []string, at int64) {
	// ★ The floor test is Policy.Satisfied, not a second count of domains here.
	// Two implementations of "is this leaf short" would eventually disagree, and
	// the one an operator sees would not be the one that refused the write.
	short := p.Satisfied(domains) != nil

	w.mu.Lock()
	defer w.mu.Unlock()

	if !short {
		delete(w.since, leaf)
		return
	}
	if prior, held := w.since[leaf]; held {
		// Still short. The domain count is news; the age is not.
		prior.Domains = distinctDomains(domains)
		prior.Policy = p
		return
	}
	w.since[leaf] = &Shortfall{
		Leaf:    leaf,
		Policy:  p,
		Domains: distinctDomains(domains),
		Since:   at,
	}
}

// Status returns every leaf currently below its floor, oldest first, with its
// age — INCLUDING leaves still inside the grace.
//
// ★ This is the half that stops the grace becoming a hiding place. An operator
// watching a rolling restart wants to SEE the dip and its recovery; they simply
// do not want to be answerable for it. Suppressing the status as well as the
// obligation would conflate those, and would make a genuine shortfall invisible
// for the length of the grace.
func (w *Watchdog) Status(now int64) []Shortfall {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := make([]Shortfall, 0, len(w.since))
	for _, s := range w.since {
		entry := *s
		entry.Age = now - s.Since
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since != out[j].Since {
			return out[i].Since < out[j].Since
		}
		return out[i].Leaf.String() < out[j].Leaf.String()
	})
	return out
}

// Report raises an ADR-038 obligation for every leaf short for LONGER than the
// grace, and returns how many it raised.
//
// ⚠ Only past the grace. Inside it the leaf is visible through [Watchdog.Status]
// and nobody is answerable for it, which is the difference between watching a
// restart and being paged for one.
func (w *Watchdog) Report(l *watch.Ledger, now int64) (int, error) {
	grace := w.Grace()

	raised := 0
	for _, s := range w.Status(now) {
		if !s.Reportable(grace) {
			continue
		}
		o := watch.Obligation{
			Kind:    observe.KindLeafBelowFloor,
			Subject: s.Leaf.String(),
			Detail: map[string]string{
				"leaf":     s.Leaf.String(),
				"domains":  strconv.Itoa(s.Domains),
				"required": strconv.Itoa(s.Policy.MinSize),
				"grace":    strconv.FormatInt(grace, 10),
			},
		}
		// Raised at the instant it went short, not now, so its age in the ledger
		// is its real age rather than the age of this report.
		if err := l.Raise(o, s.Since); err != nil {
			return raised, err
		}
		raised++
	}
	return raised, nil
}

// distinctDomains counts distinct non-empty domains, the same way
// [Policy.Satisfied] does.
func distinctDomains(domains []string) int {
	seen := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		if d == "" {
			continue
		}
		seen[d] = struct{}{}
	}
	return len(seen)
}
