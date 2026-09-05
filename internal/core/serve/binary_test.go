package serve_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/sdev1/internal/core/addr"
	"github.com/atvirokodosprendimai/sdev1/internal/core/hlc"
	"github.com/atvirokodosprendimai/sdev1/internal/core/leafstore"
	"github.com/atvirokodosprendimai/sdev1/internal/core/ports"
	"github.com/atvirokodosprendimai/sdev1/internal/core/routing"
	"github.com/atvirokodosprendimai/sdev1/internal/core/tx"
)

// listening matches the line sdev1-serve prints once it is bound.
var listening = regexp.MustCompile(`on (\S+?),`)

// TestTheBinaryServesOverARealNetwork closes rung 4 for `cmd/sdev1-serve`.
//
// ★ Every other test here binds a listener inside this process. That exercises
// the sockets but not the BINARY: the flags, the leaf parsing, the `--route`
// spelling and the wiring in `main` are all untested by a goroutine that calls
// `NewServer` directly, and a server nobody can start is a library.
//
// ⚠ Two separate processes, and the client's cache points at the WRONG one. The
// redirect therefore crosses a real process boundary, computed by a program that
// shares no memory with this test.
func TestTheBinaryServesOverARealNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts two processes")
	}

	binary := build(t)

	key := addr.KeyOf(tenant(), "planet-7")
	wanted, err := addr.Descend(key, leafDepth)
	if err != nil {
		t.Fatalf("Descend: %v", err)
	}
	other := elsewhere(wanted)

	// The process that HOLDS the key, over a leaf on a real disk.
	right := start(t, binary, seeded(t, wanted), wanted)

	// The process that does NOT, told by flag where the key went. ⚠ Its route is
	// parsed by the binary's own `--route` handling, which nothing else covers.
	wrong := start(t, binary, t.TempDir(), other,
		fmt.Sprintf("--route=%s=42@%s", wanted, right))

	// The client believes the key lives on the wrong process, at an older epoch.
	c := client(t, routing.Route{Prefix: wanted, NextHops: []string{wrong}, Epoch: 1}, 0)

	run, err := c.Read(key, "READ name FROM planet-7", registered+1)
	if err != nil {
		t.Fatalf("reading through two real processes: %v", err)
	}
	if len(run) != 1 || string(run[0].Value) != "Kepler" {
		t.Fatalf("the answer is %v, want one planet-7/name/Kepler datom", run)
	}

	route, err := c.Route(key)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if route.NextHops[0] != right || route.Epoch != 42 {
		t.Errorf("the client points at %v at epoch %d, want %s at 42 — the process that was "+
			"WRONG is what should have repaired it", route.NextHops, route.Epoch, right)
	}
}

// build compiles cmd/sdev1-serve into the test's temporary directory.
func build(t *testing.T) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "sdev1-serve")
	cmd := exec.Command("go", "build", "-o", binary, "../../../cmd/sdev1-serve")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building cmd/sdev1-serve: %v\n%s", err, out)
	}
	return binary
}

// seeded returns a directory holding one sealed leaf with one fact in it.
func seeded(t *testing.T, leaf addr.LeafID) string {
	t.Helper()

	dir := t.TempDir()
	store, err := leafstore.Open(dir, leaf)
	if err != nil {
		t.Fatalf("leafstore.Open: %v", err)
	}
	ctx := context.Background()
	if err := store.Append(ctx, ports.Datom{
		Entity: "planet-7", Attribute: "name", Value: []byte("Kepler"),
		Valid: forever(registered), Assert: true,
		TxID: tx.TxID{HLC: hlc.Timestamp{Wall: registered}, Seq: 1},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Seal(ctx); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return dir
}

// start runs one sdev1-serve process and returns the address it reports.
//
// ⚠ The address is READ BACK from the process rather than chosen here: the
// binary is asked for port 0, so only it knows what it got, and a test that
// picked a port would be racing every other test on the machine for it.
func start(t *testing.T, binary, dir string, leaf addr.LeafID, extra ...string) string {
	t.Helper()

	args := append([]string{
		"--dir=" + dir,
		"--leaf=" + leaf.String(),
		"--addr=127.0.0.1:0",
	}, extra...)

	cmd := exec.Command(binary, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", binary, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// The process announces its address on the first stderr line.
	type line struct {
		text string
		err  error
	}
	first := make(chan line, 1)
	go func() {
		r := bufio.NewReader(stderr)
		text, err := r.ReadString('\n')
		first <- line{text, err}
		// Drain the rest so a chatty process cannot block on a full pipe.
		_, _ = io.Copy(io.Discard, r)
	}()

	select {
	case got := <-first:
		if got.err != nil && got.text == "" {
			t.Fatalf("the server printed nothing before exiting: %v", got.err)
		}
		m := listening.FindStringSubmatch(got.text)
		if m == nil {
			t.Fatalf("could not find an address in %q", strings.TrimSpace(got.text))
		}
		return m[1]
	case <-time.After(15 * time.Second):
		t.Fatal("the server did not report an address within 15s")
		return ""
	}
}
