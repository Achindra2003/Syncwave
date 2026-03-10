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

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512 * 1024

	// Send channel buffer size per client.
	sendBufSize = 256
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Message is the JSON protocol.
type Message struct {
	Type     string      `json:"type"`
	Char     int         `json:"char,omitempty"`
	Position int         `json:"position"`
	UserID   string      `json:"userID,omitempty"`
	UserName string      `json:"userName,omitempty"`
	Color    string      `json:"color,omitempty"`
	Content  string      `json:"content,omitempty"`
	Users    []User      `json:"users,omitempty"`
	Seq      int         `json:"seq,omitempty"`
	LastSeq  int         `json:"lastSeq,omitempty"`
	AnchorID *core.OpID  `json:"anchorID,omitempty"` // Insert: character to insert after
	TargetID *core.OpID  `json:"targetID,omitempty"` // Delete: character to tombstone
	NewID    *core.OpID  `json:"newID,omitempty"`    // Server-assigned OpID for inserts
	NodeIDs  []core.OpID `json:"nodeIDs,omitempty"`  // full_sync: ordered visible node IDs
}

type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// ClientConn wraps a WebSocket connection with a buffered send channel.
// All writes go through the send channel and are serialized by writePump.
type ClientConn struct {
	hub  *Hub
	conn *websocket.Conn
	user User
	send chan []byte
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
		users = append(users, cc.user)
	}
	return users
}

// safeSend pushes a message to a client's send channel without blocking.
// Returns false if the channel is full (client too slow / dead).
func (cc *ClientConn) safeSend(msg []byte) bool {
	select {
	case cc.send <- msg:
		return true
	default:
		return false
	}
}

func (h *Hub) broadcastPresence() {
	users := h.getUsers()
	msg := Message{Type: "presence", Users: users}
	raw, _ := json.Marshal(msg)
	for _, cc := range h.clients {
		cc.safeSend(raw)
	}
}

func (h *Hub) broadcastExcept(raw []byte, sender *websocket.Conn) {
	for ws, cc := range h.clients {
		if ws == sender {
			continue
		}
		cc.safeSend(raw)
	}
}

// broadcastAll sends to ALL clients including sender.
func (h *Hub) broadcastAll(raw []byte) {
	for _, cc := range h.clients {
		cc.safeSend(raw)
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

// writePump pumps messages from the send channel to the WebSocket connection.
// A single goroutine runs writePump for each connection, ensuring all writes
// are serialized (gorilla/websocket does not support concurrent writers).
// It also sends periodic pings to keep the connection alive through proxies.
func (cc *ClientConn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		cc.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-cc.send:
			cc.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel — send a close frame and exit.
				cc.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := cc.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			cc.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := cc.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump reads messages from the WebSocket connection and dispatches them.
// It runs in the ServeWS goroutine (one per connection).
func (cc *ClientConn) readPump() {
	h := cc.hub
	user := cc.user

	defer func() {
		h.mu.Lock()
		delete(h.clients, cc.conn)
		fmt.Printf("[HUB] %s left (%d users)\n", user.Name, len(h.clients))
		h.broadcastPresence()
		h.mu.Unlock()
		close(cc.send) // signals writePump to exit
		cc.conn.Close()
	}()

	cc.conn.SetReadLimit(maxMessageSize)
	cc.conn.SetReadDeadline(time.Now().Add(pongWait))
	cc.conn.SetPongHandler(func(string) error {
		cc.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, raw, err := cc.conn.ReadMessage()
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
			syncRaw, _ := json.Marshal(syncMsg)
			cc.safeSend(syncRaw)

		case "batch_sync":
			// Client is replaying buffered offline ops
			var ops []Message
			if err := json.Unmarshal([]byte(msg.Content), &ops); err == nil {
				fmt.Printf("[HUB] %s replaying %d offline ops\n", user.Name, len(ops))
				for i := range ops {
					h.applyOp(&ops[i], user)
					outRaw, _ := json.Marshal(ops[i])
					// Confirm to sender so placeholder IDs get replaced
					cc.safeSend(outRaw)
					// Broadcast to other clients
					h.broadcastExcept(outRaw, cc.conn)
				}
				fmt.Printf("[HUB] %s batch complete, doc len=%d\n", user.Name, h.doc.Len())
			}

		case "insert", "delete":
			h.applyOp(&msg, user)
			outRaw, _ := json.Marshal(msg)
			// Send confirmation to sender (with server-assigned newID/targetID)
			cc.safeSend(outRaw)
			// Broadcast to all other clients
			h.broadcastExcept(outRaw, cc.conn)

		case "cursor":
			// Cursor messages: just broadcast to others, no CRDT mutation
			msg.UserID = user.ID
			msg.UserName = user.Name
			msg.Color = user.Color
			outRaw, _ := json.Marshal(msg)
			h.broadcastExcept(outRaw, cc.conn)
		}

		h.mu.Unlock()
	}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("[HUB] Upgrade error:", err)
		return
	}

	// Read join message before starting pumps
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

	cc := &ClientConn{
		hub:  h,
		conn: ws,
		user: user,
		send: make(chan []byte, sendBufSize),
	}
	h.clients[ws] = cc

	isReconnect := joinMsg.LastSeq > 0

	if isReconnect {
		// RECONNECT: Send full doc + missed ops via channel
		fmt.Printf("[HUB] %s reconnected (lastSeq=%d, current=%d)\n", user.Name, joinMsg.LastSeq, h.seqNum)

		syncMsg := h.buildFullSync(user.Color)
		syncRaw, _ := json.Marshal(syncMsg)
		cc.safeSend(syncRaw)

		// Then send missed ops so client can see what others did
		missedCount := 0
		for _, entry := range h.opLog {
			if entry.Seq > joinMsg.LastSeq && entry.Msg.UserID != user.ID {
				raw, _ := json.Marshal(entry.Msg)
				cc.safeSend(raw)
				missedCount++
			}
		}
		fmt.Printf("[HUB] Sent %d missed ops to %s\n", missedCount, user.Name)
	} else {
		// FIRST CONNECT: Send full document via channel
		syncMsg := h.buildFullSync(user.Color)
		syncRaw, _ := json.Marshal(syncMsg)
		cc.safeSend(syncRaw)
		fmt.Printf("[HUB] %s joined (%d users)\n", user.Name, len(h.clients))
	}

	h.broadcastPresence()
	h.mu.Unlock()

	// Start the write pump in a separate goroutine — it handles
	// all writes including pings, keeping the connection alive.
	go cc.writePump()

	// Read pump runs in this goroutine (blocks until connection closes)
	cc.readPump()
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
