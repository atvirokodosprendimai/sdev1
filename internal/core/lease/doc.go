// Package lease establishes who may write to a leaf, using a token that orders
// claims by recency rather than by liveness.
//
// # The problem it exists to solve
//
// A leaf has one writer. If that writer's process dies and nothing can take over,
// the leaf is readable forever and writable never — a permanent outage caused by
// a transient fault.
//
// The obvious fix makes it worse. A release call, or a timeout after which any
// caller may claim the leaf, cannot tell a DEAD holder from a SLOW one. A writer
// paused by a garbage collection, a stalled disk or a network hiccup looks
// exactly like a writer that is gone, so the timeout eventually fires on a live
// one and two writers append to the same tail. That is not a degraded system, it
// is a corrupted one. Trading a leaf that stops for a leaf that lies is a bad
// trade, which is why there is no Release method here and no expiry.
//
// # The epoch, and where it is checked
//
// Every lease carries an epoch, strictly greater than every epoch granted before
// it for that leaf. It does not say who you are; it says how recent your claim
// is, which is the only question a resource can answer without knowing anything
// about who is alive.
//
// ⚠ The epoch travels WITH the write, and the thing being written to refuses
// anything below the highest it has seen. That placement is the whole mechanism.
// The natural implementation — the writer asks "am I still the leader?", gets
// yes, then writes — has a window between the question and the write in which
// leadership can be lost, and the write lands anyway. The check and the write are
// not atomic and no amount of checking from the writer's side makes them so.
//
// So a writer that was paused for a minute comes back, appends under its old
// epoch, and is refused by the tail itself. It cannot corrupt anything. It can
// only fail, which is what it should do.
//
// # How it fails, and how it recovers
//
// A grant never waits for, notifies, or requires anything from the previous
// holder. Waiting is what would make a dead writer a permanent outage; the epoch
// is what makes not waiting safe. The old holder discovers it has been superseded
// at its next append and not before, and until then it can do no harm.
//
// Observing a higher epoch is irreversible. Once a resource has seen epoch N it
// never accepts N-1 again, even if the holder of N vanishes immediately — a leaf
// that has moved on cannot be dragged back by whichever writer was slowest.
//
// The first epoch a resource sees is accepted whatever its value, so nothing has
// to be told where the counter started.
//
// # What this package does not do
//
// It does not decide WHEN a handover should happen. That is a consensus
// question, it needs a transport, and neither exists yet — so the registry here
// is in-process and named for what it is. The fencing is real; the election is
// not.
//
// It also does not decide where a request should go, or how many copies a write
// needs. Reaching a node is not permission to write on it, and a lease holder
// writing below its durability floor is still refused — by a different mechanism,
// for a different reason.
package lease
