package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type opID struct {
	Clock  int64  `json:"clock"`
	SiteID string `json:"siteID"`
}

type message struct {
	Type            string    `json:"type"`
	Char            int       `json:"char,omitempty"`
	Position        int       `json:"position"`
	UserID          string    `json:"userID,omitempty"`
	UserName        string    `json:"userName,omitempty"`
	Content         string    `json:"content,omitempty"`
	Seq             int       `json:"seq,omitempty"`
	LastSeq         int       `json:"lastSeq,omitempty"`
	HasOfflineEdits bool      `json:"hasOfflineEdits,omitempty"`
	Ops             []message `json:"ops,omitempty"`
	ClientOpID      string    `json:"clientOpID,omitempty"`
	NewID           *opID     `json:"newID,omitempty"`
	NodeIDs         []opID    `json:"nodeIDs,omitempty"`
}

type createDocResponse struct {
	Document struct {
		ID string `json:"id"`
	} `json:"document"`
}

type clientResult struct {
	ClientID       int
	Hash           string
	TextLen        int
	OpsSent        int64
	OpsObserved    int64
	LatencySamples []time.Duration
	Errors         []string
}

type testClient struct {
	id     int
	conn   *websocket.Conn
	closed int32

	mu        sync.Mutex
	text      []rune
	lastSeq   int
	pendingAt map[string]time.Time

	opsSent     int64
	opsObserved int64
	latencies   []time.Duration
	errors      []string
}

func main() {
	baseURL := flag.String("base", "http://localhost:8080", "Base HTTP URL, e.g. https://syncwave-67yw.onrender.com")
	docID := flag.String("doc", "", "Existing document ID. If empty and -create is true, creates one")
	createDoc := flag.Bool("create", true, "Create a new document via /api/docs when doc ID is empty")
	clients := flag.Int("clients", 10, "Number of concurrent websocket clients")
	duration := flag.Int("seconds", 20, "Test duration per client (seconds)")
	opsPerSecond := flag.Int("ops", 3, "Approx operations per second per client")
	seed := flag.Int64("seed", time.Now().UnixNano(), "Random seed")
	flag.Parse()

	rand.Seed(*seed)

	if *clients <= 0 {
		fmt.Println("clients must be > 0")
		os.Exit(1)
	}
	if *opsPerSecond <= 0 {
		fmt.Println("ops must be > 0")
		os.Exit(1)
	}
	if *duration <= 0 {
		fmt.Println("seconds must be > 0")
		os.Exit(1)
	}

	normalizedBase := strings.TrimRight(*baseURL, "/")
	resolvedDocID := strings.TrimSpace(*docID)
	if resolvedDocID == "" {
		if !*createDoc {
			fmt.Println("doc id is empty and -create=false")
			os.Exit(1)
		}
		id, err := createDocument(normalizedBase)
		if err != nil {
			fmt.Printf("failed creating document: %v\n", err)
			os.Exit(1)
		}
		resolvedDocID = id
	}

	wsURL, err := toWSURL(normalizedBase, resolvedDocID)
	if err != nil {
		fmt.Printf("invalid base URL: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Running load test\n")
	fmt.Printf("- Base URL: %s\n", normalizedBase)
	fmt.Printf("- WS URL:   %s\n", wsURL)
	fmt.Printf("- Doc ID:   %s\n", resolvedDocID)
	fmt.Printf("- Clients:  %d\n", *clients)
	fmt.Printf("- Duration: %ds\n", *duration)
	fmt.Printf("- Ops/s:    %d per client\n\n", *opsPerSecond)

	results := make(chan clientResult, *clients)
	var wg sync.WaitGroup
	for i := 0; i < *clients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			res := runClient(clientID, wsURL, *duration, *opsPerSecond)
			results <- res
		}(i + 1)
	}

	wg.Wait()
	close(results)

	all := make([]clientResult, 0, *clients)
	for r := range results {
		all = append(all, r)
	}

	summarize(all)
}

func createDocument(base string) (string, error) {
	payload := []byte(`{"title":"Load Test Document"}`)
	resp, err := http.Post(base+"/api/docs", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	var out createDocResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Document.ID == "" {
		return "", fmt.Errorf("empty document id from API")
	}
	return out.Document.ID, nil
}

func toWSURL(base, docID string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	u.Path = "/ws"
	q := u.Query()
	q.Set("doc_id", docID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func runClient(clientID int, wsURL string, seconds int, opsPerSecond int) clientResult {
	c := &testClient{
		id:        clientID,
		pendingAt: make(map[string]time.Time),
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return clientResult{ClientID: clientID, Errors: []string{fmt.Sprintf("dial failed: %v", err)}}
	}
	c.conn = conn

	join := message{
		Type:            "join",
		UserID:          fmt.Sprintf("lt-%d", clientID),
		UserName:        fmt.Sprintf("load-%d", clientID),
		LastSeq:         0,
		HasOfflineEdits: false,
	}
	if err := conn.WriteJSON(join); err != nil {
		_ = conn.Close()
		return clientResult{ClientID: clientID, Errors: []string{fmt.Sprintf("join failed: %v", err)}}
	}

	ready := make(chan struct{})
	readyOnce := sync.Once{}
	done := make(chan struct{})

	go c.readLoop(ready, &readyOnce, done)

	select {
	case <-ready:
	case <-time.After(8 * time.Second):
		c.appendError("timeout waiting for full_sync")
	}

	interval := time.Second / time.Duration(opsPerSecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	stopAt := time.Now().Add(time.Duration(seconds) * time.Second)

	opSeq := int64(0)
	for time.Now().Before(stopAt) {
		<-ticker.C
		if atomic.LoadInt32(&c.closed) == 1 {
			break
		}

		opID := fmt.Sprintf("lt-%d-%d", clientID, atomic.AddInt64(&opSeq, 1))
		op, ok := c.makeRandomOp(opID)
		if !ok {
			continue
		}
		if err := c.conn.WriteJSON(op); err != nil {
			c.appendError(fmt.Sprintf("write failed: %v", err))
			break
		}
		atomic.AddInt64(&c.opsSent, 1)
	}

	_ = c.conn.Close()
	<-done

	text := c.currentText()
	hash := sha256.Sum256([]byte(text))

	return clientResult{
		ClientID:       clientID,
		Hash:           hex.EncodeToString(hash[:]),
		TextLen:        len([]rune(text)),
		OpsSent:        atomic.LoadInt64(&c.opsSent),
		OpsObserved:    atomic.LoadInt64(&c.opsObserved),
		LatencySamples: c.snapshotLatencies(),
		Errors:         c.snapshotErrors(),
	}
}

func (c *testClient) readLoop(ready chan<- struct{}, readyOnce *sync.Once, done chan<- struct{}) {
	defer func() {
		atomic.StoreInt32(&c.closed, 1)
		close(done)
	}()

	for {
		var msg message
		if err := c.conn.ReadJSON(&msg); err != nil {
			return
		}

		if msg.Seq > 0 {
			c.mu.Lock()
			if msg.Seq > c.lastSeq {
				c.lastSeq = msg.Seq
			}
			c.mu.Unlock()
		}

		switch msg.Type {
		case "full_sync":
			c.mu.Lock()
			c.text = []rune(msg.Content)
			c.mu.Unlock()
			readyOnce.Do(func() { close(ready) })

		case "replay_sync":
			for _, op := range msg.Ops {
				c.applyOp(op)
			}
			readyOnce.Do(func() { close(ready) })

		case "insert", "delete":
			c.applyOp(msg)
		}
	}
}

func (c *testClient) applyOp(msg message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch msg.Type {
	case "insert":
		pos := msg.Position
		if pos < 0 {
			pos = 0
		}
		if pos > len(c.text) {
			pos = len(c.text)
		}
		r := rune(msg.Char)
		c.text = append(c.text[:pos], append([]rune{r}, c.text[pos:]...)...)

		if msg.ClientOpID != "" {
			if started, ok := c.pendingAt[msg.ClientOpID]; ok {
				c.latencies = append(c.latencies, time.Since(started))
				delete(c.pendingAt, msg.ClientOpID)
			}
		}
		atomic.AddInt64(&c.opsObserved, 1)

	case "delete":
		pos := msg.Position
		if pos < 0 || pos >= len(c.text) {
			return
		}
		c.text = append(c.text[:pos], c.text[pos+1:]...)
		atomic.AddInt64(&c.opsObserved, 1)
	}
}

func (c *testClient) makeRandomOp(clientOpID string) (message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	canDelete := len(c.text) > 0
	doDelete := canDelete && rand.Intn(100) < 22

	if doDelete {
		pos := rand.Intn(len(c.text))
		return message{Type: "delete", Position: pos}, true
	}

	pos := len(c.text)
	if len(c.text) > 0 && rand.Intn(100) < 35 {
		pos = rand.Intn(len(c.text) + 1)
	}

	ch := randomRune()
	c.pendingAt[clientOpID] = time.Now()
	return message{Type: "insert", Position: pos, Char: int(ch), ClientOpID: clientOpID}, true
}

func (c *testClient) currentText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.text)
}

func (c *testClient) appendError(err string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, err)
}

func (c *testClient) snapshotLatencies() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.latencies))
	copy(out, c.latencies)
	return out
}

func (c *testClient) snapshotErrors() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.errors))
	copy(out, c.errors)
	return out
}

func randomRune() rune {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 .,;:-_()[]{}\n"
	return rune(letters[rand.Intn(len(letters))])
}

func summarize(results []clientResult) {
	if len(results) == 0 {
		fmt.Println("no client results")
		return
	}

	totalSent := int64(0)
	totalObserved := int64(0)
	allLatencies := make([]time.Duration, 0)
	hashes := map[string]int{}
	totalErrors := 0

	for _, r := range results {
		totalSent += r.OpsSent
		totalObserved += r.OpsObserved
		allLatencies = append(allLatencies, r.LatencySamples...)
		hashes[r.Hash]++
		totalErrors += len(r.Errors)
	}

	converged := len(hashes) == 1

	fmt.Println("=== Load Test Summary ===")
	fmt.Printf("Clients: %d\n", len(results))
	fmt.Printf("Total ops sent: %d\n", totalSent)
	fmt.Printf("Total ops observed: %d\n", totalObserved)
	fmt.Printf("Converged final state: %v\n", converged)
	fmt.Printf("Distinct final hashes: %d\n", len(hashes))
	fmt.Printf("Total client-side errors: %d\n", totalErrors)

	if len(allLatencies) > 0 {
		sort.Slice(allLatencies, func(i, j int) bool { return allLatencies[i] < allLatencies[j] })
		avg := averageLatency(allLatencies)
		p50 := percentile(allLatencies, 50)
		p95 := percentile(allLatencies, 95)
		p99 := percentile(allLatencies, 99)
		fmt.Printf("Ack latency avg: %s\n", avg)
		fmt.Printf("Ack latency p50: %s\n", p50)
		fmt.Printf("Ack latency p95: %s\n", p95)
		fmt.Printf("Ack latency p99: %s\n", p99)
	} else {
		fmt.Println("Ack latency: no samples")
	}

	fmt.Println("\nPer-client snapshot:")
	for _, r := range results {
		fmt.Printf("- Client %02d | len=%d sent=%d observed=%d errors=%d hash=%s\n",
			r.ClientID, r.TextLen, r.OpsSent, r.OpsObserved, len(r.Errors), shortHash(r.Hash))
	}

	if !converged {
		fmt.Println("\nWARNING: Non-converged final states detected. Inspect server/client logs.")
	}
	if totalErrors > 0 {
		fmt.Println("\nClient errors:")
		for _, r := range results {
			for _, e := range r.Errors {
				fmt.Printf("- client %d: %s\n", r.ClientID, e)
			}
		}
	}
}

func averageLatency(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	var total time.Duration
	for _, v := range values {
		total += v
	}
	return total / time.Duration(len(values))
}

func percentile(values []time.Duration, p int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return values[0]
	}
	if p >= 100 {
		return values[len(values)-1]
	}
	idx := int(float64(len(values)-1) * (float64(p) / 100.0))
	return values[idx]
}

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}
