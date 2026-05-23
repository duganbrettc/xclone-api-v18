package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

func initDB() *sql.DB {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://chirp:chirp@localhost:5432/chirp?sslmode=disable"
	}

	var db *sql.DB
	var err error
	for i := 0; i < 30; i++ {
		db, err = sql.Open("postgres", dsn)
		if err == nil {
			err = db.Ping()
		}
		if err == nil {
			break
		}
		log.Printf("waiting for database (%d/30): %v", i+1, err)
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatalf("cannot connect to database: %v", err)
	}
	if err := runMigrations(db); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	log.Println("database ready")
	return db
}

func runMigrations(db *sql.DB) error {
	stmts := []string{
		// Tables
		`CREATE TABLE IF NOT EXISTS users (
			id            TEXT PRIMARY KEY,
			username      TEXT UNIQUE NOT NULL CHECK(username ~ '^[a-zA-Z0-9_]+$'),
			display_name  TEXT NOT NULL DEFAULT '',
			bio           TEXT NOT NULL DEFAULT '',
			email         TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			preferences   TEXT NOT NULL DEFAULT '{}',
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS follows (
			follower_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			followee_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (follower_id, followee_id)
		)`,
		`CREATE TABLE IF NOT EXISTS blocks (
			blocker_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			blocked_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (blocker_id, blocked_id)
		)`,
		`CREATE TABLE IF NOT EXISTS posts (
			id         TEXT PRIMARY KEY,
			author_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			body       TEXT NOT NULL,
			visibility TEXT NOT NULL DEFAULT 'public' CHECK(visibility IN ('public','private')),
			reply_to   TEXT REFERENCES posts(id) ON DELETE SET NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ NULL DEFAULT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS likes (
			user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			post_id    TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, post_id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id           TEXT PRIMARY KEY,
			sender_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			recipient_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			body         TEXT NOT NULL,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Idempotent schema migrations for pre-existing databases
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ NULL DEFAULT NULL`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT NOT NULL DEFAULT ''`,

		// Indexes for timeline queries: posts by author ordered newest-first
		`CREATE INDEX IF NOT EXISTS idx_posts_author_created ON posts(author_id, created_at DESC)`,
		// Index for general timeline feed scan ordered newest-first
		`CREATE INDEX IF NOT EXISTS idx_posts_created ON posts(created_at DESC)`,
		// Index for reply threading
		`CREATE INDEX IF NOT EXISTS idx_posts_reply_to ON posts(reply_to)`,
		// Partial index for non-deleted posts
		`CREATE INDEX IF NOT EXISTS idx_posts_not_deleted ON posts(created_at DESC) WHERE deleted_at IS NULL`,

		// Index for message thread queries: all messages between exactly two users, time-ordered.
		`CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(LEAST(sender_id, recipient_id), GREATEST(sender_id, recipient_id), created_at ASC)`,
		// Index for conversation list (most-recent-per-peer): single-user scan
		`CREATE INDEX IF NOT EXISTS idx_messages_participants ON messages(sender_id, recipient_id, created_at DESC)`,

		// Indexes for follows/blocks lookups
		`CREATE INDEX IF NOT EXISTS idx_follows_follower ON follows(follower_id, followee_id)`,
		`CREATE INDEX IF NOT EXISTS idx_follows_followee ON follows(followee_id, follower_id)`,
		`CREATE INDEX IF NOT EXISTS idx_blocks_blocker ON blocks(blocker_id, blocked_id)`,
		`CREATE INDEX IF NOT EXISTS idx_blocks_blocked ON blocks(blocked_id, blocker_id)`,

		// Index for session lookups by user (used when revoking all user sessions)
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			preview := stmt
			if len(preview) > 60 {
				preview = preview[:60]
			}
			return fmt.Errorf("exec %q: %w", preview, err)
		}
	}
	log.Println("database migration complete")
	return nil
}
