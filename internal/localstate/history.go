// Package localstate owns GoRequest's local, never Git-tracked runtime state.
//
// History lives here rather than in a project directory or in internal/core's
// project store: the requirements keep request history, device preferences and
// caches outside shared project files (ADR-005). The package is pure Go and has
// no native UI dependency, so the same engine can later serve the Runner
// without pulling a window or a widget into the data path.
package localstate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorequest/internal/core"
)

// SchemaVersion is the on-disk history record version. It is deliberately
// independent of the project file schema: history is local, replaceable data.
const SchemaVersion = 1

// Default limits. They are intentionally conservative: history is a convenience,
// never the source of truth, and a runaway response must not turn into a large
// local file.
const (
	DefaultMaxRecords      = 500
	DefaultMaxBytes        = 2 << 20 // 2 MiB per project file
	DefaultMaxRecordBytes  = 512 << 10
	DefaultRetention       = 90 * 24 * time.Hour
	DefaultMaxSnapshotBody = 64 << 10 // 64 KiB per captured body
)

// Outcome values describe how a send ended. "ok" means a response was received;
// the HTTP status (including 4xx/5xx) is transport fact, not a local failure.
const (
	OutcomeOK       = "ok"
	OutcomeError    = "error"
	OutcomeCanceled = "canceled"
	OutcomeTimeout  = "timeout"
)

// Reasons a body was not written into a snapshot.
const (
	OmitPolicy    = "policy"
	OmitLarge     = "large"
	OmitBinary    = "binary"
	OmitTruncated = "truncated"
)

// Snapshot is a redacted request or response capture. Headers are stored in
// order so duplicate headers round-trip. Body is empty unless the user opted
// into body capture and the body passed the size/text checks; Omitted says why
// an absent body was left out.
type Snapshot struct {
	Headers  [][2]string `json:"headers,omitempty"`
	Body     string      `json:"body,omitempty"`
	BodySize int64       `json:"bodySize,omitempty"`
	Omitted  string      `json:"omitted,omitempty"`
	Redacted bool        `json:"redacted,omitempty"`
	// Truncated means the executor itself truncated the response, so the full
	// body was never available to store.
	Truncated bool `json:"truncated,omitempty"`
}

// Record is one local history entry. The JSON field names for the metadata
// fields are the ones the previous metadata-only store wrote, so existing local
// history files keep loading after this engine is introduced.
type Record struct {
	SchemaVersion int       `json:"schemaVersion,omitempty"`
	ID            string    `json:"id,omitempty"`
	Ts            time.Time `json:"ts"`
	ProjectKey    string    `json:"projectKey,omitempty"`
	ProjectName   string    `json:"projectName,omitempty"`
	RequestID     string    `json:"requestId,omitempty"`
	RequestName   string    `json:"requestName,omitempty"`
	Env           string    `json:"env,omitempty"`
	Method        string    `json:"method"`
	URL           string    `json:"url"`
	Status        int       `json:"status,omitempty"`
	Proto         string    `json:"proto,omitempty"`
	DurationMs    int64     `json:"durationMs,omitempty"`
	SizeBytes     int64     `json:"size,omitempty"`
	Outcome       string    `json:"outcome,omitempty"`
	Error         string    `json:"error,omitempty"`
	Request       *Snapshot `json:"request,omitempty"`
	Response      *Snapshot `json:"response,omitempty"`
}

// OutcomeValue returns the stored outcome, deriving it for records written by
// the metadata-only version (which had no outcome field).
func (r Record) OutcomeValue() string {
	switch r.Outcome {
	case OutcomeOK, OutcomeError, OutcomeCanceled, OutcomeTimeout:
		return r.Outcome
	}
	if r.Status > 0 {
		return OutcomeOK
	}
	if r.Error != "" {
		return OutcomeError
	}
	return OutcomeOK
}

// ResultClass maps a record to the UI result filter classes.
func (r Record) ResultClass() string {
	switch r.OutcomeValue() {
	case OutcomeCanceled:
		return "canceled"
	case OutcomeTimeout:
		return "timeout"
	case OutcomeError:
		return "network"
	}
	switch {
	case r.Status >= 200 && r.Status < 400:
		return "success"
	case r.Status >= 400 && r.Status < 500:
		return "client"
	case r.Status >= 500 && r.Status < 600:
		return "server"
	case r.Status > 0:
		return "success"
	}
	return "network"
}

// Policy controls how much of a send is persisted. The zero-value is safe:
// callers that do not opt in get metadata only (no request/response snapshot).
type Policy struct {
	Snapshots    bool
	Bodies       bool
	MaxBodyBytes int64
}

// DefaultPolicy captures redacted metadata snapshots (headers) but never
// bodies. This is the desktop default and matches "history defaults to a
// redacted snapshot; sensitive or large bodies may be left out".
func DefaultPolicy() Policy {
	return Policy{Snapshots: true, Bodies: false, MaxBodyBytes: DefaultMaxSnapshotBody}
}

// Input is the transport-neutral port the desktop (and later the Runner) uses
// to create a record. It contains no network or UI types beyond the shared
// core.Redactor and the raw bytes the executor already produced, so a future
// runner result can populate the same fields without inventing a second history
// model.
type HistoryInput struct {
	Ts          time.Time
	ProjectKey  string
	ProjectName string
	RequestID   string
	RequestName string
	Environment string

	Method    string
	URL       string
	Outcome   string
	Status    int
	Proto     string
	Duration  time.Duration
	SizeBytes int64
	Error     string

	// Request snapshot inputs. Headers are the fully resolved request headers
	// (including auth and body content type); Body is the encoded request body.
	RequestHeaders     [][2]string
	RequestBody        []byte
	RequestContentType string

	// Response snapshot inputs.
	ResponseHeaders   [][2]string
	ResponseBody      []byte
	ResponseTruncated bool

	// Redactor carries the secrets discovered while building the plan. It is
	// applied to every string and structured field before persistence.
	Redactor *core.Redactor
	Policy   Policy
}

// BuildRecord redacts in and applies p. It never returns a record containing a
// registered raw secret or one of its encoded variants; when a body is not
// captured the record still carries its byte size and the omission reason.
func BuildRecord(in HistoryInput, p Policy) Record {
	// A caller may pass the policy either explicitly or through the port. An
	// explicit zero-value policy is only meaningful when HistoryInput.Policy is
	// also unset; otherwise the port's policy wins so callers cannot lose their
	// opt-in by forgetting the second argument.
	if !p.Snapshots && !p.Bodies && p.MaxBodyBytes == 0 && (in.Policy != Policy{}) {
		p = in.Policy
	}
	red := in.Redactor
	if red == nil {
		red = core.NewRedactor()
	}
	ts := in.Ts
	if ts.IsZero() {
		ts = time.Now()
	}
	rec := Record{
		SchemaVersion: SchemaVersion,
		ID:            newID(),
		Ts:            ts,
		ProjectKey:    in.ProjectKey,
		ProjectName:   red.RedactString(in.ProjectName),
		RequestID:     in.RequestID,
		RequestName:   red.RedactString(in.RequestName),
		Env:           red.RedactString(in.Environment),
		Method:        strings.ToUpper(strings.TrimSpace(in.Method)),
		URL:           red.RedactString(in.URL),
		Status:        in.Status,
		Proto:         in.Proto,
		DurationMs:    in.Duration.Microseconds() / 1000,
		SizeBytes:     in.SizeBytes,
		Outcome:       normalizeOutcome(in),
		Error:         red.RedactString(in.Error),
	}

	// Sanitize the URL (including userinfo and query values) and headers through
	// the shared Redactor so B05's sensitive-slot rules are the single source of
	// truth here too.
	plan := &core.Plan{Method: rec.Method, URL: in.URL, Headers: in.RequestHeaders}
	sanPlan := red.SanitizePlan(plan)
	rec.URL = sanPlan.URL

	if p.MaxBodyBytes <= 0 {
		p.MaxBodyBytes = DefaultMaxSnapshotBody
	}
	if p.Snapshots {
		reqSnap := &Snapshot{Headers: sanPlan.Headers, Redacted: true}
		if body, size, omitted := snapshotBody(red, in.RequestBody, in.RequestContentType, p, false); omitted != "" || body != "" {
			reqSnap.Body, reqSnap.BodySize, reqSnap.Omitted = body, size, omitted
		} else if size > 0 {
			reqSnap.BodySize = size
		}
		rec.Request = reqSnap

		if in.Status > 0 || len(in.ResponseHeaders) > 0 || in.ResponseBody != nil {
			resSnap := &Snapshot{Redacted: true, Truncated: in.ResponseTruncated}
			sanRes := red.SanitizeExecResult(&core.ExecResult{Headers: in.ResponseHeaders})
			resSnap.Headers = sanRes.Headers
			body, size, omitted := snapshotBody(red, in.ResponseBody, contentTypeOf(in.ResponseHeaders), p, in.ResponseTruncated)
			resSnap.Body, resSnap.BodySize, resSnap.Omitted = body, size, omitted
			rec.Response = resSnap
		}
	}
	return rec
}

func normalizeOutcome(in HistoryInput) string {
	switch in.Outcome {
	case OutcomeOK, OutcomeError, OutcomeCanceled, OutcomeTimeout:
		return in.Outcome
	}
	if in.Status > 0 {
		return OutcomeOK
	}
	if in.Error != "" {
		return OutcomeError
	}
	return OutcomeOK
}

// snapshotBody decides whether a body may be stored. The raw size and the
// omission reason are always reported; the redacted text is returned only when
// every policy check passes.
func snapshotBody(red *core.Redactor, raw []byte, contentType string, p Policy, truncated bool) (body string, size int64, omitted string) {
	size = int64(len(raw))
	if size == 0 {
		return "", 0, ""
	}
	if !p.Bodies {
		return "", size, OmitPolicy
	}
	if truncated {
		return "", size, OmitTruncated
	}
	if p.MaxBodyBytes > 0 && size > p.MaxBodyBytes {
		return "", size, OmitLarge
	}
	if !isTextual(contentType, raw) {
		return "", size, OmitBinary
	}
	return red.RedactString(string(raw)), size, ""
}

func contentTypeOf(headers [][2]string) string {
	for _, kv := range headers {
		if strings.EqualFold(kv[0], "Content-Type") {
			return kv[1]
		}
	}
	return ""
}

func isTextual(contentType string, raw []byte) bool {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch {
	case strings.HasPrefix(ct, "text/"):
		return true
	case ct == "application/json", strings.HasSuffix(ct, "+json"):
		return true
	case ct == "application/xml", strings.HasSuffix(ct, "+xml"):
		return true
	case ct == "application/x-www-form-urlencoded", ct == "application/graphql":
		return true
	case strings.HasPrefix(ct, "multipart/"):
		return false
	case ct == "application/octet-stream":
		return false
	case ct != "":
		return false
	}
	// No declared type: only store data that is valid UTF-8 and free of NULs.
	if !utf8.Valid(raw) {
		return false
	}
	return !strings.ContainsRune(string(raw), 0)
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// ProjectKey derives a per-project local file key from the stable project id and
// a hash of the normalized local root. Two clones of the same repository share
// the id but not the root, so their history never mixes. The "id-hash" shape is
// the one the previous metadata-only store used, so existing files keep loading.
func ProjectKey(projectID, root string) string {
	id := "proj"
	if strings.TrimSpace(projectID) != "" {
		id = core.Slug(projectID)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return id + "-" + hex.EncodeToString(sum[:])[:8]
}

// Filter selects records for listing. Zero fields mean "no restriction".
type Filter struct {
	Since      time.Time
	Until      time.Time
	ProjectKey string
	RequestID  string
	Query      string // case-insensitive match on method, URL, request, project or env
	Result     string // success|client|server|network|canceled|timeout
}

// Match reports whether r satisfies f.
func (f Filter) Match(r Record) bool {
	if !f.Since.IsZero() && r.Ts.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !r.Ts.Before(f.Until) {
		return false
	}
	if f.ProjectKey != "" && r.ProjectKey != f.ProjectKey {
		return false
	}
	if f.RequestID != "" && r.RequestID != f.RequestID {
		return false
	}
	if f.Result != "" && r.ResultClass() != f.Result {
		return false
	}
	if q := strings.ToLower(strings.TrimSpace(f.Query)); q != "" {
		hay := strings.ToLower(strings.Join([]string{r.Method, r.URL, r.RequestName, r.ProjectName, r.Env}, "\n"))
		if !strings.Contains(hay, q) {
			return false
		}
	}
	return true
}

// FilterRecords returns the subset of recs matching f, preserving order.
func FilterRecords(recs []Record, f Filter) []Record {
	out := make([]Record, 0, len(recs))
	for _, r := range recs {
		if f.Match(r) {
			out = append(out, r)
		}
	}
	return out
}

// Limits bounds one project's history file. Zero values take the defaults;
// a negative Retention means "keep until the count/byte limits remove it".
type Limits struct {
	MaxRecords     int
	MaxBytes       int64
	MaxRecordBytes int64
	Retention      time.Duration
}

// DefaultLimits returns the built-in bounds.
func DefaultLimits() Limits {
	return Limits{
		MaxRecords:     DefaultMaxRecords,
		MaxBytes:       DefaultMaxBytes,
		MaxRecordBytes: DefaultMaxRecordBytes,
		Retention:      DefaultRetention,
	}
}

func (l Limits) normalized() Limits {
	d := DefaultLimits()
	if l.MaxRecords <= 0 {
		l.MaxRecords = d.MaxRecords
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = d.MaxBytes
	}
	if l.MaxRecordBytes <= 0 {
		l.MaxRecordBytes = d.MaxRecordBytes
	}
	if l.Retention == 0 {
		l.Retention = d.Retention
	}
	if l.Retention < 0 {
		l.Retention = 0 // explicit forever
	}
	if l.MaxRecordBytes > l.MaxBytes {
		l.MaxRecordBytes = l.MaxBytes
	}
	return l
}

// Diagnostics describes what a load found. It is intentionally data, not a
// log-only side effect, so the desktop can show corrupt-line and permission
// problems in the history panel instead of silently lying about an empty list.
type Diagnostics struct {
	Path         string `json:"path"`
	Exists       bool   `json:"exists"`
	Bytes        int64  `json:"bytes"`
	CorruptLines int    `json:"corruptLines"`
	ReadError    string `json:"readError,omitempty"`
}

// LoadResult is a project's history plus load diagnostics, newest first.
type LoadResult struct {
	Records     []Record
	Diagnostics Diagnostics
}

// AllResult is the cross-project view used by the history panel's project scope.
type AllResult struct {
	Records     []Record
	Diagnostics []Diagnostics
}

// AppendResult reports what an append changed, including whether optional
// snapshots had to be dropped to fit the per-record bound.
type AppendResult struct {
	Record  Record
	Trimmed bool
	Removed int
	Bytes   int64
	// CorruptLines reports lines that were unreadable before this append and
	// were dropped by the atomic rewrite, so the caller can tell the user.
	CorruptLines int
}

// CleanupResult reports how many old records were removed by a retention or
// size pass.
type CleanupResult struct {
	Removed int
	Bytes   int64
}

// ErrRecordTooLarge means a record cannot be stored even without snapshots.
var ErrRecordTooLarge = errors.New("history record exceeds the per-record limit")

// Store is a per-directory history engine. All methods are safe for concurrent
// use within one process; writes are atomic (temp file + rename), so a failed or
// interrupted append never leaves a half-written line in the visible file.
type Store struct {
	dir    string
	mu     sync.Mutex
	limits Limits
}

// NewStore creates a store. An empty dir disables persistence: all reads look
// empty and all writes are no-ops, which keeps headless tests and an
// unavailable platform directory harmless.
func NewStore(dir string, limits Limits) *Store {
	return &Store{dir: dir, limits: limits.normalized()}
}

// SetLimits changes the retention/size bounds used by subsequent writes.
func (s *Store) SetLimits(limits Limits) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = limits.normalized()
}

// Dir returns the history directory ("" when disabled).
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

func (s *Store) disabled() bool { return s == nil || s.dir == "" }

func (s *Store) path(key string) string {
	key = safeKey(key)
	if key == "" {
		return ""
	}
	return filepath.Join(s.dir, key+".jsonl")
}

// safeKey keeps a derived key from escaping the history directory or naming a
// hidden file. ProjectKey already produces [a-z0-9.-], but files in the
// directory are also re-read by LoadAll, so the guard is applied on both sides.
func safeKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".")
	return out
}

// Load reads one project's history, sorted newest first. A missing file is an
// empty history, not an error. Corrupt lines are skipped and counted; a read
// failure (permission, not-a-directory) is returned as a diagnosable error while
// the partial result is still available.
func (s *Store) Load(key string) (LoadResult, error) {
	if s.disabled() {
		return LoadResult{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(key)
}

// LoadFiltered reads one project's history and applies f.
func (s *Store) LoadFiltered(key string, f Filter) (LoadResult, error) {
	res, err := s.Load(key)
	res.Records = FilterRecords(res.Records, f)
	return res, err
}

// LoadAll reads every project's history file in the store directory and applies
// f. Per-file failures are reported in Diagnostics rather than aborting the
// whole view; only an unreadable directory returns an error.
func (s *Store) LoadAll(f Filter) (AllResult, error) {
	if s.disabled() {
		return AllResult{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AllResult{}, nil
		}
		return AllResult{}, &ReadError{Path: s.dir, Err: err}
	}
	var out AllResult
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".jsonl")
		res, loadErr := s.loadLocked(key)
		out.Diagnostics = append(out.Diagnostics, res.Diagnostics)
		_ = loadErr // already reflected in res.Diagnostics.ReadError
		for i := range res.Records {
			if res.Records[i].ProjectKey == "" {
				res.Records[i].ProjectKey = key
			}
			if res.Records[i].ProjectName == "" {
				// Records written before this engine have no display name. The key
				// is still enough to tell the projects apart in the all-project view.
				res.Records[i].ProjectName = key
			}
		}
		out.Records = append(out.Records, res.Records...)
	}
	sortRecords(out.Records)
	out.Records = FilterRecords(out.Records, f)
	return out, nil
}

func (s *Store) loadLocked(key string) (LoadResult, error) {
	p := s.path(key)
	if p == "" {
		return LoadResult{}, nil
	}
	res := LoadResult{Diagnostics: Diagnostics{Path: p}}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return res, nil
		}
		res.Diagnostics.ReadError = err.Error()
		return res, &ReadError{Path: p, Err: err}
	}
	defer f.Close()
	if fi, statErr := f.Stat(); statErr == nil {
		res.Diagnostics.Exists = true
		res.Diagnostics.Bytes = fi.Size()
	}
	data, err := io.ReadAll(f)
	if err != nil {
		res.Diagnostics.ReadError = err.Error()
		return res, &ReadError{Path: p, Err: err}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil || isCorrupt(r) {
			res.Diagnostics.CorruptLines++
			continue
		}
		if r.ProjectKey == "" {
			r.ProjectKey = key
		}
		res.Records = append(res.Records, r)
	}
	sortRecords(res.Records)
	return res, nil
}

func isCorrupt(r Record) bool {
	return r.Ts.IsZero() && strings.TrimSpace(r.URL) == "" && r.Status == 0 && r.Outcome == "" && r.Error == ""
}

// Append writes rec for key, applying redaction policy before serialization.
// It prunes retention, count and total bytes, then rewrites the file atomically.
// When the serialized record is too large it first drops the optional snapshots;
// if it still does not fit, ErrRecordTooLarge is returned and nothing is written.
func (s *Store) Append(key string, rec Record) (AppendResult, error) {
	if s.disabled() {
		return AppendResult{Record: rec}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key = safeKey(key)
	p := s.path(key)
	if p == "" {
		return AppendResult{Record: rec}, nil
	}
	if rec.SchemaVersion == 0 {
		rec.SchemaVersion = SchemaVersion
	}
	if rec.ID == "" {
		rec.ID = newID()
	}
	if rec.Ts.IsZero() {
		rec.Ts = time.Now()
	}
	if rec.ProjectKey == "" {
		rec.ProjectKey = key
	}
	if rec.Outcome == "" {
		rec.Outcome = rec.OutcomeValue()
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return AppendResult{Record: rec}, &WriteError{Path: p, Err: err}
	}
	res := AppendResult{Record: rec}
	if int64(len(line))+1 > s.limits.MaxRecordBytes {
		rec.Request, rec.Response = nil, nil
		res.Record = rec
		res.Trimmed = true
		line, err = json.Marshal(rec)
		if err != nil {
			return res, &WriteError{Path: p, Err: err}
		}
		if int64(len(line))+1 > s.limits.MaxRecordBytes {
			return res, fmt.Errorf("%w: %d bytes > %d", ErrRecordTooLarge, len(line)+1, s.limits.MaxRecordBytes)
		}
	}

	existing, loadErr := s.loadLocked(key)
	if loadErr != nil {
		// Do not append on top of an unreadable file: that would silently discard
		// records we could not read.
		return res, loadErr
	}
	recs := append([]Record{rec}, existing.Records...)
	sortRecords(recs)
	kept, removed := s.prepare(recs, time.Now())
	res.CorruptLines = existing.Diagnostics.CorruptLines
	res.Removed = removed
	if err := s.writeAllLocked(p, kept); err != nil {
		return res, err
	}
	for _, r := range kept {
		b, _ := json.Marshal(r)
		res.Bytes += int64(len(b)) + 1
	}
	return res, nil
}

// Cleanup applies retention/count/byte limits and removes corrupt lines by
// rewriting the file. It is safe to call when the file does not exist.
func (s *Store) Cleanup(key string) (CleanupResult, error) {
	if s.disabled() {
		return CleanupResult{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key = safeKey(key)
	p := s.path(key)
	if p == "" {
		return CleanupResult{}, nil
	}
	res, err := s.loadLocked(key)
	if err != nil {
		return CleanupResult{}, err
	}
	kept, removed := s.prepare(res.Records, time.Now())
	out := CleanupResult{Removed: removed}
	for _, r := range kept {
		b, _ := json.Marshal(r)
		out.Bytes += int64(len(b)) + 1
	}
	if removed == 0 && res.Diagnostics.CorruptLines == 0 {
		return out, nil
	}
	if err := s.writeAllLocked(p, kept); err != nil {
		return out, err
	}
	return out, nil
}

// Remove deletes one record by ID (preferred) or timestamp. It reports whether
// a record matched and writes atomically only when something changed.
func (s *Store) Remove(key, id string, ts time.Time) (bool, error) {
	if s.disabled() {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key = safeKey(key)
	p := s.path(key)
	if p == "" {
		return false, nil
	}
	res, err := s.loadLocked(key)
	if err != nil {
		return false, err
	}
	kept := make([]Record, 0, len(res.Records))
	found := false
	for _, r := range res.Records {
		match := false
		if id != "" {
			match = r.ID == id
		} else if !ts.IsZero() {
			match = r.Ts.Equal(ts)
		}
		if match {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return false, nil
	}
	if err := s.writeAllLocked(p, kept); err != nil {
		return false, err
	}
	return true, nil
}

// Clear removes the project's history file entirely.
func (s *Store) Clear(key string) error {
	if s.disabled() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.path(safeKey(key))
	if p == "" {
		return nil
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &WriteError{Path: p, Err: err}
	}
	return nil
}

func (s *Store) prepare(recs []Record, now time.Time) ([]Record, int) {
	sortRecords(recs)
	kept := make([]Record, 0, len(recs))
	removed := 0
	for _, r := range recs {
		if s.limits.Retention > 0 && now.Sub(r.Ts) > s.limits.Retention {
			removed++
			continue
		}
		if len(kept) >= s.limits.MaxRecords {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	// Byte budget: keep the newest records until the file would exceed it.
	out := make([]Record, 0, len(kept))
	var total int64
	for _, r := range kept {
		b, err := json.Marshal(r)
		if err != nil {
			removed++
			continue
		}
		size := int64(len(b)) + 1
		if total+size > s.limits.MaxBytes && len(out) > 0 {
			removed++
			continue
		}
		total += size
		out = append(out, r)
	}
	return out, removed
}

func (s *Store) writeAllLocked(p string, recs []Record) error {
	if len(recs) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return &WriteError{Path: p, Err: err}
		}
		return nil
	}
	var b strings.Builder
	for _, r := range recs {
		line, err := json.Marshal(r)
		if err != nil {
			return &WriteError{Path: p, Err: err}
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return &WriteError{Path: s.dir, Err: err}
	}
	tmp, err := os.CreateTemp(s.dir, ".history-*.tmp")
	if err != nil {
		return &WriteError{Path: s.dir, Err: err}
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return &WriteError{Path: tmpName, Err: err}
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return &WriteError{Path: tmpName, Err: err}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return &WriteError{Path: tmpName, Err: err}
	}
	if err := tmp.Close(); err != nil {
		return &WriteError{Path: tmpName, Err: err}
	}
	if err := os.Rename(tmpName, p); err != nil {
		return &WriteError{Path: p, Err: err}
	}
	return nil
}

func sortRecords(recs []Record) {
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].Ts.Equal(recs[j].Ts) {
			return recs[i].ID > recs[j].ID
		}
		return recs[i].Ts.After(recs[j].Ts)
	})
}

// ReadError reports that a history file or directory could not be read.
type ReadError struct {
	Path string
	Err  error
}

func (e *ReadError) Error() string {
	return fmt.Sprintf("read history %q: %v", e.Path, e.Err)
}

func (e *ReadError) Unwrap() error { return e.Err }

// WriteError reports that history could not be written. The path and the
// underlying error are both preserved so the UI can say what failed and where.
type WriteError struct {
	Path string
	Err  error
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("write history %q: %v", e.Path, e.Err)
}

func (e *WriteError) Unwrap() error { return e.Err }
