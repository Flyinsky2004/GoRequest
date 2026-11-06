package localstate

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorequest/internal/core"
)

func rec(ts time.Time, id, method, u string, status int) Record {
	return Record{Ts: ts, ID: id, Method: method, URL: u, Status: status}
}

func TestProjectKeySeparatesClonesAndIsStable(t *testing.T) {
	id := "example.users"
	a := ProjectKey(id, "/tmp/work/clone-a")
	b := ProjectKey(id, "/tmp/work/clone-b")
	if a == b {
		t.Fatalf("two clones of the same project must not share history: %q", a)
	}
	if strings.ContainsAny(a, "/\\ ") {
		t.Fatalf("key must be filesystem-safe: %q", a)
	}
	if a != ProjectKey(id, "/tmp/work/clone-a") {
		t.Fatal("key changed for the same root")
	}
	if got := ProjectKey("", "/tmp/x"); got == "" || strings.Contains(got, "..") {
		t.Fatalf("empty project id fallback is unsafe: %q", got)
	}
}

func TestAppendLoadSortRemoveClear(t *testing.T) {
	s := NewStore(t.TempDir(), DefaultLimits())
	key := "proj-abc"
	now := time.Now()
	if _, err := s.Append(key, rec(now.Add(-time.Hour), "a", "GET", "http://a", 200)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(key, rec(now, "b", "POST", "http://b", 500)); err != nil {
		t.Fatal(err)
	}
	res, err := s.Load(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 2 || res.Records[0].ID != "b" {
		t.Fatalf("newest-first order wrong: %+v", res.Records)
	}
	if res.Diagnostics.CorruptLines != 0 || !res.Diagnostics.Exists {
		t.Fatalf("unexpected diagnostics: %+v", res.Diagnostics)
	}

	removed, err := s.Remove(key, "b", time.Time{})
	if err != nil || !removed {
		t.Fatalf("remove by id = %v, %v", removed, err)
	}
	res, _ = s.Load(key)
	if len(res.Records) != 1 || res.Records[0].ID != "a" {
		t.Fatalf("remove wrong: %+v", res.Records)
	}
	// Removing a non-existent id must not rewrite the file or error.
	if removed, err := s.Remove(key, "nope", time.Time{}); err != nil || removed {
		t.Fatalf("remove missing = %v, %v", removed, err)
	}

	if err := s.Clear(key); err != nil {
		t.Fatal(err)
	}
	res, err = s.Load(key)
	if err != nil || len(res.Records) != 0 || res.Diagnostics.Exists {
		t.Fatalf("clear failed: %+v %v", res, err)
	}
}

func TestFilters(t *testing.T) {
	now := time.Now()
	recs := []Record{
		{Ts: now, ProjectKey: "p1", RequestID: "r1", RequestName: "Login", Method: "POST", URL: "https://api/login", Status: 200, Outcome: OutcomeOK},
		{Ts: now.Add(-2 * time.Hour), ProjectKey: "p1", RequestID: "r2", RequestName: "Users", Method: "GET", URL: "https://api/users", Status: 404, Outcome: OutcomeOK},
		{Ts: now.Add(-48 * time.Hour), ProjectKey: "p2", RequestID: "r3", RequestName: "Crash", Method: "GET", URL: "https://api/crash", Outcome: OutcomeError, Error: "dial failed"},
		{Ts: now.Add(-72 * time.Hour), ProjectKey: "p2", RequestID: "r4", RequestName: "Slow", Method: "GET", URL: "https://api/slow", Outcome: OutcomeCanceled},
	}
	cases := []struct {
		name string
		f    Filter
		want []string
	}{
		{"all", Filter{}, []string{"r1", "r2", "r3", "r4"}},
		{"project", Filter{ProjectKey: "p2"}, []string{"r3", "r4"}},
		{"request", Filter{RequestID: "r2"}, []string{"r2"}},
		{"success", Filter{Result: "success"}, []string{"r1"}},
		{"client", Filter{Result: "client"}, []string{"r2"}},
		{"network", Filter{Result: "network"}, []string{"r3"}},
		{"canceled", Filter{Result: "canceled"}, []string{"r4"}},
		{"since", Filter{Since: now.Add(-24 * time.Hour)}, []string{"r1", "r2"}},
		{"query-name", Filter{Query: "login"}, []string{"r1"}},
		{"query-url", Filter{Query: "USERS"}, []string{"r2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterRecords(recs, tc.f)
			var ids []string
			for _, r := range got {
				ids = append(ids, r.RequestID)
			}
			if strings.Join(ids, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("filter %+v = %v, want %v", tc.f, ids, tc.want)
			}
		})
	}
}

func TestLimitsRetentionCountAndBytes(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, Limits{MaxRecords: 3, MaxBytes: 1 << 20, MaxRecordBytes: 1 << 20, Retention: 24 * time.Hour})
	now := time.Now()
	// An expired record is pruned by the next append.
	if _, err := s.Append("k", rec(now.Add(-48*time.Hour), "old", "GET", "http://old", 200)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		r := rec(now.Add(time.Duration(i)*time.Minute), fmt.Sprintf("n%d", i), "GET", "http://x", 200)
		if _, err := s.Append("k", r); err != nil {
			t.Fatal(err)
		}
	}
	res, _ := s.Load("k")
	if len(res.Records) != 3 {
		t.Fatalf("count limit not applied: %d records", len(res.Records))
	}
	for _, r := range res.Records {
		if r.ID == "old" {
			t.Fatal("retention did not prune the expired record")
		}
	}

	// Byte budget keeps the newest records and never exceeds the bound.
	dir2 := t.TempDir()
	s2 := NewStore(dir2, Limits{MaxRecords: 1000, MaxBytes: 1400, MaxRecordBytes: 1400, Retention: -1})
	for i := 0; i < 20; i++ {
		r := rec(now.Add(time.Duration(i)*time.Second), fmt.Sprintf("b%02d", i), "GET", "http://x/"+strings.Repeat("a", 120), 200)
		if _, err := s2.Append("k", r); err != nil {
			t.Fatal(err)
		}
	}
	res2, _ := s2.Load("k")
	var total int64
	for _, r := range res2.Records {
		b, _ := json.Marshal(r)
		total += int64(len(b)) + 1
	}
	if total > 1400 {
		t.Fatalf("byte budget exceeded: %d", total)
	}
	if len(res2.Records) >= 20 || len(res2.Records) == 0 {
		t.Fatalf("byte pruning did not keep a bounded newest slice: %d", len(res2.Records))
	}
	if res2.Records[0].ID != "b19" {
		t.Fatalf("newest record lost: %+v", res2.Records[0])
	}
}

func TestAppendDropsSnapshotsBeforeRejecting(t *testing.T) {
	s := NewStore(t.TempDir(), Limits{MaxRecords: 10, MaxBytes: 4096, MaxRecordBytes: 900, Retention: -1})
	big := strings.Repeat("x", 700)
	r := Record{
		Ts: time.Now(), ID: "withsnap", Method: "GET", URL: "http://x",
		Request:  &Snapshot{Body: big, BodySize: int64(len(big))},
		Response: &Snapshot{Body: big, BodySize: int64(len(big))},
	}
	res, err := s.Append("k", r)
	if err != nil {
		t.Fatalf("append should trim snapshots, got %v", err)
	}
	if !res.Trimmed {
		t.Fatal("expected Trimmed to report dropped snapshots")
	}
	loaded, _ := s.Load("k")
	if len(loaded.Records) != 1 || loaded.Records[0].Request != nil || loaded.Records[0].Response != nil {
		t.Fatalf("trimmed record still has snapshots: %+v", loaded.Records)
	}

	// A record too large even without snapshots is rejected and nothing is written.
	s2 := NewStore(t.TempDir(), Limits{MaxRecords: 10, MaxBytes: 300, MaxRecordBytes: 300, Retention: -1})
	_, err = s2.Append("k", Record{Ts: time.Now(), Method: "GET", URL: "http://x/" + strings.Repeat("y", 500)})
	if !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("want ErrRecordTooLarge, got %v", err)
	}
	if res, _ := s2.Load("k"); len(res.Records) != 0 {
		t.Fatalf("rejected record was persisted: %+v", res.Records)
	}
}

func TestCorruptLinesAreSkippedAndDiagnosed(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, DefaultLimits())
	key := "k"
	now := time.Now()
	good1, _ := json.Marshal(rec(now, "good1", "GET", "http://a", 200))
	good2, _ := json.Marshal(rec(now.Add(-time.Minute), "good2", "GET", "http://b", 200))
	content := string(good1) + "\n{not-json\nnull\n" + string(good2) + "\n"
	if err := os.WriteFile(filepath.Join(dir, key+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Load(key)
	if err != nil {
		t.Fatalf("corrupt lines must not fail the load: %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("valid records = %d, want 2", len(res.Records))
	}
	if res.Diagnostics.CorruptLines != 2 {
		t.Fatalf("corrupt lines = %d, want 2", res.Diagnostics.CorruptLines)
	}
	// Cleanup rewrites the file without the corrupt lines.
	if _, err := s.Cleanup(key); err != nil {
		t.Fatal(err)
	}
	res, _ = s.Load(key)
	if res.Diagnostics.CorruptLines != 0 || len(res.Records) != 2 {
		t.Fatalf("cleanup did not normalize the file: %+v", res)
	}
}

func TestAppendReportsCorruptLinesItRewrites(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, DefaultLimits())
	key := "k"
	valid, _ := json.Marshal(rec(time.Now().Add(-time.Minute), "old", "GET", "http://old", 200))
	content := "{bad\n" + string(valid) + "\n"
	if err := os.WriteFile(filepath.Join(dir, key+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Append(key, rec(time.Now(), "new", "GET", "http://new", 200))
	if err != nil {
		t.Fatal(err)
	}
	if res.CorruptLines != 1 {
		t.Fatalf("append should report the corrupt line it dropped, got %d", res.CorruptLines)
	}
	loaded, _ := s.Load(key)
	if len(loaded.Records) != 2 || loaded.Diagnostics.CorruptLines != 0 {
		t.Fatalf("append/rewrite state wrong: %+v %+v", loaded.Records, loaded.Diagnostics)
	}
}

func TestWriteAndReadFailuresAreDiagnosable(t *testing.T) {
	// A plain file where the store wants a directory is an ENOTDIR write failure.
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(blocker, DefaultLimits())
	_, err := s.Append("k", rec(time.Now(), "a", "GET", "http://a", 200))
	if err == nil {
		t.Fatal("append into a non-directory must fail")
	}
	// The store refuses to append over a location it cannot read, so the
	// diagnosable failure may surface as ReadError or WriteError. Either way it
	// must name the path instead of silently succeeding.
	var we *WriteError
	var re *ReadError
	if !errors.As(err, &we) && !errors.As(err, &re) {
		t.Fatalf("filesystem failure should be diagnosable: %v", err)
	}
	if we != nil && we.Path == "" || re != nil && re.Path == "" {
		t.Fatalf("failure should carry a path: %v", err)
	}

	// A directory where the record file should be is a read failure.
	dir := t.TempDir()
	s2 := NewStore(dir, DefaultLimits())
	if err := os.Mkdir(filepath.Join(dir, "k.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := s2.Load("k")
	if err == nil {
		t.Fatal("loading a directory as history must fail")
	}
	var readErr *ReadError
	if !errors.As(err, &readErr) || readErr.Path == "" {
		t.Fatalf("read failure should carry a path: %v", err)
	}
	if res.Diagnostics.ReadError == "" {
		t.Fatal("diagnostics should record the read error")
	}
}

func TestPermissionFailureIsDiagnosable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses directory permissions")
	}
	dir := t.TempDir()
	s := NewStore(dir, DefaultLimits())
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := s.Append("k", rec(time.Now(), "a", "GET", "http://a", 200))
	if err == nil {
		t.Fatal("append into a read-only directory must fail")
	}
	var we *WriteError
	if !errors.As(err, &we) {
		t.Fatalf("permission failure should be a WriteError: %v", err)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("permission failure should preserve os.ErrPermission: %v", err)
	}
}

func TestConcurrentAppendsRemainConsistent(t *testing.T) {
	s := NewStore(t.TempDir(), Limits{MaxRecords: 1000, MaxBytes: 4 << 20, MaxRecordBytes: 1 << 20, Retention: -1})
	const goroutines, each = 8, 25
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				r := rec(time.Now(), fmt.Sprintf("g%d-%d", g, i), "GET", fmt.Sprintf("http://x/%d/%d", g, i), 200)
				if _, err := s.Append("k", r); err != nil {
					errCh <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent append: %v", err)
	}
	res, err := s.Load("k")
	if err != nil {
		t.Fatal(err)
	}
	if res.Diagnostics.CorruptLines != 0 {
		t.Fatalf("concurrent writes corrupted %d lines", res.Diagnostics.CorruptLines)
	}
	if len(res.Records) != goroutines*each {
		t.Fatalf("records = %d, want %d (lost appends?)", len(res.Records), goroutines*each)
	}
	seen := map[string]bool{}
	for _, r := range res.Records {
		if seen[r.ID] {
			t.Fatalf("duplicate id %q", r.ID)
		}
		seen[r.ID] = true
	}
	// No temporary files may be left behind.
	entries, _ := os.ReadDir(s.Dir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".history-") {
			t.Fatalf("temporary file leaked: %s", e.Name())
		}
	}
}

func TestLoadAllAcrossProjects(t *testing.T) {
	s := NewStore(t.TempDir(), DefaultLimits())
	now := time.Now()
	if _, err := s.Append("p1-aaa", Record{Ts: now, ProjectKey: "p1-aaa", ProjectName: "One", RequestID: "a", Method: "GET", URL: "http://one", Status: 200}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("p2-bbb", Record{Ts: now.Add(-time.Minute), ProjectKey: "p2-bbb", ProjectName: "Two", RequestID: "b", Method: "POST", URL: "http://two", Status: 201}); err != nil {
		t.Fatal(err)
	}
	all, err := s.LoadAll(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Records) != 2 || all.Records[0].RequestID != "a" {
		t.Fatalf("all-projects merge wrong: %+v", all.Records)
	}
	one, _ := s.LoadAll(Filter{ProjectKey: "p2-bbb"})
	if len(one.Records) != 1 || one.Records[0].ProjectName != "Two" {
		t.Fatalf("project filter wrong: %+v", one.Records)
	}
}

func TestLegacyMetadataRecordStillLoads(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, DefaultLimits())
	// Exactly the shape the metadata-only store wrote before this engine.
	legacy := `{"ts":"2026-01-02T03:04:05Z","method":"GET","url":"http://legacy/x","env":"dev","status":200,"durationMs":12,"size":34,"requestId":"r1"}`
	if err := os.WriteFile(filepath.Join(dir, "k.jsonl"), []byte(legacy+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Load("k")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 1 {
		t.Fatalf("legacy record lost: %+v", res.Records)
	}
	r := res.Records[0]
	if r.URL != "http://legacy/x" || r.Status != 200 || r.RequestID != "r1" {
		t.Fatalf("legacy fields not preserved: %+v", r)
	}
	if r.OutcomeValue() != OutcomeOK || r.ResultClass() != "success" {
		t.Fatalf("legacy outcome derived wrong: %+v", r)
	}
	if r.ProjectKey != "k" {
		t.Fatalf("legacy record should inherit its file key: %q", r.ProjectKey)
	}
}

func TestSafeKeyBlocksPathTraversal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "history")
	s := NewStore(dir, DefaultLimits())
	if _, err := s.Append("../../escape", rec(time.Now(), "a", "GET", "http://a", 200)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "escape.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("history escaped its directory: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || strings.Contains(entries[0].Name(), "/") {
		t.Fatalf("unexpected files in history dir: %v", entries)
	}
}

func TestDisabledStoreIsInert(t *testing.T) {
	s := NewStore("", DefaultLimits())
	if _, err := s.Append("k", rec(time.Now(), "a", "GET", "http://a", 200)); err != nil {
		t.Fatalf("disabled append should be a no-op: %v", err)
	}
	if res, err := s.Load("k"); err != nil || len(res.Records) != 0 {
		t.Fatalf("disabled load = %+v %v", res, err)
	}
	if err := s.Clear("k"); err != nil {
		t.Fatalf("disabled clear should be a no-op: %v", err)
	}
}

func TestBuildRecordRedactsSecretsAndDefaultsToNoBodies(t *testing.T) {
	const secret = "tok-" + "Test-Value-7f3b"
	red := core.NewRedactor()
	red.RegisterSecret(secret)
	red.RegisterBasicPair("alice", "pw-Test-Value-9c4a")
	red.RegisterSensitiveKey("header", "X-Api-Key")
	b64 := base64.StdEncoding.EncodeToString([]byte(secret))
	escaped := url.QueryEscape(secret)

	in := HistoryInput{
		ProjectKey:  "p1",
		ProjectName: "Demo",
		RequestID:   "r1",
		RequestName: "Login",
		Environment: "dev",
		Method:      "post",
		URL:         "https://user:p@ssw0rd@api.test/" + secret + "?token=" + secret + "&q=1",
		Status:      200,
		Duration:    1500 * time.Millisecond,
		SizeBytes:   42,
		RequestHeaders: [][2]string{
			{"Authorization", "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:p@ssw0rd"))},
			{"X-Api-Key", "abc123"},
			{"Cookie", "sid=" + secret},
			{"Accept", "application/json"},
		},
		RequestBody:        []byte(`{"password":"` + secret + `"}`),
		RequestContentType: "application/json",
		ResponseHeaders:    [][2]string{{"Set-Cookie", "token=" + secret + "; HttpOnly"}, {"Content-Type", "application/json"}},
		ResponseBody:       []byte(`{"echo":"` + secret + `","encoded":"` + b64 + `"}`),
		Error:              "request failed with " + secret + " / " + escaped,
		Redactor:           red,
		Policy:             DefaultPolicy(),
	}
	r := BuildRecord(in, DefaultPolicy())
	if r.Method != "POST" {
		t.Fatalf("method not normalized: %q", r.Method)
	}
	all, _ := json.Marshal(r)
	for _, leak := range []string{secret, b64, escaped, "abc123", "pw-Test-Value-9c4a"} {
		if strings.Contains(string(all), leak) {
			t.Fatalf("record leaked %q: %s", leak, all)
		}
	}
	if r.Request == nil || r.Response == nil {
		t.Fatal("default policy should keep redacted metadata snapshots")
	}
	if r.Request.Body != "" || r.Request.Omitted != OmitPolicy {
		t.Fatalf("default policy must not persist bodies: %+v", r.Request)
	}
	if r.Request.BodySize != int64(len(in.RequestBody)) {
		t.Fatalf("body size not reported: %+v", r.Request)
	}
	if !strings.Contains(r.Request.Headers[0][1], "Basic ***") {
		t.Fatalf("authorization scheme/value not masked: %+v", r.Request.Headers)
	}

	withBodies := DefaultPolicy()
	withBodies.Bodies = true
	withBodies.MaxBodyBytes = 1024
	r2 := BuildRecord(in, withBodies)
	if r2.Request.Body == "" || r2.Response.Body == "" {
		t.Fatalf("bodies should be captured when enabled: %+v", r2)
	}
	all2, _ := json.Marshal(r2)
	for _, leak := range []string{secret, b64, escaped, "abc123", "pw-Test-Value-9c4a"} {
		if strings.Contains(string(all2), leak) {
			t.Fatalf("captured body leaked %q: %s", leak, all2)
		}
	}
	if !strings.Contains(r2.Request.Body, "***") {
		t.Fatalf("redaction mask missing from request body: %q", r2.Request.Body)
	}
}

func TestBuildRecordBodyOmissionReasons(t *testing.T) {
	p := DefaultPolicy()
	p.Bodies = true
	p.MaxBodyBytes = 32

	cases := []struct {
		name        string
		body        []byte
		contentType string
		truncated   bool
		want        string
	}{
		{"large", []byte(strings.Repeat("x", 64)), "text/plain", false, OmitLarge},
		{"binary", []byte{0x89, 'P', 'N', 'G', 0, 0}, "image/png", false, OmitBinary},
		{"truncated", []byte("small"), "text/plain", true, OmitTruncated},
		{"text", []byte("hello"), "text/plain", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := BuildRecord(HistoryInput{Method: "GET", URL: "http://x", ResponseBody: tc.body, ResponseHeaders: [][2]string{{"Content-Type", tc.contentType}}, ResponseTruncated: tc.truncated, Policy: p}, p)
			if r.Response.Omitted != tc.want {
				t.Fatalf("omitted = %q, want %q", r.Response.Omitted, tc.want)
			}
			if tc.want == "" && r.Response.Body != "hello" {
				t.Fatalf("text body not stored: %+v", r.Response)
			}
			if tc.want != "" && r.Response.Body != "" {
				t.Fatalf("omitted body was stored: %+v", r.Response)
			}
		})
	}
}

func TestOutcomeDerivationForOldRecords(t *testing.T) {
	ok := Record{Status: 204}
	if ok.OutcomeValue() != OutcomeOK || ok.ResultClass() != "success" {
		t.Fatalf("old 204 record misclassified: %+v", ok)
	}
	failed := Record{Error: "dial failed"}
	if failed.OutcomeValue() != OutcomeError || failed.ResultClass() != "network" {
		t.Fatalf("old error record misclassified: %+v", failed)
	}
	server := Record{Status: 503}
	if server.ResultClass() != "server" {
		t.Fatalf("5xx class wrong: %q", server.ResultClass())
	}
}
