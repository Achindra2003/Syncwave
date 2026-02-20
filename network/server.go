package network

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Message is the JSON payload between server and clients.
type Message struct {
	Type     string `json:"type"`
	Char     int    `json:"char"`
	Clock    int64  `json:"clock"`
	SiteID   string `json:"siteID"`
	AnchorClock  int64  `json:"anchorClock"`
	AnchorSiteID string `json:"anchorSiteID"`
	FullText string `json:"fullText,omitempty"`
}

// Hub manages all WebSocket connections and broadcasting.
type Hub struct {
	clients map[*websocket.Conn]bool
	mu      sync.Mutex
}

func NewHub() *Hub {
	return &Hub{clients: make(map[*websocket.Conn]bool)}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("[HUB] Upgrade error:", err)
		return
	}

	h.mu.Lock()
	h.clients[ws] = true
	clientCount := len(h.clients)
	h.mu.Unlock()

	fmt.Printf("[HUB] Client connected (%d total)\n", clientCount)

	defer func() {
		h.mu.Lock()
		delete(h.clients, ws)
		remaining := len(h.clients)
		h.mu.Unlock()
		ws.Close()
		fmt.Printf("[HUB] Client disconnected (%d remaining)\n", remaining)
	}()

	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		fmt.Printf("[HUB] %s: '%c' from %s\n", msg.Type, rune(msg.Char), msg.SiteID)

		// Broadcast to all OTHER clients
		h.mu.Lock()
		for client := range h.clients {
			if client == ws {
				continue
			}
			if err := client.WriteMessage(websocket.TextMessage, raw); err != nil {
				client.Close()
				delete(h.clients, client)
			}
		}
		h.mu.Unlock()
	}
}
