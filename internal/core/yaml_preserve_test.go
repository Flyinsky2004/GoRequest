package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeRequestYAMLPreservesCommentsOrderStylesAndExtensions(t *testing.T) {
	original := `# Managed by Auth Team
# Do not strip these comments!
schemaVersion: 1
tags: [auth, smoke] # flow style tags
id: auth.login
name: 用户登录
protocol: http
timeout: 10s # current SLA
http:
  # Primary endpoint
  url: "{{baseUrl}}/auth/login"
  method: POST
  headers:
    # Required JSON content type
    - name: Content-Type
      value: application/json
      enabled: true
  body:
    type: json
    value:
      # Credentials block
      username: "{{username}}"
      password: "{{secret.loginPassword}}"
auth:
  ref: bearer
assert:
  - type: status
    equals: 200
extensions:
  x-owner: identity-squad # preserved extension
`
	var req Request
	if err := decodeStrictForSync([]byte(original), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// 1. Exact no-op round-trip (even after simulating Desktop JSON body round-trip
	// where BodyEditText -> ParseBodyText turns body.value into sorted flow JSON!).
	editText := BodyEditText(req.HTTP.Body)
	reParsedBody, err := ParseBodyText("json", editText)
	if err != nil {
		t.Fatalf("ParseBodyText: %v", err)
	}
	req.HTTP.Body = reParsedBody
	// Simulate Desktop KV round-trip where enabled=true becomes Enabled=nil.
	req.HTTP.Headers[0].Enabled = nil

	noopOut, err := MergeRequestYAML([]byte(original), &req)
	if err != nil {
		t.Fatalf("MergeRequestYAML noop: %v", err)
	}
	if string(noopOut) != original {
		t.Fatalf("noop save altered file!\ngot:\n%s\nwant:\n%s", noopOut, original)
	}

	// 2. Edit ONLY timeout and one field inside JSON body; everything else
	// (comments, key order: tags before id, url before method, flow tags,
	// header enabled:true + comment, JSON block YAML + comment, extensions)
	// MUST remain intact!
	req.Timeout = "25s"
	updatedBody, err := ParseBodyText("json", "{\n  \"password\": \"{{secret.loginPassword}}\",\n  \"username\": \"alice\"\n}")
	if err != nil {
		t.Fatal(err)
	}
	req.HTTP.Body = updatedBody

	out, err := MergeRequestYAML([]byte(original), &req)
	if err != nil {
		t.Fatalf("MergeRequestYAML edited: %v", err)
	}
	got := string(out)

	mustContain := []string{
		"# Managed by Auth Team",
		"# Do not strip these comments!",
		"tags: [auth, smoke] # flow style tags",
		"timeout: 25s # current SLA",
		"# Primary endpoint",
		`url: "{{baseUrl}}/auth/login"`,
		"# Required JSON content type",
		"enabled: true",
		"# Credentials block",
		"username: \"alice\"",
		"password: \"{{secret.loginPassword}}\"",
		"extensions:",
		"x-owner: identity-squad # preserved extension",
	}
	for _, sub := range mustContain {
		if !strings.Contains(got, sub) {
			t.Errorf("merged YAML missing %q:\n%s", sub, got)
		}
	}
	// Verify custom key ordering was preserved (tags before id; url before method; username before password).
	if strings.Index(got, "tags:") > strings.Index(got, "id: auth.login") {
		t.Errorf("top-level key order lost:\n%s", got)
	}
	if strings.Index(got, "url:") > strings.Index(got, "method: POST") {
		t.Errorf("http key order lost:\n%s", got)
	}
	if strings.Index(got, "username:") > strings.Index(got, "password:") {
		t.Errorf("body.value key order lost:\n%s", got)
	}
}

func TestSavePreservingRefusesSchemaDowngradeAndDetectsConflict(t *testing.T) {
	_, p := mustNewProject(t, "PreserveSave")
	gq := p.Dir

	// Write a request with comments, save via SaveRequestPreserving, and verify on disk.
	commented := "# Header note\nschemaVersion: 1\nid: items.list\nname: List Items\nprotocol: http\nhttp:\n  method: GET\n  url: \"{{baseUrl}}/items\" # endpoint\nextensions:\n  x-tier: critical\n"
	rel := "collections/items.request.yaml"
	writeRawProjectFile(t, gq, rel, commented)

	p2, err := OpenProjectStrict(filepath.Dir(gq), StoreLimits{}, YAMLLimits{})
	if err != nil {
		t.Fatalf("OpenProjectStrict: %v", err)
	}
	// OpenProject itself uses decodeStrict without extension stripper until wired,
	// so load via SyncEngine to populate the extension-bearing request cleanly:
	eng, err := p2.NewSyncEngine(SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Trigger applySnapshotToProject for initial extension-bearing request.
	eng.lastSnap = &DiskSnapshot{Files: map[string]*DiskFileRecord{}}
	if _, err := eng.ScanAndSync(); err != nil {
		t.Fatal(err)
	}
	entry := p2.Requests["items.list"]
	if entry == nil {
		t.Fatal("items.list not loaded by sync engine")
	}

	edited := *entry.Request
	editedHTTP := *entry.Request.HTTP
	editedHTTP.URL = "{{baseUrl}}/v2/items"
	edited.HTTP = &editedHTTP

	newDigest, err := p2.SaveRequestPreserving(entry, &edited, entry.BaselineDigest)
	if err != nil {
		t.Fatalf("SaveRequestPreserving: %v", err)
	}
	diskAfter, _, err := p2.Store.Read(rel)
	if err != nil {
		t.Fatal(err)
	}
	if Digest(diskAfter) != newDigest {
		t.Fatal("returned digest does not match saved bytes")
	}
	if !strings.Contains(string(diskAfter), "# Header note") || !strings.Contains(string(diskAfter), "# endpoint") || !strings.Contains(string(diskAfter), "x-tier: critical") {
		t.Fatalf("SaveRequestPreserving stripped comments or extensions:\n%s", diskAfter)
	}

	// Higher schemaVersion on disk MUST never be downgraded by MergeRequestYAML!
	futureYAML := []byte("schemaVersion: 2\nid: items.list\nname: Future\nfutureField: keep-me\nhttp:\n  method: GET\n  url: http://h\n")
	if _, err := MergeRequestYAML(futureYAML, &edited); err == nil {
		t.Fatal("expected MergeRequestYAML to refuse downgrading schemaVersion 2")
	}
}

func TestEnvironmentAndProjectPreservingSave(t *testing.T) {
	dir, p := mustNewProject(t, "EnvPreserve")
	gq := p.Dir

	rawEnv := "# Shared dev config\nschemaVersion: 1\nid: dev\nvariables:\n  # Keep z before a\n  zKey: zVal\n  baseUrl: \"http://127.0.0.1:4010\"\nsecrets:\n  - apiToken # local secret ref\nextensions:\n  x-region: ap-east\n"
	writeRawProjectFile(t, gq, "environments/dev.yaml", rawEnv)

	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := p2.NewSyncEngine(SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	eng.lastSnap = &DiskSnapshot{Files: map[string]*DiskFileRecord{}}
	if _, err := eng.ScanAndSync(); err != nil {
		t.Fatal(err)
	}

	env := &Environment{
		SchemaVersion: 1,
		ID:            "dev",
		Variables: map[string]string{
			"zKey":    "zVal",
			"baseUrl": "http://127.0.0.1:9090",
			"newKey":  "added",
		},
		Secrets: []string{"apiToken"},
	}
	if err := p2.SaveEnvironmentPreserving("dev", env); err != nil {
		t.Fatalf("SaveEnvironmentPreserving: %v", err)
	}
	savedBytes, _, err := p2.Store.Read("environments/dev.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got := string(savedBytes)
	for _, want := range []string{
		"# Shared dev config",
		"# Keep z before a",
		`baseUrl: "http://127.0.0.1:9090"`,
		"newKey: added",
		"- apiToken # local secret ref",
		"x-region: ap-east",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("saved environment missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "zKey:") > strings.Index(got, "baseUrl:") {
		t.Errorf("environment variable order lost:\n%s", got)
	}
}

func TestCommentPreservingThreeWayMergeNoFalseConflicts(t *testing.T) {
	baseline := []byte(`# Team request
schemaVersion: 1
id: users.get
name: Get User
http:
  method: GET
  url: "{{baseUrl}}/users/1"
timeout: 5s
`)
	var localReq Request
	if err := decodeStrict(baseline, &localReq); err != nil {
		t.Fatal(err)
	}
	// User changes URL locally; EncodeRequestAgainstBaseline preserves header comment and key order!
	localReq.HTTP.URL = "{{baseUrl}}/users/42"
	editedBytes, err := EncodeRequestAgainstBaseline(baseline, &localReq)
	if err != nil {
		t.Fatal(err)
	}

	// External editor changes timeout and adds an inline comment on disk!
	diskBytes := []byte(`# Team request
schemaVersion: 1
id: users.get
name: Get User
http:
  method: GET
  url: "{{baseUrl}}/users/1"
timeout: 30s # increased for batch report
`)

	merged, clean := MergeThreeWay(baseline, editedBytes, diskBytes)
	if !clean {
		t.Fatalf("expected clean 3-way merge without false conflicts, got:\n%s", merged)
	}
	got := string(merged)
	if !strings.Contains(got, "# Team request") ||
		!strings.Contains(got, `url: "{{baseUrl}}/users/42"`) ||
		!strings.Contains(got, "timeout: 30s # increased for batch report") {
		t.Fatalf("3-way merge lost comment or side change:\n%s", got)
	}
}
