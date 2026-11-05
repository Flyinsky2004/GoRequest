package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// newHandler builds the demo server's routes. It is a separate function so the
// tests can mount the exact same handler on an httptest server without going
// through main or a real listener.
func newHandler() http.Handler {
	users := newUserStore()

	mux := http.NewServeMux()

	// Compatibility endpoints (also used by the existing sample requests).
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/auth/login", handleLogin)
	mux.HandleFunc("/users/me", handleMe)

	// Request/response inspection helpers.
	mux.HandleFunc("/echo", handleEcho)
	mux.HandleFunc("/status/", handleStatus)
	mux.HandleFunc("/delay", handleDelay)
	mux.HandleFunc("/redirect", handleRedirect)
	mux.HandleFunc("/redirect/", handleRedirectChain)
	mux.HandleFunc("/format/", handleFormat)
	mux.HandleFunc("/response/multi", handleMultiHeader)
	mux.HandleFunc("/large", handleLarge)

	// In-memory user collection with pagination and CRUD.
	mux.HandleFunc("/users", users.handleCollection)
	mux.HandleFunc("/users/", users.handleItem)

	// Fallback so unknown paths still return a structured JSON error.
	mux.HandleFunc("/", handleNotFound)

	return mux
}

// writeJSON writes v as JSON with the given status. It is the single place that
// sets the demo marker header so responses are easy to recognize in the app.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Demo-Server", "gorequest-demo")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// errorBody is the uniform shape for error responses.
type errorBody struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// writeError writes a structured JSON error. Never include secrets or internal
// paths here: the demo surfaces only user-facing reasons.
func writeError(w http.ResponseWriter, status int, message, detail string) {
	writeJSON(w, status, errorBody{Error: message, Detail: detail})
}

// requireMethod enforces the allowed methods for an endpoint, returning a JSON
// 405 with an Allow header instead of the default plain-text response.
func requireMethod(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for _, m := range allowed {
		if r.Method == m {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed", "allowed: "+strings.Join(allowed, ", "))
	return false
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not found", "no route for "+r.URL.Path)
}

// handleHealth mirrors the original health check so existing samples keep
// working unchanged.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleLogin keeps the original contract: POST JSON credentials, respond with
// data.accessToken. The token is obviously fake and derived from the username so
// callers (and the bearer sample) can see a stable, non-secret value.
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return
	}
	if strings.TrimSpace(in.Username) == "" || in.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"accessToken": "demo-access-token-" + in.Username,
			"tokenType":   "Bearer",
			"expiresIn":   3600,
		},
	})
}

// handleMe keeps the original bearer-only contract used by users.me.
func handleMe(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token", "send Authorization: Bearer <token>")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"username": "alice",
			"settings": map[string]any{"theme": "dark", "locale": "zh-CN"},
		},
	})
}
