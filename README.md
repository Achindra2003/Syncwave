# SyncWave

**A distributed real-time collaborative text editor built end-to-end in Go.**

SyncWave enables multiple users to edit the same document simultaneously — even offline — using a **Conflict-Free Replicated Data Type (CRDT)**. It features AI-powered writing suggestions streamed via Groq, a Google Docs-style UI, and deploys as a single binary thanks to Go's `embed` package.

---

## Features

- **Real-Time Collaboration** — Type in one browser tab, see it instantly in another via WebSocket
- **Multi-Document Rooms** — Collaborate per document using `?doc_id=<your-id>` in the URL
- **Offline Editing & Sync** — Edits buffer locally during disconnects, with three-way merge on reconnect
- **CRDT Conflict Resolution** — RGA (Replicated Growable Array) with Lamport timestamps and RGA tie-breaking
- **AI Writing Assistant** — Streaming ghost-text completions via Groq (llama-3.1-8b-instant); press Tab to accept
- **User Presence** — Colored avatars, remote cursor rendering, activity feed
- **Graceful Shutdown** — Signal handling (SIGINT/SIGTERM) with connection draining
- **Embedded Frontend** — Static files compiled into the binary via `go:embed`

## Go Concepts Demonstrated

| Concept | Where |
|---|---|
| Goroutines & Channels | Per-client `writePump` goroutine with buffered `chan []byte` ([client.go](internal/hub/client.go)) |
| Mutexes (`sync.Mutex`) | Hub-level lock protecting shared CRDT document ([hub.go](internal/hub/hub.go)) |
| `sync.Once` | Safe channel close preventing double-close panics ([client.go](internal/hub/client.go)) |
| Interfaces & Structs | `Config` struct, `Document` API |
| `embed.FS` | Frontend assets embedded in binary ([handler.go](internal/web/handler.go)) |
| `log/slog` | Structured logging throughout |
| `net/http` | HTTP server with custom `ServeMux`, middleware chain |
| Graceful Shutdown | `signal.Notify` + `context.WithTimeout` in [main.go](cmd/server/main.go) |
| Testing | Table-driven tests, subtests, stress test ([document_test.go](internal/crdt/document_test.go)) |
| `internal/` Convention | Encapsulated packages not importable by external modules |
| Error Wrapping | `fmt.Errorf("...: %w", err)` pattern in AI package |
| JSON Marshaling | Tagged structs for WebSocket protocol ([message.go](internal/hub/message.go)) |
| WebSocket Ping/Pong | Keepalive for reverse proxy deployments (Render, Cloudflare) |

## Architecture

```
SyncWave/
├── cmd/
│   └── server/
│       └── main.go              # Entry point: config, DI, graceful shutdown
├── internal/
│   ├── config/
│   │   └── config.go            # Env-based configuration (twelve-factor app)
│   ├── crdt/
│   │   ├── document.go          # RGA CRDT engine (doubly-linked list)
│   │   └── document_test.go     # Unit tests (table-driven, stress)
│   ├── hub/
│   │   ├── hub.go               # WebSocket hub: broadcast, CRDT, op log
│   │   ├── client.go            # Per-client read/write pumps, ping/pong
│   │   └── message.go           # Protocol message types
│   ├── ai/
│   │   └── assistant.go         # Groq LLM streaming completion
│   └── web/
│       ├── handler.go            # Routes + embedded static file server
│       ├── static/               # JS, CSS (embedded via go:embed)
│       └── templates/            # HTML (embedded via go:embed)
├── go.mod
├── Dockerfile                    # Multi-stage build (single binary output)
├── .env.example                  # Environment variable template
└── README.md
```

| Layer | Technology |
|---|---|
| Backend | Go 1.24, `gorilla/websocket`, `log/slog` |
| Frontend | Vanilla JS, CSS3 |
| AI | Groq API via `langchaingo` (OpenAI-compatible) |
| Protocol | JSON over WebSocket + SSE for AI streaming |
| Algorithm | RGA CRDT with Lamport clocks |
| Deployment | Docker multi-stage build → Render free tier |

## How It Works

### Real-Time Collaboration
1. Each user connects via **WebSocket** and receives the full document (`full_sync`)
2. Keystrokes produce `insert`/`delete` ops with **OpID-based addressing**
3. The server applies ops to the **authoritative CRDT** and broadcasts to all clients
4. Each client maintains a **shadow CRDT array** mapping positions ↔ OpIDs

### Offline Sync
1. On disconnect, the client snapshots `lastSyncedContent` as the merge base
2. Edits continue locally with **buffered operations**
3. On reconnect, a **three-way merge** (`base`, `local`, `server`) reconciles changes
4. If the server restarted (empty doc), a `restore` protocol rebuilds it from the first client

### AI Assistant
1. After 800ms of idle with 10+ characters, a context-aware completion request fires
2. Text before **and** after the cursor is sent for intelligent suggestions
3. Tokens stream back via SSE and render as **grey ghost text**
4. Press **Tab** to accept — insertions flow through the CRDT like normal edits

## Getting Started

### Prerequisites
- [Go 1.24+](https://go.dev/dl/)
- A [Groq API key](https://console.groq.com/keys) (optional, for AI features)

### Setup
```bash
git clone https://github.com/Achindra2003/Syncwave.git
cd SyncWave
cp .env.example .env
# Edit .env and add your GROQ_API_KEY
```

Current MVP env vars used by the server:

```dotenv
PORT=8080
LOG_LEVEL=info
GROQ_API_KEY=your-key
DATABASE_URL=postgres://postgres:password@db.your-project-ref.supabase.co:5432/postgres?sslmode=require
```

### Run
```bash
go run ./cmd/server
```

Open **http://localhost:8080** in two or more browser tabs and start typing!

Product flow:
- Open `/` to create or access documents.
- Editor route is `/editor?doc_id=<document-id>`.

### Test
```bash
go test ./internal/crdt/ -v
```

### Load Test (CRDT + Concurrency)
Run multi-client websocket stress test against local or deployed URL:

```bash
go run ./cmd/loadtest -base https://syncwave-67yw.onrender.com -create=true -clients 12 -seconds 30 -ops 4
```

Example with existing document:

```bash
go run ./cmd/loadtest -base https://syncwave-67yw.onrender.com -doc doc-abc123 -clients 20 -seconds 45 -ops 5
```

### Deploy with Docker
```bash
docker build -t syncwave .
docker run -p 8080:8080 -e GROQ_API_KEY=your_key syncwave
```

### Deploy to Render
1. Push to GitHub
2. Create a **Web Service** on [Render](https://render.com) pointing to the repo
3. Set **Build Command**: (auto-detected from Dockerfile)
4. Set **Environment Variables**: `DATABASE_URL` (required), `GROQ_API_KEY` (optional)
5. Deploy — Render auto-detects the Dockerfile

### Demo: Offline Sync
1. Open two tabs — both see the same document
2. Kill the server (`Ctrl+C`) or disconnect your network
3. Keep typing in both tabs (they show "Offline" banner)
4. Restart the server — both tabs auto-sync and merge edits without data loss

## License
MIT
