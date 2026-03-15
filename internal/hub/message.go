package hub

import "syncwave/internal/crdt"

// Message represents the WebSocket protocol message format.
// All client-server communication is serialized as JSON using this structure.
//
// Message types:
//   - "join"       — client → server on connect (carries userID, userName, lastSeq)
//   - "full_sync"  — server → client with complete document state
//   - "replay_sync" — server → client with ordered missed operations since lastSeq
//   - "insert"     — bidirectional character insertion
//   - "delete"     — bidirectional character deletion
//   - "cursor"     — client → server cursor position broadcast
//   - "presence"   — server → client active user list
//   - "batch_sync" — client → server replay of offline operations
//   - "restore"    — client → server document restore after server restart
//   - "ping/pong"  — application-level keepalive
type Message struct {
	Type            string      `json:"type"`
	Char            int         `json:"char,omitempty"`
	Position        int         `json:"position"`
	UserID          string      `json:"userID,omitempty"`
	UserName        string      `json:"userName,omitempty"`
	Color           string      `json:"color,omitempty"`
	Content         string      `json:"content,omitempty"`
	Users           []User      `json:"users,omitempty"`
	Seq             int         `json:"seq,omitempty"`
	LastSeq         int         `json:"lastSeq,omitempty"`
	HasOfflineEdits bool        `json:"hasOfflineEdits,omitempty"`
	SessionToken    string      `json:"sessionToken,omitempty"`
	Ops             []Message   `json:"ops,omitempty"`
	AnchorID        *crdt.OpID  `json:"anchorID,omitempty"` // insert: character to insert after
	TargetID        *crdt.OpID  `json:"targetID,omitempty"` // delete: character to tombstone
	NewID           *crdt.OpID  `json:"newID,omitempty"`    // server-assigned OpID for inserts
	NodeIDs         []crdt.OpID `json:"nodeIDs,omitempty"`  // full_sync: ordered visible node IDs
}

// User represents a connected collaborator.
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// LogEntry records an operation for replay during client reconnection.
type LogEntry struct {
	Seq int     `json:"seq"`
	Msg Message `json:"msg"`
}
