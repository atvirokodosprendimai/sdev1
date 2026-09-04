// Package crypt makes a subject's bytes readable only through a key held
// somewhere else, so that destroying the key erases the subject.
//
// # Why erasure is key destruction
//
// This system is append-only by construction. A segment is immutable, its
// fragments are spread over failure domains, and a backup replays them
// elsewhere. Every one of those decisions makes stored bytes harder to reach and
// change, which is the point — and it makes deletion, in the ordinary sense,
// impossible.
//
// Erasure is nevertheless required, so the data cannot be destroyed and must
// become unreadable instead. That leaves one mechanism: encrypt per subject, and
// destroy the key.
//
// Destroying a key reaches places no delete can. A coded stripe scattered over
// ten failure domains, a replica offline for a month, a backup on a shelf — none
// of them has to be found, visited or rewritten. They all become unreadable at
// the same instant, because the thing that made them readable is gone.
//
// # The ciphertext is permanent, and that is the whole constraint
//
// Everything else here follows from one fact: the encrypted bytes survive
// forever, and so does whatever is stored beside them.
//
// So if a block says WHOSE data it is, the subject's identity outlives the
// erasure that was supposed to remove it. An identifier is personal data. A
// system that shreds the content and keeps an un-erasable label naming the
// person has not erased anything — it has produced a permanent record of the
// fact that this person's data was deleted, which is very nearly the opposite of
// what was asked.
//
// A key handle is therefore ALLOCATED, never derived. It is random bytes with no
// relationship to the subject, and the mapping from subject to handle lives in
// the keystore and is destroyed along with the key. A handle computed from an
// email address would be a permanent, confirmable label for that address, and it
// would be confirmable by anyone who could guess the address.
//
// # Where the key is, and where it is not
//
// The key lives in a mutable keystore. The ciphertext lives in immutable
// storage. They are never the same place, because an immutable segment cannot
// give up a key it contains.
//
// [Open] therefore takes the keystore rather than a key. The envelope says which
// key; the keystore says whether you may still have it. A caller that could
// supply its own key would be a second authority on whether a shredded subject
// is readable, and there would be as many authorities as callers.
//
// # How it fails, and how it recovers
//
// The cipher is authenticated, so failure is closed rather than quiet. A
// destroyed key, a wrong key, a flipped bit and a swapped handle all produce an
// error; none of them produces plausible plaintext. That matters more here than
// in most places, because "unreadable" is the guarantee being made.
//
// A shredded subject does not recover, and must not. Destruction that could be
// undone would not be an erasure.
//
// ⚠ There is one way to undo it by accident: restoring a backup that contains
// the keystore alongside the data resurrects the key beside the ciphertext, and
// nothing reports it. The keystore's backup is a separate concern with a separate
// retention, and this is the single easiest way to get crypto-shredding wrong.
//
// # What this package does not decide
//
// It decides how erasure works. It does not decide when, who may ask, or what
// else an erasure request must reach — a request's fan-out to sinks and its
// per-sink acknowledgement belong elsewhere, and so does authorization. A
// package that also authorized would be a second gate over the same door.
//
// Crypto-shredding also cannot un-disclose what already leaked. It makes data
// unreadable going forward, and no mechanism does more than that.
package crypt
