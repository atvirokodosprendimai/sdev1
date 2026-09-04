// Package link decides what a reference between entities is, and what walking
// one means.
//
// # A link is a datom
//
// It is bitemporal, retractable, bound to one entity and inside the tenant
// subtree — not because those were decided here, but because a link is not a new
// kind of thing. A separate edge store would have to re-decide all four, and
// would get at least one of them wrong.
//
// ⚠ The kind is STORED, never inferred. Guessing from the shape of the bytes —
// "that looks like an entity name" — turns every string resembling an identifier
// into an accidental edge, and the guess changes when unrelated data does. There
// has to be a way to store a string that merely looks like a name.
//
// # The rule this package exists for
//
// ⚠ A TRAVERSAL THAT RESOLVES EACH HOP AT ITS OWN INSTANT PRODUCES A TREE THAT
// NEVER EXISTED.
//
// Ask for a hierarchy as it stood last March. The natural implementation reads
// the root at March, then reads its children with a fresh read — at today's
// instant. The answer is March's root with today's children: a shape that was
// never true at any moment, assembled from two that were.
//
// Every node in it is real. Every edge in it existed at some point. Nothing about
// it looks wrong. And it is fiction — precisely in the historical queries a
// bitemporal store exists to answer.
//
// [Walk] therefore takes ONE snapshot and hands that same snapshot to every hop.
// [Resolver] takes the snapshot as a parameter so a caller structurally cannot
// resolve a hop without saying when.
//
// # Three more rules, each with a reason
//
// A walk is depth-BOUNDED and the bound is required: an unbounded walk over a
// graph the caller does not control is a scan they did not ask for.
//
// A cycle is REPORTED, never truncated. A partial answer that reads as complete
// is worse than a refusal. ⚠ And cycles are not hypothetical: a hierarchy edited
// over time can contain a loop that exists only at instants between two edits —
// visible in a historical query and in no current one.
//
// ⚠ An unresolvable reference is an ORDINARY ABSENCE, never an error. The target
// may have been retracted, may not exist yet at the instant asked about, or may
// have been ERASED — and those three must be indistinguishable, or a traversal
// becomes the existence oracle that crypto-shredding exists to remove.
//
// # What this package does not do
//
// It stores nothing and reads no storage. [Walk] is handed a [Resolver] and a
// snapshot and returns what is reachable. Keeping the walk separate from the
// fetch is what makes the same-instant rule provable against a fixture whose
// graph changes between instants — which is the only way to catch the defect.
package link
