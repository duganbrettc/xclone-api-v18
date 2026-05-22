package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"
)

// scanPostRow scans a post row from DB (9-column form without author_id).
func scanPostRow(rows *sql.Rows) (Post, error) {
	var p Post
	var replyTo sql.NullString
	err := rows.Scan(&p.ID, &p.Author, &p.Text, &p.Visibility, &replyTo, &p.CreatedAt,
		&p.LikeCount, &p.ReplyCount, &p.LikedByMe)
	if err != nil {
		return p, err
	}
	if replyTo.Valid {
		p.ReplyTo = &replyTo.String
	}
	return p, nil
}

// getPostFull fetches a post plus its author_id for access-control checks.
// Returns (post, authorID, error).
func getPostFull(db *sql.DB, postID, viewerID string) (*Post, string, error) {
	var p Post
	var replyTo sql.NullString
	var authorID string
	var likedByMe bool
	err := db.QueryRow(`
		SELECT p.id, u.username, p.text, p.visibility, p.reply_to, p.created_at,
		       p.author_id,
		       (SELECT COUNT(*) FROM likes WHERE post_id = p.id),
		       (SELECT COUNT(*) FROM posts WHERE reply_to = p.id),
		       CASE WHEN $2 != '' THEN EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $2)
		            ELSE FALSE END
		FROM posts p JOIN users u ON u.id = p.author_id
		WHERE p.id = $1
	`, postID, viewerID).Scan(
		&p.ID, &p.Author, &p.Text, &p.Visibility, &replyTo, &p.CreatedAt,
		&authorID, &p.LikeCount, &p.ReplyCount, &likedByMe,
	)
	if err != nil {
		return nil, "", err
	}
	p.LikedByMe = likedByMe
	if replyTo.Valid {
		p.ReplyTo = &replyTo.String
	}
	return &p, authorID, nil
}

// getPostByID returns a single post by ID with like/reply counts.
func getPostByID(db *sql.DB, postID, viewerID string) (*Post, error) {
	row := db.QueryRow(`
		SELECT p.id, u.username, p.text, p.visibility, p.reply_to, p.created_at,
			(SELECT COUNT(*) FROM likes WHERE post_id = p.id),
			(SELECT COUNT(*) FROM posts WHERE reply_to = p.id),
			CASE WHEN $2 != '' THEN EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $2)
			     ELSE FALSE END
		FROM posts p JOIN users u ON u.id = p.author_id
		WHERE p.id = $1
	`, postID, viewerID)
	var p Post
	var replyTo sql.NullString
	var likedByMe bool
	err := row.Scan(&p.ID, &p.Author, &p.Text, &p.Visibility, &replyTo, &p.CreatedAt,
		&p.LikeCount, &p.ReplyCount, &likedByMe)
	if err != nil {
		return nil, err
	}
	p.LikedByMe = likedByMe
	if replyTo.Valid {
		p.ReplyTo = &replyTo.String
	}
	return &p, nil
}

// fetchPostsByAuthor returns posts by authorID, visibility-filtered for viewerID.
func fetchPostsByAuthor(db *sql.DB, authorID, viewerID string) []Post {
	var q string
	var args []any

	if viewerID == "" {
		// Unauthenticated: public posts only
		q = `SELECT p.id, u.username, p.text, p.visibility, p.reply_to, p.created_at,
			(SELECT COUNT(*) FROM likes WHERE post_id = p.id),
			(SELECT COUNT(*) FROM posts WHERE reply_to = p.id),
			FALSE
		FROM posts p JOIN users u ON u.id = p.author_id
		WHERE p.author_id = $1 AND p.visibility = 'public'
		ORDER BY p.created_at DESC LIMIT 50`
		args = []any{authorID}
	} else if viewerID == authorID {
		// Own profile: see all posts
		q = `SELECT p.id, u.username, p.text, p.visibility, p.reply_to, p.created_at,
			(SELECT COUNT(*) FROM likes WHERE post_id = p.id),
			(SELECT COUNT(*) FROM posts WHERE reply_to = p.id),
			EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $2)
		FROM posts p JOIN users u ON u.id = p.author_id
		WHERE p.author_id = $1
		ORDER BY p.created_at DESC LIMIT 50`
		args = []any{authorID, viewerID}
	} else {
		// Authenticated viewer: public OR if viewer follows the author
		q = `SELECT p.id, u.username, p.text, p.visibility, p.reply_to, p.created_at,
			(SELECT COUNT(*) FROM likes WHERE post_id = p.id),
			(SELECT COUNT(*) FROM posts WHERE reply_to = p.id),
			EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $2)
		FROM posts p JOIN users u ON u.id = p.author_id
		WHERE p.author_id = $1
		  AND (p.visibility = 'public' OR $2 = p.author_id
		       OR EXISTS(SELECT 1 FROM follows WHERE follower_id = $2 AND following_id = p.author_id))
		ORDER BY p.created_at DESC LIMIT 50`
		args = []any{authorID, viewerID}
	}

	rows, err := db.Query(q, args...)
	if err != nil {
		return []Post{}
	}
	defer rows.Close()

	posts := []Post{}
	for rows.Next() {
		p, err := scanPostRow(rows)
		if err == nil {
			posts = append(posts, p)
		}
	}
	return posts
}

func handleCreatePost(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		var body struct {
			Text       string  `json:"text"`
			Visibility string  `json:"visibility"`
			ReplyTo    *string `json:"replyTo"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		if body.Text == "" {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "text required"))
			return
		}
		if utf8.RuneCountInString(body.Text) > 280 {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "text exceeds 280 characters"))
			return
		}
		if body.Visibility == "" {
			body.Visibility = "public"
		}
		if body.Visibility != "public" && body.Visibility != "private" {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "visibility must be public or private"))
			return
		}

		id, err := generateID()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not generate ID"))
			return
		}

		var replyTo sql.NullString
		if body.ReplyTo != nil && *body.ReplyTo != "" {
			replyTo = sql.NullString{String: *body.ReplyTo, Valid: true}
		}

		_, err = db.Exec(
			`INSERT INTO posts (id, author_id, text, visibility, reply_to) VALUES ($1, $2, $3, $4, $5)`,
			id, caller.ID, body.Text, body.Visibility, replyTo,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not create post"))
			return
		}

		p, err := getPostByID(db, id, caller.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not fetch post"))
			return
		}
		writeJSON(w, http.StatusCreated, p)
	}
}

func handleGetPost(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		postID := r.PathValue("id")
		viewerID, _ := getSessionUserID(db, r)

		p, authorID, err := getPostFull(db, postID, viewerID)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "Post not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "Post not found"))
			return
		}

		// Block check: if viewer has blocked author or vice versa → 404 (not 403, to avoid leaking)
		if viewerID != "" && viewerID != authorID {
			if isBlocked(db, viewerID, authorID) {
				writeJSON(w, http.StatusNotFound, errResp("not_found", "Post not found"))
				return
			}
		}

		// Visibility check: private posts visible only to author and followers → 404 (not 403)
		if p.Visibility != "public" {
			if viewerID == "" {
				writeJSON(w, http.StatusNotFound, errResp("not_found", "Post not found"))
				return
			}
			if viewerID != authorID {
				var follows bool
				db.QueryRow(
					`SELECT EXISTS(SELECT 1 FROM follows WHERE follower_id = $1 AND following_id = $2)`,
					viewerID, authorID,
				).Scan(&follows) //nolint
				if !follows {
					writeJSON(w, http.StatusNotFound, errResp("not_found", "Post not found"))
					return
				}
			}
		}

		// Fetch replies (visibility-filtered for viewer)
		replyRows, err := db.Query(`
			SELECT p.id, u.username, p.text, p.visibility, p.reply_to, p.created_at,
				(SELECT COUNT(*) FROM likes WHERE post_id = p.id),
				(SELECT COUNT(*) FROM posts WHERE reply_to = p.id),
				CASE WHEN $2 != '' THEN EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $2)
				     ELSE FALSE END
			FROM posts p JOIN users u ON u.id = p.author_id
			WHERE p.reply_to = $1
			  AND (p.visibility = 'public' OR p.author_id = $2
			       OR ($2 != '' AND EXISTS(SELECT 1 FROM follows WHERE follower_id = $2 AND following_id = p.author_id)))
			ORDER BY p.created_at ASC
		`, postID, viewerID)
		replies := []Post{}
		if err == nil {
			defer replyRows.Close()
			for replyRows.Next() {
				rp, err := scanPostRow(replyRows)
				if err == nil {
					replies = append(replies, rp)
				}
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"post":    p,
			"replies": replies,
		})
	}
}

func handleLikePost(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		postID := r.PathValue("id")

		var exists bool
		db.QueryRow(`SELECT EXISTS(SELECT 1 FROM posts WHERE id = $1)`, postID).Scan(&exists) //nolint
		if !exists {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "Post not found"))
			return
		}
		db.Exec(`INSERT INTO likes (user_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, caller.ID, postID) //nolint
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleUnlikePost(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		postID := r.PathValue("id")
		db.Exec(`DELETE FROM likes WHERE user_id = $1 AND post_id = $2`, caller.ID, postID) //nolint
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleCreateReply(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		parentID := r.PathValue("id")

		var parentExists bool
		db.QueryRow(`SELECT EXISTS(SELECT 1 FROM posts WHERE id = $1)`, parentID).Scan(&parentExists) //nolint
		if !parentExists {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "Post not found"))
			return
		}

		var body struct {
			Text string `json:"text"`
		}
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "Invalid JSON"))
			return
		}
		if body.Text == "" {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "text required"))
			return
		}
		if utf8.RuneCountInString(body.Text) > 280 {
			writeJSON(w, http.StatusBadRequest, errResp("bad_request", "text exceeds 280 characters"))
			return
		}

		id, err := generateID()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not generate ID"))
			return
		}

		_, err = db.Exec(
			`INSERT INTO posts (id, author_id, text, visibility, reply_to) VALUES ($1, $2, $3, 'public', $4)`,
			id, caller.ID, body.Text, parentID,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not create reply"))
			return
		}

		p, err := getPostByID(db, id, caller.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not fetch reply"))
			return
		}
		writeJSON(w, http.StatusCreated, p)
	}
}

func handleDeletePost(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)
		postID := r.PathValue("id")

		var authorID string
		err := db.QueryRow(`SELECT author_id FROM posts WHERE id = $1`, postID).Scan(&authorID)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, errResp("not_found", "Post not found"))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not fetch post"))
			return
		}
		if authorID != caller.ID {
			writeJSON(w, http.StatusForbidden, errResp("forbidden", "You can only delete your own posts"))
			return
		}

		// Delete likes and replies cascade (or just the post; FK cascade handles rest)
		db.Exec(`DELETE FROM likes WHERE post_id = $1`, postID)             //nolint
		db.Exec(`DELETE FROM posts WHERE reply_to = $1`, postID)            //nolint
		db.Exec(`DELETE FROM posts WHERE id = $1`, postID)                  //nolint
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleTimeline(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := currentUser(r)

		limitStr := r.URL.Query().Get("limit")
		limit := 50
		if limitStr != "" {
			var l int
			fmt.Sscanf(limitStr, "%d", &l)
			if l > 0 && l <= 200 {
				limit = l
			}
		}

		beforeStr := r.URL.Query().Get("before")
		var beforeTime *time.Time
		if beforeStr != "" {
			t, err := time.Parse(time.RFC3339, beforeStr)
			if err == nil {
				beforeTime = &t
			}
		}

		var rows *sql.Rows
		var err error
		if beforeTime == nil {
			rows, err = db.Query(`
				SELECT p.id, u.username, p.text, p.visibility, p.reply_to, p.created_at,
					(SELECT COUNT(*) FROM likes WHERE post_id = p.id),
					(SELECT COUNT(*) FROM posts WHERE reply_to = p.id),
					EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $1)
				FROM posts p
				JOIN users u ON u.id = p.author_id
				WHERE (p.author_id = $1
				       OR p.author_id IN (SELECT following_id FROM follows WHERE follower_id = $1))
				  AND p.author_id NOT IN (SELECT blocked_id FROM blocks WHERE blocker_id = $1)
				  AND p.author_id NOT IN (SELECT blocker_id FROM blocks WHERE blocked_id = $1)
				  AND (p.visibility = 'public'
				       OR p.author_id = $1
				       OR EXISTS(SELECT 1 FROM follows WHERE follower_id = $1 AND following_id = p.author_id))
				ORDER BY p.created_at DESC
				LIMIT $2
			`, caller.ID, limit)
		} else {
			rows, err = db.Query(`
				SELECT p.id, u.username, p.text, p.visibility, p.reply_to, p.created_at,
					(SELECT COUNT(*) FROM likes WHERE post_id = p.id),
					(SELECT COUNT(*) FROM posts WHERE reply_to = p.id),
					EXISTS(SELECT 1 FROM likes WHERE post_id = p.id AND user_id = $1)
				FROM posts p
				JOIN users u ON u.id = p.author_id
				WHERE (p.author_id = $1
				       OR p.author_id IN (SELECT following_id FROM follows WHERE follower_id = $1))
				  AND p.author_id NOT IN (SELECT blocked_id FROM blocks WHERE blocker_id = $1)
				  AND p.author_id NOT IN (SELECT blocker_id FROM blocks WHERE blocked_id = $1)
				  AND (p.visibility = 'public'
				       OR p.author_id = $1
				       OR EXISTS(SELECT 1 FROM follows WHERE follower_id = $1 AND following_id = p.author_id))
				  AND p.created_at < $3
				ORDER BY p.created_at DESC
				LIMIT $2
			`, caller.ID, limit, *beforeTime)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResp("internal", "Could not fetch timeline"))
			return
		}
		defer rows.Close()

		posts := []Post{}
		for rows.Next() {
			p, err := scanPostRow(rows)
			if err == nil {
				posts = append(posts, p)
			}
		}

		var nextBefore *string
		if len(posts) == limit {
			s := posts[len(posts)-1].CreatedAt.UTC().Format(time.RFC3339)
			nextBefore = &s
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"posts":      posts,
			"nextBefore": nextBefore,
		})
	}
}
