# xclone-api-v18 — Sparrow Backend API

Go (net/http) REST API backend for the **Sparrow** social platform (xclone v18 cascade).

## Language & Framework

- **Language**: Go 1.22
- **HTTP**: stdlib `net/http` with Go 1.22 pattern-matching router
- **Database**: PostgreSQL 16 via `lib/pq`
- **Sessions**: cookie-based (HttpOnly, SameSite=Lax, opaque token stored in `sessions` table)

## Internal Port

The service listens on **port 8080** (set via `PORT` env var, default `8080`).  
`DATABASE_URL` is **required** — the service exits immediately if it is not set.

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | **yes** | — | Postgres connection string (e.g. `postgres://sparrow:pw@db:5432/sparrow?sslmode=disable`) |
| `SESSION_SECRET` | yes | — | Secret for session token signing (32+ random bytes) |
| `PORT` | no | `8080` | Port to bind |

## Running Locally

```bash
# 1. Start a local Postgres (example with Docker)
docker run -d --name sparrow-db \
  -e POSTGRES_USER=sparrow \
  -e POSTGRES_PASSWORD=sparrow-dev \
  -e POSTGRES_DB=sparrow \
  -p 5432:5432 postgres:16-alpine

# 2. Build and run
go build -o server .
DATABASE_URL="postgres://sparrow:sparrow-dev@localhost:5432/sparrow?sslmode=disable" \
SESSION_SECRET="dev-secret-change-in-prod" \
./server
```

Migrations run automatically on startup — no separate migration step is needed.

## Migrations

SQL schema lives in `migrations/001_initial_schema.sql`.  
The service runs all migrations via `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS` on startup, so they are safe to re-apply against an existing database.

## Docker

```bash
docker build -t xclone-api .
docker run -p 8080:8080 \
  -e DATABASE_URL=postgres://sparrow:sparrow-dev@host.docker.internal:5432/sparrow?sslmode=disable \
  -e SESSION_SECRET=dev-secret \
  xclone-api
```

## Health Check

```
GET /healthz  →  200 {"status":"ok"}
```

Available immediately on startup (before the DB connection is established).

## API Routes

All application routes are prefixed with `/api/`. See `openapi.json` for the full OpenAPI spec.

Key endpoints:
- `POST /api/auth/signup` — create account + set session cookie
- `POST /api/auth/login` — authenticate + set session cookie
- `GET /api/auth/me` — current user (requires session)
- `GET /api/timeline` — home timeline (requires session)
- `DELETE /api/posts/:id` — 204 for owner, 403 for non-owner, 401 unauthenticated
