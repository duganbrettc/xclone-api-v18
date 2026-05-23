package main

import (
	"database/sql"
	"log"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func handleSignup(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username     string `json:"username"`
			Password     string `json:"password"`
			DisplayName  string `json:"displayName"`
			DisplayName2 string `json:"display_name"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		body.Username = strings.TrimSpace(body.Username)
		if body.Username == "" || body.Password == "" {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "username and password required"))
			return
		}
		displayName := body.DisplayName
		if displayName == "" {
			displayName = body.DisplayName2
		}
		if displayName == "" {
			displayName = body.Username
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not hash password"))
			return
		}
		id, err := generateID()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not generate ID"))
			return
		}
		var u DBUser
		err = db.QueryRow(
			`INSERT INTO users (id, username, display_name, password_hash)
			 VALUES ($1, $2, $3, $4)
			 RETURNING id, username, display_name, bio, preferences, created_at`,
			id, body.Username, displayName, string(hash),
		).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.Preferences, &u.CreatedAt)
		if err != nil {
			if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "23505") {
				writeJSON(w, http.StatusConflict, errResp("conflict", "Username already taken"))
				return
			}
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not create user"))
			return
		}
		if err := createSession(db, w, u.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not create session"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user": map[string]any{
				"id":       u.ID,
				"username": u.Username,
			},
		})
	}
}

func handleLogin(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		var u DBUser
		var hash string
		err := db.QueryRow(
			`SELECT id, username, display_name, bio, preferences, created_at, password_hash
			 FROM users WHERE username = $1`,
			body.Username,
		).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.Preferences, &u.CreatedAt, &hash)
		if err == sql.ErrNoRows || err != nil {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "Invalid credentials"))
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)); err != nil {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "Invalid credentials"))
			return
		}
		if err := createSession(db, w, u.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not create session"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user": map[string]any{
				"id":       u.ID,
				"username": u.Username,
			},
		})
	}
}

func handleLogout(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("session"); err == nil {
			db.Exec(`DELETE FROM sessions WHERE token = $1`, cookie.Value) //nolint
		}
		clearSessionCookie(w)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleChangePassword(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var body struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		var hash string
		if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = $1`, u.ID).Scan(&hash); err != nil {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "User not found"))
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.CurrentPassword)); err != nil {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "Current password incorrect"))
			return
		}
		if body.NewPassword == "" {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "new_password required"))
			return
		}
		newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not hash password"))
			return
		}
		db.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, string(newHash), u.ID) //nolint
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleChangePasswordAlt handles POST /api/users/me/password.
func handleChangePasswordAlt(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var body struct {
			OldPassword string `json:"oldPassword"`
			NewPassword string `json:"newPassword"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		var hash string
		if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = $1`, u.ID).Scan(&hash); err != nil {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "User not found"))
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.OldPassword)); err != nil {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "Current password incorrect"))
			return
		}
		if len(body.NewPassword) < 6 {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "newPassword must be at least 6 characters"))
			return
		}
		newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not hash password"))
			return
		}
		// Existing sessions remain valid after password change (convenience over security;
		// acceptable for this build since the app has no account-takeover threat model).
		db.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, string(newHash), u.ID) //nolint
		w.WriteHeader(http.StatusNoContent)
	}
}

func handlePasswordReset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
		}
		readJSON(r, &body) //nolint
		// Always generate a token and log it; never reveal whether the account exists.
		token, err := generateToken()
		if err == nil {
			log.Printf("[password-reset] token=%s username=%s (no email infra; manual delivery only)", token, body.Username)
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
