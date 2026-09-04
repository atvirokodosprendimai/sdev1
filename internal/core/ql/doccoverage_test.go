package ql

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docPath is the guide this package's exported surface must be documented in.
const docPath = "../../../docs/QUERY-LANGUAGE.md"

// TestQueryLanguageDocIsComplete fails when an exported identifier in this
// package is absent from the query-language guide.
//
// ★ A public language is its documentation. Its caller cannot read the source,
// and for the agent surface built on top of it the caller cannot read anything
// at all — so an undocumented export is a feature that effectively does not
// exist. This makes falling behind a test failure rather than a discovery.
//
// ⚠ It searches only CODE SPANS — fenced blocks and `backticked` names — never
// prose. An earlier guard in this repository grepped raw source text for a
// symbol and matched the comment explaining why not to use it; a check that
// fires on prose gets switched off, and then it protects nothing. Requiring a
// code span also means a passing mention has to look like the identifier rather
// than merely contain the word.
func TestQueryLanguageDocIsComplete(t *testing.T) {
	exported := exportedTopLevelNames(t)
	if len(exported) == 0 {
		t.Fatal("found no exported identifiers to check; the parser or the file filter is wrong, " +
			"and a coverage test that checks nothing passes for the wrong reason")
	}

	documented := codeSpans(t, docPath)

	var missing []string
	for _, name := range exported {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(documented) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d exported identifier(s) are not documented in %s:\n  %s\n\n"+
			"Document each in a code block or as a `backticked` name. This package is a public "+
			"language: an export its callers cannot read about is a feature that does not exist.",
			len(missing), docPath, strings.Join(missing, "\n  "))
	}

	// ⚠ Positive control. Without it, a bug that emptied `documented` would make
	// every lookup fail — but a bug that made every lookup SUCCEED would pass
	// silently, which is the direction that matters.
	if regexp.MustCompile(`\bNoSuchIdentifierInTheGuide\b`).MatchString(documented) {
		t.Fatal("the extracted code spans match an identifier that appears nowhere; " +
			"the extraction is returning something other than the document")
	}
}

// exportedTopLevelNames returns every exported type, function, constant and
// variable declared at the top level of this package.
//
// Methods are deliberately excluded: a method is documented with its type, and
// requiring each one by name would push the guide towards listing signatures
// instead of explaining them.
func exportedTopLevelNames(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := goparser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}

	seen := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.IsExported() {
						seen[d.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if s.Name.IsExported() {
								seen[s.Name.Name] = true
							}
						case *ast.ValueSpec:
							for _, name := range s.Names {
								if name.IsExported() {
									seen[name.Name] = true
								}
							}
						}
					}
				}
			}
		}
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// inlineCode matches a `backticked` span on one line.
var inlineCode = regexp.MustCompile("`([^`\n]+)`")

// codeSpans returns the concatenated code content of a markdown file: every
// fenced block, plus every inline backticked span outside one.
func codeSpans(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
			b.WriteString(m[1])
			b.WriteByte('\n')
		}
	}
	if inFence {
		t.Fatalf("%s has an unclosed code fence; the extraction cannot be trusted", path)
	}
	return b.String()
}
