package main

import (
	"database/sql"
	"net/http"
)

// handleListConversations handles GET /api/messages/threads, GET /api/dms and GET /api/messages.
// Returns one entry per distinct peer, with the most recent message per peer.
func handleListConversations(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)

		rows, err := db.QueryContext(r.Context(), `
			SELECT DISTINCT ON (peer_id)
				peer_u.username,
				m.id, from_u.username, to_u.username, m.body, m.created_at
			FROM (
				SELECT id, sender_id, recipient_id, body, created_at,
					CASE WHEN sender_id = $1 THEN recipient_id ELSE sender_id END AS peer_id
				FROM messages
				WHERE sender_id = $1 OR recipient_id = $1
			) m
			JOIN users peer_u ON peer_u.id = m.peer_id
			JOIN users from_u ON from_u.id = m.sender_id
			JOIN users to_u   ON to_u.id   = m.recipient_id
			ORDER BY peer_id, m.created_at DESC
		`, caller.ID)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"conversations": []any{}})
			return
		}
		defer rows.Close()

		type Conv struct {
			Peer        string `json:"peer"`
			LastMessage DM     `json:"lastMessage"`
		}

		convs := []Conv{}
		for rows.Next() {
			var peer string
			var dm DM
			if err := rows.Scan(&peer, &dm.ID, &dm.From, &dm.To, &dm.Body, &dm.CreatedAt); err != nil {
				continue
			}
			dm.Text = dm.Body
			convs = append(convs, Conv{Peer: peer, LastMessage: dm})
		}
		writeJSON(w, http.StatusOK, map[string]any{"conversations": convs})
	}
}

// handleGetConversation handles GET /api/messages/threads/{username}, GET /api/dms/{username}
// and GET /api/messages/{username}.
// Returns all messages between caller and {username} in ascending createdAt order.
func handleGetConversation(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")

		target, err := getUserByUsername(db, username)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusOK, map[string]any{"messages": []DM{}})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
			return
		}

		rows, err := db.QueryContext(r.Context(), `
			SELECT m.id, from_u.username, to_u.username, m.body, m.created_at
			FROM messages m
			JOIN users from_u ON from_u.id = m.sender_id
			JOIN users to_u   ON to_u.id   = m.recipient_id
			WHERE (m.sender_id = $1 AND m.recipient_id = $2)
			   OR (m.sender_id = $2 AND m.recipient_id = $1)
			ORDER BY m.created_at ASC
		`, caller.ID, target.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not fetch messages"))
			return
		}
		defer rows.Close()

		messages := []DM{}
		for rows.Next() {
			var dm DM
			if err := rows.Scan(&dm.ID, &dm.From, &dm.To, &dm.Body, &dm.CreatedAt); err == nil {
				dm.Text = dm.Body
				messages = append(messages, dm)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
	}
}

// handleSendDM handles POST /api/messages (cascade-style: body {to_username, text/body}).
func handleSendDM(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		var body struct {
			ToUsername string `json:"to_username"`
			Text       string `json:"text"`
			Body       string `json:"body"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		text := body.Body
		if text == "" {
			text = body.Text
		}
		if body.ToUsername == "" || text == "" {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "to_username and body required"))
			return
		}
		sendDM(db, w, r, caller, body.ToUsername, text)
	}
}

// handleSendDMToUsername handles POST /api/messages/threads/{username},
// POST /api/dms/{username}, POST /api/dm/{username} (body {text} or {body}).
func handleSendDMToUsername(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		username := r.PathValue("username")
		var body struct {
			Text string `json:"text"`
			Body string `json:"body"` // v18 spec field name
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		text := body.Body
		if text == "" {
			text = body.Text
		}
		if text == "" {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "body required"))
			return
		}
		if len(text) > 1000 {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "body exceeds 1000 characters"))
			return
		}
		sendDM(db, w, r, caller, username, text)
	}
}

// sendDM is the shared DM-send logic used by both route styles.
func sendDM(db *sql.DB, w http.ResponseWriter, r *http.Request, caller *DBUser, toUsername, text string) {
	target, err := getUserByUsername(db, toUsername)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, errResp("not_found", "User not found"))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp("internal", "Internal error"))
		return
	}
	if target.ID == caller.ID {
		writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Cannot DM yourself"))
		return
	}

	// 403 if the target has blocked the caller (or caller has blocked target).
	var blocked bool
	db.QueryRowContext(r.Context(), `
		SELECT EXISTS(
			SELECT 1 FROM blocks
			WHERE (blocker_id = $1 AND blocked_id = $2)
			   OR (blocker_id = $2 AND blocked_id = $1)
		)
	`, target.ID, caller.ID).Scan(&blocked) //nolint
	if blocked {
		writeJSON(w, http.StatusForbidden, errResp("forbidden", "Cannot send message: blocked"))
		return
	}

	id, err := generateID()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not generate ID"))
		return
	}

	var dm DM
	var ignoredSenderID, ignoredRecipientID string
	err = db.QueryRowContext(r.Context(), `
		INSERT INTO messages (id, sender_id, recipient_id, body)
		VALUES ($1, $2, $3, $4)
		RETURNING id, sender_id, recipient_id, body, created_at
	`, id, caller.ID, target.ID, text).Scan(&dm.ID, &ignoredSenderID, &ignoredRecipientID, &dm.Body, &dm.CreatedAt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not send message"))
		return
	}

	dm.From = caller.Username
	dm.To = target.Username
	dm.Text = dm.Body
	writeJSON(w, http.StatusCreated, dm)
}
