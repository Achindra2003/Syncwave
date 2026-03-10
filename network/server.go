package network

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"syncwave/core"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Message is the JSON protocol.
type Message struct {
	Type     string     `json:"type"`
	Char     int        `json:"char,omitempty"`
	Position int        `json:"position"`
	UserID   string     `json:"userID,omitempty"`
	UserName string     `json:"userName,omitempty"`
	Color    string     `json:"color,omitempty"`
	Content  string     `json:"content,omitempty"`
	Users    []User     `json:"users,omitempty"`
	Seq      int        `json:"seq,omitempty"`
	LastSeq  int        `json:"lastSeq,omitempty"`
	AnchorID *core.OpID `json:"anchorID,omitempty"` // Insert: character to insert after
	TargetID *core.OpID `json:"targetID,omitempty"` // Delete: character to tombstone
	NewID    *core.OpID `json:"newID,omitempty"`    // Server-assigned OpID for inserts
	NodeIDs  []core.OpID `json:"nodeIDs,omitempty"` // full_sync: ordered visible node IDs
}

type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type ClientConn struct {
	Conn *websocket.Conn
	User User
}

// LogEntry is a recorded operation for replay.
type LogEntry struct {
	Seq int     `json:"seq"`
	Msg Message `json:"msg"`
}

// Hub manages connections, the document, and the operation log.
type Hub struct {
	clients  map[*websocket.Conn]*ClientConn
	doc      *core.Document // RGA CRDT document
	clock    int64          // Lamport clock for generating OpIDs
	opLog    []LogEntry     // Ordered log of all operations
	seqNum   int            // Global sequence counter
	mu       sync.Mutex
	colors   []string
	colorIdx int
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[*websocket.Conn]*ClientConn),
		doc:     core.NewDocument(),
		clock:   0,
		opLog:   []LogEntry{},
		seqNum:  0,
		colors: []string{
			"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4",
			"#FFEAA7", "#DDA0DD", "#98D8C8", "#F7DC6F",
			"#BB8FCE", "#85C1E9", "#F0B27A", "#82E0AA",
		},
	}
}

func (h *Hub) assignColor() string {
	h.colorIdx = (h.colorIdx + 1) % len(h.colors)
	return h.colors[h.colorIdx]
}

func (h *Hub) getUsers() []User {
	users := []User{}
	for _, cc := range h.clients {
		users = append(users, cc.User)
	}
	return users
}

func (h *Hub) broadcastPresence() {
	users := h.getUsers()
	msg := Message{Type: "presence", Users: users}
	raw, _ := json.Marshal(msg)
	for ws := range h.clients {
		ws.WriteMessage(websocket.TextMessage, raw)
	}
}

func (h *Hub) broadcastExcept(raw []byte, sender *websocket.Conn) {
	for ws := range h.clients {
		if ws == sender {
			continue
		}
		if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
			ws.Close()
			delete(h.clients, ws)
		}
	}
}

// broadcastAll sends to ALL clients including sender.
func (h *Hub) broadcastAll(raw []byte) {
	for ws := range h.clients {
		if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
			ws.Close()
			delete(h.clients, ws)
		}
	}
}

const maxOpLogSize = 1000

// applyOp applies an operation to the CRDT document and logs it.
func (h *Hub) applyOp(msg *Message, user User) {
	switch msg.Type {
	case "insert":
		char := rune(msg.Char)
		pos := msg.Position
		docLen := h.doc.Len()
		if pos < 0 {
			pos = 0
		}
		if pos > docLen {
			pos = docLen
		}

		// Resolve anchor: use client-sent anchorID if valid, otherwise derive from position
		var anchorID core.OpID
		if msg.AnchorID != nil && msg.AnchorID.Clock >= 0 && h.doc.FindNode(*msg.AnchorID) != nil {
			anchorID = *msg.AnchorID
		} else {
			// Fallback: resolve anchor from position
			anchorID = h.doc.GetAnchorIDForPos(pos)
		}

		h.clock++
		newID := core.OpID{Clock: h.clock, SiteID: user.ID}
		h.doc.Insert(char, anchorID, newID)

		// Stamp the server-assigned newID and resolved position back onto the message
		msg.NewID = &newID
		msg.AnchorID = &anchorID
		msg.Position = h.doc.GetVisiblePosOfNode(newID)

		fmt.Printf("[DOC] %s inserted '%c' anchor={%d,%s} newID={%d,%s} pos=%d | len=%d\n",
			user.Name, char, anchorID.Clock, anchorID.SiteID,
			newID.Clock, newID.SiteID, msg.Position, h.doc.Len())

	case "delete":
		pos := msg.Position

		// Resolve target: use client-sent targetID if valid, otherwise derive from position
		var targetID core.OpID
		if msg.TargetID != nil && msg.TargetID.Clock >= 0 && h.doc.FindNode(*msg.TargetID) != nil {
			targetID = *msg.TargetID
		} else {
			// Fallback: resolve target from position
			targetID = h.doc.GetNodeIDAtVisiblePos(pos)
		}

		// Resolve visible position before deleting (for client cursor adjustment)
		resolvedPos := h.doc.GetVisiblePosOfNode(targetID)
		if resolvedPos >= 0 {
			msg.Position = resolvedPos
		}
		msg.TargetID = &targetID
		h.doc.Delete(targetID)

		fmt.Printf("[DOC] %s deleted targetID={%d,%s} pos=%d | len=%d\n",
			user.Name, targetID.Clock, targetID.SiteID, msg.Position, h.doc.Len())
	}

	// Log the operation
	h.seqNum++
	msg.Seq = h.seqNum
	msg.UserID = user.ID
	msg.UserName = user.Name
	msg.Color = user.Color
	h.opLog = append(h.opLog, LogEntry{Seq: h.seqNum, Msg: *msg})

	// Trim opLog to prevent unbounded growth
	if len(h.opLog) > maxOpLogSize {
		h.opLog = h.opLog[len(h.opLog)-maxOpLogSize:]
	}
}

// buildFullSync constructs a full_sync message including visible node IDs.
func (h *Hub) buildFullSync(color string) Message {
	return Message{
		Type:    "full_sync",
		Content: h.doc.String(),
		Color:   color,
		Seq:     h.seqNum,
		NodeIDs: h.doc.GetVisibleNodeIDs(),
	}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("[HUB] Upgrade error:", err)
		return
	}

	// Read join message
	var joinMsg Message
	if err := ws.ReadJSON(&joinMsg); err != nil {
		ws.Close()
		return
	}

	h.mu.Lock()

	user := User{
		ID:    joinMsg.UserID,
		Name:  joinMsg.UserName,
		Color: h.assignColor(),
	}
	h.clients[ws] = &ClientConn{Conn: ws, User: user}

	isReconnect := joinMsg.LastSeq > 0

	if isReconnect {
		// RECONNECT: Send full doc + missed ops
		fmt.Printf("[HUB] %s reconnected (lastSeq=%d, current=%d)\n", user.Name, joinMsg.LastSeq, h.seqNum)

		syncMsg := h.buildFullSync(user.Color)
		ws.WriteJSON(syncMsg)

		// Then send missed ops so client can see what others did
		missedCount := 0
		for _, entry := range h.opLog {
			if entry.Seq > joinMsg.LastSeq && entry.Msg.UserID != user.ID {
				raw, _ := json.Marshal(entry.Msg)
				ws.WriteMessage(websocket.TextMessage, raw)
				missedCount++
			}
		}
		fmt.Printf("[HUB] Sent %d missed ops to %s\n", missedCount, user.Name)
	} else {
		// FIRST CONNECT: Send full document
		syncMsg := h.buildFullSync(user.Color)
		ws.WriteJSON(syncMsg)
		fmt.Printf("[HUB] %s joined (%d users)\n", user.Name, len(h.clients))
	}

	h.broadcastPresence()
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, ws)
		fmt.Printf("[HUB] %s left (%d users)\n", user.Name, len(h.clients))
		h.broadcastPresence()
		h.mu.Unlock()
		ws.Close()
	}()

	// Message loop
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		h.mu.Lock()

		switch msg.Type {
		case "restore":
			// Client wants to restore the document after a server restart.
			// Only the first restorer populates the empty doc; others get the current state.
			if h.doc.Len() == 0 && len(msg.Content) > 0 {
				runes := []rune(msg.Content)
				for i, ch := range runes {
					h.clock++
					newID := core.OpID{Clock: h.clock, SiteID: user.ID}
					anchor := core.RootID
					if i > 0 {
						anchor = core.OpID{Clock: h.clock - 1, SiteID: user.ID}
					}
					h.doc.Insert(ch, anchor, newID)
				}
				fmt.Printf("[HUB] %s restored doc (%d chars)\n", user.Name, h.doc.Len())
			} else {
				fmt.Printf("[HUB] %s restore skipped (doc already has %d chars)\n", user.Name, h.doc.Len())
			}
			// Always respond with current state so client can merge
			syncMsg := h.buildFullSync(user.Color)
			ws.WriteJSON(syncMsg)

		case "batch_sync":
			// Client is replaying buffered offline ops
			var ops []Message
			if err := json.Unmarshal([]byte(msg.Content), &ops); err == nil {
				fmt.Printf("[HUB] %s replaying %d offline ops\n", user.Name, len(ops))
				for i := range ops {
					h.applyOp(&ops[i], user)
					outRaw, _ := json.Marshal(ops[i])
					// Confirm to sender (same as normal ops) so placeholder IDs get replaced
					ws.WriteMessage(websocket.TextMessage, outRaw)
					// Broadcast to other clients
					h.broadcastExcept(outRaw, ws)
				}
				fmt.Printf("[HUB] %s batch complete, doc len=%d\n", user.Name, h.doc.Len())
			}

		case "insert", "delete":
			h.applyOp(&msg, user)
			outRaw, _ := json.Marshal(msg)
			// Send confirmation to sender (with server-assigned newID/targetID)
			ws.WriteMessage(websocket.TextMessage, outRaw)
			// Broadcast to all other clients
			h.broadcastExcept(outRaw, ws)

		case "cursor":
			// Cursor messages: just broadcast to others, no CRDT mutation
			msg.UserID = user.ID
			msg.UserName = user.Name
			msg.Color = user.Color
			outRaw, _ := json.Marshal(msg)
			h.broadcastExcept(outRaw, ws)
		}

		h.mu.Unlock()
	}
}

func (h *Hub) ServeStats(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()

	stats := map[string]interface{}{
		"users":      len(h.clients),
		"docLength":  h.doc.Len(),
		"opLogSize":  len(h.opLog),
		"seqNum":     h.seqNum,
		"serverTime": time.Now().Format("15:04:05"),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
