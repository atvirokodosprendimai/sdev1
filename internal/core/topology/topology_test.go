package topology

import (
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixturePath returns the shared topology fixture, which lives at the module
// root so the command in cmd/sdev1-addr can use the same file these tests do.
func fixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "testdata", "topology", "minimal.json")
}

func loadFixture(t *testing.T) Map {
	t.Helper()
	f, err := os.Open(fixturePath(t))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	m, err := Load(f)
	if err != nil {
		t.Fatalf("Load fixture: %v", err)
	}
	return m
}

func loadString(t *testing.T, s string) (Map, error) {
	t.Helper()
	return Load(strings.NewReader(s))
}

// TestLoadRejectsUnknownVersion checks a map written by a future release is
// refused rather than partially read.
func TestLoadRejectsUnknownVersion(t *testing.T) {
	const future = `{"version":99,"depth":1,"levels":["a"],"root":{"level":"a","name":"x"}}`
	if _, err := loadString(t, future); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("Load(version 99) error = %v, want ErrUnknownVersion", err)
	}
}

// TestLoadRejectsDepthOutOfRange checks depth is validated on load, so an
// out-of-range value can never reach the descent in package addr.
func TestLoadRejectsDepthOutOfRange(t *testing.T) {
	for _, depth := range []int{0, 33, 255} {
		src := `{"version":1,"depth":` + itoa(depth) + `,"levels":["a"],"root":{"level":"a","name":"x"}}`
		if _, err := loadString(t, src); !errors.Is(err, ErrDepthOutOfRange) {
			t.Errorf("Load(depth %d) error = %v, want ErrDepthOutOfRange", depth, err)
		}
	}
}

// TestLevelsAreDataNotTypes is the falsifier for the extensibility this format
// advertises: a map declaring level labels this package has never heard of must
// load and resolve. It fails if anyone reintroduces hardcoded level types.
func TestLevelsAreDataNotTypes(t *testing.T) {
	const exotic = `{
	  "version":1,"depth":1,
	  "levels":["universe","region","pod","host","device"],
	  "root":{"level":"universe","name":"u","children":[
	    {"level":"region","name":"r","children":[
	      {"level":"pod","name":"p","children":[
	        {"level":"host","name":"h1","children":[{"level":"device","name":"h1-dev"}]},
	        {"level":"host","name":"h2","children":[{"level":"device","name":"h2-dev"}]}
	      ]}
	    ]}
	  ]}
	}`
	m, err := loadString(t, exotic)
	if err != nil {
		t.Fatalf("Load(exotic levels): %v", err)
	}
	if got, want := m.Levels, []string{"universe", "region", "pod", "host", "device"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Levels = %v, want %v", got, want)
	}
	// And the distance primitive works over labels the package never declared:
	// two hosts under one pod are one hop apart.
	d, err := m.Distance("h1", "h2")
	if err != nil {
		t.Fatalf("Distance over exotic levels: %v", err)
	}
	if d != 1 {
		t.Errorf("Distance(h1,h2) = %d, want 1 (both under one pod)", d)
	}
	anc, err := m.AncestorAtLevel("h1", m.LevelIndex("pod"))
	if err != nil {
		t.Fatalf("AncestorAtLevel over exotic levels: %v", err)
	}
	if anc.Name != "p" {
		t.Errorf("AncestorAtLevel(h1, pod) = %q, want %q", anc.Name, "p")
	}
}

// TestLoadRejectsNodeAtUndeclaredLevel checks both structural refusals: a level
// label absent from Levels, and a child that is not strictly deeper than its
// parent.
func TestLoadRejectsNodeAtUndeclaredLevel(t *testing.T) {
	const undeclared = `{"version":1,"depth":1,"levels":["a","b"],
	  "root":{"level":"a","name":"x","children":[{"level":"zzz","name":"y"}]}}`
	if _, err := loadString(t, undeclared); !errors.Is(err, ErrUndeclaredLevel) {
		t.Errorf("Load(undeclared level) error = %v, want ErrUndeclaredLevel", err)
	}

	const notDeeper = `{"version":1,"depth":1,"levels":["a","b"],
	  "root":{"level":"b","name":"x","children":[{"level":"a","name":"y"}]}}`
	if _, err := loadString(t, notDeeper); !errors.Is(err, ErrLevelNotDeeper) {
		t.Errorf("Load(child not deeper) error = %v, want ErrLevelNotDeeper", err)
	}

	const sameLevel = `{"version":1,"depth":1,"levels":["a","b"],
	  "root":{"level":"a","name":"x","children":[{"level":"a","name":"y"}]}}`
	if _, err := loadString(t, sameLevel); !errors.Is(err, ErrLevelNotDeeper) {
		t.Errorf("Load(child at same level) error = %v, want ErrLevelNotDeeper", err)
	}
}

// TestLoadRejectsDuplicateName checks names are unique across the map, since a
// name is how every other package addresses a node.
func TestLoadRejectsDuplicateName(t *testing.T) {
	const dup = `{"version":1,"depth":1,"levels":["a","b"],
	  "root":{"level":"a","name":"x","children":[{"level":"b","name":"x"}]}}`
	if _, err := loadString(t, dup); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("Load(duplicate name) error = %v, want ErrDuplicateName", err)
	}
}

// TestIntervalsNestStrictly checks the property every later range comparison
// rests on: any two intervals are either disjoint or one strictly contains the
// other, and no interval is empty or inverted.
func TestIntervalsNestStrictly(t *testing.T) {
	m := loadFixture(t)
	for i, a := range m.Nodes {
		if a.Lft >= a.Rgt {
			t.Fatalf("node %q has empty or inverted interval [%d,%d]", a.Name, a.Lft, a.Rgt)
		}
		for j, b := range m.Nodes {
			if i == j {
				continue
			}
			disjoint := a.Rgt < b.Lft || b.Rgt < a.Lft
			aContainsB := a.Lft < b.Lft && b.Rgt < a.Rgt
			bContainsA := b.Lft < a.Lft && a.Rgt < b.Rgt
			if !disjoint && !aContainsB && !bContainsA {
				t.Fatalf("intervals of %q [%d,%d] and %q [%d,%d] partially overlap",
					a.Name, a.Lft, a.Rgt, b.Name, b.Lft, b.Rgt)
			}
			if aContainsB && a.LevelIdx >= b.LevelIdx {
				t.Fatalf("%q contains %q but is not at a shallower level (%d vs %d)",
					a.Name, b.Name, a.LevelIdx, b.LevelIdx)
			}
		}
	}
	// Nodes are held sorted by Lft so the resident form is binary-searchable.
	for i := 1; i < len(m.Nodes); i++ {
		if m.Nodes[i-1].Lft >= m.Nodes[i].Lft {
			t.Fatalf("Nodes not sorted by Lft at index %d", i)
		}
	}
}

// TestDistanceIsCommonAncestorLevel checks same-rack is nearer than
// same-datacenter, which is nearer than same-planet, and that Distance is
// symmetric. Exercises the two-rack fixture.
func TestDistanceIsCommonAncestorLevel(t *testing.T) {
	m := loadFixture(t)

	sameRack, err := m.Distance("srv-1", "srv-2")
	if err != nil {
		t.Fatalf("Distance(srv-1,srv-2): %v", err)
	}
	crossRack, err := m.Distance("srv-1", "srv-3")
	if err != nil {
		t.Fatalf("Distance(srv-1,srv-3): %v", err)
	}
	// Distance counts levels climbed to the common ancestor, so SMALLER is
	// nearer and a node is distance 0 from itself.
	if sameRack >= crossRack {
		t.Errorf("same-rack distance %d is not nearer than cross-rack %d "+
			"(smaller must be nearer)", sameRack, crossRack)
	}
	if sameRack != 1 {
		t.Errorf("Distance(srv-1,srv-2) = %d, want 1 (one hop up to the shared rack)", sameRack)
	}
	if crossRack != 2 {
		t.Errorf("Distance(srv-1,srv-3) = %d, want 2 (up through rack to the shared datacenter)", crossRack)
	}

	back, err := m.Distance("srv-3", "srv-1")
	if err != nil {
		t.Fatalf("Distance(srv-3,srv-1): %v", err)
	}
	if back != crossRack {
		t.Errorf("Distance is not symmetric: %d then %d", crossRack, back)
	}
	self, err := m.Distance("srv-1", "srv-1")
	if err != nil {
		t.Fatalf("Distance(srv-1,srv-1): %v", err)
	}
	if self != 0 {
		t.Errorf("Distance(srv-1,srv-1) = %d, want 0", self)
	}

	// The failure-domain question is AncestorAtLevel's, not Distance's: the
	// common ancestor of two same-rack servers is that rack.
	anc, err := m.AncestorAtLevel("srv-1", m.LevelIndex("rack"))
	if err != nil {
		t.Fatalf("AncestorAtLevel(srv-1, rack): %v", err)
	}
	if anc.Name != "rack-a" {
		t.Errorf("AncestorAtLevel(srv-1, rack) = %q, want %q", anc.Name, "rack-a")
	}
}

// TestAncestorAtLevelIsIntervalLookup checks the primitive a durability rule
// uses: two servers in one rack share an ancestor at level rack, two in
// different racks do not.
func TestAncestorAtLevelIsIntervalLookup(t *testing.T) {
	m := loadFixture(t)
	rack := m.LevelIndex("rack")

	a, err := m.AncestorAtLevel("srv-1", rack)
	if err != nil {
		t.Fatalf("AncestorAtLevel(srv-1, rack): %v", err)
	}
	b, err := m.AncestorAtLevel("srv-2", rack)
	if err != nil {
		t.Fatalf("AncestorAtLevel(srv-2, rack): %v", err)
	}
	c, err := m.AncestorAtLevel("srv-3", rack)
	if err != nil {
		t.Fatalf("AncestorAtLevel(srv-3, rack): %v", err)
	}
	if a.Name != b.Name {
		t.Errorf("srv-1 and srv-2 are in one rack but resolved to %q and %q", a.Name, b.Name)
	}
	if a.Name == c.Name {
		t.Errorf("srv-1 and srv-3 are in different racks but both resolved to %q", a.Name)
	}

	// A level deeper than the node itself has no ancestor there.
	disk := m.LevelIndex("disk")
	if _, err := m.AncestorAtLevel("srv-1", disk); !errors.Is(err, ErrNoAncestorAtLevel) {
		t.Errorf("AncestorAtLevel(srv-1, disk) error = %v, want ErrNoAncestorAtLevel", err)
	}
	if _, err := m.AncestorAtLevel("no-such-server", rack); !errors.Is(err, ErrUnknownNode) {
		t.Errorf("AncestorAtLevel(unknown) error = %v, want ErrUnknownNode", err)
	}
}

// TestAncestorAtLevelRefusesWhenLevelIsSkipped covers the case the fixture
// cannot: a node that has NO ancestor at the requested level.
//
// Load requires a child to be strictly deeper than its parent, not adjacent to
// it, so a server may sit directly under a datacenter with no rack between them.
// AncestorAtLevel finds its candidate by binary search on Lft and must then
// check the interval actually encloses the node — without that second half it
// returns whichever rack happens to start earlier, which is a confidently wrong
// failure-domain answer rather than an error.
//
// This test exists because a mutation dropping the right bound from
// Node.Contains SURVIVED the rest of the suite: every server in the fixture is
// inside a rack, so nothing discriminated. Recorded 2026-09-04.
func TestAncestorAtLevelRefusesWhenLevelIsSkipped(t *testing.T) {
	const skipped = `{
	  "version":1,"depth":1,
	  "levels":["datacenter","rack","server"],
	  "root":{"level":"datacenter","name":"dc","children":[
	    {"level":"rack","name":"rk","children":[{"level":"server","name":"in-rack"}]},
	    {"level":"server","name":"no-rack"}
	  ]}
	}`
	m, err := loadString(t, skipped)
	if err != nil {
		t.Fatalf("Load(skipped level): %v", err)
	}
	rack := m.LevelIndex("rack")

	// The server that skips the rack level has no ancestor there.
	if got, err := m.AncestorAtLevel("no-rack", rack); !errors.Is(err, ErrNoAncestorAtLevel) {
		t.Errorf("AncestorAtLevel(no-rack, rack) = %q, %v; want ErrNoAncestorAtLevel — "+
			"a node outside every rack must not be reported as inside one", got.Name, err)
	}
	// And the one that does not skip it still resolves.
	anc, err := m.AncestorAtLevel("in-rack", rack)
	if err != nil {
		t.Fatalf("AncestorAtLevel(in-rack, rack): %v", err)
	}
	if anc.Name != "rk" {
		t.Errorf("AncestorAtLevel(in-rack, rack) = %q, want %q", anc.Name, "rk")
	}
}

// TestNestedAndIntervalFormsRoundTrip is the falsifier for the two-representation
// defect: a value flowing through an authored nested form and a resident
// interval form, where a check exercising only one path cannot see the other
// silently narrowing. It is a property test over generated trees rather than a
// fixture, because a hand-written fixture encodes what its author expected and
// so cannot falsify the expectation.
func TestNestedAndIntervalFormsRoundTrip(t *testing.T) {
	const seed = 20260904
	rng := rand.New(rand.NewSource(seed))
	levels := []string{"l0", "l1", "l2", "l3"}

	for i := 0; i < 200; i++ {
		var counter int
		authored := genTree(rng, levels, 0, &counter)
		src, err := json.Marshal(authoredMap{Version: FormatVersion, Depth: 1, Levels: levels, Root: authored})
		if err != nil {
			t.Fatalf("seed %d case %d: marshal: %v", seed, i, err)
		}
		m, err := Load(strings.NewReader(string(src)))
		if err != nil {
			t.Fatalf("seed %d case %d: Load: %v\n%s", seed, i, err, src)
		}
		if got := m.Tree(); !reflect.DeepEqual(got, authored) {
			t.Fatalf("seed %d case %d: round trip is not the identity\n got: %+v\nwant: %+v",
				seed, i, got, authored)
		}
	}
}

// genTree builds a random authored tree whose children are always strictly
// deeper than their parent.
func genTree(rng *rand.Rand, levels []string, level int, counter *int) AuthoredNode {
	*counter++
	n := AuthoredNode{
		Level:  levels[level],
		Name:   "n" + itoa(*counter),
		Weight: rng.Intn(100) + 1,
	}
	if level == len(levels)-1 {
		return n
	}
	for k := rng.Intn(3); k > 0; k-- {
		n.Children = append(n.Children, genTree(rng, levels, level+1, counter))
	}
	return n
}

// TestMapDeclaresNoObjectLocations checks the bound that keeps the map small
// enough to ship to every client: it declares cluster SHAPE and never the
// location of an individual key, object, datom or segment.
func TestMapDeclaresNoObjectLocations(t *testing.T) {
	forbidden := []string{"key", "object", "segment", "datom", "entity", "leaf", "location", "placement"}
	for _, typ := range []reflect.Type{reflect.TypeOf(Map{}), reflect.TypeOf(Node{}), reflect.TypeOf(AuthoredNode{})} {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, bad := range forbidden {
				if strings.Contains(name, bad) {
					t.Errorf("%s has field %q: the topology map must declare shape only, "+
						"never per-object location", typ.Name(), typ.Field(i).Name)
				}
			}
		}
	}
}

// TestLoadFixtureRoundTrips checks the shipped fixture decodes to the hierarchy
// it declares, with every level label and name preserved.
func TestLoadFixtureRoundTrips(t *testing.T) {
	m := loadFixture(t)
	// The shipped fixture is the one `cmd/sdev1-addr` places against, so it
	// carries a generation and must report itself placeable.
	if !m.Placeable() {
		t.Error("the shipped fixture carries no generation, so nothing can place against it")
	}
	if m.FormatVersion != FormatVersion {
		t.Errorf("Version = %d, want %d", m.FormatVersion, FormatVersion)
	}
	if m.Depth != 1 {
		t.Errorf("Depth = %d, want 1", m.Depth)
	}
	want := []string{"universe", "planet", "datacenter", "rack", "server", "disk"}
	if !reflect.DeepEqual(m.Levels, want) {
		t.Errorf("Levels = %v, want %v", m.Levels, want)
	}
	for _, name := range []string{"u", "earth", "dc-1", "rack-a", "rack-b", "srv-1", "srv-2", "srv-3", "srv-1-d0"} {
		if _, err := m.Node(name); err != nil {
			t.Errorf("fixture is missing node %q: %v", name, err)
		}
	}
}

// itoa avoids a strconv import in the table-driven sources above.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
