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
		ID:        OpID{Clock: 0, SiteID: "ROOT"},
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

func (d *Document) findNode(id OpID) *Node {
	if m, ok := d.Map[id.Clock]; ok {
		if n, ok := m[id.SiteID]; ok {
			return n
		}
	}
	return nil
}

// Insert places a character after the node with anchorID.
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
