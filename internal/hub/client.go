package hub

import (
	"encoding/json"
	"sync"
	"time"

	"syncwave/internal/crdt"

	"github.com/gorilla/websocket"
)

const (
	// writeWait is the time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// pongWait is the time allowed to read the next pong from the peer.
	// Set to 30s to stay within reverse-proxy idle timeouts (e.g., Render).
	pongWait = 30 * time.Second

	// pingPeriod sends pings at this interval. Must be less than pongWait.
	pingPeriod = 20 * time.Second

	// maxMessageSize is the maximum allowed incoming message size.
	maxMessageSize = 512 * 1024

	// sendBufSize is the per-client channel buffer for outgoing messages.
	sendBufSize = 256
)

// ClientConn wraps a WebSocket connection with a buffered send channel.
// All writes are serialized through the writePump goroutine to prevent
// concurrent write panics in gorilla/websocket.
type ClientConn struct {
	hub       *Hub
	conn      *websocket.Conn
	user      User
	send      chan []byte
	closeOnce sync.Once // ensures the send channel is closed exactly once
}

// newClientConn creates a new client connection wrapper.
func newClientConn(h *Hub, conn *websocket.Conn, user User) *ClientConn {
	return &ClientConn{
		hub:  h,
		conn: conn,
		user: user,
		send: make(chan []byte, sendBufSize),
	}
}

// safeSend pushes a message to the client's send channel without blocking.
// Returns false if the channel is full (client is too slow / dead).
func (cc *ClientConn) safeSend(msg []byte) bool {
	select {
	case cc.send <- msg:
		return true
	default:
		return false
	}
}

// closeSend closes the send channel exactly once, preventing double-close panics.
func (cc *ClientConn) closeSend() {
	cc.closeOnce.Do(func() {
		close(cc.send)
	})
}

// writePump pumps messages from the send channel to the WebSocket connection.
// A single goroutine runs writePump for each connection, ensuring all writes
// are serialized. It also sends periodic WebSocket pings to keep the
// connection alive through reverse proxies (e.g., Render, Cloudflare).
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
// It runs in its own goroutine (one per connection) started by ServeWS.
func (cc *ClientConn) readPump() {
	h := cc.hub
	user := cc.user

	defer func() {
		h.mu.Lock()
		delete(h.clients, cc.conn)
		h.logger.Info("user left", "name", user.Name, "users", len(h.clients))
		h.broadcastPresence()
		h.mu.Unlock()
		cc.closeSend() // signals writePump to exit
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
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
			) {
				h.logger.Warn("unexpected close", "user", user.Name, "error", err)
			}
			break
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		h.mu.Lock()
		cc.handleMessage(&msg, user)
		h.mu.Unlock()
	}
}

// handleMessage dispatches a decoded message. Caller must hold h.mu.
func (cc *ClientConn) handleMessage(msg *Message, user User) {
	h := cc.hub

	switch msg.Type {
	case "ping":
		// Application-level keepalive — respond with pong.
		pongRaw, _ := json.Marshal(Message{Type: "pong"})
		cc.safeSend(pongRaw)

	case "restore":
		// Client wants to restore the document after a server restart.
		// Only the first restorer populates the empty doc; others get the current state.
		if h.doc.Len() == 0 && len(msg.Content) > 0 {
			runes := []rune(msg.Content)
			for i, ch := range runes {
				h.clock++
				newID := crdt.OpID{Clock: h.clock, SiteID: user.ID}
				anchor := crdt.RootID
				if i > 0 {
					anchor = crdt.OpID{Clock: h.clock - 1, SiteID: user.ID}
				}
				h.doc.Insert(ch, anchor, newID)
			}
			h.logger.Info("document restored", "user", user.Name, "chars", h.doc.Len())
		} else {
			h.logger.Info("restore skipped (doc not empty)", "user", user.Name, "docLen", h.doc.Len())
		}
		syncMsg := h.buildFullSync(user.Color)
		syncRaw, _ := json.Marshal(syncMsg)
		cc.safeSend(syncRaw)

	case "batch_sync":
		// Client replaying buffered offline operations.
		var ops []Message
		if err := json.Unmarshal([]byte(msg.Content), &ops); err == nil {
			h.logger.Info("batch sync", "user", user.Name, "ops", len(ops))
			for i := range ops {
				h.applyOp(&ops[i], user)
				outRaw, _ := json.Marshal(ops[i])
				cc.safeSend(outRaw)
				h.broadcastExcept(outRaw, cc.conn)
			}
			h.logger.Info("batch complete", "user", user.Name, "docLen", h.doc.Len())
		}

	case "insert", "delete":
		h.applyOp(msg, user)
		outRaw, _ := json.Marshal(msg)
		cc.safeSend(outRaw)              // confirm to sender
		h.broadcastExcept(outRaw, cc.conn) // broadcast to others

	case "cursor":
		msg.UserID = user.ID
		msg.UserName = user.Name
		msg.Color = user.Color
		outRaw, _ := json.Marshal(msg)
		h.broadcastExcept(outRaw, cc.conn)

	case "typing":
		// Typing indicator broadcast — ephemeral, not logged.
		msg.UserID = user.ID
		msg.UserName = user.Name
		msg.Color = user.Color
		outRaw, _ := json.Marshal(msg)
		h.broadcastExcept(outRaw, cc.conn)
	}
}
