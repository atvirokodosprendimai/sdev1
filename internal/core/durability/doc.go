// Package durability expresses how many copies of data exist, spread across
// what, and what happens when the cluster cannot maintain that.
//
// # Two knobs, because one cannot answer the important question
//
// A policy carries a TARGET copy count and a FLOOR. The target is what the
// cluster aims for. The floor is the point below which writes are REFUSED.
//
// Most systems ship one number, which leaves no answer to "the cluster is
// degraded, do we still accept writes" other than yes — and yes means accepting
// data at a durability the operator believes they have and do not. Two numbers
// make the answer explicit and make refusing the default.
//
// ⚠ The floor is at least 2 and cannot be constructed lower. A configuration
// permitting one copy will eventually be set to one copy, and the moment it
// would be relaxed is the moment nobody is reading warnings.
//
// # Two is a DURABILITY floor, not a CONSENSUS floor
//
// This is the trap worth naming before anyone deploys against it. Two voting
// members give a quorum of two, so losing either stops writes — a bare pair is
// LESS available than a single node while being more durable. The minimum viable
// live-tier shape is therefore two data replicas plus one witness that votes and
// stores no data: three participants for two copies.
//
// # Why durability is per TIER and not per cluster
//
// The live and sealed tiers have incompatible requirements, so one setting must
// sacrifice one of them everywhere.
//
// Consensus needs WHOLE replicas: a fragment can neither serve a read
// independently nor cast a vote, so the live tier cannot be erasure-coded.
// Immutable sealed data has no such constraint and coding buys it back an order
// of magnitude in storage.
//
// # The arithmetic that makes two requirements conflict
//
// Surviving the loss of two servers out of three requires each survivor to hold
// a COMPLETE recoverable copy — three-way replication, 200% overhead, and no
// code beats it, because a single survivor must suffice alone.
//
// An erasure code needs at least k+m independent failure domains AT THE LEVEL it
// is meant to survive. A (8,2) code across ten racks survives two rack losses at
// 25% overhead; the same code across three servers survives nothing at the
// server level, however its fragments are arranged. The number of failure
// domains BOUNDS what coding can buy, and no configuration escapes that bound.
//
// The resolution is the per-tier split above: whole copies where consensus needs
// them, coding where the domain count supports it.
//
// # Two checks that do not substitute for each other
//
// [Policy.Validate] asks whether a cluster could EVER satisfy a policy, from the
// declared topology alone. [Policy.Satisfied] asks whether it does RIGHT NOW,
// from the domains currently holding copies. A design with only the first
// accepts writes into a degraded cluster; one with only the second discovers a
// misconfiguration during an incident.
//
// ⚠ Validate checks a DECLARATION. A map claiming ten racks that share one power
// feed declares ten domains and has one, and no property of the map can tell the
// difference. That is a limit of the approach rather than of this
// implementation, and it is stated rather than mitigated.
//
// The decision this package implements is recorded in
// docs/adr/ADR-004-durability-policy.md.
package durability
