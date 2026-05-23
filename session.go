package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

type contextKey string

const ctxUser contextKey = "user"

// DBUser is the internal user representation with all DB fields.
type DBUser struct {
	ID          string
	Username    string
	DisplayName string
	Bio         string
	Preferences string
	CreatedAt   time.Time
}

// toUser converts a DBUser to the public API User response shape.
func (u *DBUser) toUser() User {
	return User{
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Bio:         u.Bio,
		CreatedAt:   u.CreatedAt,
	}
}

// User is the public API response shape.
type User struct {
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Bio         string    `json:"bio,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Post is the public API response shape.
// v18: uses "author" (not "authorUsername") to match xclone-web-v18 api.ts.
type Post struct {
	ID         string    `json:"id"`
	Author     string    `json:"author"`
	Body       string    `json:"body"`
	Text       string    `json:"text"` // alias for Body; kept for backward compat
	Visibility string    `json:"visibility"`
	CreatedAt  time.Time `json:"createdAt"`
	LikeCount  int       `json:"likeCount"`
	ReplyCount int       `json:"replyCount"`
	ReplyTo    *string   `json:"replyTo"`
	LikedByMe  bool      `json:"likedByMe"`
}

// DM is the public API response shape for direct messages.
// v18: uses "from"/"to" (not "fromUsername"/"toUsername") to match xclone-web-v18 api.ts.
type DM struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Body      string    `json:"body"`
	Text      string    `json:"text"` // alias for Body; kept for backward compat
	CreatedAt time.Time `json:"createdAt"`
}

// generateID returns a 32-char hex string (16 random bytes).
func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateToken returns a 64-char hex string (32 random bytes).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// createSession inserts a session row and sets the cookie.
func createSession(db *sql.DB, w http.ResponseWriter, userID string) error {
	token, err := generateToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(30 * 24 * time.Hour)
	_, err = db.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, expires,
	)
	if err != nil {
		return err
	}
	setSessionCookie(w, token)
	return nil
}

// getSessionUserID reads the session cookie and returns the user_id, or ("", false).
func getSessionUserID(db *sql.DB, r *http.Request) (string, bool) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return "", false
	}
	var uid string
	err = db.QueryRow(
		`SELECT user_id FROM sessions WHERE token = $1 AND expires_at > NOW()`,
		cookie.Value,
	).Scan(&uid)
	if err != nil {
		return "", false
	}
	return uid, true
}

// getUserByID fetches a DBUser by ID.
func getUserByID(db *sql.DB, id string) (*DBUser, error) {
	var u DBUser
	err := db.QueryRow(
		`SELECT id, username, display_name, bio, preferences, created_at FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.Preferences, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// getUserByUsername fetches a DBUser by username.
func getUserByUsername(db *sql.DB, username string) (*DBUser, error) {
	var u DBUser
	err := db.QueryRow(
		`SELECT id, username, display_name, bio, preferences, created_at FROM users WHERE username = $1`, username,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.Preferences, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// setSessionCookie sets the HttpOnly session cookie.
// Secure is false so cookies work over HTTP in Docker deployments.
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})
}

// clearSessionCookie removes the session cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// requireAuth middleware validates the session and injects the user into context.
func requireAuth(db *sql.DB, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := getSessionUserID(db, r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "not authenticated"))
			return
		}
		// Store uid in context via X-UID header (simple approach)
		r.Header.Set("X-UID", uid)
		u, err := getUserByID(db, uid)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "session invalid"))
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, u)
		next(w, r.WithContext(ctx))
	}
}

// currentUser retrieves the authenticated user from context.
func currentUser(r *http.Request) *DBUser {
	u, _ := r.Context().Value(ctxUser).(*DBUser)
	return u
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint
}

// readJSON decodes the request body as JSON.
func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// errResp returns a standardised error JSON map.
func errResp(code, message string) map[string]string {
	return map[string]string{"error": code, "message": message}
}
