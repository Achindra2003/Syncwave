package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syncwave/internal/ai"
	"syncwave/internal/hub"
)

type fakeCompleter struct {
	stream func(ctx context.Context, textBefore, textAfter string) <-chan ai.StreamResult
}

func (f fakeCompleter) StreamComplete(ctx context.Context, textBefore, textAfter string) <-chan ai.StreamResult {
	return f.stream(ctx, textBefore, textAfter)
}

func TestHandleCompleteMethodNotAllowed(t *testing.T) {
	h := handleComplete(fakeCompleter{stream: func(ctx context.Context, textBefore, textAfter string) <-chan ai.StreamResult {
		ch := make(chan ai.StreamResult)
		close(ch)
		return ch
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/complete", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCompleteStreamsDone(t *testing.T) {
	h := handleComplete(fakeCompleter{stream: func(ctx context.Context, textBefore, textAfter string) <-chan ai.StreamResult {
		ch := make(chan ai.StreamResult)
		close(ch)
		return ch
	}})

	body := `{"textBefore":"this is enough context","textAfter":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/complete", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "data: [DONE]") {
		t.Fatalf("expected SSE DONE marker, got body=%q", rr.Body.String())
	}
}

func TestHandleCompleteStreamsErrorPayload(t *testing.T) {
	h := handleComplete(fakeCompleter{stream: func(ctx context.Context, textBefore, textAfter string) <-chan ai.StreamResult {
		ch := make(chan ai.StreamResult, 1)
		ch <- ai.StreamResult{Error: errors.New("stream failed")}
		close(ch)
		return ch
	}})

	body := `{"textBefore":"this is enough context","textAfter":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/complete", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), `"error":"stream failed"`) {
		t.Fatalf("expected streamed error payload, got body=%q", rr.Body.String())
	}
}

func TestRegisterRoutesRootServesHTML(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.NewHub(logger)
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("content-type: got %q, want html", got)
	}
}

func TestRegisterRoutesCompleteUnavailableWithoutAssistant(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.NewHub(logger)
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, nil)

	body := `{"textBefore":"this is enough context","textAfter":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/complete", strings.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestRegisterRoutesWebSocketEndpointExists(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.NewHub(logger)
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, nil)

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code == http.StatusNotFound {
		t.Fatalf("expected websocket route to be registered")
	}
}
