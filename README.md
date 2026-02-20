# SyncWave
### A High-Performance Distributed Collaboration Engine using CRDTs

SyncWave is a real-time collaborative text editor built in **Go**, inspired by Google Docs. It uses **Conflict-Free Replicated Data Types (CRDTs)** to enable multiple users to edit the same document simultaneously without conflicts.

---

## Features

- **Real-Time Collaboration** — Type in one browser tab, see it instantly in another
- **CRDT-Based Conflict Resolution** — Uses an RGA (Replicated Growable Array) with Lamport Timestamps
- **WebSocket Communication** — Low-latency bidirectional messaging
- **Zero Dependencies Frontend** — Vanilla HTML/CSS/JS with a premium dark UI
- **Activity Feed** — Live log of all connected users' actions

## Architecture

```
SyncWave/
├── core/crdt.go         # CRDT Engine (Doubly Linked List + Lamport Timestamps)
├── network/server.go    # WebSocket Hub (Fan-Out Broadcast Server)
├── static/
│   ├── index.html       # Editor UI (Dark Theme, Glassmorphism)
│   └── app.js           # WebSocket Client Logic
├── main.go              # HTTP Server Entry Point
└── go.mod
```

| Component | Technology |
|---|---|
| Backend | Go 1.25, Gorilla WebSocket |
| Frontend | HTML5, CSS3, Vanilla JS |
| Protocol | JSON over WebSocket |
| Algorithm | RGA CRDT with Lamport Clocks |

## How It Works

1. Each user connects to the Go server via **WebSocket**
2. When a user types, the keystroke is packaged as a **JSON operation** and sent to the server
3. The server **broadcasts** the operation to all other connected clients
4. Each client applies the remote operation to its local editor
5. The **CRDT logic** ensures all clients converge to the same document state, even under concurrent edits

## Getting Started

### Prerequisites
- [Go 1.21+](https://go.dev/dl/)

### Run Locally
```bash
git clone https://github.com/Achindra2003/Syncwave.git
cd Syncwave
go mod tidy
go run main.go
```

Then open **http://localhost:8080** in two or more browser tabs and start typing!

## Tech Stack Deep Dive

### CRDT (Conflict-Free Replicated Data Type)
- **Algorithm**: RGA (Replicated Growable Array)
- **Structure**: Doubly Linked List where each character node has a unique `(LamportClock, SiteID)` identifier
- **Conflict Resolution**: When two users insert at the same position simultaneously, the node with the higher clock value wins. If clocks are equal, the SiteID (string comparison) breaks ties deterministically

### WebSocket Hub
- **Pattern**: Fan-In / Fan-Out
- **Concurrency**: Uses `sync.Mutex` for thread-safe client management
- **Protocol**: Raw JSON messages forwarded as-is for minimal latency

## License
MIT
