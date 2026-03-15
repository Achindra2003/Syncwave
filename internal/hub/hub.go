package hub

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"syncwave/internal/crdt"

	"github.com/gorilla/websocket"
)

const maxOpLogSize = 1000

var upgrader = websocket.Upgrader{
	CheckOrigin:      func(r *http.Request) bool { return true },
	ReadBufferSize:   1024,
	WriteBufferSize:  1024,
	HandshakeTimeout: 30 * time.Second,
}

type Hub struct {
	rooms    map[string]*Room
	mu       sync.Mutex
	logger   *slog.Logger
	colors   []string
	colorIdx int
	userSeq  uint64

	strictOrigin   bool
	allowedOrigins map[string]struct{}
}

type Room struct {
	docID   string
	clients map[*websocket.Conn]*ClientConn
	doc     *crdt.Document
	clock   int64
	opLog   []LogEntry
	seqNum  int
	mu      sync.Mutex
}

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		rooms:          make(map[string]*Room),
		logger:         logger,
		allowedOrigins: make(map[string]struct{}),
		colors: []string{
			"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4",
			"#FFEAA7", "#DDA0DD", "#98D8C8", "#F7DC6F",
			"#BB8FCE", "#85C1E9", "#F0B27A", "#82E0AA",
		},
	}
}

func (h *Hub) ConfigureAllowedOrigins(origins []string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.allowedOrigins = make(map[string]struct{})
	for _, origin := range origins {
		h.allowedOrigins[origin] = struct{}{}
	}
	h.strictOrigin = len(h.allowedOrigins) > 0
}

func (h *Hub) isOriginAllowed(r *http.Request) bool {
	h.mu.Lock()
	strict := h.strictOrigin
	allowed := make(map[string]struct{}, len(h.allowedOrigins))
	for origin := range h.allowedOrigins {
		allowed[origin] = struct{}{}
	}
	h.mu.Unlock()

	if !strict {
		return true
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	normalized := u.Scheme + "://" + u.Host
	_, ok := allowed[normalized]
	return ok
}

func newRoom(docID string) *Room {
	return &Room{
		docID:   docID,
		clients: make(map[*websocket.Conn]*ClientConn),
		doc:     crdt.NewDocument(),
	}
}

func (h *Hub) Shutdown() {
	h.mu.Lock()
	rooms := make([]*Room, 0, len(h.rooms))
	for _, room := range h.rooms {
		rooms = append(rooms, room)
	}
	h.mu.Unlock()

	clientsDisconnected := 0
	for _, room := range rooms {
		room.mu.Lock()
		for _, cc := range room.clients {
			clientsDisconnected++
			cc.closeSend()
		}
		room.mu.Unlock()
	}
	h.logger.Info("hub shut down", "clients_disconnected", clientsDisconnected, "rooms", len(rooms))
}

func (h *Hub) assignColor() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	color := h.colors[h.colorIdx]
	h.colorIdx = (h.colorIdx + 1) % len(h.colors)
	return color
}

func (h *Hub) nextUserID() string {
	seq := atomic.AddUint64(&h.userSeq, 1)
	return fmt.Sprintf("U-%d", seq)
}

func (h *Hub) getOrCreateRoom(docID string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room, ok := h.rooms[docID]; ok {
		return room
	}
	room := newRoom(docID)
	h.rooms[docID] = room
	h.logger.Info("room created", "docID", docID)
	return room
}

func (h *Hub) removeRoomIfEmpty(docID string, expected *Room) {
	expected.mu.Lock()
	clientCount := len(expected.clients)
	expected.mu.Unlock()
	if clientCount > 0 {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.rooms[docID]; ok && existing == expected {
		delete(h.rooms, docID)
		h.logger.Info("room removed", "docID", docID)
	}
}

func (r *Room) getUsers() []User {
	users := make([]User, 0, len(r.clients))
	for _, cc := range r.clients {
		users = append(users, cc.user)
	}
	return users
}

func (r *Room) broadcastPresence() {
	users := r.getUsers()
	msg := Message{Type: "presence", Users: users}
	raw, _ := json.Marshal(msg)
	for _, cc := range r.clients {
		cc.safeSend(raw)
	}
}

func (r *Room) broadcastExcept(raw []byte, sender *websocket.Conn) {
	for ws, cc := range r.clients {
		if ws == sender {
			continue
		}
		cc.safeSend(raw)
	}
}

func (r *Room) applyOp(msg *Message, user User, logger *slog.Logger) bool {
	switch msg.Type {
	case "insert":
		char := rune(msg.Char)
		pos := msg.Position
		docLen := r.doc.Len()
		if pos < 0 {
			pos = 0
		}
		if pos > docLen {
			pos = docLen
		}

		var anchorID crdt.OpID
		if msg.AnchorID != nil && msg.AnchorID.Clock >= 0 && r.doc.FindNode(*msg.AnchorID) != nil {
			anchorID = *msg.AnchorID
		} else {
			anchorID = r.doc.GetAnchorIDForPos(pos)
		}

		r.clock++
		newID := crdt.OpID{Clock: r.clock, SiteID: user.ID}
		r.doc.Insert(char, anchorID, newID)

		msg.NewID = &newID
		msg.AnchorID = &anchorID
		msg.Position = r.doc.GetVisiblePosOfNode(newID)

		logger.Debug("insert", "user", user.Name, "char", string(char), "pos", msg.Position, "docID", r.docID)

	case "delete":
		docLen := r.doc.Len()
		if docLen == 0 {
			return false
		}

		var targetID crdt.OpID
		if msg.TargetID != nil && msg.TargetID.Clock >= 0 && r.doc.FindNode(*msg.TargetID) != nil {
			targetID = *msg.TargetID
		} else {
			if msg.Position < 0 || msg.Position >= docLen {
				return false
			}
			targetID = r.doc.GetNodeIDAtVisiblePos(msg.Position)
		}

		resolvedPos := r.doc.GetVisiblePosOfNode(targetID)
		if resolvedPos < 0 {
			return false
		}
		msg.Position = resolvedPos
		msg.TargetID = &targetID
		r.doc.Delete(targetID)

		logger.Debug("delete", "user", user.Name, "pos", msg.Position, "docID", r.docID)

	default:
		return false
	}

	r.seqNum++
	msg.Seq = r.seqNum
	msg.UserID = user.ID
	msg.UserName = user.Name
	msg.Color = user.Color
	r.opLog = append(r.opLog, LogEntry{Seq: msg.Seq, Msg: *msg})
	if len(r.opLog) > maxOpLogSize {
		r.opLog = r.opLog[len(r.opLog)-maxOpLogSize:]
	}

	return true
}

func (r *Room) buildFullSync(userID, color string) Message {
	return Message{
		Type:    "full_sync",
		UserID:  userID,
		Content: r.doc.String(),
		Color:   color,
		Seq:     r.seqNum,
		NodeIDs: r.doc.GetVisibleNodeIDs(),
	}
}

func (r *Room) buildReplaySync(userID, color string, sinceSeq int) Message {
	ops := make([]Message, 0)
	for _, entry := range r.opLog {
		if entry.Seq > sinceSeq {
			ops = append(ops, entry.Msg)
		}
	}
	return Message{Type: "replay_sync", UserID: userID, Color: color, Seq: r.seqNum, Ops: ops}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("websocket upgrade request", "remoteAddr", r.RemoteAddr, "host", r.Host)
	if !h.isOriginAllowed(r) {
		h.logger.Warn("websocket origin blocked", "origin", r.Header.Get("Origin"))
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", "error", err, "remoteAddr", r.RemoteAddr)
		return
	}

	ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var joinMsg Message
	if err := ws.ReadJSON(&joinMsg); err != nil {
		h.logger.Warn("join message read failed", "error", err)
		ws.Close()
		return
	}
	ws.SetReadDeadline(time.Time{})

	docID := r.URL.Query().Get("doc_id")
	if docID == "" {
		docID = "default"
	}
	room := h.getOrCreateRoom(docID)

	name := joinMsg.UserName
	if name == "" {
		name = "Anonymous"
	}
	user := User{ID: h.nextUserID(), Name: name, Color: h.assignColor()}
	cc := newClientConn(h, room, ws, user)

	room.mu.Lock()
	room.clients[ws] = cc
	if joinMsg.LastSeq > 0 && !joinMsg.HasOfflineEdits && joinMsg.LastSeq <= room.seqNum {
		replay := room.buildReplaySync(user.ID, user.Color, joinMsg.LastSeq)
		replayRaw, _ := json.Marshal(replay)
		cc.safeSend(replayRaw)
	} else {
		sync := room.buildFullSync(user.ID, user.Color)
		syncRaw, _ := json.Marshal(sync)
		cc.safeSend(syncRaw)
	}
	room.broadcastPresence()
	room.mu.Unlock()

	go cc.writePump()
	cc.readPump()
}

func (h *Hub) ServeStats(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	roomsCopy := make(map[string]*Room, len(h.rooms))
	for docID, room := range h.rooms {
		roomsCopy[docID] = room
	}
	h.mu.Unlock()

	totalUsers := 0
	roomStats := make([]map[string]interface{}, 0, len(roomsCopy))
	for docID, room := range roomsCopy {
		room.mu.Lock()
		users := len(room.clients)
		docLength := room.doc.Len()
		opLogSize := len(room.opLog)
		seqNum := room.seqNum
		room.mu.Unlock()

		totalUsers += users
		roomStats = append(roomStats, map[string]interface{}{
			"docID":     docID,
			"users":     users,
			"docLength": docLength,
			"opLogSize": opLogSize,
			"seqNum":    seqNum,
		})
	}

	stats := map[string]interface{}{
		"rooms":      len(roomsCopy),
		"users":      totalUsers,
		"roomStats":  roomStats,
		"serverTime": time.Now().Format("15:04:05"),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}
