package localstate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorequest/internal/core"
)

func TestFullPolicyPreservesRawSecretsBinaryAndLargeBodiesAcrossStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// Exercise DefaultLimits() directly to prove full-capture records are NOT
	// secondarily trimmed by DefaultMaxRecordBytes (512 KiB) or DefaultMaxBytes (2 MiB).
	s := NewStore(dir, DefaultLimits())

	secretTok := "{{SECRET_full_tok_91a}}"
	secretPass := "{{SECRET_full_pass_82b}}"
	secretCookie := "{{SECRET_full_sid_73c}}"
	red := core.NewRedactor()
	red.RegisterSecret(secretTok)
	red.RegisterSecret(secretPass)
	red.RegisterSecret(secretCookie)

	// 768 KiB binary payload (> 64 KiB DefaultMaxSnapshotBody and > 512 KiB DefaultMaxRecordBytes)
	// containing PNG header, NUL bytes, invalid UTF-8 sequences, and secret bytes.
	binReq := make([]byte, 768<<10)
	copy(binReq, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0xff, 0xfe, 0xfd})
	copy(binReq[64:], []byte("embedded:"+secretTok))
	for i := 256; i < len(binReq); i++ {
		binReq[i] = byte((i*31 + 7) & 0xff)
	}

	// 1.5 MiB UTF-8 JSON response (> 64 KiB and > 512 KiB).
	largePad := strings.Repeat("abcdefghij012345", (1536<<10)/16)
	jsonResp := []byte(`{"token":"` + secretTok + `","cookie":"` + secretCookie + `","pad":"` + largePad + `"}`)

	rawURL := "https://svc:" + secretPass + "@api.example.test/v1/upload?access_token=" + secretTok + "&keep=1"
	reqHeaders := [][2]string{
		{"Authorization", "Bearer " + secretTok},
		{"Cookie", "sid=" + secretCookie},
		{"X-Duplicate", "first"},
		{"X-Duplicate", "second-" + secretPass},
		{"Content-Type", "application/octet-stream"},
	}
	respHeaders := [][2]string{
		{"Content-Type", "application/json; charset=utf-8"},
		{"Set-Cookie", "sid=" + secretCookie + "; HttpOnly"},
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	rec1 := BuildRecord(HistoryInput{
		Ts:                 now,
		ProjectKey:         "proj-full",
		ProjectName:        "Proj-" + secretTok,
		RequestID:          "req-upload",
		RequestName:        "Upload-" + secretPass,
		Environment:        "prod-" + secretTok,
		Method:             "post",
		URL:                rawURL,
		Outcome:            OutcomeOK,
		Status:             201,
		Proto:              "HTTP/2.0",
		Duration:           42 * time.Millisecond,
		SizeBytes:          int64(len(jsonResp)),
		RequestHeaders:     reqHeaders,
		RequestBody:        binReq,
		RequestHasBody:     true,
		RequestContentType: "application/octet-stream",
		ResponseHeaders:    respHeaders,
		ResponseBody:       jsonResp,
		Redactor:           red,
	}, FullPolicy())

	// Second large full-capture record so the total .jsonl file exceeds DefaultMaxBytes (2 MiB).
	rec2 := BuildRecord(HistoryInput{
		Ts:              now.Add(-time.Second),
		ProjectKey:      "proj-full",
		RequestID:       "req-second",
		Method:          "PUT",
		URL:             "https://api.example.test/v1/second?key=" + secretTok,
		Outcome:         OutcomeOK,
		Status:          200,
		SizeBytes:       int64(len(binReq)),
		RequestHeaders:  [][2]string{{"X-Api-Key", secretTok}},
		RequestBody:     binReq,
		RequestHasBody:  true,
		ResponseHeaders: [][2]string{{"Content-Type", "application/octet-stream"}},
		ResponseBody:    binReq,
		Redactor:        red,
	}, FullPolicy())

	ar2, err := s.Append("proj-full", rec2)
	if err != nil {
		t.Fatalf("Append rec2: %v", err)
	}
	if ar2.Trimmed || ar2.Record.SnapshotTrimmed {
		t.Fatalf("rec2 unexpectedly trimmed: %+v", ar2)
	}
	ar1, err := s.Append("proj-full", rec1)
	if err != nil {
		t.Fatalf("Append rec1: %v", err)
	}
	if ar1.Trimmed || ar1.Record.SnapshotTrimmed {
		t.Fatalf("rec1 unexpectedly trimmed: %+v", ar1)
	}
	if ar1.Bytes <= DefaultMaxBytes {
		t.Fatalf("expected combined JSONL > DefaultMaxBytes (%d), got %d", DefaultMaxBytes, ar1.Bytes)
	}

	// Reopen a brand-new Store instance and run Cleanup + Load to prove disk reload
	// and maintenance passes do not strip or drop full-capture payloads.
	reopened := NewStore(dir, DefaultLimits())
	if _, err := reopened.Cleanup("proj-full"); err != nil {
		t.Fatalf("Cleanup after reopen: %v", err)
	}
	loaded, err := reopened.Load("proj-full")
	if err != nil {
		t.Fatalf("Load after reopen: %v", err)
	}
	if loaded.Diagnostics.CorruptLines != 0 {
		t.Fatalf("corrupt lines = %d", loaded.Diagnostics.CorruptLines)
	}
	if len(loaded.Records) != 2 {
		t.Fatalf("records after reopen+cleanup = %d, want 2", len(loaded.Records))
	}

	got := loaded.Records[0]
	if !got.IsUnredacted() || !got.HasFullSnapshots() {
		t.Fatalf("expected full unredacted record: Unredacted=%v SnapshotTrimmed=%v", got.Unredacted, got.SnapshotTrimmed)
	}
	if got.URL != rawURL {
		t.Fatalf("URL = %q, want %q", got.URL, rawURL)
	}
	if got.ProjectName != "Proj-"+secretTok || got.RequestName != "Upload-"+secretPass || got.Env != "prod-"+secretTok {
		t.Fatalf("metadata unexpectedly redacted: %+v", got)
	}
	if got.Request == nil || got.Request.Redacted || !got.Request.IsBinaryBody() {
		t.Fatalf("unexpected request snapshot: %+v", got.Request)
	}
	if len(got.Request.Headers) != len(reqHeaders) || got.Request.Headers[0][1] != "Bearer "+secretTok || got.Request.Headers[3][1] != "second-"+secretPass {
		t.Fatalf("request headers altered: %+v", got.Request.Headers)
	}
	gotBinReq, err := got.Request.BodyBytes()
	if err != nil {
		t.Fatalf("Request.BodyBytes: %v", err)
	}
	if !bytes.Equal(gotBinReq, binReq) {
		t.Fatalf("binary request body mismatch: got %d bytes, want %d bytes", len(gotBinReq), len(binReq))
	}

	if got.Response == nil || got.Response.Redacted || got.Response.IsBinaryBody() {
		t.Fatalf("unexpected response snapshot: %+v", got.Response)
	}
	if got.Response.Headers[1][1] != "sid="+secretCookie+"; HttpOnly" {
		t.Fatalf("response Set-Cookie altered: %+v", got.Response.Headers)
	}
	gotJSONResp, err := got.Response.BodyBytes()
	if err != nil {
		t.Fatalf("Response.BodyBytes: %v", err)
	}
	if !bytes.Equal(gotJSONResp, jsonResp) {
		t.Fatalf("large JSON response body mismatch: got %d bytes, want %d bytes", len(gotJSONResp), len(jsonResp))
	}
}

func TestFullPolicyInvalidUTF8DeclaredAsTextRoundTripsLosslessly(t *testing.T) {
	s := NewStore(t.TempDir(), FullLimits())
	// Even if a server mislabels arbitrary binary bytes as text/plain, json.Marshal
	// must never replace invalid UTF-8 with U+FFFD in full mode.
	raw := []byte{'h', 'i', 0x00, 0x80, 0xff, 0xfe, 'z'}
	rec := BuildRecord(HistoryInput{
		Method:          "GET",
		URL:             "https://api.example.test/raw",
		Status:          200,
		ResponseHeaders: [][2]string{{"Content-Type", "text/plain"}},
		ResponseBody:    raw,
		SizeBytes:       int64(len(raw)),
	}, FullPolicy())

	if _, err := s.Append("k", rec); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load("k")
	if err != nil {
		t.Fatal(err)
	}
	got, err := loaded.Records[0].Response.BodyBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("got %x, want %x", got, raw)
	}
}

func TestFullPolicyPreservesRawErrorStrings(t *testing.T) {
	s := NewStore(t.TempDir(), FullLimits())
	secret := "{{SECRET_err_token}}"
	red := core.NewRedactor()
	red.RegisterSecret(secret)
	rawErr := `Get "https://api.example.test/hook?token=` + secret + `": x509: certificate signed by unknown authority`

	rec := BuildRecord(HistoryInput{
		Method:         "GET",
		URL:            "https://api.example.test/hook?token=" + secret,
		Outcome:        OutcomeError,
		Error:          rawErr,
		RequestHeaders: [][2]string{{"Authorization", "Bearer " + secret}},
		Redactor:       red,
	}, FullPolicy())

	if _, err := s.Append("k", rec); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load("k")
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Records[0]
	if !got.IsUnredacted() {
		t.Fatal("expected IsUnredacted() == true")
	}
	if got.Error != rawErr {
		t.Fatalf("Error = %q, want %q", got.Error, rawErr)
	}
	if got.URL != "https://api.example.test/hook?token="+secret {
		t.Fatalf("URL = %q", got.URL)
	}
	if got.Request == nil || got.Request.Headers[0][1] != "Bearer "+secret {
		t.Fatalf("request headers = %+v", got.Request)
	}
}

func TestFullPolicyNetworkTruncatedAndStreamedUploadBoundaries(t *testing.T) {
	s := NewStore(t.TempDir(), FullLimits())
	secret := "{{SECRET_trunc_tok}}"
	red := core.NewRedactor()
	red.RegisterSecret(secret)

	// 1. Network-truncated response: captured prefix bytes MUST be preserved
	// losslessly while Truncated=true marks that the unreceived tail is missing.
	capturedPrefix := []byte{0x00, 0xff, 'p', 'a', 'r', 't', ':'}
	capturedPrefix = append(capturedPrefix, []byte(secret)...)
	recTrunc := BuildRecord(HistoryInput{
		Ts:                time.Now().UTC(),
		Method:            "GET",
		URL:               "https://api.example.test/stream?token=" + secret,
		Status:            200,
		SizeBytes:         int64(len(capturedPrefix)),
		ResponseHeaders:   [][2]string{{"Content-Type", "application/octet-stream"}},
		ResponseBody:      capturedPrefix,
		ResponseTruncated: true,
		Redactor:          red,
	}, FullPolicy())
	if _, err := s.Append("trunc", recTrunc); err != nil {
		t.Fatal(err)
	}
	loadedTrunc, err := s.Load("trunc")
	if err != nil {
		t.Fatal(err)
	}
	gt := loadedTrunc.Records[0]
	if !gt.IsUnredacted() {
		t.Fatal("expected unredacted flag on network-truncated full record")
	}
	if gt.HasFullSnapshots() {
		t.Fatal("network-truncated record must NOT claim HasFullSnapshots()")
	}
	if gt.Response == nil || !gt.Response.Truncated || gt.Response.Omitted != "" {
		t.Fatalf("expected Truncated=true with captured prefix stored: %+v", gt.Response)
	}
	gotPrefix, err := gt.Response.BodyBytes()
	if err != nil || !bytes.Equal(gotPrefix, capturedPrefix) {
		t.Fatalf("captured prefix lost: got=%x want=%x err=%v", gotPrefix, capturedPrefix, err)
	}

	// 2. Unbuffered streamed upload (HasBody=true, RequestBody=nil): must mark OmitStream
	// rather than pretending an empty body was the complete upload.
	recStream := BuildRecord(HistoryInput{
		Ts:             time.Now().UTC(),
		Method:         "POST",
		URL:            "https://api.example.test/upload-stream",
		Status:         200,
		RequestHeaders: [][2]string{{"Content-Type", "multipart/form-data; boundary=x"}, {"Content-Length", "10485760"}},
		RequestHasBody: true,
		RequestBody:    nil,
		ResponseBody:   []byte(`{"ok":true}`),
	}, FullPolicy())
	if _, err := s.Append("stream", recStream); err != nil {
		t.Fatal(err)
	}
	loadedStream, err := s.Load("stream")
	if err != nil {
		t.Fatal(err)
	}
	gs := loadedStream.Records[0]
	if gs.HasFullSnapshots() {
		t.Fatal("streamed upload without buffered bytes must NOT claim HasFullSnapshots()")
	}
	if gs.Request == nil || gs.Request.Omitted != OmitStream || gs.Request.BodySize != 10485760 {
		t.Fatalf("expected OmitStream and Content-Length size, got %+v", gs.Request)
	}

	// 3. Explicit tiny MaxRecordBytes trimming marks SnapshotTrimmed=true on disk.
	tinyStore := NewStore(t.TempDir(), Limits{MaxRecords: 10, MaxBytes: 4096, MaxRecordBytes: 500, Retention: -1})
	bigFull := BuildRecord(HistoryInput{
		Method:       "POST",
		URL:          "https://api.example.test/tiny",
		Status:       200,
		RequestBody:  bytes.Repeat([]byte("a"), 800),
		ResponseBody: bytes.Repeat([]byte("b"), 800),
	}, FullPolicy())
	ar, err := tinyStore.Append("tiny", bigFull)
	if err != nil {
		t.Fatalf("tinyStore.Append: %v", err)
	}
	if !ar.Trimmed {
		t.Fatal("expected Trimmed=true under explicit 500B MaxRecordBytes")
	}
	loadedTiny, _ := tinyStore.Load("tiny")
	if len(loadedTiny.Records) != 1 || !loadedTiny.Records[0].SnapshotTrimmed || loadedTiny.Records[0].HasFullSnapshots() {
		t.Fatalf("trimmed record must persist SnapshotTrimmed=true and HasFullSnapshots()=false: %+v", loadedTiny.Records)
	}
}

func TestDefaultRedactedPolicyAndLegacyRecordsRemainDistinguishable(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, Limits{
		MaxRecords:     DefaultMaxRecords,
		MaxBytes:       FullMaxBytes,
		MaxRecordBytes: FullMaxRecordBytes,
		Retention:      30 * 24 * time.Hour,
	})
	secret := "{{SECRET_default_still_redacted}}"
	red := core.NewRedactor()
	red.RegisterSecret(secret)

	now := time.Now().UTC()
	// Expired full record (> 30 days old) should still be pruned by retention cleanup.
	expiredFull := BuildRecord(HistoryInput{
		Ts:           now.AddDate(0, 0, -45),
		Method:       "GET",
		URL:          "https://api.example.test/expired?token=" + secret,
		Status:       200,
		ResponseBody: []byte(secret),
		Redactor:     red,
	}, FullPolicy())
	// Legacy / default redacted record.
	redactedRec := BuildRecord(HistoryInput{
		Ts:             now.Add(-time.Minute),
		Method:         "POST",
		URL:            "https://api.example.test/login?token=" + secret,
		Status:         401,
		Error:          "auth failed for " + secret,
		RequestHeaders: [][2]string{{"Authorization", "Bearer " + secret}},
		RequestBody:    []byte("pw=" + secret),
		ResponseBody:   []byte("denied " + secret),
		Redactor:       red,
	}, Policy{Snapshots: true, Bodies: true, MaxBodyBytes: DefaultMaxSnapshotBody})
	// Fresh full record.
	freshFull := BuildRecord(HistoryInput{
		Ts:             now,
		Method:         "POST",
		URL:            "https://api.example.test/login?token=" + secret,
		Status:         200,
		RequestHeaders: [][2]string{{"Authorization", "Bearer " + secret}},
		RequestBody:    []byte("pw=" + secret),
		ResponseBody:   []byte("ok " + secret),
		Redactor:       red,
	}, FullPolicy())

	for _, r := range []Record{expiredFull, redactedRec, freshFull} {
		if _, err := s.Append("mix", r); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := s.Load("mix")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Records) != 2 {
		t.Fatalf("retention should keep 2 fresh records and drop expired full record, got %d", len(loaded.Records))
	}
	newest, older := loaded.Records[0], loaded.Records[1]
	if !newest.IsUnredacted() || !strings.Contains(newest.URL, secret) || !strings.Contains(newest.Request.Body, secret) {
		t.Fatalf("newest full record lost unredacted content: %+v", newest)
	}
	if older.IsUnredacted() {
		t.Fatalf("previously redacted record must NOT claim IsUnredacted(): %+v", older)
	}
	rawOlder, _ := json.Marshal(older)
	if strings.Contains(string(rawOlder), secret) {
		t.Fatalf("default redacted policy leaked secret: %s", rawOlder)
	}
}
