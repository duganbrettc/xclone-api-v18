package main

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

func handleGetMe(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		writeJSON(w, http.StatusOK, u.toUser())
	}
}

func handleUpdateMe(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var body struct {
			DisplayName *string         `json:"displayName"`
			Bio         *string         `json:"bio"`
			Privacy     *string         `json:"privacy"`
			Preferences json.RawMessage `json:"preferences"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		if body.DisplayName != nil {
			db.Exec(`UPDATE users SET display_name = $1 WHERE id = $2`, *body.DisplayName, u.ID) //nolint
		}
		if body.Bio != nil {
			db.Exec(`UPDATE users SET bio = $1 WHERE id = $2`, *body.Bio, u.ID) //nolint
		}
		if body.Privacy != nil && (*body.Privacy == "public" || *body.Privacy == "private") {
			db.Exec(`UPDATE users SET privacy = $1 WHERE id = $2`, *body.Privacy, u.ID) //nolint
		}
		if body.Preferences != nil {
			db.Exec(`UPDATE users SET preferences = $1 WHERE id = $2`, string(body.Preferences), u.ID) //nolint
		}
		updated, err := getUserByID(db, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not fetch user"))
			return
		}
		writeJSON(w, http.StatusOK, updated.toUser())
	}
}

func handleGetSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var prefsJSON string
		if err := db.QueryRow(`SELECT preferences FROM users WHERE id = $1`, u.ID).Scan(&prefsJSON); err != nil {
			prefsJSON = "{}"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(prefsJSON)) //nolint
	}
}

func handleUpdateSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var rawBody json.RawMessage
		if err := readJSON(r, &rawBody); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		db.Exec(`UPDATE users SET preferences = $1 WHERE id = $2`, string(rawBody), u.ID) //nolint
		var prefsJSON string
		if err := db.QueryRow(`SELECT preferences FROM users WHERE id = $1`, u.ID).Scan(&prefsJSON); err != nil {
			prefsJSON = string(rawBody)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(prefsJSON)) //nolint
	}
}

// handlePostSettings handles POST /api/users/me/settings per the binding contract.
// Body: {currentPassword?, newPassword?, email?, preferences?}. Returns {ok: true}.
func handlePostSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var body struct {
			CurrentPassword string          `json:"currentPassword"`
			NewPassword     string          `json:"newPassword"`
			Email           string          `json:"email"`
			Preferences     json.RawMessage `json:"preferences"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		if body.NewPassword != "" {
			var hash string
			if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = $1`, u.ID).Scan(&hash); err != nil {
				writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "User not found"))
				return
			}
			if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.CurrentPassword)); err != nil {
				writeJSON(w, http.StatusUnauthorized, errResp("unauthorized", "Current password incorrect"))
				return
			}
			newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not hash password"))
				return
			}
			db.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, string(newHash), u.ID) //nolint
		}
		if body.Preferences != nil {
			db.Exec(`UPDATE users SET preferences = $1 WHERE id = $2`, string(body.Preferences), u.ID) //nolint
		}
		// Email is accepted but not persisted (no email column in v17 schema).
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func handleGetUserProfile(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		profile, err := getUserByUsername(db, username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}

		// Viewer (may be unauthenticated)
		viewerID, _ := getSessionUserID(db, r)

		// Follower/following counts
		var followerCount, followingCount int
		db.QueryRow(`SELECT COUNT(*) FROM follows WHERE followee_id = $1`, profile.ID).Scan(&followerCount)   //nolint
		db.QueryRow(`SELECT COUNT(*) FROM follows WHERE follower_id = $1`, profile.ID).Scan(&followingCount) //nolint

		var iFollow, iBlock bool
		if viewerID != "" && viewerID != profile.ID {
			db.QueryRow(`SELECT EXISTS(SELECT 1 FROM follows WHERE follower_id=$1 AND followee_id=$2)`, viewerID, profile.ID).Scan(&iFollow) //nolint
			db.QueryRow(`SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker_id=$1 AND blocked_id=$2)`, viewerID, profile.ID).Scan(&iBlock)     //nolint
		}

		// Privacy: private profiles show limited info to non-followers
		var profilePrivacy string
		db.QueryRow(`SELECT COALESCE(privacy, 'public') FROM users WHERE id = $1`, profile.ID).Scan(&profilePrivacy) //nolint
		if profilePrivacy == "private" && viewerID != profile.ID && !iFollow {
			writeJSON(w, http.StatusOK, map[string]any{
				"user": map[string]any{
					"username":    profile.Username,
					"displayName": profile.DisplayName,
					"bio":         "",
					"createdAt":   profile.CreatedAt,
					"isPrivate":   true,
				},
				"posts":          []Post{},
				"followerCount":  followerCount,
				"followingCount": followingCount,
				"iFollow":        iFollow,
				"iBlock":         iBlock,
			})
			return
		}

		posts := fetchPostsByAuthor(db, profile.ID, viewerID)
		writeJSON(w, http.StatusOK, map[string]any{
			"user":           profile.toUser(),
			"posts":          posts,
			"followerCount":  followerCount,
			"followingCount": followingCount,
			"iFollow":        iFollow,
			"iBlock":         iBlock,
		})
	}
}

// handleGetUserPosts handles GET /api/users/{username}/posts.
// Returns {"posts": [...]} with the user's posts, visibility-filtered for the viewer.
// Public posts are visible to everyone; private posts are visible only to the author
// and authenticated followers.
func handleGetUserPosts(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		profile, err := getUserByUsername(db, username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}

		// Viewer may be unauthenticated
		viewerID, _ := getSessionUserID(db, r)

		posts := fetchPostsByAuthor(db, profile.ID, viewerID)
		writeJSON(w, http.StatusOK, map[string]any{
			"posts": posts,
		})
	}
}

func handleGetFollowers(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		profile, err := getUserByUsername(db, username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}
		rows, err := db.Query(`
			SELECT u.username, u.display_name, u.bio, u.created_at
			FROM follows f
			JOIN users u ON u.id = f.follower_id
			WHERE f.followee_id = $1
			ORDER BY f.created_at DESC
		`, profile.ID)
		if err != nil {
			writeJSON(w, http.StatusOK, []User{})
			return
		}
		defer rows.Close()
		users := []User{}
		for rows.Next() {
			var u User
			rows.Scan(&u.Username, &u.DisplayName, &u.Bio, &u.CreatedAt) //nolint
			users = append(users, u)
		}
		writeJSON(w, http.StatusOK, users)
	}
}

func handleGetFollowing(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		profile, err := getUserByUsername(db, username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}
		rows, err := db.Query(`
			SELECT u.username, u.display_name, u.bio, u.created_at
			FROM follows f
			JOIN users u ON u.id = f.followee_id
			WHERE f.follower_id = $1
			ORDER BY f.created_at DESC
		`, profile.ID)
		if err != nil {
			writeJSON(w, http.StatusOK, []User{})
			return
		}
		defer rows.Close()
		users := []User{}
		for rows.Next() {
			var u User
			rows.Scan(&u.Username, &u.DisplayName, &u.Bio, &u.CreatedAt) //nolint
			users = append(users, u)
		}
		writeJSON(w, http.StatusOK, users)
	}
}
