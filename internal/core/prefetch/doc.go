// Package prefetch decides which fragments a read should ask for, and whether
// it may.
//
// # Fetch k, not k+m
//
// A coded stripe has k+m fragments and reconstructs from any k. Fetching all of
// them is the simpler implementation and looks more robust, and it wastes m/k of
// the bandwidth on every healthy read — 25% at RS(8,2).
//
// ⚠ That waste lands on the same link admission control is shedding against, so
// an over-eager prefetch makes a node shed the reads it was trying to
// accelerate. Which is the failure worth naming: the prefetch competes with
// itself, and it does so hardest exactly when the node is busiest.
//
// So a plan fetches exactly k — the NEAREST k, since any k reconstruct and which
// k is therefore free to be chosen well. The remaining m are a HEDGE: carried,
// not fetched, and drawn on only when a fetch is late. Fetching them upfront is
// the waste above; discarding them means one slow node stalls the block, and at
// this scale one of any k nodes being slow is the normal case.
//
// # A prefetch is a hint, and nothing may depend on it
//
// A plan is a value. It has no side effects, nothing to clean up, and a caller
// that ignores it is in exactly the same position as one that never asked.
//
// ⚠ That is not a stylistic preference. The moment a read is only correct
// because a prefetch succeeded, every memory-pressure event becomes a
// correctness event — and it surfaces only under the load that caused the
// pressure, which is when eviction happens. The bug and its trigger arrive
// together.
//
// # A budget, because the same instruction has two outcomes
//
// "Load every part of the file into memory" is exactly right for a 40MB blob and
// an out-of-memory kill for a 4TB one, on a node that was serving other tenants
// perfectly well. The instruction is identical; only the blob differs.
//
// So a caller declares bytes, and the window says how far ahead that reaches:
// the whole file for a small one, a bounded prefix for a large one. The same
// request, two answers, neither unbounded.
//
// ⚠ And a plan that exceeds its budget is REFUSED rather than truncated. Fewer
// than k fragments cannot reconstruct, so a truncated plan spends bandwidth and
// delivers nothing — strictly worse than not prefetching, while the caller's
// ordinary read path always works.
//
// # A guess is evicted before evidence
//
// [Cache] is where a fetched block lives so a prefetch can pay off (ADR-037), and
// its whole policy rests on one distinction: a DEMANDED block was asked for by a
// read that needed it, and a SPECULATIVE one was pulled by a prefetch that guessed
// it would be. Evidence and a guess.
//
// ⚠ Least-recently-used is the obvious policy and it is wrong here. A sequential
// scan fills a cache with speculative blocks, and under LRU those evict the
// working set of every other reader on the node — the scan destroys what it never
// needed, to hold what it will not read again.
//
// So eviction takes SPECULATIVE entries first, least-recently-used within each
// class. ★ That ordering is right on every workload, which is why it can be chosen
// without one — unlike ARC or 2Q, where choosing between them genuinely needs
// traffic to measure against.
//
// ★ And a speculative entry that is READ becomes demanded. Without that
// promotion, a perfectly prefetched sequential read would keep evicting the blocks
// it is about to use: prefetching would make things worse, and worst on the
// workload it exists for.
//
// ⚠ This bounds what a WRONG prefetch costs, which is the part of "when to
// prefetch" worth deciding: bandwidth already counted against the read budget,
// plus ZERO demanded evictions. Detecting sequentiality is still deferred, but its
// blast radius is not.
//
// # What this package does not do
//
// It plans, and it holds. It fetches nothing and reconstructs nothing — those need
// a transport, which does not exist. Keeping the plan separate from its execution
// is what makes the decision testable with no cluster at all.
//
// It does not decide WHEN to prefetch. Detecting a sequential access pattern is a
// heuristic and needs a workload to tune against; what its being wrong may cost is
// decided above.
//
// It also does not decide where fragments ARE. A durability policy and the
// placement service decide that; this package takes their locations as given and
// chooses among them.
package prefetch
