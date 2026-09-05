package watch

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/atvirokodosprendimai/sdev1/internal/core/observe"
	"github.com/atvirokodosprendimai/sdev1/internal/core/subscribe"
)

var (
	// ErrNoSubject reports an obligation that names nothing.
	//
	// ⚠ An obligation with no subject cannot be acted on and cannot be
	// acknowledged, because there is nothing to say you dealt with.
	ErrNoSubject = errors.New("watch: an obligation must name what it is about")

	// ErrNotOutstanding reports an acknowledgement of something not outstanding.
	//
	// ★ It is an error rather than a silent success. A no-op would let a mistyped
	// subject read as "dealt with", which is the one outcome worse than the
	// obligation simply remaining.
	ErrNotOutstanding = errors.New("watch: no such obligation is outstanding")
)

// Obligation is something that happened, matters, and nobody has dealt with.
//
// ★ It is a STATE, not an event. The event announces it; this survives the
// event, the stream that carried it, and any retention applied to either.
type Obligation struct {
	// Kind is what happened, named by ADR-012's declared vocabulary so an
	// obligation and the event announcing it cannot drift apart.
	Kind observe.Kind
	// Subject is what it is about.
	Subject string
	// Detail is what an operator needs in order to act — for an incomplete
	// purge, the sinks that have not acknowledged.
	//
	// ⚠ It is UPDATED by a re-raise while the raised time is not: the current
	// list of outstanding sinks is news, and the age is not.
	Detail map[string]string
}

// key identifies an obligation. The same condition about the same subject is ONE
// obligation, not a stream of them.
type key struct {
	kind    observe.Kind
	subject string
}

func (o Obligation) key() key { return key{kind: o.Kind, subject: o.Subject} }

// Outstanding is an obligation and how long it has gone unanswered.
type Outstanding struct {
	Obligation
	// Raised is when it was FIRST raised, in nanoseconds.
	Raised int64
	// Age is how long it has been outstanding, in nanoseconds.
	Age int64
}

// record is a raised obligation and when it was first seen.
type record struct {
	obligation Obligation
	raised     int64
}

// Ledger is the set of outstanding obligations.
//
// ⚠ It is in memory, so a restart loses it. See the package comment: that is a
// named gap, not a design.
type Ledger struct {
	mu      sync.Mutex
	records map[key]*record
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger { return &Ledger{records: make(map[key]*record)} }

// Raise records an obligation as outstanding, at the instant given.
//
// ⚠ Raising one that is already outstanding KEEPS ITS FIRST RAISED TIME and
// updates only its detail. ★ A purge that retries daily and fails daily must not
// look one day old forever — age is the whole signal, so anything that resets the
// clock disables the mechanism while leaving it apparently working.
func (l *Ledger) Raise(o Obligation, at int64) error {
	if o.Subject == "" {
		return fmt.Errorf("%w: kind %q", ErrNoSubject, o.Kind)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if prior, held := l.records[o.key()]; held {
		// The detail is news; the age is not.
		prior.obligation.Detail = o.Detail
		return nil
	}
	l.records[o.key()] = &record{obligation: o, raised: at}
	return nil
}

// Acknowledge clears an obligation, naming who dealt with it and when.
//
// ⚠ It is the ONLY thing that clears one. Not time, not retention, and not the
// condition ceasing to recur — a purge nobody retried is indistinguishable from
// one that completed, and only a person can tell them apart.
func (l *Ledger) Acknowledge(kind observe.Kind, subject, by string, at int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	k := key{kind: kind, subject: subject}
	if _, held := l.records[k]; !held {
		return fmt.Errorf("%w: %s about %q", ErrNotOutstanding, kind, subject)
	}
	if by == "" {
		return fmt.Errorf("watch: acknowledging %s about %q must name who did it", kind, subject)
	}
	delete(l.records, k)
	return nil
}

// Outstanding returns everything unanswered, OLDEST FIRST, with each one's age
// at the instant given.
//
// ⚠ IT TAKES NO HORIZON, NO AGE FILTER AND NO LIMIT, and that is the enforcement
// rather than an omission. Applying ADR-010's retention horizon here would make
// an old problem stop being reported BECAUSE it is old — the system would answer
// "nothing is outstanding" precisely at the moment the answer became most wrong.
//
// ★ Oldest first because the question is whether an old unanswered thing reaches
// a person, and newest-first buries it further every day.
func (l *Ledger) Outstanding(now int64) []Outstanding {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]Outstanding, 0, len(l.records))
	for _, r := range l.records {
		out = append(out, Outstanding{
			Obligation: r.obligation,
			Raised:     r.raised,
			Age:        now - r.raised,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Raised != out[j].Raised {
			return out[i].Raised < out[j].Raised
		}
		// A stable tiebreak, so two obligations raised at the same instant do not
		// swap places between reads and make the report look like it changed.
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

// Len is how many obligations are outstanding.
func (l *Ledger) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.records)
}

// FromPurge turns an incomplete purge into an obligation, and reports whether
// there was one to make.
//
// ★ ADR-010 already computes who has not acknowledged; it was being thrown away.
// This keeps it, so the obligation names who to chase rather than only that
// something is wrong.
//
// A purge that is not [subscribe.PurgeIncomplete] produces nothing: a completed
// purge owes nobody anything, and a refused one did not happen.
func FromPurge(r subscribe.PurgeResult) (Obligation, bool) {
	if r.State != subscribe.PurgeIncomplete {
		return Obligation{}, false
	}
	detail := map[string]string{
		"verb":        r.Verb,
		"outstanding": joined(r.Outstanding),
	}
	if len(r.Acknowledged) > 0 {
		detail["acknowledged"] = joined(r.Acknowledged)
	}
	return Obligation{
		Kind:    observe.KindPurgeIncomplete,
		Subject: r.Subject,
		Detail:  detail,
	}, true
}

// joined renders a list of sink names for an operator to read.
func joined(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
