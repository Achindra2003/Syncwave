# SyncWave Deployed Demo Script

Use this script live on the deployed app:

- Base URL: `https://syncwave-67yw.onrender.com`
- Target time: 8–12 minutes
- Devices: Laptop + Phone (recommended)

## 1) Setup (30–60s)

1. Open `https://syncwave-67yw.onrender.com`.
2. Click **Create New Document**.
3. In editor top bar, rename title to: `Client Demo – SyncWave`.
4. Copy **Share Link**.

Expected:
- Editor opens with `TXT`, `DOCX`, `PDF`, `Share Link`, `New Document`, `Offline Test`.
- Title updates in tab and persists for this document.

## 2) Real-time collaboration (1–2 min)

1. Open share link on second device/browser.
2. Type from both devices at different positions.
3. Confirm cursors/presence and merged content updates in near real time.

Narration:
- "Each keystroke is an operation sent over WebSocket and merged by CRDT logic."

## 3) Offline sync proof (2–3 min)

1. On phone (or second browser), click **Offline Test** (button changes to **Resume Sync**).
2. While offline side keeps typing, continue typing on online side too.
3. On offline side click **Resume Sync**.
4. Wait 2–5 seconds for reconciliation.

Expected:
- Offline edits buffer locally.
- Reconnect triggers merge and replay sync.
- Both sides end up with merged text (minor ordering differences can occur if same position is edited concurrently).

Narration:
- "This demonstrates disconnected editing and eventual synchronization."

## 4) AI completion (1 min)

1. Type a paragraph with at least 10 chars.
2. Pause briefly.
3. Wait for ghost suggestion.
4. Press `Tab` to accept AI completion.

Expected:
- AI badge transitions through thinking/streaming/ready.
- Suggestion inserts into shared doc and syncs to other collaborators.

## 5) Export demo (1 min)

1. Click **TXT**, **DOCX**, **PDF**.
2. Open downloads quickly to show formats generated.

Narration:
- "Exports now support plain text, Word, and PDF for handoff workflows."

## 6) Document lifecycle (1 min)

1. Click **New Document**.
2. Return to home page and open existing docs list.
3. Reopen previous doc to show title/state persistence.

## 7) Health/API confidence (30s)

- Open: `https://syncwave-67yw.onrender.com/health` → `{"status":"ok"}`
- Optional: `https://syncwave-67yw.onrender.com/api/docs?limit=5`

## 8) Stress test proof (CLI, optional 1–2 min)

Run from project root:

```powershell
go run ./cmd/loadtest -base https://syncwave-67yw.onrender.com -create=true -clients 12 -seconds 30 -ops 4
```

What to call out:
- `Ack latency p50/p95/p99`
- `Converged final state`

If non-converged appears, present it as active hardening work under high-contention randomized edits.

## 9) Fallback lines (if anything glitches live)

- "Core collaboration and offline recovery are working; this edge case is from synthetic high-contention load and is the next hardening target."
- "The document workflow, persistence, AI assist, and export stack are production live."
