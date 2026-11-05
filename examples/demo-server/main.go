// Command demo-server is a small local HTTP service for exercising the sample
// project in examples/sample-project. It is for local development only.
//
//	go run ./examples/demo-server -addr 127.0.0.1:4010
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"strings"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:4010", "listen address")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
			return
		}
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad json"})
			return
		}
		if in.Username == "" || in.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username and password required"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{"accessToken": "demo-token-for-" + in.Username},
		})
	})
	mux.HandleFunc("/users/me", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing bearer token"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"username": "alice",
				"settings": map[string]any{"theme": "dark", "locale": "zh-CN"},
			},
		})
	})

	log.Printf("demo server listening on http://%s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
