// Package crdt implements an RGA (Replicated Growable Array) CRDT
// for conflict-free collaborative text editing. The data structure is a
// doubly-linked list of character nodes, each identified by a unique OpID
// (Lamport clock + site ID). Concurrent inserts are resolved deterministically
// via RGA tie-breaking (higher clock wins; equal clocks break by site ID).
//
// All public methods are safe for concurrent use.
package crdt

import (
	"strings"
	"sync"
)

// OpID uniquely identifies every character ever inserted into the document.
// It combines a Lamport timestamp with the originating site's identifier.
type OpID struct {
	Clock  int64  `json:"clock"`
	SiteID string `json:"siteID"`
}

// IsZero returns true for the zero-value OpID.
func (id OpID) IsZero() bool {
	return id.Clock == 0 && id.SiteID == ""
}

// RootID is the sentinel anchor representing the start of the document.
// Every first character is inserted "after" this virtual root.
var RootID = OpID{Clock: 0, SiteID: "ROOT"}

// Node is a single character in the CRDT linked list.
type Node struct {
	ID        OpID
	Char      rune
	IsDeleted bool // tombstone flag — deleted nodes are kept for ordering
	Next      *Node
	Prev      *Node
}

// Document is the RGA CRDT — a doubly-linked list with fast OpID→Node lookup.
type Document struct {
	Head *Node
	Tail *Node
	Map  map[int64]map[string]*Node // Clock → SiteID → Node
	Mu   sync.Mutex
}

// NewDocument creates an empty document with only the sentinel root node.
func NewDocument() *Document {
	root := &Node{
		ID:        RootID,
		Char:      0,
		IsDeleted: true,
	}
	doc := &Document{
		Head: root,
		Tail: root,
		Map:  make(map[int64]map[string]*Node),
	}
	doc.registerNode(root)
	return doc
}

func (d *Document) registerNode(n *Node) {
	if d.Map[n.ID.Clock] == nil {
		d.Map[n.ID.Clock] = make(map[string]*Node)
	}
	d.Map[n.ID.Clock][n.ID.SiteID] = n
}

// findNode returns the node for the given OpID, or nil.
// Caller must hold d.Mu or guarantee exclusive access (e.g. via Hub mutex).
func (d *Document) findNode(id OpID) *Node {
	if m, ok := d.Map[id.Clock]; ok {
		if n, ok := m[id.SiteID]; ok {
			return n
		}
	}
	return nil
}

// FindNode returns the node for the given OpID (lock-safe for external callers).
func (d *Document) FindNode(id OpID) *Node {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	return d.findNode(id)
}

// Insert places a character after the node with anchorID using RGA ordering.
// The operation is idempotent — reinserting the same newID is a no-op.
func (d *Document) Insert(char rune, anchorID OpID, newID OpID) {
	d.Mu.Lock()
	defer d.Mu.Unlock()

	if d.findNode(newID) != nil {
		return // already applied (idempotent)
	}

	anchor := d.findNode(anchorID)
	if anchor == nil {
		return // anchor missing — drop the op
	}

	newNode := &Node{
		ID:   newID,
		Char: char,
	}

	// RGA tie-breaking: skip right while the next node has higher priority.
	cursor := anchor
	for cursor.Next != nil && d.isHigherPriority(cursor.Next.ID, newID) {
		cursor = cursor.Next
	}

	// Splice the new node in after cursor.
	newNode.Prev = cursor
	newNode.Next = cursor.Next
	if cursor.Next != nil {
		cursor.Next.Prev = newNode
	} else {
		d.Tail = newNode
	}
	cursor.Next = newNode

	d.registerNode(newNode)
}

// isHigherPriority returns true if a should sort before b in the list.
func (d *Document) isHigherPriority(a, b OpID) bool {
	if a.Clock > b.Clock {
		return true
	}
	if a.Clock == b.Clock {
		return a.SiteID > b.SiteID
	}
	return false
}

// Delete marks a character as invisible (tombstone deletion).
// The node is kept in the list to preserve ordering for other operations.
func (d *Document) Delete(targetID OpID) {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	if n := d.findNode(targetID); n != nil {
		n.IsDeleted = true
	}
}

// String renders the visible text of the document.
func (d *Document) String() string {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	var sb strings.Builder
	cur := d.Head.Next
	for cur != nil {
		if !cur.IsDeleted {
			sb.WriteRune(cur.Char)
		}
		cur = cur.Next
	}
	return sb.String()
}

// Len returns the number of visible (non-deleted) characters.
func (d *Document) Len() int {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	count := 0
	cur := d.Head.Next
	for cur != nil {
		if !cur.IsDeleted {
			count++
		}
		cur = cur.Next
	}
	return count
}

// GetNodeIDAtVisiblePos returns the OpID of the visible character at position pos (0-indexed).
// Returns RootID if pos is out of range.
func (d *Document) GetNodeIDAtVisiblePos(pos int) OpID {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	if pos < 0 {
		return RootID
	}
	count := 0
	cur := d.Head.Next
	for cur != nil {
		if !cur.IsDeleted {
			if count == pos {
				return cur.ID
			}
			count++
		}
		cur = cur.Next
	}
	return RootID
}

// GetAnchorIDForPos returns the OpID of the character just before visible position pos.
// For pos 0, returns RootID. For pos > 0, returns the ID at pos-1.
func (d *Document) GetAnchorIDForPos(pos int) OpID {
	if pos <= 0 {
		return RootID
	}
	return d.GetNodeIDAtVisiblePos(pos - 1)
}

// GetVisiblePosOfNode returns the visible index of the given node, or -1 if not found/deleted.
func (d *Document) GetVisiblePosOfNode(id OpID) int {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	count := 0
	cur := d.Head.Next
	for cur != nil {
		if !cur.IsDeleted {
			if cur.ID.Clock == id.Clock && cur.ID.SiteID == id.SiteID {
				return count
			}
			count++
		}
		cur = cur.Next
	}
	return -1
}

// GetVisibleNodeIDs returns all visible node OpIDs in document order.
// Used for full_sync so clients can rebuild their local CRDT shadow.
func (d *Document) GetVisibleNodeIDs() []OpID {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	ids := []OpID{}
	cur := d.Head.Next
	for cur != nil {
		if !cur.IsDeleted {
			ids = append(ids, cur.ID)
		}
		cur = cur.Next
	}
	return ids
}

