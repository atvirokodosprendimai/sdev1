// Package topology declares the shape of a cluster: an ordered list of level
// labels, and a tree of named nodes beneath them.
//
// # Levels are data, not types
//
// The default map declares universe, planet, datacenter, rack, server and disk,
// but this package hardcodes none of those names. [Map.Levels] is an ordered
// list, broadest first, carried in the map file — so a deployment may insert a
// row or a pod level, or drop universe entirely, without any change here.
// Inserting a level is a map edit.
//
// The cost of that choice is that a mistyped level label is a load-time error
// rather than a build error, which is why [Load] refuses an undeclared label
// loudly instead of placing the node somewhere plausible.
//
// # Two forms, one codec
//
// A map is AUTHORED as a nested tree, because that is what a person can read and
// edit ([AuthoredNode]). It is RESIDENT as a flat, pointer-free slice of nodes
// carrying nested-set intervals, sorted by Lft, because that is what answers
// questions cheaply. [Load] is the only path from the first form to the second
// and [Map.Tree] is the only path back.
//
// Having one value flow through two representations is a defect shape worth
// naming: a check that exercises only one path cannot see the other silently
// narrowing what it carries. That is why the round trip is asserted by a
// property test over generated trees rather than by a fixture — a fixture
// encodes what its author expected, so it cannot falsify the expectation.
//
// # Why nested sets
//
// Each node holds an interval [Lft, Rgt] assigned by a depth-first walk, so one
// node contains another exactly when its interval strictly encloses the other's.
// Ancestry, distance and failure-domain membership then become integer
// comparisons on a flat array rather than pointer walks, which matters because
// those questions sit on the hot path: every read-preference decision and every
// placement check asks one of them.
//
// The classic objection to nested sets is that inserting or moving a node
// renumbers much of the tree. That cost does not apply here, because a map is
// never mutated in place — it is republished whole as a new version, and
// renumbering happens once, on load. Anything that tries to add a single node to
// a resident map breaks that assumption and the objection returns.
//
// # What a map never carries
//
// Cluster shape only. A map never holds the location of an individual key,
// object, datom or segment: those are computed from a key, not looked up. That
// bound is what keeps a map small enough to hand to every client, which in turn
// is what keeps a metadata service off the read and write paths.
//
// The decision this package implements is recorded in
// docs/adr/ADR-001-address-space.md.
package topology
