package core

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCustomHTTPMethodsEndToEnd(t *testing.T) {
	var hits atomic.Int32
	var gotMethod, gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusMultiStatus)
	}))
	defer srv.Close()

	for _, meth := range []string{"PURGE", "PROPFIND", "REPORT", "QUERY", "MKCOL"} {
		req := &Request{
			SchemaVersion: 1,
			ID:            "custom-" + strings.ToLower(meth),
			Name:          meth,
			HTTP: &HTTPDef{
				Method: meth,
				URL:    srv.URL + "/dav/resource",
				Body:   &Body{Type: "xml", Value: scalarStringNode(`<propfind xmlns="DAV:"><allprop/></propfind>`)},
			},
		}
		plan, err := BuildPlanWithOptions(context.Background(), req, nil, nil, nil, PlanOptions{})
		if err != nil {
			t.Fatalf("%s BuildPlanWithOptions: %v", meth, err)
		}
		res, err := Execute(context.Background(), plan, srv.Client())
		if err != nil {
			t.Fatalf("%s Execute: %v", meth, err)
		}
		if res.Status != http.StatusMultiStatus || gotMethod != meth {
			t.Fatalf("%s: status=%d gotMethod=%q", meth, res.Status, gotMethod)
		}
		if gotCT != "application/xml" || !strings.Contains(gotBody, "<propfind") {
			t.Fatalf("%s: ct=%q body=%q", meth, gotCT, gotBody)
		}
	}

	before := hits.Load()
	for _, bad := range []string{"BAD METHOD", "GET\r\nInjected: 1", "GET/POST", "(bad)", "POST:1"} {
		req := &Request{HTTP: &HTTPDef{Method: bad, URL: srv.URL}}
		_, err := BuildPlanWithOptions(context.Background(), req, nil, nil, nil, PlanOptions{})
		if err == nil || !IsConfig(err) {
			t.Fatalf("method %q: expected ConfigError, got %v", bad, err)
		}
	}
	if hits.Load() != before {
		t.Fatalf("invalid HTTP methods must fail before hitting server")
	}
}

func TestURLEncodedRoundTripAndServerEcho(t *testing.T) {
	dir := t.TempDir()
	proj, err := NewProject(dir, "FormDemo")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := proj.NewRequest("", "Token Exchange")
	if err != nil {
		t.Fatal(err)
	}

	tb := &TypedBody{
		Kind: BodyURLEncoded,
		FormFields: []FormField{
			{Name: "grant_type", Value: "client_credentials"},
			{Name: "tag", Value: "alpha"},
			{Name: "tag", Value: "beta"},
			{Name: "empty_val", Value: ""},
			{Name: "who", Value: "{{displayName}}"},
			{Name: "client_secret", Value: "{{secret.clientSecret}}"},
			{Name: "pre_sig", Value: "ab%2Bcd%3D", Encoded: true},
			{Name: "pre_mix", Value: "prefix%20{{suffix}}", Encoded: true},
			{Name: "disabled_key", Value: "never-sent", Enabled: BoolPtr(false)},
		},
	}
	body, err := tb.ToBody()
	if err != nil {
		t.Fatal(err)
	}
	entry.Request.HTTP.Method = "POST"
	entry.Request.HTTP.Body = body

	if _, err := proj.SaveRequest(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotEntry := reloaded.Requests[entry.Request.ID]

	var rawWire string
	var formVals url.Values
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		rawWire = string(b)
		r.Body = io.NopCloser(bytes.NewReader(b))
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		formVals = r.PostForm
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	gotEntry.Request.HTTP.URL = srv.URL + "/oauth/token"
	vars := map[string]string{
		"displayName": "alice & bob = 100%",
		"suffix":      "a+b",
	}
	secrets := MapSecrets{"clientSecret": "s3cr3t+val/1"}

	plan, err := BuildPlanWithOptions(context.Background(), gotEntry.Request, vars, secrets, nil, PlanOptions{
		ProjectRoot: reloaded.Dir,
	})
	if err != nil {
		t.Fatalf("BuildPlanWithOptions: %v", err)
	}
	if _, err := Execute(context.Background(), plan, srv.Client()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if gotCT != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q", gotCT)
	}
	// Verify field ordering and single encoding on the wire.
	wantPrefix := "grant_type=client_credentials&tag=alpha&tag=beta&empty_val=&who="
	if !strings.HasPrefix(rawWire, wantPrefix) {
		t.Fatalf("wire order lost: %q", rawWire)
	}
	if strings.Contains(rawWire, "disabled_key") {
		t.Fatalf("disabled field must not be sent: %q", rawWire)
	}
	if strings.Contains(rawWire, "%252B") || strings.Contains(rawWire, "%2520") {
		t.Fatalf("double encoding detected on wire: %q", rawWire)
	}
	if got := formVals["tag"]; len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("duplicate form key tag = %v", got)
	}
	if got := formVals["empty_val"]; len(got) != 1 || got[0] != "" {
		t.Fatalf("empty_val = %v", got)
	}
	if got := formVals.Get("who"); got != "alice & bob = 100%" {
		t.Fatalf("who = %q (context encoding failed)", got)
	}
	if got := formVals.Get("client_secret"); got != "s3cr3t+val/1" {
		t.Fatalf("client_secret = %q", got)
	}
	if got := formVals.Get("pre_sig"); got != "ab+cd=" {
		t.Fatalf("pre_sig = %q, want ab+cd= (no double encoding)", got)
	}
	if got := formVals.Get("pre_mix"); got != "prefix a+b" {
		t.Fatalf("pre_mix = %q, want \"prefix a+b\"", got)
	}
}

func TestMultipartFormDataRoundTripAndServerEcho(t *testing.T) {
	dir := t.TempDir()
	proj, err := NewProject(dir, "MultipartDemo")
	if err != nil {
		t.Fatal(err)
	}
	assetsDir := filepath.Join(proj.Dir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projNoteBytes := []byte("hello from project asset\n{{literal}}\n")
	if err := os.WriteFile(filepath.Join(assetsDir, "note.txt"), projNoteBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	externalDir := t.TempDir()
	localBinPath := filepath.Join(externalDir, "avatar.png")
	localBinBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, '{', '{', 'x', '}', '}'}
	if err := os.WriteFile(localBinPath, localBinBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	entry, err := proj.NewRequest("", "Upload Bundle")
	if err != nil {
		t.Fatal(err)
	}
	tb := &TypedBody{
		Kind: BodyFormData,
		MultipartParts: []MultipartPart{
			{Name: "title", Kind: "text", Value: "Report for {{owner}}"},
			{Name: "tag", Value: "v1"},
			{Name: "tag", Value: "v2"},
			{Name: "meta", Kind: "text", Value: `{"ok":true}`, ContentType: "application/json"},
			{Name: "doc", Kind: "file", File: "assets/note.txt"},
			{Name: "avatar", Kind: "file", LocalFile: "user-avatar", FileName: `quote"avatar.png`, ContentType: "image/png"},
			{Name: "skipped", Kind: "file", File: "assets/missing-disabled.txt", Enabled: BoolPtr(false)},
		},
	}
	body, err := tb.ToBody()
	if err != nil {
		t.Fatal(err)
	}
	entry.Request.HTTP.Method = "POST"
	// Include a user Content-Type header without boundary to verify the engine
	// overrides it with the generated multipart boundary.
	entry.Request.HTTP.Headers = []KV{{Name: "Content-Type", Value: "multipart/form-data"}}
	entry.Request.HTTP.Body = body

	if _, err := proj.SaveRequest(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}
	savedYAML, _, err := proj.Store.Read(entry.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(savedYAML), externalDir) || strings.Contains(string(savedYAML), localBinPath) {
		t.Fatalf("machine path leaked into saved YAML:\n%s", savedYAML)
	}
	if !strings.Contains(string(savedYAML), "localFile: user-avatar") {
		t.Fatalf("expected symbolic localFile in YAML:\n%s", savedYAML)
	}

	reloaded, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotEntry := reloaded.Requests[entry.Request.ID]

	type rxPart struct {
		name        string
		fileName    string
		contentType string
		data        []byte
	}
	var received []rxPart
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
			t.Errorf("bad Content-Type %q: %v", r.Header.Get("Content-Type"), err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("NextPart: %v", err)
				break
			}
			b, _ := io.ReadAll(p)
			received = append(received, rxPart{
				name:        p.FormName(),
				fileName:    p.FileName(),
				contentType: p.Header.Get("Content-Type"),
				data:        b,
			})
			_ = p.Close()
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	gotEntry.Request.HTTP.URL = srv.URL + "/upload"
	plan, err := BuildPlanWithOptions(
		context.Background(),
		gotEntry.Request,
		map[string]string{"owner": "Ada"},
		nil,
		nil,
		PlanOptions{
			ProjectRoot: reloaded.Dir,
			LocalFiles:  MapLocalFiles{"user-avatar": localBinPath},
		},
	)
	if err != nil {
		t.Fatalf("BuildPlanWithOptions: %v", err)
	}
	res, err := Execute(context.Background(), plan, srv.Client())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != http.StatusCreated {
		t.Fatalf("status = %d", res.Status)
	}
	if len(received) != 6 {
		t.Fatalf("received %d parts, want 6", len(received))
	}
	if received[0].name != "title" || string(received[0].data) != "Report for Ada" {
		t.Fatalf("part 0 = %+v", received[0])
	}
	if received[1].name != "tag" || string(received[1].data) != "v1" || received[2].name != "tag" || string(received[2].data) != "v2" {
		t.Fatalf("duplicate tag parts = %+v, %+v", received[1], received[2])
	}
	if received[3].contentType != "application/json" || string(received[3].data) != `{"ok":true}` {
		t.Fatalf("meta part = %+v", received[3])
	}
	if received[4].fileName != "note.txt" || !bytes.Equal(received[4].data, projNoteBytes) {
		t.Fatalf("doc part mismatch: filename=%q data=%q", received[4].fileName, received[4].data)
	}
	if received[5].fileName != `quote"avatar.png` || received[5].contentType != "image/png" || !bytes.Equal(received[5].data, localBinBytes) {
		t.Fatalf("avatar part mismatch: filename=%q ct=%q", received[5].fileName, received[5].contentType)
	}
}

func TestBinaryBodyProjectFileAndLocalMapping(t *testing.T) {
	gq := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gq, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	// Append literal "{{not_var}}" to prove binary files never undergo template replacement.
	allBytes = append(allBytes, []byte("{{not_var}}")...)
	if err := os.WriteFile(filepath.Join(gq, "assets", "raw.bin"), allBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	extFile := filepath.Join(t.TempDir(), "local.bin")
	if err := os.WriteFile(extFile, allBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	var gotBytes []byte
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotBytes, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name string
		file string
	}{
		{"project-file", "assets/raw.bin"},
		{"local-mapping", "local:raw-slot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &Request{
				SchemaVersion: 1,
				ID:            tc.name,
				Name:          tc.name,
				HTTP: &HTTPDef{
					Method: "PUT",
					URL:    srv.URL + "/blob",
					Body:   &Body{Type: "binary", File: tc.file},
				},
			}
			rawYAML, err := encodeYAML(req)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(rawYAML), extFile) {
				t.Fatalf("local path leaked into YAML:\n%s", rawYAML)
			}
			var decoded Request
			if err := decodeStrict(rawYAML, &decoded); err != nil {
				t.Fatal(err)
			}
			plan, err := BuildPlanWithOptions(context.Background(), &decoded, nil, nil, nil, PlanOptions{
				ProjectRoot: gq,
				LocalFiles:  MapLocalFiles{"raw-slot": extFile},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Execute(context.Background(), plan, srv.Client()); err != nil {
				t.Fatal(err)
			}
			if gotCT != "application/octet-stream" {
				t.Fatalf("Content-Type = %q", gotCT)
			}
			if !bytes.Equal(gotBytes, allBytes) {
				t.Fatalf("binary payload corrupted: got %d bytes, want %d", len(gotBytes), len(allBytes))
			}
		})
	}
}

func TestBodyFileForJSONTextXMLAndMutualExclusivity(t *testing.T) {
	gq := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gq, "payloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gq, "payloads", "user.json"), []byte(`{"name":"{{who}}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gq, "payloads", "bad.json"), []byte(`{not-json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gq, "payloads", "note.txt"), []byte("plain file body"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Templated JSON file escapes quotes safely instead of raw string splicing.
	reqJSON := &Request{HTTP: &HTTPDef{
		Method: "POST",
		URL:    "http://example.com/u",
		Body:   &Body{Type: "json", File: "payloads/user.json"},
	}}
	plan, err := BuildPlanWithOptions(context.Background(), reqJSON, map[string]string{"who": `O"Reilly`}, nil, nil, PlanOptions{ProjectRoot: gq})
	if err != nil {
		t.Fatal(err)
	}
	if string(plan.Body) != `{"name":"O\"Reilly"}` {
		t.Fatalf("json file body = %s", plan.Body)
	}

	// Malformed JSON file fails before network.
	reqBadJSON := &Request{HTTP: &HTTPDef{
		Method: "POST",
		URL:    "http://example.com/u",
		Body:   &Body{Type: "json", File: "payloads/bad.json"},
	}}
	if _, err := BuildPlanWithOptions(context.Background(), reqBadJSON, nil, nil, nil, PlanOptions{ProjectRoot: gq}); !IsConfig(err) {
		t.Fatalf("expected ConfigError for bad json file, got %v", err)
	}

	// Mutual exclusivity: setting both value and file must fail.
	reqBoth := &Request{HTTP: &HTTPDef{
		Method: "POST",
		URL:    "http://example.com/u",
		Body:   &Body{Type: "text", File: "payloads/note.txt", Value: scalarStringNode("inline")},
	}}
	if _, err := BuildPlanWithOptions(context.Background(), reqBoth, nil, nil, nil, PlanOptions{ProjectRoot: gq}); !IsConfig(err) {
		t.Fatalf("expected ConfigError when both value and file are set, got %v", err)
	}
}

func TestFileSecurityPathTraversalSymlinkAndMissingPreNetwork(t *testing.T) {
	gq := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	assetsDir := filepath.Join(gq, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Symlink file pointing outside .gorequest/.
	_ = os.Symlink(outsideFile, filepath.Join(assetsDir, "escape-file.txt"))
	// Symlink directory pointing outside .gorequest/.
	_ = os.Symlink(outsideDir, filepath.Join(assetsDir, "escape-dir"))
	// Broken symlink inside .gorequest/.
	_ = os.Symlink(filepath.Join(outsideDir, "does-not-exist"), filepath.Join(assetsDir, "broken.txt"))

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	badBodies := []struct {
		name string
		body *Body
	}{
		{"parent-traversal", &Body{Type: "binary", File: "../secret.txt"}},
		{"nested-traversal", &Body{Type: "binary", File: "assets/../../secret.txt"}},
		{"abs-posix", &Body{Type: "binary", File: outsideFile}},
		{"abs-windows-drive", &Body{Type: "binary", File: `C:\Users\secret.txt`}},
		{"backslash-path", &Body{Type: "binary", File: `assets\note.txt`}},
		{"symlink-file-escape", &Body{Type: "binary", File: "assets/escape-file.txt"}},
		{"symlink-dir-escape", &Body{Type: "binary", File: "assets/escape-dir/secret.txt"}},
		{"broken-symlink", &Body{Type: "binary", File: "assets/broken.txt"}},
		{"directory-target", &Body{Type: "binary", File: "assets"}},
		{"missing-project-file", &Body{Type: "binary", File: "assets/missing.bin"}},
		{"unbound-local-slot", &Body{Type: "binary", File: "local:unbound-slot"}},
		{"missing-local-target", &Body{Type: "binary", File: "local:deleted-slot"}},
		{"local-slot-with-path", &Body{Type: "binary", File: "local:../evil"}},
	}

	for _, tc := range badBodies {
		t.Run(tc.name, func(t *testing.T) {
			req := &Request{HTTP: &HTTPDef{Method: "POST", URL: srv.URL, Body: tc.body}}
			_, err := BuildPlanWithOptions(context.Background(), req, nil, nil, nil, PlanOptions{
				ProjectRoot: gq,
				LocalFiles: MapLocalFiles{
					"deleted-slot": filepath.Join(outsideDir, "gone.bin"),
				},
			})
			if err == nil || !IsConfig(err) {
				t.Fatalf("%s: expected pre-network ConfigError, got %v", tc.name, err)
			}
		})
	}

	// Multipart missing attachment must also fail before hitting network.
	mpMissing, err := (&TypedBody{
		Kind: BodyFormData,
		MultipartParts: []MultipartPart{
			{Name: "ok", Value: "1"},
			{Name: "bad", Kind: "file", File: "assets/escape-file.txt"},
		},
	}).ToBody()
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildPlanWithOptions(context.Background(), &Request{
		HTTP: &HTTPDef{Method: "POST", URL: srv.URL, Body: mpMissing},
	}, nil, nil, nil, PlanOptions{ProjectRoot: gq})
	if err == nil || !IsConfig(err) {
		t.Fatalf("multipart symlink escape: expected ConfigError, got %v", err)
	}

	if hits.Load() != 0 {
		t.Fatalf("expected 0 network requests on invalid/missing attachments, got %d", hits.Load())
	}
}

type slowCancelOpener struct {
	cancel context.CancelFunc
}

func (s *slowCancelOpener) OpenProjectFile(ctx context.Context, rel string) (*OpenedFile, error) {
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write(bytes.Repeat([]byte("a"), uploadReadChunkSize))
		s.cancel()
		_, _ = pw.Write(bytes.Repeat([]byte("b"), uploadReadChunkSize))
		_ = pw.Close()
	}()
	return &OpenedFile{Reader: pr, Name: "stream.bin", Size: -1}, nil
}

func TestUploadSizeLimitAndCancellation(t *testing.T) {
	gq := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gq, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	chunk600 := bytes.Repeat([]byte("x"), 600)
	if err := os.WriteFile(filepath.Join(gq, "assets", "a.bin"), chunk600, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gq, "assets", "b.bin"), chunk600, 0o644); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 1. Single binary file exceeds MaxUploadBytes (1024 < 1200 or 500 < 600).
	reqBin := &Request{HTTP: &HTTPDef{
		Method: "POST",
		URL:    srv.URL,
		Body:   &Body{Type: "binary", File: "assets/a.bin"},
	}}
	_, err := BuildPlanWithOptions(context.Background(), reqBin, nil, nil, nil, PlanOptions{
		ProjectRoot:    gq,
		MaxUploadBytes: 500,
	})
	if err == nil || !IsConfig(err) {
		t.Fatalf("expected ConfigError for oversized binary upload, got %v", err)
	}

	// 2. Cumulative multipart files exceed MaxUploadBytes (600 + 600 > 1000).
	mpBody, err := (&TypedBody{
		Kind: BodyFormData,
		MultipartParts: []MultipartPart{
			{Name: "f1", Kind: "file", File: "assets/a.bin"},
			{Name: "f2", Kind: "file", File: "assets/b.bin"},
		},
	}).ToBody()
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildPlanWithOptions(context.Background(), &Request{
		HTTP: &HTTPDef{Method: "POST", URL: srv.URL, Body: mpBody},
	}, nil, nil, nil, PlanOptions{
		ProjectRoot:    gq,
		MaxUploadBytes: 1000,
	})
	if err == nil || !IsConfig(err) {
		t.Fatalf("expected ConfigError for cumulative multipart upload limit, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("oversized uploads must fail before hitting network, hits=%d", hits.Load())
	}

	// 3. Cancellation mid-read while building body from file stream.
	ctx, cancel := context.WithCancel(context.Background())
	_, err = BuildPlanWithOptions(ctx, reqBin, nil, nil, nil, PlanOptions{
		ProjectFiles: &slowCancelOpener{cancel: cancel},
	})
	if err == nil || !IsRun(err) || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled RunError during body read, got %v", err)
	}

	// 4. Mid-flight network upload cancellation via Execute.
	started := make(chan struct{})
	unblock := make(chan struct{})
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64)
		_, _ = r.Body.Read(buf)
		close(started)
		<-unblock
	}))
	defer slowSrv.Close()
	defer close(unblock)

	validPlan, err := BuildPlanWithOptions(context.Background(), &Request{
		HTTP:    &HTTPDef{Method: "POST", URL: slowSrv.URL, Body: &Body{Type: "binary", File: "assets/a.bin"}},
		Timeout: "5s",
	}, nil, nil, nil, PlanOptions{ProjectRoot: gq})
	if err != nil {
		t.Fatal(err)
	}
	execCtx, execCancel := context.WithCancel(context.Background())
	go func() {
		<-started
		execCancel()
	}()
	_, err = Execute(execCtx, validPlan, slowSrv.Client())
	if err == nil || !IsRun(err) || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled RunError during upload Execute, got %v", err)
	}

	// 5. OpenBodyStream enforces both size bound and cancellation on Read.
	stream, err := OpenBodyStream(execCtx, &Body{Type: "binary", File: "assets/a.bin"}, nil, PlanOptions{ProjectRoot: gq})
	if err == nil {
		_ = stream.Reader.Close()
		t.Fatal("expected OpenBodyStream to reject already-canceled context")
	}
}

func TestQueryHeaderDuplicatesEmptyValuesAndNoDoubleEncoding(t *testing.T) {
	rawInput := "?q=hello%20world&filter=a%3Db%26c&dup=1&dup=2&empty=&id={{userId}}\n# off=skip"
	kvs, err := ParseRawQueryKV(rawInput)
	if err != nil {
		t.Fatalf("ParseRawQueryKV: %v", err)
	}
	formatted := FormatRawQueryKV(kvs)
	if strings.Contains(formatted, "%25") {
		t.Fatalf("FormatRawQueryKV double-encoded percent: %q", formatted)
	}
	kvsBack, err := ParseRawQueryKV(formatted)
	if err != nil {
		t.Fatalf("re-parse raw query: %v", err)
	}
	if len(kvsBack) != len(kvs) {
		t.Fatalf("round-trip count = %d, want %d", len(kvsBack), len(kvs))
	}

	var gotRawQuery string
	var gotDupHeaders []string
	var gotEmptyHeaderPresent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		gotDupHeaders = r.Header.Values("X-Dup")
		_, gotEmptyHeaderPresent = r.Header["X-Empty"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := &Request{
		HTTP: &HTTPDef{
			Method: "GET",
			URL:    srv.URL + "/items?pre=one%20two",
			Query:  kvsBack,
			Headers: []KV{
				{Name: "X-Dup", Value: "first"},
				{Name: "X-Dup", Value: "second"},
				{Name: "X-Empty", Value: ""},
			},
		},
	}
	vars := map[string]string{"userId": "u/42&admin=false"}
	plan, err := BuildPlanWithOptions(context.Background(), req, vars, nil, nil, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), plan, srv.Client()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(gotRawQuery, "%25") {
		t.Fatalf("server received double-encoded query: %q", gotRawQuery)
	}
	parsedQ, err := http.NewRequest("GET", "http://x?"+gotRawQuery, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := parsedQ.URL.Query()
	if q.Get("pre") != "one two" {
		t.Fatalf("pre = %q, want \"one two\"", q.Get("pre"))
	}
	if q.Get("q") != "hello world" {
		t.Fatalf("q = %q", q.Get("q"))
	}
	if q.Get("filter") != "a=b&c" {
		t.Fatalf("filter = %q", q.Get("filter"))
	}
	if got := q["dup"]; len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("dup = %v", got)
	}
	if got := q["empty"]; len(got) != 1 || got[0] != "" {
		t.Fatalf("empty = %v", got)
	}
	if q.Get("id") != "u/42&admin=false" {
		t.Fatalf("id = %q", q.Get("id"))
	}
	if _, exists := q["off"]; exists {
		t.Fatalf("disabled query row sent: %v", q)
	}
	if len(gotDupHeaders) != 2 || gotDupHeaders[0] != "first" || gotDupHeaders[1] != "second" {
		t.Fatalf("duplicate headers = %v", gotDupHeaders)
	}
	if !gotEmptyHeaderPresent {
		t.Fatal("expected empty header X-Empty to be sent")
	}

	// Secret masking in URL preview: secret value must NEVER appear in preview output.
	prev := PreviewRequestURL(
		"{{baseUrl}}/items?pre=one%20two",
		append(kvsBack, KV{Name: "key", Value: "{{secret.apiKey}}"}),
		map[string]string{"baseUrl": "https://api.example.com", "userId": "u/42"},
		MapSecrets{"apiKey": "SUPER_SECRET_VALUE_999"},
	)
	if strings.Contains(prev.ResolvedURL, "SUPER_SECRET_VALUE_999") {
		t.Fatalf("secret leaked in URL preview: %q", prev.ResolvedURL)
	}
	for _, vp := range prev.Variables {
		if strings.Contains(vp.Display, "SUPER_SECRET_VALUE_999") {
			t.Fatalf("secret leaked in variable preview item: %+v", vp)
		}
	}
	if !strings.Contains(prev.ResolvedURL, "pre=one%20two") || strings.Contains(prev.ResolvedURL, "%2520") {
		t.Fatalf("URL preview corrupted or double-encoded pre-existing query: %q", prev.ResolvedURL)
	}
}

func TestPreservesUnknownFieldsAndUnsupportedBodyModes(t *testing.T) {
	// 1. Unsupported future body mode (e.g. graphql) must survive InspectBody -> ToBody -> YAML without loss.
	gqlYAML := []byte("schemaVersion: 1\nid: gql.me\nname: Viewer\nhttp:\n  method: POST\n  url: https://api.example.com/graphql\n  body:\n    type: graphql\n    value:\n      query: \"{ viewer { id } }\"\n      variables:\n        first: 10\n")
	var gqlReq Request
	if err := decodeStrict(gqlYAML, &gqlReq); err != nil {
		t.Fatalf("decodeStrict graphql: %v", err)
	}
	tb, err := InspectBody(gqlReq.HTTP.Body)
	if err != nil {
		t.Fatalf("InspectBody should preserve unsupported body without error, got %v", err)
	}
	if tb.Kind != BodyUnsupported {
		t.Fatalf("Kind = %q, want BodyUnsupported", tb.Kind)
	}
	preservedBody, err := tb.ToBody()
	if err != nil {
		t.Fatal(err)
	}
	gqlReq.HTTP.Body = preservedBody
	outYAML, err := encodeYAML(&gqlReq)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outYAML), "type: graphql") || !strings.Contains(string(outYAML), "viewer { id }") || !strings.Contains(string(outYAML), "first: 10") {
		t.Fatalf("unsupported body mode swallowed on save:\n%s", outYAML)
	}
	// Execution and strict validation must still reject unsupported modes before network.
	if err := ValidateBody(gqlReq.HTTP.Body); !IsConfig(err) {
		t.Fatalf("ValidateBody should reject unsupported mode, got %v", err)
	}
	if _, err := BuildPlan(&gqlReq, nil, nil, nil); !IsConfig(err) {
		t.Fatalf("BuildPlan should reject unsupported mode, got %v", err)
	}

	// 2. Unknown fields inside a form-data item are preserved on round-trip, but rejected on execution.
	unknownItemYAML := []byte("schemaVersion: 1\nid: fd.ext\nname: Ext\nhttp:\n  method: POST\n  url: https://api.example.com/up\n  body:\n    type: form-data\n    value:\n      - name: field1\n        value: hello\n        futureExtensionField: keep-me\n")
	var extReq Request
	if err := decodeStrict(unknownItemYAML, &extReq); err != nil {
		t.Fatal(err)
	}
	extTB, err := InspectBody(extReq.HTTP.Body)
	if err != nil {
		t.Fatal(err)
	}
	roundTripBody, err := extTB.ToBody()
	if err != nil {
		t.Fatal(err)
	}
	extReq.HTTP.Body = roundTripBody
	extOut, err := encodeYAML(&extReq)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(extOut), "futureExtensionField: keep-me") {
		t.Fatalf("unknown item field swallowed on round-trip:\n%s", extOut)
	}
	if err := ValidateBody(extReq.HTTP.Body); !IsConfig(err) {
		t.Fatalf("ValidateBody must flag unknown item field, got %v", err)
	}
	if _, err := BuildPlan(&extReq, nil, nil, nil); !IsConfig(err) {
		t.Fatalf("BuildPlan must refuse unknown item field before network, got %v", err)
	}

	// 3. JSON BodyEditText -> ParseBodyText preserves key ordering (z before a) and templates.
	jsonBody := &Body{Type: "json", Value: bodyValue(t, "z: 1\na: \"{{who}}\"\n")}
	editTxt := BodyEditText(jsonBody)
	zIdx := strings.Index(editTxt, `"z"`)
	aIdx := strings.Index(editTxt, `"a"`)
	if zIdx < 0 || aIdx < 0 || zIdx > aIdx {
		t.Fatalf("BodyEditText reordered JSON keys:\n%s", editTxt)
	}
	reparsed, err := ParseBodyText("json", editTxt)
	if err != nil {
		t.Fatal(err)
	}
	yml, err := encodeYAML(reparsed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(yml), "z:") > strings.Index(string(yml), "a:") {
		t.Fatalf("ParseBodyText lost key order:\n%s", yml)
	}
}
