package core

import (
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// CookieEntry represents a single HTTP cookie stored in an isolated project or
// session CookieJar, or parsed from a response's Set-Cookie headers.
// Cookie values are runtime/device-local state and must never be written to
// shared .gorequest/ repository files.
type CookieEntry struct {
	Name       string
	Value      string
	Domain     string
	HostOnly   bool
	Path       string
	Secure     bool
	HttpOnly   bool
	SameSite   string // "", "Lax", "Strict", "None"
	Expires    time.Time
	Persistent bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// IsExpired reports whether the cookie is a persistent cookie whose expiration
// time has passed relative to now.
func (c CookieEntry) IsExpired(now time.Time) bool {
	return c.Persistent && !c.Expires.IsZero() && !now.Before(c.Expires)
}

// Key returns the unique (domain, path, name) identity tuple for a cookie in
// RFC 6265 storage.
func (c CookieEntry) Key() string {
	return strings.ToLower(strings.TrimSpace(c.Domain)) + "\x00" + normalizeCookiePath(c.Path) + "\x00" + c.Name
}

// CookieJar is a concurrency-safe, RFC 6265 compliant HTTP cookie store
// isolated to a single project or execution session. There is no global cookie
// jar: two projects or two isolated Runner sessions never share CookieJar
// instances unless explicitly passed the same pointer.
type CookieJar struct {
	mu      sync.RWMutex
	scopeID string
	entries map[string]CookieEntry
	nowFunc func() time.Time
	seq     uint64
}

// NewCookieJar creates an isolated CookieJar for the given project or session
// scope identifier.
func NewCookieJar(scopeID string) *CookieJar {
	return &CookieJar{
		scopeID: strings.TrimSpace(scopeID),
		entries: make(map[string]CookieEntry),
		nowFunc: time.Now,
	}
}

// NewSessionCookieJar creates an ephemeral in-memory CookieJar for a single
// isolated session or data-driven Runner row.
func NewSessionCookieJar() *CookieJar {
	return NewCookieJar("session")
}

// ScopeID returns the diagnostic scope label of this jar (e.g. project ID or
// session ID).
func (j *CookieJar) ScopeID() string {
	if j == nil {
		return ""
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.scopeID
}

// CloneForSession returns a new isolated CookieJar pre-populated with a deep
// copy of all non-expired cookies from j. Subsequent Set-Cookie mutations on
// the clone never affect j.
func (j *CookieJar) CloneForSession(sessionScopeID string) *CookieJar {
	clone := NewCookieJar(sessionScopeID)
	if j == nil {
		return clone
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	now := j.now()
	clone.nowFunc = j.nowFunc
	for k, v := range j.entries {
		if !v.IsExpired(now) {
			clone.entries[k] = v
		}
	}
	return clone
}

func (j *CookieJar) now() time.Time {
	if j != nil && j.nowFunc != nil {
		return j.nowFunc()
	}
	return time.Now()
}

// ValidateCookieEntry validates a CookieEntry for manual insertion/editing in
// the UI or programmatic Upsert, rejecting header-injection characters or
// invalid domains/names.
func ValidateCookieEntry(entry CookieEntry) error {
	name := strings.TrimSpace(entry.Name)
	if name == "" {
		return configErrorf("cookie name is empty")
	}
	for i := 0; i < len(name); i++ {
		if !isHTTPTokenByte(name[i]) {
			return configErrorf("invalid cookie name %q", entry.Name)
		}
	}
	if strings.ContainsAny(entry.Value, "\r\n\x00;") {
		return configErrorf("cookie %q value contains invalid characters (newline, NUL, or ';')", name)
	}
	domain := normalizeCookieDomain(entry.Domain)
	if domain == "" {
		return configErrorf("cookie %q domain is empty", name)
	}
	if strings.ContainsAny(domain, " /\t\r\n\x00:") {
		return configErrorf("invalid cookie domain %q (specify host name without scheme or port)", entry.Domain)
	}
	if strings.ContainsAny(entry.Path, "\r\n\x00;") {
		return configErrorf("invalid cookie path %q", entry.Path)
	}
	return nil
}

// Upsert inserts or updates a cookie entry directly (used by the Desktop Cookie
// Manager UI and test setup). If entry is already expired, any existing cookie
// with the same key is deleted.
func (j *CookieJar) Upsert(entry CookieEntry) error {
	if j == nil {
		return configErrorf("nil cookie jar")
	}
	if err := ValidateCookieEntry(entry); err != nil {
		return err
	}
	hadLeadingDot := strings.HasPrefix(strings.TrimSpace(entry.Domain), ".")
	domain := normalizeCookieDomain(entry.Domain)
	hostOnly := entry.HostOnly
	if isIPLiteral(domain) {
		hostOnly = true
	} else if hadLeadingDot {
		hostOnly = false
	}

	entry.Name = strings.TrimSpace(entry.Name)
	entry.Domain = domain
	entry.HostOnly = hostOnly
	entry.Path = normalizeCookiePath(entry.Path)
	entry.SameSite = normalizeSameSite(entry.SameSite)
	if !entry.Expires.IsZero() {
		entry.Persistent = true
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	key := entry.Key()
	if entry.IsExpired(now) {
		delete(j.entries, key)
		return nil
	}
	if prev, ok := j.entries[key]; ok {
		entry.CreatedAt = prev.CreatedAt
	} else if entry.CreatedAt.IsZero() {
		j.seq++
		entry.CreatedAt = now.Add(time.Duration(j.seq) * time.Nanosecond)
	}
	entry.UpdatedAt = now
	j.entries[key] = entry
	return nil
}

// Delete removes a single cookie identified by (domain, path, name). It returns
// true if an entry existed and was removed.
func (j *CookieJar) Delete(domain, path, name string) bool {
	if j == nil {
		return false
	}
	normDomain := normalizeCookieDomain(domain)
	normPath := normalizeCookiePath(path)
	normName := strings.TrimSpace(name)
	key := normDomain + "\x00" + normPath + "\x00" + normName

	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.entries[key]; ok {
		delete(j.entries, key)
		return true
	}
	return false
}

// Clear removes all cookies from the jar.
func (j *CookieJar) Clear() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	clear(j.entries)
}

// ClearDomain removes all cookies whose domain matches domainFilter (exact or
// subdomain) and returns the number of removed entries.
func (j *CookieJar) ClearDomain(domainFilter string) int {
	if j == nil {
		return 0
	}
	target := normalizeCookieDomain(domainFilter)
	if target == "" {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	removed := 0
	for k, e := range j.entries {
		if e.Domain == target || strings.HasSuffix(e.Domain, "."+target) {
			delete(j.entries, k)
			removed++
		}
	}
	return removed
}

// Count returns the number of active (non-expired) cookies in the jar.
func (j *CookieJar) Count() int {
	if j == nil {
		return 0
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	now := j.now()
	n := 0
	for _, e := range j.entries {
		if !e.IsExpired(now) {
			n++
		}
	}
	return n
}

// Entries returns a deterministic snapshot of all non-expired cookies in the
// jar, sorted by Domain, Path (longest first), and Name.
func (j *CookieJar) Entries() []CookieEntry {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	out := make([]CookieEntry, 0, len(j.entries))
	for k, e := range j.entries {
		if e.IsExpired(now) {
			delete(j.entries, k)
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].Domain != out[k].Domain {
			return out[i].Domain < out[k].Domain
		}
		if len(out[i].Path) != len(out[k].Path) {
			return len(out[i].Path) > len(out[k].Path)
		}
		if out[i].Path != out[k].Path {
			return out[i].Path < out[k].Path
		}
		return out[i].Name < out[k].Name
	})
	return out
}

// SetCookies implements http.CookieJar. It records cookies received in a
// response from u, enforcing RFC 6265 domain/path scoping and expiration rules.
func (j *CookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if j == nil || u == nil || len(cookies) == 0 {
		return
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return
	}
	reqHost := normalizeHost(u.Hostname())
	if reqHost == "" {
		return
	}
	defaultPath := defaultCookiePath(u.EscapedPath())

	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()

	for _, hc := range cookies {
		if hc == nil {
			continue
		}
		entry, deleteExisting, ok := cookieEntryFromHTTP(hc, reqHost, defaultPath, scheme == "https", now)
		if !ok {
			continue
		}
		key := entry.Key()
		if deleteExisting || entry.IsExpired(now) {
			delete(j.entries, key)
			continue
		}
		if prev, exists := j.entries[key]; exists {
			entry.CreatedAt = prev.CreatedAt
		} else {
			j.seq++
			entry.CreatedAt = now.Add(time.Duration(j.seq) * time.Nanosecond)
		}
		entry.UpdatedAt = now
		j.entries[key] = entry
	}
}

// Cookies implements http.CookieJar. It returns the cookies to send in a
// request to u, ordered per RFC 6265 §5.4 (longer Path first, then earlier
// CreatedAt).
func (j *CookieJar) Cookies(u *url.URL) []*http.Cookie {
	entries := j. matchingEntriesForURL(u)
	if len(entries) == 0 {
		return nil
	}
	out := make([]*http.Cookie, len(entries))
	for i, e := range entries {
		out[i] = &http.Cookie{
			Name:  e.Name,
			Value: e.Value,
		}
	}
	return out
}

// EntriesForURL returns the full CookieEntry metadata for all cookies in the
// jar that would be sent to rawURL.
func (j *CookieJar) EntriesForURL(rawURL string) []CookieEntry {
	if j == nil {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil
	}
	return j.matchingEntriesForURL(u)
}

func (j *CookieJar) matchingEntriesForURL(u *url.URL) []CookieEntry {
	if j == nil || u == nil {
		return nil
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return nil
	}
	reqHost := normalizeHost(u.Hostname())
	if reqHost == "" {
		return nil
	}
	reqPath := u.EscapedPath()
	if reqPath == "" {
		reqPath = "/"
	}
	isHTTPS := scheme == "https"

	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()

	var matched []CookieEntry
	for k, e := range j.entries {
		if e.IsExpired(now) {
			delete(j.entries, k)
			continue
		}
		if e.Secure && !isHTTPS {
			continue
		}
		if !cookieDomainMatches(reqHost, e.Domain, e.HostOnly) {
			continue
		}
		if !cookiePathMatches(reqPath, e.Path) {
			continue
		}
		matched = append(matched, e)
	}

	sort.Slice(matched, func(i, k int) bool {
		if len(matched[i].Path) != len(matched[k].Path) {
			return len(matched[i].Path) > len(matched[k].Path)
		}
		if !matched[i].CreatedAt.Equal(matched[k].CreatedAt) {
			return matched[i].CreatedAt.Before(matched[k].CreatedAt)
		}
		return matched[i].Name < matched[k].Name
	})
	return matched
}

// RegisterSecrets registers all cookie values stored in j with red so any
// transport error or diagnostic log is automatically scrubbed.
func (j *CookieJar) RegisterSecrets(red *Redactor) {
	if j == nil || red == nil {
		return
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	for _, e := range j.entries {
		if e.Name != "" {
			red.RegisterSensitiveKey("cookie", e.Name)
		}
		if e.Value != "" {
			red.RegisterSecret(e.Value)
		}
	}
}

// ParseResponseCookies parses all Set-Cookie headers from a response and
// returns their structured CookieEntry representations (including cookies with
// Max-Age <= 0 or past Expires so the user can inspect what the server sent).
func ParseResponseCookies(rawURL string, headers [][2]string) []CookieEntry {
	if len(headers) == 0 {
		return nil
	}
	h := http.Header{}
	for _, kv := range headers {
		if strings.EqualFold(strings.TrimSpace(kv[0]), "Set-Cookie") {
			h.Add("Set-Cookie", kv[1])
		}
	}
	if len(h) == 0 {
		return nil
	}
	resp := &http.Response{Header: h}
	parsedCookies := resp.Cookies()
	if len(parsedCookies) == 0 {
		return nil
	}

	var reqHost, defaultPath string
	isHTTPS := true
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil && u != nil {
		reqHost = normalizeHost(u.Hostname())
		defaultPath = defaultCookiePath(u.EscapedPath())
		isHTTPS = strings.EqualFold(u.Scheme, "https")
	}
	if defaultPath == "" {
		defaultPath = "/"
	}
	now := time.Now()
	out := make([]CookieEntry, 0, len(parsedCookies))
	for _, hc := range parsedCookies {
		if hc == nil {
			continue
		}
		if reqHost != "" {
			if entry, _, ok := cookieEntryFromHTTP(hc, reqHost, defaultPath, isHTTPS, now); ok {
				out = append(out, entry)
				continue
			}
		}
		// Fallback when URL is absent or domain was rejected: still surface the
		// raw parsed cookie attributes in the response inspector.
		dom := normalizeCookieDomain(hc.Domain)
		hostOnly := dom == ""
		if dom == "" {
			dom = reqHost
		}
		p := hc.Path
		if p == "" {
			p = defaultPath
		}
		entry := CookieEntry{
			Name:      hc.Name,
			Value:     hc.Value,
			Domain:    dom,
			HostOnly:  hostOnly,
			Path:      normalizeCookiePath(p),
			Secure:    hc.Secure,
			HttpOnly:  hc.HttpOnly,
			SameSite:  sameSiteString(hc.SameSite),
			CreatedAt: now,
			UpdatedAt: now,
		}
		if hc.MaxAge < 0 {
			entry.Persistent = true
			entry.Expires = time.Unix(1, 0).UTC()
		} else if hc.MaxAge > 0 {
			entry.Persistent = true
			entry.Expires = now.Add(time.Duration(hc.MaxAge) * time.Second)
		} else if !hc.Expires.IsZero() {
			entry.Persistent = true
			entry.Expires = hc.Expires
		}
		out = append(out, entry)
	}
	return out
}

func cookieEntryFromHTTP(hc *http.Cookie, reqHost, defaultPath string, isHTTPS bool, now time.Time) (CookieEntry, bool, bool) {
	name := strings.TrimSpace(hc.Name)
	if name == "" {
		return CookieEntry{}, false, false
	}
	for i := 0; i < len(name); i++ {
		if !isHTTPTokenByte(name[i]) {
			return CookieEntry{}, false, false
		}
	}
	if strings.ContainsAny(hc.Value, "\r\n\x00;") {
		return CookieEntry{}, false, false
	}
	// Secure cookies must not be set by insecure HTTP origins (RFC 6265bis).
	if hc.Secure && !isHTTPS && !IsLoopbackHost(reqHost) {
		return CookieEntry{}, false, false
	}

	var domain string
	var hostOnly bool
	if strings.TrimSpace(hc.Domain) == "" {
		domain = reqHost
		hostOnly = true
	} else {
		attrDom := normalizeCookieDomain(hc.Domain)
		if attrDom == "" {
			return CookieEntry{}, false, false
		}
		// Reject public-suffix-like single-label domains unless exact match
		// (e.g. "com" cannot be set by "example.com").
		if !strings.Contains(attrDom, ".") && attrDom != reqHost {
			return CookieEntry{}, false, false
		}
		if isIPLiteral(reqHost) {
			if attrDom != reqHost {
				return CookieEntry{}, false, false
			}
			domain = reqHost
			hostOnly = true
		} else {
			if !cookieDomainMatches(reqHost, attrDom, false) {
				return CookieEntry{}, false, false
			}
			domain = attrDom
			hostOnly = false
		}
	}

	cookiePath := strings.TrimSpace(hc.Path)
	if cookiePath == "" || !strings.HasPrefix(cookiePath, "/") {
		cookiePath = defaultPath
	} else {
		cookiePath = normalizeCookiePath(cookiePath)
	}

	entry := CookieEntry{
		Name:     name,
		Value:    hc.Value,
		Domain:   domain,
		HostOnly: hostOnly,
		Path:     cookiePath,
		Secure:   hc.Secure,
		HttpOnly: hc.HttpOnly,
		SameSite: sameSiteString(hc.SameSite),
	}

	deleteExisting := false
	if hc.MaxAge < 0 {
		entry.Persistent = true
		entry.Expires = time.Unix(1, 0).UTC()
		deleteExisting = true
	} else if hc.MaxAge > 0 {
		entry.Persistent = true
		entry.Expires = now.Add(time.Duration(hc.MaxAge) * time.Second)
	} else if !hc.Expires.IsZero() {
		entry.Persistent = true
		entry.Expires = hc.Expires
		if !now.Before(hc.Expires) {
			deleteExisting = true
		}
	}
	return entry, deleteExisting, true
}

func normalizeCookieDomain(raw string) string {
	d := normalizeHost(raw)
	d = strings.TrimPrefix(d, ".")
	return d
}

func normalizeCookiePath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" || !strings.HasPrefix(p, "/") {
		return "/"
	}
	return p
}

// defaultCookiePath computes RFC 6265 §5.1.4 default-path from request path.
func defaultCookiePath(escapedPath string) string {
	if escapedPath == "" || !strings.HasPrefix(escapedPath, "/") {
		return "/"
	}
	if escapedPath == "/" {
		return "/"
	}
	lastSlash := strings.LastIndexByte(escapedPath, '/')
	if lastSlash <= 0 {
		return "/"
	}
	return escapedPath[:lastSlash]
}

func cookieDomainMatches(reqHost, cookieDomain string, hostOnly bool) bool {
	if reqHost == "" || cookieDomain == "" {
		return false
	}
	if reqHost == cookieDomain {
		return true
	}
	if hostOnly || isIPLiteral(reqHost) {
		return false
	}
	return len(reqHost) > len(cookieDomain)+1 &&
		strings.HasSuffix(reqHost, "."+cookieDomain)
}

// cookiePathMatches implements RFC 6265 §5.1.4 path-match.
func cookiePathMatches(reqPath, cookiePath string) bool {
	if cookiePath == "" {
		cookiePath = "/"
	}
	if reqPath == "" {
		reqPath = "/"
	}
	if reqPath == cookiePath {
		return true
	}
	if strings.HasPrefix(reqPath, cookiePath) {
		if strings.HasSuffix(cookiePath, "/") {
			return true
		}
		if len(reqPath) > len(cookiePath) && reqPath[len(cookiePath)] == '/' {
			return true
		}
	}
	return false
}

func isIPLiteral(host string) bool {
	return net.ParseIP(normalizeHost(host)) != nil
}

func sameSiteString(s http.SameSite) string {
	switch s {
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return ""
	}
}

func normalizeSameSite(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "lax":
		return "Lax"
	case "strict":
		return "Strict"
	case "none":
		return "None"
	default:
		return ""
	}
}
