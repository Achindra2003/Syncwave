# SyncWave
### A High-Performance Distributed Collaboration Engine using CRDTs

SyncWave is a real-time collaborative text editor built in **Go**, inspired by Google Docs and Figma. It uses **Conflict-Free Replicated Data Types (CRDTs)** to enable multiple users to edit the same document simultaneously — even offline.

---

## Features

- **Real-Time Collaboration** — Type in one browser tab, see it instantly in another
- **Offline Editing** — Keep typing when disconnected. Edits are buffered locally and synced automatically on reconnect
- **CRDT-Based Conflict Resolution** — Uses an RGA (Replicated Growable Array) with Lamport Timestamps
- **AI Writing Assistant** — Streaming text completion powered by Groq (llama-3.1-8b). Ghost text appears as you type; press Tab to accept
- **WebSocket Communication** — Low-latency bidirectional messaging with auto-reconnect and exponential backoff
- **User Presence** — See who's online with colored avatar indicators and name tooltips
- **Operation Log** — Server maintains a sequenced log of all operations for replay on reconnect
- **Activity Feed** — Live log of all connected users' actions

## Architecture

```
SyncWave/
├── ai/assistant.go       # Groq LLM integration (streaming completion)
├── core/crdt.go          # CRDT Engine (Doubly Linked List + Lamport Timestamps)
├── network/server.go     # WebSocket Hub (presence, broadcast, op log, offline sync)
├── static/
│   ├── index.html        # Google Docs-style UI with ghost text + offline banner
│   └── app.js            # Collaboration + AI + offline buffer + auto-reconnect
├── main.go               # HTTP + WebSocket + SSE AI endpoint
├── .env                  # Groq API key
└── go.mod
```

| Component | Technology |
|---|---|
| Backend | Go, Gorilla WebSocket |
| Frontend | HTML5, CSS3, Vanilla JS |
| AI | Groq API (llama-3.1-8b-instant) via langchaingo |
| Protocol | JSON over WebSocket + SSE for AI |
| Algorithm | RGA CRDT with Lamport Clocks |

## How It Works

### Real-Time Collaboration
1. Each user connects to the Go server via **WebSocket**
2. Keystrokes are sent as **JSON operations** (insert/delete with position)
3. The server maintains an **authoritative document** and **operation log**
4. Operations are **broadcast** to all other connected clients
5. New clients receive the **full document** on connect

### Offline Sync (CRDT)
1. When the WebSocket disconnects (internet drops, server restart, etc.), the client detects it automatically
2. The UI shows an **"Offline Mode"** banner
3. All edits are **buffered locally** in an operation array
4. The client **auto-reconnects** with exponential backoff (1s → 2s → 4s → ... → 30s max)
5. On reconnect, the client sends its **buffered operations** as a `batch_sync` message
6. The server applies them, updates the document, and broadcasts to other users
7. **Result**: Zero edits lost, automatic merge

### AI Assistant
1. After typing 10+ characters and pausing, an AI completion request is sent to Groq
2. The response streams back token-by-token as **grey ghost text** in the editor
3. Press **Tab** to accept — each character is sent over WebSocket to stay in sync with collaborators
4. The AI takes text **before and after** the cursor into account for context-aware completions

## Getting Started

### Prerequisites
- [Go 1.22+](https://go.dev/dl/)
- A [Groq API key](https://console.groq.com/keys) (optional, for AI features)

### Setup
```bash
git clone https://github.com/Achindra2003/Syncwave.git
cd SyncWave
```

Create a `.env` file:
```
GROQ_API_KEY=gsk_your_key_here
```

### Run
```bash
go mod tidy
go run main.go
```

Open **http://localhost:8080** in two or more browser tabs and start typing!

### Deploy
Set the `PORT` environment variable to use a custom port:
```bash
PORT=3000 go run main.go
```

### Demo: Offline Sync
1. Open two tabs — both see the same document
2. Kill the server (`Ctrl+C`) or disconnect your network
3. Keep typing in both tabs (they'll show "Offline" banner)
4. Restart the server / reconnect — both tabs auto-sync and merge edits

## License
MIT
