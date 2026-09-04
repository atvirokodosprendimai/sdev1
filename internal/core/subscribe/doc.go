// Package subscribe lets a consumer follow the log, and lets an operator remove
// a subject in a way that says which sinks still have it.
//
// # Three things get called "delete", and only one is erasure
//
// MARKING makes a subject invisible to queries. It is immediate and cheap, and
// it changes nothing about the bytes — anyone holding them still has them. It is
// what most systems mean by a delete.
//
// SHREDDING destroys the subject's key. It is immediate and irreversible, and it
// is the only one of the three that is erasure: it reaches coded stripes,
// offline replicas and backups without visiting any of them, because the thing
// that made them readable is gone.
//
// SWEEPING reclaims space. It is eventual, bounded by a retention horizon, and
// it reaches neither a backup nor a coded stripe already written elsewhere.
//
// ⚠ An operator who MARKS a subject and reports it erased has said something
// false. An operator who waits for a SWEEP has said something that will be true
// locally and never true of the backup. That is why there is no Delete verb
// here: one verb would be answered by a different mechanism depending on
// context, and the caller would not know which they got.
//
// # A purge is a fan-out, and the third outcome is the useful one
//
// This system replays its log into sinks — a streaming backup, a read model, an
// operator console. Those are three consumers of one primitive: a cursor that
// reads a position and advances it.
//
// ⚠ Which is what makes removal dangerous. A purge that removes the primary copy
// and reports success, while a sink nobody remembered still holds the data,
// produces the failure this corpus is built to avoid: a restore that resurrects
// what an operator was told was gone, months later, with nothing having reported
// anything.
//
// So a purge collects an acknowledgement from every REGISTERED sink, and an
// unacknowledged sink makes the result INCOMPLETE — not done, and not failed.
// Both of the usual two answers are wrong here. "Done" is a lie that surfaces at
// the next restore. "Failed" suggests nothing happened, when the primary copy is
// already gone, and would send an operator to retry everything instead of
// chasing one sink. Incomplete is the only answer that is both true and
// actionable.
//
// A sink outside the registry is invisible to all of this. Registration is the
// whole mechanism, and nothing can see what nothing told it about.
//
// # Following, and how it resumes
//
// A cursor is a transaction identifier, not an offset. It survives compaction
// and renumbering, and two subscribers' positions are comparable with everything
// else the system orders by — including a snapshot a reader is holding.
//
// A cursor advances only past entries the sink ACKNOWLEDGED, so a sink that
// crashes resumes exactly where it stopped. Nothing between the cursor and the
// watermark is ever skipped, because a backup missing entries looks exactly like
// a complete one.
//
// ⚠ Delivery is therefore at-least-once, and a sink must tolerate a repeat.
// Exactly-once is not available here and saying so is more useful than implying
// otherwise: it requires the sink's own writes to be transactional with its
// cursor advance, which is the sink's property and not this primitive's.
//
// # What this package does not do
//
// It does not make anything unreadable — that is key destruction, and a purge
// claiming "erased" would be claiming that guarantee without doing its work. It
// reclaims no space, because nothing here opens a file. It delivers nothing over
// a network. And it does not escalate a purge that stays incomplete: an operator
// has to notice, which is a gap a console should close rather than this package.
package subscribe
