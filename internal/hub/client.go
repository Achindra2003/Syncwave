package hub

import (
	"encoding/json"
	"sync"
	"time"

	"syncwave/internal/crdt"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 30 * time.Second
	pingPeriod     = 20 * time.Second
	maxMessageSize = 512 * 1024
	sendBufSize    = 256
)

type ClientConn struct {
	hub       *Hub
	room      *Room
	conn      *websocket.Conn
	user      User
	send      chan []byte
	sendMu    sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func newClientConn(h *Hub, room *Room, conn *websocket.Conn, user User) *ClientConn {
	return &ClientConn{
		hub:  h,
		room: room,
		conn: conn,
		user: user,
		send: make(chan []byte, sendBufSize),
	}
}

func (cc *ClientConn) safeSend(msg []byte) bool {
	cc.sendMu.Lock()
	defer cc.sendMu.Unlock()
	if cc.closed {
		return false
	}

	select {
	case cc.send <- msg:
		return true
	default:
		return false
	}
}

func (cc *ClientConn) closeSend() {
	cc.closeOnce.Do(func() {
		cc.sendMu.Lock()
		defer cc.sendMu.Unlock()
		cc.closed = true
		close(cc.send)
	})
}

func (cc *ClientConn) writePump() {
	defer func() {
		if recovered := recover(); recovered != nil {
			cc.hub.logger.Error("writePump panic recovered", "user", cc.user.Name, "docID", cc.room.docID)
		}
		cc.conn.Close()
	}()

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-cc.send:
			cc.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = cc.conn.WriteMessage(websocket.CloseMessage, []byte{})
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

func (cc *ClientConn) readPump() {
	h := cc.hub
	room := cc.room
	user := cc.user
	docID := room.docID

	defer func() {
		if recover() != nil {
			h.logger.Error("readPump panic recovered", "user", user.Name, "docID", docID)
		}
		room.mu.Lock()
		delete(room.clients, cc.conn)
		h.logger.Info("user left", "name", user.Name, "users", len(room.clients), "docID", docID)
		room.broadcastPresence()
		room.mu.Unlock()

		h.removeRoomIfEmpty(docID, room)
		cc.closeSend()
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
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				h.logger.Warn("unexpected close", "user", user.Name, "error", err)
			}
			break
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		room.mu.Lock()
		cc.handleMessage(&msg, user)
		room.mu.Unlock()
	}
}

func (cc *ClientConn) handleMessage(msg *Message, user User) {
	h := cc.hub
	room := cc.room

	switch msg.Type {
	case "ping":
		pongRaw, _ := json.Marshal(Message{Type: "pong"})
		cc.safeSend(pongRaw)

	case "restore":
		if room.doc.Len() == 0 && len(msg.Content) > 0 {
			runes := []rune(msg.Content)
			for i, ch := range runes {
				room.clock++
				newID := crdt.OpID{Clock: room.clock, SiteID: user.ID}
				anchor := crdt.RootID
				if i > 0 {
					anchor = crdt.OpID{Clock: room.clock - 1, SiteID: user.ID}
				}
				room.doc.Insert(ch, anchor, newID)
			}
			h.logger.Info("document restored", "user", user.Name, "chars", room.doc.Len(), "docID", room.docID)
		}
		syncMsg := room.buildFullSync(user.ID, user.Color)
		syncRaw, _ := json.Marshal(syncMsg)
		cc.safeSend(syncRaw)

	case "batch_sync":
		var ops []Message
		if err := json.Unmarshal([]byte(msg.Content), &ops); err == nil {
			for i := range ops {
				if !room.applyOp(&ops[i], user, h.logger) {
					continue
				}
				outRaw, _ := json.Marshal(ops[i])
				cc.safeSend(outRaw)
				room.broadcastExcept(outRaw, cc.conn)
			}
		}

	case "insert", "delete":
		if !room.applyOp(msg, user, h.logger) {
			return
		}
		outRaw, _ := json.Marshal(msg)
		cc.safeSend(outRaw)
		room.broadcastExcept(outRaw, cc.conn)

	case "cursor":
		msg.UserID = user.ID
		msg.UserName = user.Name
		msg.Color = user.Color
		outRaw, _ := json.Marshal(msg)
		room.broadcastExcept(outRaw, cc.conn)

	case "typing":
		msg.UserID = user.ID
		msg.UserName = user.Name
		msg.Color = user.Color
		outRaw, _ := json.Marshal(msg)
		room.broadcastExcept(outRaw, cc.conn)
	}
}
