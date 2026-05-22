# xclone-api-v18 — Chirp Backend API

Go HTTP API backend for the Chirp social platform (xclone v18 cascade).

## Runtime Contract: xclone-api-runtime-v18

- Listens on port 9321 (container) — injected via `PORT` env var by deployer
- Exposes `GET /healthz` returning HTTP 200 within 10s of start
- Postgres database with auto-migrations on startup
- Session cookie auth (HttpOnly, SameSite=Lax)
- API routes under `/api/*` prefix

## New in v18 vs v17

- `DELETE /api/posts/:id` — post authors can delete their own posts (403 for non-owners)
- Response field names aligned with xclone-web-v18 api.ts: posts use `author` field, DMs use `from`/`to`
- `GET /api/posts/:id` returns `{ post, replies }` object

## Build

```bash
go build -o server .
```

## Docker

```bash
docker build -t xclone-api-v18 .
docker run -p 9321:9321 -e DATABASE_URL=postgres://... -e SESSION_SECRET=... xclone-api-v18
```
