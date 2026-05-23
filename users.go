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
		writeJSON(w, http.StatusOK, map[string]any{
			"user": map[string]any{
				"id":           u.ID,
				"username":     u.Username,
				"display_name": u.DisplayName,
				"bio":          u.Bio,
				"created_at":   u.CreatedAt,
			},
		})
	}
}

func handleUpdateMe(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var body struct {
			DisplayName  *string         `json:"displayName"`
			DisplayName2 *string         `json:"display_name"`
			Bio          *string         `json:"bio"`
			Privacy      *string         `json:"privacy"`
			Preferences  json.RawMessage `json:"preferences"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		// Accept both camelCase (displayName) and snake_case (display_name) per contract
		displayName := body.DisplayName
		if displayName == nil {
			displayName = body.DisplayName2
		}
		if displayName != nil {
			db.Exec(`UPDATE users SET display_name = $1 WHERE id = $2`, *displayName, u.ID) //nolint
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
		writeJSON(w, http.StatusOK, map[string]any{
			"user": map[string]any{
				"id":           updated.ID,
				"username":     updated.Username,
				"display_name": updated.DisplayName,
				"bio":          updated.Bio,
				"created_at":   updated.CreatedAt,
			},
		})
	}
}

func handleGetSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var prefsJSON string
		if err := db.QueryRow(`SELECT preferences FROM users WHERE id = $1`, u.ID).Scan(&prefsJSON); err != nil {
			prefsJSON = "{}"
		}
		// Contract: {"settings": {"default_post_visibility": "public|private", "email": "string?"}}
		var prefs map[string]any
		if err := json.Unmarshal([]byte(prefsJSON), &prefs); err != nil {
			prefs = map[string]any{}
		}
		visibility, _ := prefs["default_post_visibility"].(string)
		if visibility == "" {
			visibility = "public"
		}
		email, _ := prefs["email"].(string)
		writeJSON(w, http.StatusOK, map[string]any{
			"settings": map[string]any{
				"default_post_visibility": visibility,
				"email":                   email,
			},
		})
	}
}

func handleUpdateSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		var body struct {
			DefaultPostVisibility *string `json:"default_post_visibility"`
			Email                 *string `json:"email"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		// Load existing prefs
		var prefsJSON string
		if err := db.QueryRow(`SELECT preferences FROM users WHERE id = $1`, u.ID).Scan(&prefsJSON); err != nil {
			prefsJSON = "{}"
		}
		var prefs map[string]any
		if err := json.Unmarshal([]byte(prefsJSON), &prefs); err != nil {
			prefs = map[string]any{}
		}
		if body.DefaultPostVisibility != nil {
			prefs["default_post_visibility"] = *body.DefaultPostVisibility
		}
		if body.Email != nil {
			prefs["email"] = *body.Email
		}
		updated, _ := json.Marshal(prefs)
		db.Exec(`UPDATE users SET preferences = $1 WHERE id = $2`, string(updated), u.ID) //nolint

		visibility, _ := prefs["default_post_visibility"].(string)
		if visibility == "" {
			visibility = "public"
		}
		email, _ := prefs["email"].(string)
		writeJSON(w, http.StatusOK, map[string]any{
			"settings": map[string]any{
				"default_post_visibility": visibility,
				"email":                   email,
			},
		})
	}
}

// handlePostSettings handles POST /api/users/me/settings per the binding contract.
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
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleGetUserProfile handles GET /api/users/{username}.
// Returns the contract-spec user shape: {user: {id, username, display_name, bio,
// is_following, is_blocked, post_count, follower_count, following_count}}.
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

		var followerCount, followingCount, postCount int
		db.QueryRow(`SELECT COUNT(*) FROM follows WHERE followee_id = $1`, profile.ID).Scan(&followerCount)   //nolint
		db.QueryRow(`SELECT COUNT(*) FROM follows WHERE follower_id = $1`, profile.ID).Scan(&followingCount) //nolint
		db.QueryRow(`SELECT COUNT(*) FROM posts WHERE author_id = $1 AND deleted_at IS NULL`, profile.ID).Scan(&postCount) //nolint

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
					"id":              profile.ID,
					"username":        profile.Username,
					"display_name":    profile.DisplayName,
					"bio":             "",
					"is_following":    iFollow,
					"is_blocked":      iBlock,
					"post_count":      0,
					"follower_count":  followerCount,
					"following_count": followingCount,
					"is_private":      true,
				},
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"user": map[string]any{
				"id":              profile.ID,
				"username":        profile.Username,
				"display_name":    profile.DisplayName,
				"bio":             profile.Bio,
				"is_following":    iFollow,
				"is_blocked":      iBlock,
				"post_count":      postCount,
				"follower_count":  followerCount,
				"following_count": followingCount,
			},
		})
	}
}

// handleGetUserPosts handles GET /api/users/{username}/posts.
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
			SELECT u.id, u.username, u.display_name, u.bio, u.created_at
			FROM follows f
			JOIN users u ON u.id = f.follower_id
			WHERE f.followee_id = $1
			ORDER BY f.created_at DESC
		`, profile.ID)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"users": []any{}})
			return
		}
		defer rows.Close()
		users := []map[string]any{}
		for rows.Next() {
			var u DBUser
			rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.CreatedAt) //nolint
			users = append(users, map[string]any{
				"id":           u.ID,
				"username":     u.Username,
				"display_name": u.DisplayName,
				"bio":          u.Bio,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
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
			SELECT u.id, u.username, u.display_name, u.bio, u.created_at
			FROM follows f
			JOIN users u ON u.id = f.followee_id
			WHERE f.follower_id = $1
			ORDER BY f.created_at DESC
		`, profile.ID)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"users": []any{}})
			return
		}
		defer rows.Close()
		users := []map[string]any{}
		for rows.Next() {
			var u DBUser
			rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.CreatedAt) //nolint
			users = append(users, map[string]any{
				"id":           u.ID,
				"username":     u.Username,
				"display_name": u.DisplayName,
				"bio":          u.Bio,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
	}
}
