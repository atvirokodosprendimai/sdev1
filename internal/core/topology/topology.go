package topology

import (
	"errors"
	"fmt"
)

// FormatVersion is the map format this build understands. A map declaring any
// other version is refused rather than partially read.
const FormatVersion = 1

// MaxDepth bounds the address trie's live depth. It matches the key width in
// package addr: a descent cannot consume more bytes than a key has.
const MaxDepth = 32

var (
	// ErrUnknownVersion reports a map written by a different release.
	ErrUnknownVersion = errors.New("topology: unknown format version")

	// ErrDepthOutOfRange reports a live depth outside 1..[MaxDepth]. It is
	// refused on load so an impossible depth can never reach a descent.
	ErrDepthOutOfRange = errors.New("topology: depth out of range")

	// ErrEmptyLevels reports a map that declares no level labels.
	ErrEmptyLevels = errors.New("topology: no levels declared")

	// ErrUndeclaredLevel reports a node whose level label is absent from the
	// declared list. Levels are data, so this is the check that turns a typo
	// into a refusal rather than a misplaced node.
	ErrUndeclaredLevel = errors.New("topology: node at undeclared level")

	// ErrLevelNotDeeper reports a child whose level is not strictly deeper than
	// its parent's, which would break the nesting every interval comparison
	// rests on.
	ErrLevelNotDeeper = errors.New("topology: child level is not deeper than its parent")

	// ErrDuplicateName reports a name used twice. A name is how every other
	// package addresses a node, so names are unique across the whole map.
	ErrDuplicateName = errors.New("topology: duplicate node name")

	// ErrUnknownNode reports a name this map does not declare.
	ErrUnknownNode = errors.New("topology: unknown node")

	// ErrNoAncestorAtLevel reports that a node has no ancestor at the requested
	// level, which happens when the level is deeper than the node itself.
	ErrNoAncestorAtLevel = errors.New("topology: no ancestor at that level")
)

// AuthoredNode is the nested form of a node, as written in a map file. It is
// what a person reads and edits; [Node] is what the process holds.
type AuthoredNode struct {
	Level    string         `json:"level"`
	Name     string         `json:"name"`
	Weight   int            `json:"weight,omitempty"`
	Children []AuthoredNode `json:"children,omitempty"`
}

// authoredMap is the on-disk shape of a whole map.
type authoredMap struct {
	Version int          `json:"version"`
	Depth   uint8        `json:"depth"`
	Levels  []string     `json:"levels"`
	Root    AuthoredNode `json:"root"`
}

// Node is the resident form of a node: a name, the index of its level, and the
// nested-set interval that places it in the tree.
//
// Only Lft and Rgt encode structure. There are no parent or child pointers,
// which is what lets the whole map be a flat slice.
type Node struct {
	Name     string
	LevelIdx int
	Lft      int
	Rgt      int
	Weight   int
}

// Contains reports whether other lies within n's subtree. A node contains
// itself.
func (n Node) Contains(other Node) bool {
	return n.Lft <= other.Lft && other.Rgt <= n.Rgt
}

// Map is a cluster's declared shape.
//
// Nodes is flat, pointer-free and sorted ascending by Lft, so it is
// binary-searchable and cheap to hand to a client. The map declares shape only
// and never the location of an individual key, object or segment.
type Map struct {
	Version int
	Depth   uint8
	Levels  []string
	Nodes   []Node

	byName  map[string]int
	byLevel [][]int
}

// LevelIndex returns the index of a level label, or -1 when the map does not
// declare it. Smaller indexes are broader.
func (m Map) LevelIndex(label string) int {
	for i, l := range m.Levels {
		if l == label {
			return i
		}
	}
	return -1
}

// Node returns the node with the given name.
func (m Map) Node(name string) (Node, error) {
	i, ok := m.byName[name]
	if !ok {
		return Node{}, fmt.Errorf("%w: %q", ErrUnknownNode, name)
	}
	return m.Nodes[i], nil
}
