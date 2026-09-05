// Package watch keeps what is outstanding: things that happened, matter, and
// nobody has dealt with.
//
// It answers docs/adr/BACKLOG.md §21's "watching" bullet, which ADR-010 deferred
// and ADR-012 could express but not close.
//
// # The bar, and why it rules out the obvious design
//
// §21 does not ask for a watcher to be declared. It asks *"whether an incomplete
// purge from a month ago would actually reach a person"* — because a declared
// reader that never runs is the exact failure ADR-012 exists to prevent, one
// level up.
//
// A watcher over the event stream is what one reaches for, and it fails that
// test three separate ways:
//
//   - ⚠ The stream DROPS by design. ADR-012 made a full buffer drop rather than
//     block, because observability that can stall the thing it observes is worse
//     than none. So the one event that mattered is exactly the one that can be
//     lost — and it is lost under load, which is when purges go incomplete.
//   - ⚠ A stream does not persist. A month-old event scrolled past a month ago.
//   - ⚠ Retention would silently resolve it. See below.
//
// ★ So an incomplete purge is a STATE, not an event. The event announces it; the
// obligation survives it, and only an acknowledgement clears it.
//
// # Retention bounds the log and never the obligation
//
// ⚠ This is the trap, and §21 walks toward it while being right about something
// else. §21 says retention should reuse ADR-010's [subscribe.Horizon] rather than
// growing a second notion — correct, for the LOG.
//
// Applied to the OBLIGATION it inverts what age means. Under a thirty-day
// horizon a thirty-one-day-old incomplete purge stops being reported, and the
// system answers "nothing is outstanding" precisely BECAUSE the problem got old.
//
// ★ So [Ledger.Outstanding] takes no horizon, no age filter and no limit. The
// signature is the enforcement: a caller cannot age an obligation out, because
// there is nothing to age it out with.
//
// # Age is the whole signal
//
// Obligations come back OLDEST FIRST, with their age, because the question is
// whether an old unanswered thing reaches somebody — and newest-first buries it
// further every day.
//
// ⚠ Which is why re-raising keeps the FIRST raised time. A purge that retries
// daily and fails daily must not look one day old forever: that disables the
// mechanism while leaving it apparently working, which is the worst available
// failure and the one a retry loop produces by default.
//
// ⚠ And why only an acknowledgement clears one. Silence is not resolution — a
// purge nobody retried is indistinguishable from one that completed.
//
// # Raised by the emitter, never from the stream
//
// An obligation is recorded where the condition is DETECTED. ★ That also settles
// §21's sampling warning for this path structurally rather than by argument: a
// sampled stream and a dropped stream look identical to a consumer, and an
// obligation never travels on a stream at all, so no sampling or drop policy can
// lose one.
//
// # What this package does not do
//
// It does not wake anybody. Paging, email and a console need a transport and a
// surface, and neither exists (BACKLOG.md §18/§25).
//
// ⚠ And it does not survive a restart. The ledger is in memory, so a process
// restart loses every outstanding obligation — which means the honest reading of
// §21's test today is "a month-old obligation reaches a person, in a process that
// has been up a month". That is a real gap rather than a design, it is named in
// ADR-038's Consequences, and closing it is a follow-up.
//
// See docs/adr/ADR-038-obligations.md.
package watch
