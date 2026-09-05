package topology

import (
	"encoding/json"
	"fmt"
	"io"
)

// Load reads a map in its authored nested form, validates it, and returns the
// resident interval form.
//
// Every refusal is a named sentinel rather than a default. A map is either
// wholly applied or wholly refused: partially reading a map whose shape this
// build does not understand would place data using a hierarchy nobody declared.
func Load(r io.Reader) (Map, error) {
	var am authoredMap
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&am); err != nil {
		return Map{}, fmt.Errorf("topology: decode: %w", err)
	}

	if am.Version != FormatVersion {
		return Map{}, fmt.Errorf("%w: %d, this build understands %d",
			ErrUnknownVersion, am.Version, FormatVersion)
	}
	if am.Depth < 1 || am.Depth > MaxDepth {
		return Map{}, fmt.Errorf("%w: %d not in 1..%d", ErrDepthOutOfRange, am.Depth, MaxDepth)
	}
	if len(am.Levels) == 0 {
		return Map{}, ErrEmptyLevels
	}

	levelOf := make(map[string]int, len(am.Levels))
	for i, l := range am.Levels {
		if _, dup := levelOf[l]; dup {
			return Map{}, fmt.Errorf("%w: level label %q declared twice", ErrUndeclaredLevel, l)
		}
		levelOf[l] = i
	}

	seen := make(map[string]bool)
	if err := validate(am.Root, -1, levelOf, seen); err != nil {
		return Map{}, err
	}

	nodes := number(am.Root, levelOf)

	// ⚠ Decoded, never GENERATED. A generation minted here would give the same
	// file a different identity in every process that read it, which is exactly
	// the failure a generation exists to fix.
	generation, err := decodeGeneration(am.Generation)
	if err != nil {
		return Map{}, err
	}

	m := Map{
		FormatVersion: am.Version,
		Generation:    generation,
		Depth:         am.Depth,
		Levels:        am.Levels,
		Nodes:         nodes,
		byName:        make(map[string]int, len(nodes)),
		byLevel:       make([][]int, len(am.Levels)),
	}
	for i, n := range nodes {
		m.byName[n.Name] = i
		m.byLevel[n.LevelIdx] = append(m.byLevel[n.LevelIdx], i)
	}
	return m, nil
}

// validate walks the authored tree checking the three structural rules: every
// level label is declared, every child is strictly deeper than its parent, and
// every name is unique across the whole map.
//
// parentLevel is -1 at the root, which has no parent to be deeper than.
func validate(a AuthoredNode, parentLevel int, levelOf map[string]int, seen map[string]bool) error {
	idx, ok := levelOf[a.Level]
	if !ok {
		return fmt.Errorf("%w: node %q declares level %q", ErrUndeclaredLevel, a.Name, a.Level)
	}
	if parentLevel >= 0 && idx <= parentLevel {
		return fmt.Errorf("%w: node %q at level %q is not deeper than its parent",
			ErrLevelNotDeeper, a.Name, a.Level)
	}
	if a.Name == "" {
		return fmt.Errorf("%w: a node at level %q has no name", ErrUnknownNode, a.Level)
	}
	if seen[a.Name] {
		return fmt.Errorf("%w: %q", ErrDuplicateName, a.Name)
	}
	seen[a.Name] = true
	for _, c := range a.Children {
		if err := validate(c, idx, levelOf, seen); err != nil {
			return err
		}
	}
	return nil
}
