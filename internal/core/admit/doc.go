// Package admit decides when a node stops taking work.
//
// # Shedding is withdrawal, not refusal
//
// A node saturates. Returning an error turns that into visible failure and
// invites a retry, which arrives at the same node and makes it worse. Redirecting
// the client makes every client a participant in load balancing, and needs them
// to agree about who is loaded — a consensus problem hiding inside a redirect.
// Doing nothing means latency climbs until something times out, which is the same
// failure with less information.
//
// So a loaded node stops PULLING. Reads come from a shared queue and are served
// by whichever replica takes them, so a node that withdraws is simply offered
// none. The client is told nothing, retries nothing, and a peer serves it.
// Saturation becomes a routing outcome.
//
// # Read and write budgets share nothing
//
// ⚠ This is the central claim and the one that is easy to get wrong for the sake
// of one fewer number to tune.
//
// A leaf has one writer, so a shed write has nowhere to go — it would be an
// outage rather than a re-route. A read is elastic: any replica can serve it. If
// the two shared a budget, a read burst could exhaust it and stall a leaf's
// ingest, turning a load spike into data not being accepted. That is the failure
// that actually matters, so there are two budgets, no third, and nothing shared
// between them.
//
// # The ceiling is declared, and it is bandwidth
//
// A node that measures its own ceiling discovers it by exceeding it — the
// discovery IS the incident. So an operator states what the link is, and the node
// states what fraction of it it will use. A wrong declared ceiling is an
// operator's mistake, and it is the better of the two failures.
//
// It is bandwidth rather than a request count because a degraded read pulls k
// fragments across k failure domains while a healthy one pulls one. A count would
// treat a cluster running on parity as though it were healthy, which is precisely
// when it is not.
//
// # Two thresholds, because one oscillates
//
// ⚠ Withdrawing and rejoining at the same level flaps by construction: the node
// rejoins at exactly the load that made it leave, immediately takes a burst, and
// leaves again. The flapping costs more than the original load did.
//
// So the rejoin level is meaningfully below the withdraw level, and the band
// between them is where the current state is simply KEPT. That band is the
// mechanism, and a decision that consulted only one threshold would not have it.
//
// # Shed what a peer could serve instead
//
// A saturated node has to give something up, and the two read classes are not
// alike (ADR-039). ★ The ordering principle is the one this package already uses
// one level up, and it is ELASTICITY rather than importance:
//
//   - A USER read is elastic. Any replica holds the data, so shedding it
//     re-routes the read to a peer — which is what this mechanism is for.
//   - A REPAIR read is not. It reads the fragments THIS node holds, so shedding
//     it moves the work nowhere: it CANCELS it, which is precisely what makes a
//     shed write an outage rather than a re-route.
//
// ⚠ So a withdrawn node refuses user reads and keeps serving repair reads. Three
// tiers — writes always, repair while withdrawn, user only while joined — out of
// one budget, one utilisation and one state.
//
// ⚠ It is NOT a budget per class. There is still one read ceiling and one read
// utilisation, and both classes move the same number; a per-class ceiling would
// be the third budget kind this package refused, and it would let the two classes
// stop competing for the resource they share.
//
// ★ A second reason points the same way: a degraded read costs k fragment
// fetches, so the reads a repair makes unnecessary are also the expensive ones.
// Shedding repair prolongs the degradation generating the load.
//
// ⚠ The starvation risk is real and is NOT bounded here. A node saturated by
// repair work stays withdrawn and keeps refusing user reads; what bounds that is a
// bound on repair traffic, which is docs/adr/BACKLOG.md §3 and is open.
//
// # A node decides alone
//
// ⚠ [Controller.Decide] consults this node's own utilisation and takes NO peer
// state. That is the enforcement rather than an omission, and BACKLOG.md §22
// names why: *a node that refuses to withdraw because its peers already have is a
// node that keeps taking work it cannot serve* — the error-returning behaviour
// this package rejected, arrived at by a different route.
//
// ★ "Should I withdraw" and "what do we do when everyone has" are two questions,
// and conflating them is the only way to write that trap. The second belongs to
// [Fleet], which holds STATES rather than controllers, so there is nothing to
// reach back through.
//
// # What this package does not do
//
// It measures nothing — it is told its utilisation, and the counters that carry
// it belong to the observability package, because two counts of one quantity
// diverge and the one an operator sees would not be the one that shed.
//
// It does not refuse a write for durability reasons. That refusal exists, and it
// means something entirely different: a write is refused there because it would
// not be SAFE, not because a node is BUSY. Conflating them would make a busy node
// look unsafe.
package admit
