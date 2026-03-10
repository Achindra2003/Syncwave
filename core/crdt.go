package core

import (
	"strings"
	"sync"
)

// OpID uniquely identifies every character in the document.
type OpID struct {
	Clock  int64  `json:"clock"`
	SiteID string `json:"siteID"`
}

// IsZero returns true for the zero-value OpID.
func (id OpID) IsZero() bool {
	return id.Clock == 0 && id.SiteID == ""
}

// RootID is the sentinel anchor for the beginning of the document.
var RootID = OpID{Clock: 0, SiteID: "ROOT"}

// Node is a single character in the CRDT linked list.
type Node struct {
	ID        OpID
	Char      rune
	IsDeleted bool
	Next      *Node
	Prev      *Node
}

// Document is the RGA (Replicated Growable Array) CRDT.
type Document struct {
	Head *Node
	Tail *Node
	Map  map[int64]map[string]*Node // Clock -> SiteID -> Node (fast lookup)
	Mu   sync.Mutex
}

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
// Caller must hold d.Mu OR guarantee exclusive access (e.g. via Hub mutex).
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

// Insert places a character after the node with anchorID.
// This is the primary insert API — uses OpID-based addressing for CRDT correctness.
func (d *Document) Insert(char rune, anchorID OpID, newID OpID) {
	d.Mu.Lock()
	defer d.Mu.Unlock()

	if d.findNode(newID) != nil {
		return // Already applied (idempotent)
	}

	anchor := d.findNode(anchorID)
	if anchor == nil {
		return // Anchor missing
	}

	newNode := &Node{
		ID:   newID,
		Char: char,
	}

	// RGA tie-breaking: skip over nodes with higher priority
	cursor := anchor
	for cursor.Next != nil && d.isHigherPriority(cursor.Next.ID, newID) {
		cursor = cursor.Next
	}

	// Splice in
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

func (d *Document) isHigherPriority(a, b OpID) bool {
	if a.Clock > b.Clock {
		return true
	}
	if a.Clock == b.Clock {
		return a.SiteID > b.SiteID
	}
	return false
}

// Delete marks a character as invisible (tombstone).
func (d *Document) Delete(targetID OpID) {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	if n := d.findNode(targetID); n != nil {
		n.IsDeleted = true
	}
}

// String renders visible text.
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

// Len returns visible character count.
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
// Returns RootID if pos < 0 or the document is empty.
// Caller should NOT hold the lock.
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
	// pos beyond end — return last visible node's ID, or ROOT
	return d.lastVisibleID()
}

// GetAnchorIDForPos returns the OpID of the character just before visible position pos.
// For pos 0, returns RootID. For pos > 0, returns the ID of the char at pos-1.
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

func (d *Document) lastVisibleID() OpID {
	cur := d.Tail
	for cur != nil && cur != d.Head {
		if !cur.IsDeleted {
			return cur.ID
		}
		cur = cur.Prev
	}
	return RootID
}
