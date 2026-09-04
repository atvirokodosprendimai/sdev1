// Package observe decides what a component may say, and proves that somebody
// reads it.
//
// # A closed vocabulary, because an open one becomes a log
//
// If any component may emit any shape, a consumer has to pattern-match strings,
// every producer invents its own field names, and the console becomes a grep.
// The cost is not felt while there are three producers; it is unpayable at
// thirty.
//
// So an event has one shape — a declared kind, the leaf it concerns, the
// transaction that orders it, and named fields — and there is no free-form
// message. An undeclared kind is refused at EMISSION rather than at read time,
// so drift fails where it happens instead of becoming somebody else's problem
// months later.
//
// ⚠ A free-form message field is what every caller wants during an incident, and
// adding one turns this back into a log. That is why there is not one.
//
// # A counter nobody reads is worse than no counter
//
// It costs emission on a hot path, it makes a dashboard look thorough, and it
// answers no question. And it survives forever, because deleting a metric looks
// risky and nobody can prove it is unused.
//
// So the rule is at DECLARATION time rather than in a periodic cleanup that
// never happens: every counter states the operator question it settles, and a
// counter whose question cannot be written is one nobody needs. Writing the
// question is where that becomes obvious.
//
// ★ The question is not a description of what is counted — the name already says
// that. "How many blocks were read" is a name. "Is a degraded read costing us
// more than a healthy one" is a question, and only the second says why the number
// exists.
//
// The same rule applies to events: every declared kind names its READER. Without
// that, a closed vocabulary is just a long tidy list of things nobody looks at,
// which is the failure that actually accumulates.
//
// # Emission never blocks, and never loses events silently
//
// Observability that can stall the thing it observes is worse than none, and a
// blocking emit is the failure that actually happens rather than a hypothetical
// one. So a full buffer drops rather than waiting, and emission never errors
// into the caller.
//
// ⚠ And every drop is COUNTED. A stream that loses events without saying so is
// lying exactly under the load an operator is investigating, which is the moment
// it most needs to be trusted. The drop counter is itself declared, with its own
// reader and its own question — a count that reveals lost events must not be
// another unread number.
//
// # What this package does not do
//
// It observes and does not act. Shedding load, refusing a write, repairing a
// stripe — each belongs to the record that owns that decision, and a component
// that observed AND acted would make every emission a control decision.
//
// It also renders nothing and ships nothing anywhere. The console is a sink over
// the subscription primitive, and it needs a transport that does not exist yet.
package observe
