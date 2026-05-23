package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = os.Getenv("API_PORT")
	}
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()

	// /healthz registered immediately so it responds before the DB is ready.
	mux.HandleFunc("GET /healthz", handleHealthz)
	// /api/healthz alias so nginx-proxied health checks work (/api/* → api:PORT/api/* preserves path).
	mux.HandleFunc("GET /api/healthz", handleHealthz)

	// OpenAPI spec (no DB needed)
	mux.HandleFunc("GET /api/openapi.json", handleOpenAPI)

	// Start the server in a goroutine so /healthz is reachable right away.
	go func() {
		log.Printf("xclone-api listening on :%s", port)
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Connect to and migrate the database (may block retrying until Postgres is up).
	db := initDB()

	// Register all routes that need the DB after the connection is ready.
	// Auth routes
	mux.HandleFunc("POST /api/auth/signup", handleSignup(db))
	mux.HandleFunc("POST /api/auth/login", handleLogin(db))
	mux.HandleFunc("POST /api/auth/logout", requireAuth(db, handleLogout(db)))
	mux.HandleFunc("GET /api/auth/me", requireAuth(db, handleGetMe(db)))
	mux.HandleFunc("POST /api/auth/password", requireAuth(db, handleChangePassword(db)))
	mux.HandleFunc("POST /api/auth/password-reset", handlePasswordReset())

	// Current user routes (registered before /{username} to take precedence)
	mux.HandleFunc("GET /api/users/me", requireAuth(db, handleGetMe(db)))
	mux.HandleFunc("PATCH /api/users/me", requireAuth(db, handleUpdateMe(db)))
	mux.HandleFunc("GET /api/users/me/settings", requireAuth(db, handleGetSettings(db)))
	mux.HandleFunc("PATCH /api/users/me/settings", requireAuth(db, handleUpdateSettings(db)))
	mux.HandleFunc("POST /api/users/me/settings", requireAuth(db, handlePostSettings(db)))
	mux.HandleFunc("POST /api/users/me/password", requireAuth(db, handleChangePasswordAlt(db)))

	// Contract-spec settings routes: GET /api/settings, PATCH /api/settings
	mux.HandleFunc("GET /api/settings", requireAuth(db, handleGetSettings(db)))
	mux.HandleFunc("PATCH /api/settings", requireAuth(db, handleUpdateSettings(db)))

	// Public user profile routes
	mux.HandleFunc("GET /api/users/{username}", handleGetUserProfile(db))
	mux.HandleFunc("GET /api/users/{username}/posts", handleGetUserPosts(db))
	mux.HandleFunc("GET /api/users/{username}/followers", handleGetFollowers(db))
	mux.HandleFunc("GET /api/users/{username}/following", handleGetFollowing(db))

	// OpenAPI-style follow/block routes (per-user)
	mux.HandleFunc("POST /api/users/{username}/follow", requireAuth(db, handleFollowByUsername(db)))
	mux.HandleFunc("DELETE /api/users/{username}/follow", requireAuth(db, handleUnfollowByUsername(db)))
	mux.HandleFunc("POST /api/users/{username}/block", requireAuth(db, handleBlockByUsername(db)))
	mux.HandleFunc("DELETE /api/users/{username}/block", requireAuth(db, handleUnblockByUsername(db)))

	// Cascade-style follow/block routes
	mux.HandleFunc("POST /api/follows", requireAuth(db, handleFollow(db)))
	mux.HandleFunc("DELETE /api/follows/{username}", requireAuth(db, handleUnfollow(db)))
	mux.HandleFunc("POST /api/blocks", requireAuth(db, handleBlock(db)))
	mux.HandleFunc("DELETE /api/blocks/{username}", requireAuth(db, handleUnblock(db)))

	// Contract-spec singular follow/block routes (return {isFollowing}/{isBlocked})
	mux.HandleFunc("POST /api/follow/{username}", requireAuth(db, handleFollowContract(db)))
	mux.HandleFunc("DELETE /api/follow/{username}", requireAuth(db, handleUnfollowContract(db)))
	mux.HandleFunc("POST /api/block/{username}", requireAuth(db, handleBlockContract(db)))
	mux.HandleFunc("DELETE /api/block/{username}", requireAuth(db, handleUnblockContract(db)))

	// Timeline
	mux.HandleFunc("GET /api/timeline", requireAuth(db, handleTimeline(db)))

	// Posts
	mux.HandleFunc("GET /api/posts", handleListPublicPosts(db))
	mux.HandleFunc("POST /api/posts", requireAuth(db, handleCreatePost(db)))
	mux.HandleFunc("GET /api/posts/{id}", handleGetPost(db))
	mux.HandleFunc("GET /api/posts/{id}/replies", handleGetReplies(db))
	mux.HandleFunc("POST /api/posts/{id}/like", requireAuth(db, handleLikePost(db)))
	mux.HandleFunc("DELETE /api/posts/{id}/like", requireAuth(db, handleUnlikePost(db)))
	mux.HandleFunc("POST /api/posts/{id}/likes", requireAuth(db, handleLikePost(db)))
	mux.HandleFunc("DELETE /api/posts/{id}/likes", requireAuth(db, handleUnlikePost(db)))
	mux.HandleFunc("DELETE /api/posts/{id}", requireAuth(db, handleDeletePost(db)))
	mux.HandleFunc("POST /api/posts/{id}/replies", requireAuth(db, handleCreateReply(db)))
	// Contract-spec singular reply path
	mux.HandleFunc("POST /api/posts/{id}/reply", requireAuth(db, handleCreateReply(db)))

	// Direct messages — /api/messages/threads (contract-spec)
	mux.HandleFunc("GET /api/messages/threads", requireAuth(db, handleListConversations(db)))
	mux.HandleFunc("GET /api/messages/threads/{username}", requireAuth(db, handleGetConversation(db)))
	mux.HandleFunc("POST /api/messages/threads/{username}", requireAuth(db, handleSendDMToUsername(db)))

	// Direct messages – cascade-style /api/messages routes
	mux.HandleFunc("GET /api/messages", requireAuth(db, handleListConversations(db)))
	mux.HandleFunc("POST /api/messages", requireAuth(db, handleSendDM(db)))
	mux.HandleFunc("GET /api/messages/{username}", requireAuth(db, handleGetConversation(db)))
	mux.HandleFunc("POST /api/messages/{username}", requireAuth(db, handleSendDMToUsername(db)))

	// Direct messages – OpenAPI-style /api/dms routes (same logic)
	mux.HandleFunc("GET /api/dms", requireAuth(db, handleListConversations(db)))
	mux.HandleFunc("POST /api/dms/{username}", requireAuth(db, handleSendDMToUsername(db)))
	mux.HandleFunc("GET /api/dms/{username}", requireAuth(db, handleGetConversation(db)))

	// Contract-spec singular /api/dm routes (same logic)
	mux.HandleFunc("POST /api/dm/{username}", requireAuth(db, handleSendDMToUsername(db)))
	mux.HandleFunc("GET /api/dm/{username}", requireAuth(db, handleGetConversation(db)))

	log.Println("all routes registered, api fully ready")
	defer db.Close()
	select {} // block forever; server goroutine handles requests
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`)) //nolint
}
