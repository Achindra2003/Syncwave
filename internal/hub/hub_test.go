package hub

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestClientConnSafeSendAfterClose(t *testing.T) {
	cc := &ClientConn{send: make(chan []byte, 1)}
	cc.closeSend()

	if ok := cc.safeSend([]byte("x")); ok {
		t.Fatalf("expected safeSend to return false after close")
	}
}

func TestClientConnCloseSendConcurrent(t *testing.T) {
	cc := &ClientConn{send: make(chan []byte, 1)}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cc.closeSend()
		}()
	}
	wg.Wait()

	if ok := cc.safeSend([]byte("x")); ok {
		t.Fatalf("expected safeSend to return false after concurrent close")
	}
}

func TestRemoveRoomIfEmptyRemovesRoom(t *testing.T) {
	h := NewHub(testLogger())
	room := newRoom("doc1")

	h.mu.Lock()
	h.rooms["doc1"] = room
	h.mu.Unlock()

	h.removeRoomIfEmpty("doc1", room)

	h.mu.Lock()
	_, exists := h.rooms["doc1"]
	h.mu.Unlock()
	if exists {
		t.Fatalf("expected empty room to be removed")
	}
}

func TestRemoveRoomIfEmptyKeepsNonEmptyRoom(t *testing.T) {
	h := NewHub(testLogger())
	room := newRoom("doc2")

	var ws *websocket.Conn
	room.clients[ws] = &ClientConn{}

	h.mu.Lock()
	h.rooms["doc2"] = room
	h.mu.Unlock()

	h.removeRoomIfEmpty("doc2", room)

	h.mu.Lock()
	_, exists := h.rooms["doc2"]
	h.mu.Unlock()
	if !exists {
		t.Fatalf("expected non-empty room to remain")
	}
}
