// Package erasure turns a block into fragments that survive the loss of some of
// themselves, and turns the survivors back into the block.
//
// # A stripe describes itself
//
// A stripe is one block's worth of fragments. Its header records how many data
// fragments and how many parity fragments produced it, how large each is, and
// the block's true length — everything needed to decode it. None of that is read
// from configuration.
//
// That matters because the scheme is operator-configurable and therefore varies
// over the life of a cluster. Changing RS(8,2) to RS(10,4) changes what the next
// write produces; it reinterprets nothing already written, because nothing
// already written asks configuration what it is. This is the same rule the
// address space applies to its fan-out and the segment format applies to its
// codec: a constant that is safe as POLICY is fatal as a FORMAT ASSUMPTION.
//
// # An erasure is not an error, and the difference is the whole package
//
// A Reed-Solomon code with k data fragments and m parity fragments corrects
// either:
//
//   - m ERASURES — fragments known to be missing, where the code is told which
//     positions to solve for; or
//   - ⌊m/2⌋ ERRORS — fragments present but wrong, where the code must first work
//     out which ones are lying.
//
// An error costs twice an erasure because locating the fault consumes as much
// redundancy as repairing it. RS(8,2) therefore tolerates two lost fragments and
// ZERO silently corrupted ones. Given one rotten fragment it cannot identify,
// the decoder returns a block that is wrong, and raises no error anywhere.
//
// That is the worst failure this system can have. Not loss, which is visible,
// but corruption reported as success.
//
// So every fragment carries its own checksum and is verified BEFORE decoding
// begins. A fragment that fails is treated as absent, which turns every error
// into an erasure and restores the full m tolerance. The fragment checksum is
// not a nicety layered on top of the code — it is what makes the code's stated
// tolerance true.
//
// The block checksum in the segment format does not close this gap. It is
// computed over the reassembled block and is therefore only available AFTER
// reconstruction, so it reports that the answer is wrong without saying which
// fragment to exclude — leaving a search over subsets as the only recovery.
// It is still checked at the end of a reconstruction, because it is the one
// check that spans the whole path and a coding-matrix bug is exactly the class
// that passes every local check and fails that one.
//
// # How it fails, and how it recovers
//
// A lost disk, server or rack removes whatever fragments it held. While at
// least k fragments verify, the block is rebuilt from them and reads succeed at
// the cost of k fragment reads instead of one. Below k, the information is not
// present: Reconstruct refuses with ErrInsufficientFragments rather than
// returning a best effort, because there is no best effort — there is only
// invention.
//
// A rotten fragment is caught by its checksum, excluded, and the block rebuilt
// from the rest. The cluster is then one fault closer to the floor without
// having lost anything, which is the state a repair is for.
//
// Recovery is re-encoding the block and writing replacement fragments. It
// depends on encoding being deterministic — the same block and scheme produce
// byte-identical fragments on any node — because a rebuilt fragment must be
// indistinguishable from the one it replaces. Otherwise two copies of one
// fragment differ and nothing can say which is right.
//
// # What this package does not do
//
// It performs no I/O and makes no placement decision. Where fragments go is a
// durability policy's business, and a coder that also placed would be a second
// authority over one question.
package erasure
