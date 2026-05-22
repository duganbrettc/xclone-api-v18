package main

import (
	"database/sql"
	"net/http"
)

// isBlocked returns true if a block exists in either direction between userA and userB.
// Used by posts, timeline, and DM modules to enforce block semantics.
func isBlocked(db *sql.DB, userA, userB string) bool {
	var blocked bool
	db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM blocks
			WHERE (blocker_id=$1 AND blocked_id=$2)
			   OR (blocker_id=$2 AND blocked_id=$1)
		)
	`, userA, userB).Scan(&blocked) //nolint
	return blocked
}

// doFollow is the core follow logic: caller follows target.
func doFollow(db *sql.DB, callerID, targetID string) {
	db.Exec(`INSERT INTO follows (follower_id, following_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, //nolint
		callerID, targetID)
}

// doUnfollow is the core unfollow logic.
func doUnfollow(db *sql.DB, callerID, targetID string) {
	db.Exec(`DELETE FROM follows WHERE follower_id=$1 AND following_id=$2`, callerID, targetID) //nolint
}

// doBlock is the core block logic: remove follows then insert block.
func doBlock(db *sql.DB, blockerID, blockedID string) {
	db.Exec(`DELETE FROM follows WHERE (follower_id=$1 AND following_id=$2) OR (follower_id=$2 AND following_id=$1)`, //nolint
		blockerID, blockedID)
	db.Exec(`INSERT INTO blocks (blocker_id, blocked_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, //nolint
		blockerID, blockedID)
}

// doUnblock removes the block record.
func doUnblock(db *sql.DB, blockerID, blockedID string) {
	db.Exec(`DELETE FROM blocks WHERE blocker_id=$1 AND blocked_id=$2`, blockerID, blockedID) //nolint
}

// handleFollow handles POST /api/follows (body: {username}).
func handleFollow(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		var body struct {
			Username string `json:"username"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		target, err := getUserByUsername(db, body.Username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}
		if target.ID == caller.ID {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Cannot follow yourself"))
			return
		}
		doFollow(db, caller.ID, target.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleUnfollow handles DELETE /api/follows/{username}.
func handleUnfollow(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		doUnfollow(db, caller.ID, target.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleBlock handles POST /api/blocks (body: {username}).
func handleBlock(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		var body struct {
			Username string `json:"username"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		target, err := getUserByUsername(db, body.Username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}
		if target.ID == caller.ID {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Cannot block yourself"))
			return
		}
		doBlock(db, caller.ID, target.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleUnblock handles DELETE /api/blocks/{username}.
func handleUnblock(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		doUnblock(db, caller.ID, target.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleFollowByUsername handles POST /api/users/{username}/follow.
func handleFollowByUsername(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}
		if target.ID == caller.ID {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Cannot follow yourself"))
			return
		}
		doFollow(db, caller.ID, target.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleUnfollowByUsername handles DELETE /api/users/{username}/follow.
func handleUnfollowByUsername(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		doUnfollow(db, caller.ID, target.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleBlockByUsername handles POST /api/users/{username}/block.
func handleBlockByUsername(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}
		if target.ID == caller.ID {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Cannot block yourself"))
			return
		}
		doBlock(db, caller.ID, target.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleUnblockByUsername handles DELETE /api/users/{username}/block.
func handleUnblockByUsername(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		doUnblock(db, caller.ID, target.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleFollowContract handles POST /api/follow/{username} — returns {isFollowing:true}.
func handleFollowContract(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}
		if target.ID == caller.ID {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Cannot follow yourself"))
			return
		}
		doFollow(db, caller.ID, target.ID)
		writeJSON(w, http.StatusOK, map[string]bool{"isFollowing": true})
	}
}

// handleUnfollowContract handles DELETE /api/follow/{username} — returns {isFollowing:false}.
func handleUnfollowContract(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]bool{"isFollowing": false})
			return
		}
		doUnfollow(db, caller.ID, target.ID)
		writeJSON(w, http.StatusOK, map[string]bool{"isFollowing": false})
	}
}

// handleBlockContract handles POST /api/block/{username} — returns {isBlocked:true}.
func handleBlockContract(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}
		if target.ID == caller.ID {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Cannot block yourself"))
			return
		}
		doBlock(db, caller.ID, target.ID)
		writeJSON(w, http.StatusOK, map[string]bool{"isBlocked": true})
	}
}

// handleUnblockContract handles DELETE /api/block/{username} — returns {isBlocked:false}.
func handleUnblockContract(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		target, err := getUserByUsername(db, username)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]bool{"isBlocked": false})
			return
		}
		doUnblock(db, caller.ID, target.ID)
		writeJSON(w, http.StatusOK, map[string]bool{"isBlocked": false})
	}
}
