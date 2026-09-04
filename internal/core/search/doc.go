// Package search decides what a full-text posting is, who can see one, and what
// a facet may answer.
//
// # Why this package exists at all
//
// ⚠ AN ORDINARY INVERTED INDEX SILENTLY UNDOES CRYPTO-SHREDDING, and that single
// fact shapes everything here.
//
// Erasure in this system is the destruction of a subject's key: the ciphertext
// remains and becomes permanently unreadable. That is what lets an erasure reach
// a coded stripe spread over ten failure domains, a replica that has been offline
// for a month, and a backup on a shelf — without finding, visiting or rewriting
// any of them. The argument holds only because nothing READABLE sits beside the
// ciphertext.
//
// An inverted index is, by construction, extracted plaintext. Index a subject and
// the index holds their terms in the clear, keyed to them. Destroy the key
// afterwards and the segments go dark while the index still answers
// "term → subject-42". The erasure is undone, and in the worst possible way: the
// index is now the FASTEST structure in the system for finding the subject
// somebody asked to have erased.
//
// # What is done instead
//
// A [Posting] is sealed with the SUBJECT's own key. Shredding that key makes the
// posting undecryptable, so it stops being a result in the live index, in every
// replica, and in every backup of the index — all at once, and without anything
// having to go and find them. Erasure reaches the index by exactly the argument
// that makes it reach a coded stripe.
//
// ⚠ [Visible] drops what it cannot open and returns NO count of what it dropped.
// A caller told "3 results withheld" has an oracle for the existence of erased
// subjects, which is the property the erasure design spent a whole record
// removing. Incrementing a counter when a decrypt fails is the natural thing to
// write inside that loop, which is why [Visible] is shaped so it cannot.
//
// ⚠ Deleting a shredded subject's postings is NOT an acceptable substitute, even
// though it looks sufficient. It reintroduces a deletion that has to find and
// visit every copy, and a replica that was offline during the purge keeps its
// own. That is the model crypto-shredding replaced.
//
// # The index is derived, and the log wins
//
// Nothing here is authoritative. An index is a read model over the datom log,
// rebuildable at any time, and a search result is a set of CANDIDATES that must
// be confirmed against the datoms before being returned — an index fed by
// subscription is always behind, so trusting it returns entities that no longer
// match with nothing able to detect it.
//
// A posting carries the transaction range it held over, so a search can be
// qualified in time like every other read. The price is that postings accumulate
// with history rather than with data.
//
// # Counting
//
// ⚠ A facet is EXACT or REFUSED. An approximate count that is not labelled
// approximate is a lie, and a facet count is precisely the number somebody
// reconciles against a total. [Facet] refuses a matched set larger than its
// declared bound rather than estimating or truncating, which returns the caller
// to a narrower query — something that works.
//
// # What this package does not do
//
// It builds no index, persists nothing and reads no storage. [Analyze] turns text
// into terms, [Visible] decides which sealed postings a caller can actually see,
// and [Facet] counts a matched set it is handed. Keeping that separate from the
// index build is what makes the erasure property — the one thing that must not be
// got wrong — provable today against the real keystore, with no cluster.
//
// # The residue
//
// ⚠ A sufficiently RARE TERM remains disclosive. This design confines the leak
// from the subject to the term, and does not remove it: a dictionary is a shared
// structure, and a rare enough term approximates an identifier. That is recorded
// as a boundary rather than claimed as solved, and it is the reason not to index
// high-cardinality identifiers.
package search
