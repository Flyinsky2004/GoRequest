package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// user is one fictional record in the in-memory collection. Values are made up
// and carry no real personal data.
type user struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

var allowedRoles = map[string]bool{"admin": true, "member": true, "viewer": true}

// userStore keeps users in memory behind a RWMutex. Every read returns a copy
// so a caller can encode the value after the lock is released without racing a
// concurrent update.
type userStore struct {
	mu     sync.RWMutex
	users  map[int]user
	nextID int
}

func newUserStore() *userStore {
	s := &userStore{users: map[int]user{}, nextID: 1}
	for _, seed := range []struct {
		name, email, role string
	}{
		{"alice", "alice@example.com", "admin"},
		{"bob", "bob@example.com", "member"},
		{"carol", "carol@example.com", "viewer"},
		{"dave", "dave@example.com", "member"},
		{"erin", "erin@example.com", "member"},
	} {
		s.create(seed.name, seed.email, seed.role)
	}
	return s
}

func (s *userStore) create(name, email, role string) user {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := user{
		ID:        s.nextID,
		Name:      name,
		Email:     email,
		Role:      role,
		CreatedAt: time.Now().UTC(),
	}
	s.nextID++
	s.users[u.ID] = u
	return u
}

func (s *userStore) get(id int) (user, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	return u, ok
}

// list returns one page of users ordered by ID plus the total count.
func (s *userStore) list(page, pageSize int) ([]user, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := make([]user, 0, len(s.users))
	for _, u := range s.users {
		all = append(all, u)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	total := len(all)
	// Clamp before multiplying: even a valid positive page can overflow int.
	start := total
	if page-1 <= total/pageSize {
		start = (page - 1) * pageSize
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pageUsers := make([]user, end-start)
	copy(pageUsers, all[start:end])
	return pageUsers, total
}

type userInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (in userInput) validate() (userInput, string) {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.TrimSpace(in.Email)
	in.Role = strings.TrimSpace(in.Role)
	if in.Name == "" {
		return in, "name is required"
	}
	if in.Email != "" && !strings.Contains(in.Email, "@") {
		return in, "email must contain @"
	}
	if in.Role == "" {
		in.Role = "member"
	}
	if !allowedRoles[in.Role] {
		return in, "role must be one of admin, member, viewer"
	}
	return in, ""
}

func (s *userStore) update(id int, in userInput) (user, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return user{}, false
	}
	u.Name = in.Name
	u.Email = in.Email
	u.Role = in.Role
	s.users[id] = u
	return u, true
}

// patchInput allows partial updates: nil fields are left untouched.
type patchInput struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
	Role  *string `json:"role"`
}

func (s *userStore) patch(id int, in patchInput) (user, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return user{}, false, ""
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return user{}, true, "name cannot be empty"
		}
		u.Name = name
	}
	if in.Email != nil {
		email := strings.TrimSpace(*in.Email)
		if email != "" && !strings.Contains(email, "@") {
			return user{}, true, "email must contain @"
		}
		u.Email = email
	}
	if in.Role != nil {
		role := strings.TrimSpace(*in.Role)
		if !allowedRoles[role] {
			return user{}, true, "role must be one of admin, member, viewer"
		}
		u.Role = role
	}
	s.users[id] = u
	return u, true, ""
}

func (s *userStore) delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return false
	}
	delete(s.users, id)
	return true
}

// handleCollection serves GET /users (paginated list) and POST /users (create).
func (s *userStore) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleList(w, r)
	case http.MethodPost:
		s.handleCreate(w, r)
	default:
		requireMethod(w, r, http.MethodGet, http.MethodPost)
	}
}

func (s *userStore) handleList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := parsePagination(r)
	if err != "" {
		writeError(w, http.StatusBadRequest, "invalid pagination", err)
		return
	}
	users, total := s.list(page, pageSize)
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": users,
		"pagination": map[string]int{
			"page":       page,
			"pageSize":   pageSize,
			"total":      total,
			"totalPages": totalPages,
		},
	})
}

const (
	defaultPageSize = 10
	maxPageSize     = 100
)

// parsePagination validates the page/pageSize query parameters. It returns a
// non-empty detail string on any invalid value so the caller can answer 400.
func parsePagination(r *http.Request) (page, pageSize int, detail string) {
	page = 1
	pageSize = defaultPageSize
	q := r.URL.Query()

	if raw := q.Get("page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			return 0, 0, "page must be a positive integer"
		}
		page = v
	}
	if raw := q.Get("pageSize"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > maxPageSize {
			return 0, 0, "pageSize must be an integer between 1 and 100"
		}
		pageSize = v
	}
	return page, pageSize, ""
}

func (s *userStore) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in userInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return
	}
	clean, detail := in.validate()
	if detail != "" {
		writeError(w, http.StatusBadRequest, "invalid user", detail)
		return
	}
	u := s.create(clean.Name, clean.Email, clean.Role)
	w.Header().Set("Location", "/users/"+strconv.Itoa(u.ID))
	writeJSON(w, http.StatusCreated, map[string]any{"data": u})
}

// handleItem serves /users/{id} for GET, PUT, PATCH and DELETE.
func (s *userStore) handleItem(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/users/")
	if raw == "" || strings.Contains(raw, "/") {
		writeError(w, http.StatusNotFound, "not found", "expected /users/{id}")
		return
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid user id", "id must be a positive integer")
		return
	}

	switch r.Method {
	case http.MethodGet:
		u, ok := s.get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "user not found", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": u})
	case http.MethodPut:
		s.handleUpdate(w, r, id)
	case http.MethodPatch:
		s.handlePatch(w, r, id)
	case http.MethodDelete:
		if !s.delete(id) {
			writeError(w, http.StatusNotFound, "user not found", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		requireMethod(w, r, http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete)
	}
}

func (s *userStore) handleUpdate(w http.ResponseWriter, r *http.Request, id int) {
	var in userInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return
	}
	clean, detail := in.validate()
	if detail != "" {
		writeError(w, http.StatusBadRequest, "invalid user", detail)
		return
	}
	u, ok := s.update(id, clean)
	if !ok {
		writeError(w, http.StatusNotFound, "user not found", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": u})
}

func (s *userStore) handlePatch(w http.ResponseWriter, r *http.Request, id int) {
	var in patchInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return
	}
	u, found, detail := s.patch(id, in)
	if !found {
		writeError(w, http.StatusNotFound, "user not found", "")
		return
	}
	if detail != "" {
		writeError(w, http.StatusBadRequest, "invalid user", detail)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": u})
}
