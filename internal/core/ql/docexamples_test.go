package ql

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docsWithSQL are the published documents whose ```sql blocks must be real.
//
// ★ A worked example that does not parse is worse than no example: a reader
// copies it, gets an error, and cannot tell whether the language or their typing
// is at fault. This makes every published statement executable proof.
var docsWithSQL = []string{
	"../../../README.md",
	"../../../docs/QUERY-LANGUAGE.md",
}

// TestPublishedExamplesParse parses every statement in every ```sql block of the
// published documentation.
//
// ⚠ Examples deliberately marked as REFUSED are skipped by a trailing
// `-- refused` comment, and the test asserts they really are refused rather than
// merely ignoring them — an example that documents a refusal and quietly parses
// is teaching the opposite of what it says.
func TestPublishedExamplesParse(t *testing.T) {
	total := 0
	for _, path := range docsWithSQL {
		for _, block := range sqlBlocks(t, path) {
			for _, stmt := range statementsIn(block) {
				total++
				runExample(t, path, stmt)
			}
		}
	}
	if total == 0 {
		t.Fatal("found no SQL examples to check; the extraction is wrong, and a test that checks " +
			"nothing passes for the wrong reason")
	}
	t.Logf("checked %d published examples", total)
}

// runExample parses one documented statement, or asserts it is refused.
func runExample(t *testing.T, path, stmt string) {
	t.Helper()

	body, wantRefused := stripRefusalMarker(stmt)
	if strings.TrimSpace(body) == "" {
		return
	}

	var err error
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(body)), "WITH ") {
		// A storage policy is a clause, not a statement, and has its own entry
		// point until a write statement exists to carry it.
		_, err = ParsePolicyClause(body)
	} else {
		_, err = Parse(body)
	}

	switch {
	case wantRefused && err == nil:
		t.Errorf("%s documents this as REFUSED and it parsed:\n  %s", path, body)
	case !wantRefused && err != nil:
		t.Errorf("%s publishes an example that does not parse:\n  %s\n  %v", path, body, err)
	}
}

// stripRefusalMarker removes a trailing `-- refused …` comment and reports
// whether it was there.
func stripRefusalMarker(stmt string) (string, bool) {
	var kept []string
	refused := false
	for _, line := range strings.Split(stmt, "\n") {
		comment := strings.Index(line, "--")
		if comment >= 0 {
			if strings.Contains(strings.ToLower(line[comment:]), "refused") {
				refused = true
			}
			line = line[:comment]
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), refused
}

// statementsIn splits a block into statements, which are separated by blank
// lines, and drops comment-only chunks.
func statementsIn(block string) []string {
	var out []string
	for _, chunk := range strings.Split(block, "\n\n") {
		var lines []string
		for _, line := range strings.Split(chunk, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			continue
		}
		// A chunk that is only comments documents something rather than saying
		// it; there is nothing to parse.
		onlyComments := true
		for _, line := range lines {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				onlyComments = false
				break
			}
		}
		if onlyComments {
			continue
		}
		// Each remaining LINE is its own statement unless the block is indented
		// continuation, which the language uses for multi-line MATCH and SEARCH.
		out = append(out, splitByIndent(lines)...)
	}
	return out
}

// splitByIndent groups lines into statements: an unindented line starts one, and
// indented lines continue it.
func splitByIndent(lines []string) []string {
	var out []string
	var current []string
	for _, line := range lines {
		indented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		if !indented && len(current) > 0 {
			out = append(out, strings.Join(current, "\n"))
			current = nil
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		out = append(out, strings.Join(current, "\n"))
	}
	return out
}

// sqlBlocks returns the content of every ```sql fence in a markdown file.
func sqlBlocks(t *testing.T, path string) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var blocks []string
	var current []string
	inSQL := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !inSQL && trimmed == "```sql":
			inSQL = true
			current = nil
		case inSQL && strings.HasPrefix(trimmed, "```"):
			blocks = append(blocks, strings.Join(current, "\n"))
			inSQL = false
		case inSQL:
			current = append(current, line)
		}
	}
	if inSQL {
		t.Fatalf("%s has an unclosed ```sql fence", path)
	}
	return blocks
}
