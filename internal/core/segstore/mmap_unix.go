//go:build darwin || linux

package segstore

import (
	"os"

	"golang.org/x/sys/unix"
)

// There is deliberately NO implementation for any other platform. A build that
// cannot map a file should fail to compile, naming the symbols a new file would
// have to provide, rather than succeeding with a read path nobody chose.
//
// golang.org/x/sys is used rather than the standard syscall package, which offers
// the same two calls: syscall is frozen, so its per-platform surface is no longer
// corrected. It costs nothing here — the module already carries x/sys behind
// klauspost/cpuid, so this promotes an indirect dependency to a direct one rather
// than adding one.

// mmapFile maps the whole of f read-only and returns the mapping.
//
// The mapping is MAP_SHARED because the file is never written again: a private
// mapping would ask the kernel for copy-on-write bookkeeping that nothing here
// can ever use.
//
// ⚠ size must be greater than zero. A zero-length mapping is refused by the
// kernel with EINVAL, and that refusal would reach a caller instead of the
// [ErrNotASegment] the empty file actually deserves — so [Open] checks the length
// before calling this.
func mmapFile(f *os.File, size int) ([]byte, error) {
	return unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ, unix.MAP_SHARED)
}

// munmap releases a mapping returned by [mmapFile].
//
// ⚠ Every slice that aliases the mapping is invalid afterwards. Touching one is
// not a Go error but a signal, with no stack naming this call — which is why
// nothing in this package hands a caller a slice it did not allocate.
func munmap(data []byte) error {
	return unix.Munmap(data)
}
