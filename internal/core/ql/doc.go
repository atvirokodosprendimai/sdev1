// Package ql is the query language: the public contract through which
// everything else in this system is reached.
//
// # Time is a clause, not a family of verbs
//
// The alternative was a verb per combination — READ and READ AS OF, MATCH
// and MATCH AS OF — and it fails twice. The list doubles with every new
// statement, and a per-leg time qualifier ("this leg as of last year, that one
// now") would need a second grammar rather than falling out of the first.
//
// As a clause, time composes with every term. The per-leg form is free, which is
// the whole reason the shape was chosen.
//
// # A lone instant binds VALID time, and leaves transaction time open
//
// There are two axes. VALID time is the business axis, supplied by the writer:
// when the fact was true. TRANSACTION time is the system axis: when the system
// learned it.
//
//	the caller wrote          AsOf      ValidAt
//	nothing                   open      now
//	AS OF t                   open      t
//	AS OF t TRANSACTION u     u         t
//	TRANSACTION u             u         now
//
// ⚠ Row two is the one that matters, and it is the one a reasonable implementer
// gets wrong. Passing a lone instant to BOTH axes looks obviously right and is
// the defect: a write made now but valid from last year commits at a transaction
// time AFTER the instant being asked about, so binding both axes excludes it, and
// a query at the instant it was backdated to returns nothing.
//
// A predecessor project in this workspace shipped exactly that. Roughly 140
// tests including the race detector stayed green, because every one of them
// happened to write with valid_from equal to the transaction time — so the two
// parameters were never actually different in any test, and the bug had
// structurally no test that could see it.
//
// ★ Which is why [TimeClause.Resolve] calls the temporal package's resolver and
// branches on nothing itself. The table has exactly ONE implementation. Two
// would drift, and the drift is invisible until a query returns the wrong
// history.
//
// # An optional leg binds nothing rather than dropping the row
//
// A shape query names required legs and optional legs. If an optional leg that
// matches nothing dropped its row, OPTIONAL would mean the same as REQUIRE — and
// the difference would show up only on data where the leg is sometimes absent,
// which is not the data anyone tests with.
//
// So the row is returned, carrying an unbound value for that leg. Every consumer
// then has to handle unbound, which is a real cost paid to keep the two kinds of
// leg meaning different things.
//
// A shape query also states its metric and its threshold. Similarity without
// them is not a query: it is a result nobody can reproduce.
//
// # What this package does not do
//
// It parses. It does not evaluate, plan, or optimise anything, and it touches no
// storage — those need a storage engine that does not exist yet. It does not
// decide what time MEANS; the temporal package does, and this one calls it.
//
// A storage-policy clause sets the policy for NEW writes only. Every block
// records how it was written, so a policy change reinterprets nothing already
// stored — and there is deliberately no syntax for re-encoding what exists.
package ql
