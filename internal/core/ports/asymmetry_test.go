package ports

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writablePorts are the identifiers that grant the ability to write. A package
// naming one of these can write, whatever its intent.
var writablePorts = []string{"ports.Writer", "ports.Store"}

// exemptions are the packages permitted to name a writable port, each with the
// reason it is permitted.
//
// ★ An exemption is a DECISION, so it is written down with its reason rather
// than inferred from a path pattern. A pattern silently exempts whatever grows
// to match it; a list does not.
//
// ⚠ A long list means the asymmetry has stopped being structural and become
// paperwork. Two or three entries is the healthy size; if it grows, reconsider
// the boundary rather than adding another row.
var exemptions = map[string]string{
	"internal/core/ports": "declares the ports, so it necessarily names them",
	"internal/core/leafstore": "IMPLEMENTS the write port rather than consuming one — it is the " +
		"storage engine on the other side of the boundary, and its compile-time assertion that a " +
		"leaf satisfies ports.Store is what proves a read model can be handed one safely",
}

// scanRoot is the tree the guard covers.
const scanRoot = "../../.."

// TestNoReadPackageDependsOnWriter fails when a package outside the exemption
// list names a writable port.
//
// The rule this enforces is ADR-003's: a read model is handed a Reader and
// therefore cannot write. That holds at the call site; this holds it at the
// package level, so a read model cannot acquire a writable port by importing
// one and constructing it itself.
func TestNoReadPackageDependsOnWriter(t *testing.T) {
	offenders := findOffenders(scanGoFiles(t))
	if len(offenders) > 0 {
		t.Errorf("these files name a writable port without an exemption:\n  %s\n"+
			"A read model is handed a Reader so that writing is not expressible. If one of these "+
			"genuinely belongs to the write path, add it to the exemption list WITH ITS REASON.",
			strings.Join(offenders, "\n  "))
	}
}

// TestExemptionListIsExhaustive checks every exemption names a package that
// exists and carries a reason, so a stale entry cannot silently widen the
// guard's blind spot after a rename or a deletion.
func TestExemptionListIsExhaustive(t *testing.T) {
	for pkg, reason := range exemptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exemption %q has no reason; an exemption is a decision and must say why", pkg)
		}
		info, err := os.Stat(filepath.Join(scanRoot, pkg))
		if err != nil || !info.IsDir() {
			t.Errorf("exemption %q names no package that exists (%v) — a stale exemption widens "+
				"the blind spot without anyone noticing", pkg, err)
		}
	}
}

// TestGuardScansEveryPackage is the assertion that separates "the guard found
// nothing" from "the guard looked at nothing".
//
// ★ A source-scanning guard's characteristic failure is a walk whose universe is
// empty or wrongly rooted: it reports clean, forever, about nothing, and reads
// exactly like a guard that passed. This is the only thing that tells the two
// apart.
func TestGuardScansEveryPackage(t *testing.T) {
	files := scanGoFiles(t)
	if len(files) < 10 {
		t.Fatalf("the guard scanned only %d Go files; the walk is almost certainly rooted wrong, "+
			"and a guard that looks at nothing reports clean forever", len(files))
	}

	// It must reach beyond its own package, or it is guarding only itself.
	seen := make(map[string]bool)
	for _, f := range files {
		seen[packageDirOf(f.path)] = true
	}
	if len(seen) < 3 {
		t.Errorf("the guard reached only %d packages (%v); it must cover the tree, not its own directory",
			len(seen), keysOf(seen))
	}
	if !seen["internal/core/command"] {
		t.Error("the guard did not reach internal/core/command, which is the package most likely " +
			"to acquire a writable port first")
	}
}

// findOffenders is the guard's logic, extracted so it can be run against a
// known-bad input as well as against the real tree.
func findOffenders(files []goFile) []string {
	var offenders []string
	for _, f := range files {
		pkg := packageDirOf(f.path)
		if _, exempt := exemptions[pkg]; exempt {
			continue
		}
		for _, ident := range writablePorts {
			if strings.Contains(f.src, ident) {
				offenders = append(offenders, f.path+" names "+ident)
			}
		}
	}
	return offenders
}

// TestGuardFlagsAKnownOffender is the POSITIVE CONTROL, and without it the rest
// of this guard proves nothing.
//
// ★ The tree currently contains no offender, so every assertion above passes
// whether the guard works or not — including if the exemption check were
// widened to exempt everything. A negative assertion with no positive case is
// unfalsifiable: it cannot tell "nothing is wrong" from "nothing is being
// checked". This runs the same logic against a file that SHOULD be flagged, and
// against one that should not.
func TestGuardFlagsAKnownOffender(t *testing.T) {
	bad := goFile{
		path: "internal/core/somereadmodel/projection.go",
		src:  "package somereadmodel\n\nfunc build(s ports.Store) {}\n",
	}
	if got := findOffenders([]goFile{bad}); len(got) != 1 {
		t.Fatalf("the guard flagged %d offenders in a file that names ports.Store, want 1 — "+
			"the guard does not detect what it exists to detect", len(got))
	}

	exempt := goFile{
		path: "internal/core/ports/ports.go",
		src:  "package ports\n\ntype Store interface{ Reader; Writer }\n// ports.Store\n",
	}
	if got := findOffenders([]goFile{exempt}); len(got) != 0 {
		t.Errorf("the guard flagged the declaring package itself: %v", got)
	}

	clean := goFile{
		path: "internal/core/somereadmodel/reader.go",
		src:  "package somereadmodel\n\nfunc build(r ports.Reader) {}\n",
	}
	if got := findOffenders([]goFile{clean}); len(got) != 0 {
		t.Errorf("the guard flagged a file that names only ports.Reader: %v", got)
	}
}

type goFile struct {
	path string
	src  string
}

// scanGoFiles walks the tree once and returns every Go file with its contents.
func scanGoFiles(t *testing.T) []goFile {
	t.Helper()
	var out []goFile
	root := filepath.Join(scanRoot, "internal")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, goFile{path: filepath.ToSlash(path), src: string(b)})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return out
}

// packageDirOf turns a scanned path into a module-relative package directory.
func packageDirOf(path string) string {
	dir := filepath.ToSlash(filepath.Dir(path))
	if i := strings.Index(dir, "internal/"); i >= 0 {
		return dir[i:]
	}
	return dir
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
