// Package hub manages WebSocket connections, the shared CRDT document,
// and the operation log for a collaborative editing session.
//
// Architecture:
//   - One Hub instance manages all connected clients
//   - Each client has two goroutines: readPump (reads from WebSocket) and
//     writePump (writes to WebSocket). All writes are serialized through a
//     buffered channel to prevent concurrent write panics in gorilla/websocket.
//   - The Hub's mutex protects the shared CRDT document and client map.
package hub

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"syncwave/internal/crdt"

	"github.com/gorilla/websocket"
)

const maxOpLogSize = 1000

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Allow 30s for the upgrade handshake (Render cold starts can be slow)
	HandshakeTimeout: 30 * time.Second,
}

// Hub manages connections, the document, and the operation log.
type Hub struct {
	clients  map[*websocket.Conn]*ClientConn
	doc      *crdt.Document
	clock    int64
	opLog    []LogEntry
	seqNum   int
	mu       sync.Mutex
	logger   *slog.Logger
	colors   []string
	colorIdx int
}

// NewHub creates a new collaboration hub with an empty CRDT document.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients: make(map[*websocket.Conn]*ClientConn),
		doc:     crdt.NewDocument(),
		logger:  logger,
		colors: []string{
			"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4",
			"#FFEAA7", "#DDA0DD", "#98D8C8", "#F7DC6F",
			"#BB8FCE", "#85C1E9", "#F0B27A", "#82E0AA",
		},
	}
}

// Shutdown gracefully closes all client connections.
func (h *Hub) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, cc := range h.clients {
		cc.closeSend()
	}
	h.logger.Info("hub shut down", "clients_disconnected", len(h.clients))
}

func (h *Hub) assignColor() string {
	h.colorIdx = (h.colorIdx + 1) % len(h.colors)
	return h.colors[h.colorIdx]
}

func (h *Hub) getUsers() []User {
	users := make([]User, 0, len(h.clients))
	for _, cc := range h.clients {
		users = append(users, cc.user)
	}
	return users
}

// broadcastPresence sends the current user list to all clients.
func (h *Hub) broadcastPresence() {
	users := h.getUsers()
	msg := Message{Type: "presence", Users: users}
	raw, _ := json.Marshal(msg)
	for _, cc := range h.clients {
		cc.safeSend(raw)
	}
}

// broadcastExcept sends a message to all clients except the sender.
func (h *Hub) broadcastExcept(raw []byte, sender *websocket.Conn) {
	for ws, cc := range h.clients {
		if ws == sender {
			continue
		}
		cc.safeSend(raw)
	}
}

// applyOp applies an insert or delete operation to the CRDT document and logs it.
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
		var anchorID crdt.OpID
		if msg.AnchorID != nil && msg.AnchorID.Clock >= 0 && h.doc.FindNode(*msg.AnchorID) != nil {
			anchorID = *msg.AnchorID
		} else {
			anchorID = h.doc.GetAnchorIDForPos(pos)
		}

		h.clock++
		newID := crdt.OpID{Clock: h.clock, SiteID: user.ID}
		h.doc.Insert(char, anchorID, newID)

		msg.NewID = &newID
		msg.AnchorID = &anchorID
		msg.Position = h.doc.GetVisiblePosOfNode(newID)

		h.logger.Debug("insert",
			"user", user.Name,
			"char", string(char),
			"pos", msg.Position,
			"docLen", h.doc.Len(),
		)

	case "delete":
		var targetID crdt.OpID
		if msg.TargetID != nil && msg.TargetID.Clock >= 0 && h.doc.FindNode(*msg.TargetID) != nil {
			targetID = *msg.TargetID
		} else {
			targetID = h.doc.GetNodeIDAtVisiblePos(msg.Position)
		}

		resolvedPos := h.doc.GetVisiblePosOfNode(targetID)
		if resolvedPos >= 0 {
			msg.Position = resolvedPos
		}
		msg.TargetID = &targetID
		h.doc.Delete(targetID)

		h.logger.Debug("delete",
			"user", user.Name,
			"pos", msg.Position,
			"docLen", h.doc.Len(),
		)
	}

	// Log the operation
	h.seqNum++
	msg.Seq = h.seqNum
	msg.UserID = user.ID
	msg.UserName = user.Name
	msg.Color = user.Color
	h.opLog = append(h.opLog, LogEntry{Seq: h.seqNum, Msg: *msg})

	if len(h.opLog) > maxOpLogSize {
		h.opLog = h.opLog[len(h.opLog)-maxOpLogSize:]
	}
}

// buildFullSync constructs a full_sync message with the complete document state.
func (h *Hub) buildFullSync(color string) Message {
	return Message{
		Type:    "full_sync",
		Content: h.doc.String(),
		Color:   color,
		Seq:     h.seqNum,
		NodeIDs: h.doc.GetVisibleNodeIDs(),
	}
}

// ServeWS handles a new WebSocket connection upgrade and starts the
// client's read/write pump goroutines.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("websocket upgrade request",
		"remoteAddr", r.RemoteAddr,
		"origin", r.Header.Get("Origin"),
		"host", r.Host,
	)

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed",
			"error", err,
			"remoteAddr", r.RemoteAddr,
		)
		// upgrader.Upgrade already wrote an HTTP error response
		return
	}

	h.logger.Info("websocket upgrade succeeded", "remoteAddr", r.RemoteAddr)

	// Set a timeout for the initial join message
	ws.SetReadDeadline(time.Now().Add(10 * time.Second))

	var joinMsg Message
	if err := ws.ReadJSON(&joinMsg); err != nil {
		h.logger.Warn("join message read failed", "error", err)
		ws.Close()
		return
	}

	// Clear the deadline — readPump will set its own
	ws.SetReadDeadline(time.Time{})

	h.mu.Lock()

	user := User{
		ID:    joinMsg.UserID,
		Name:  joinMsg.UserName,
		Color: h.assignColor(),
	}

	cc := newClientConn(h, ws, user)
	h.clients[ws] = cc

	if joinMsg.LastSeq > 0 {
		// Reconnect: send full doc + missed ops
		h.logger.Info("user reconnected",
			"name", user.Name,
			"lastSeq", joinMsg.LastSeq,
			"currentSeq", h.seqNum,
		)

		syncMsg := h.buildFullSync(user.Color)
		syncRaw, _ := json.Marshal(syncMsg)
		cc.safeSend(syncRaw)

		missed := 0
		for _, entry := range h.opLog {
			if entry.Seq > joinMsg.LastSeq && entry.Msg.UserID != user.ID {
				raw, _ := json.Marshal(entry.Msg)
				cc.safeSend(raw)
				missed++
			}
		}
		h.logger.Info("sent missed ops", "name", user.Name, "count", missed)
	} else {
		// First connect
		syncMsg := h.buildFullSync(user.Color)
		syncRaw, _ := json.Marshal(syncMsg)
		cc.safeSend(syncRaw)
		h.logger.Info("user joined", "name", user.Name, "users", len(h.clients))
	}

	h.broadcastPresence()
	h.mu.Unlock()

	// Start the write pump in a separate goroutine — handles all writes
	// including pings for keepalive.
	go cc.writePump()

	// Read pump runs in this goroutine (blocks until connection closes)
	cc.readPump()
}

// ServeStats returns a JSON snapshot of the hub's current state.
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
