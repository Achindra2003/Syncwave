# SyncWave: Public-by-Link Now, Auth-Ready Next

## Current Product Mode (MVP)
- Anyone can open the deployed URL.
- Users can create documents and share links.
- Each document ID maps to one live collaboration room.
- Real-time sync is handled by CRDT + WebSocket fanout.
- Persistence is handled by SQLite.

## Why This Is Valid
- Fast onboarding: no registration friction.
- Strong demo value: CRDT sync, offline recovery, AI assistance, concurrency.
- Fits current scale target: multiple rooms with ~10-20 users per room on one instance.

## Auth-Ready Roadmap

### Phase A — User Accounts
- Add `users` table:
  - `id`, `email`, `password_hash`, `created_at`, `last_login_at`
- Add session cookies (HTTP-only, secure in production).
- Endpoints:
  - `POST /api/auth/register`
  - `POST /api/auth/login`
  - `POST /api/auth/logout`
  - `GET /api/auth/me`

### Phase B — Document Ownership + Permissions
- Add `document_permissions` table:
  - `doc_id`, `user_id`, `role` (`owner`, `editor`, `viewer`)
- On create document:
  - creator becomes `owner`.
- Add sharing endpoint:
  - `POST /api/docs/:id/share` (invite email or share token)
- Enforce access in:
  - `GET /api/docs`
  - `/editor?doc_id=...`
  - `/ws?doc_id=...`

### Phase C — Role-Aware Collaboration
- Viewers: read-only, no insert/delete.
- Editors: full collaboration permissions.
- Owners: permission management + editor rights.

### Phase D — Production Scale Path
- For single instance: keep SQLite.
- For multi-instance:
  - move persistence to Postgres,
  - add Redis pub/sub for cross-instance room fanout,
  - keep CRDT operation protocol unchanged.

## Security Hardening Checklist
- Rotate exposed API secrets.
- Enforce strict `ALLOWED_ORIGINS` in production.
- Add rate limiting for auth/document APIs.
- Add request/operation size caps for API and WS payloads.
- Add audit logging for auth + permission updates.

## Recommended Demo Story
1. Create document from home page.
2. Share link with another browser/device.
3. Type concurrently and show convergence.
4. Disconnect one client, edit offline, reconnect, show merge.
5. Show AI completion in active collaboration.
6. Explain how auth/permissions slot into this architecture.
