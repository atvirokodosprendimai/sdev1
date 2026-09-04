package observe

// The declared vocabulary.
//
// ★ This file is what SELECTS an event kind: a kind not registered here cannot
// be emitted at all. Every entry names its READER, because a declared kind
// nobody consumes is the failure this package exists to prevent — and the
// vocabulary check reads this registry against what is actually emitted, in both
// directions.
//
// Each kind below exists because another record produces something an operator
// needs and had no way to show it.
const (
	// KindRedirect: a client was sent elsewhere because its route was stale.
	// ADR-008 makes staleness a redirect rather than an error, and the rate of
	// these is how an operator sees a repair moving.
	KindRedirect Kind = "routing.redirect"

	// KindWriteRefusedBelowFloor: a write was refused because the surviving
	// failure domains did not meet the policy's floor. ADR-004 refuses rather
	// than accepting at a durability nobody has, and this is how an operator
	// learns which leaves are refusing.
	KindWriteRefusedBelowFloor Kind = "durability.write-refused"

	// KindPurgeIncomplete: a purge removed the primary copy and at least one
	// sink has not acknowledged. ADR-010 deliberately does not escalate and
	// defers that to this record's console; this is the event it would watch.
	KindPurgeIncomplete Kind = "subscribe.purge-incomplete"

	// KindStripeReconstructed: a block was rebuilt from surviving fragments.
	// ADR-006 makes that succeed silently, so without this an operator cannot
	// tell a healthy cluster from one running on parity.
	KindStripeReconstructed Kind = "erasure.stripe-reconstructed"

	// KindWriterFencedOut: an append was refused because its epoch was older
	// than one the tail had seen. ADR-009 makes a superseded writer fail
	// harmlessly, and a burst of these is how a flapping handover looks.
	KindWriterFencedOut Kind = "lease.writer-fenced-out"
)

func init() {
	for _, d := range []Declaration{
		{
			Kind:   KindRedirect,
			Reader: "console: routing panel — routing and placement side by side",
			Fields: []string{"from", "to", "epoch"},
		},
		{
			Kind:   KindWriteRefusedBelowFloor,
			Reader: "console: durability panel, and the below-floor alert",
			Fields: []string{"surviving", "floor", "domain_level"},
		},
		{
			Kind:   KindPurgeIncomplete,
			Reader: "console: purge panel — the outstanding sinks an operator must chase",
			Fields: []string{"subject", "outstanding", "verb"},
		},
		{
			Kind:   KindStripeReconstructed,
			Reader: "console: durability panel — how much is running on parity",
			Fields: []string{"lost", "scheme", "excluded_corrupt"},
		},
		{
			Kind:   KindWriterFencedOut,
			Reader: "console: leases panel — handover flapping",
			Fields: []string{"offered_epoch", "seen_epoch", "holder"},
		},
	} {
		if err := Register(d); err != nil {
			panic("observe: declaring " + string(d.Kind) + ": " + err.Error())
		}
	}
}
