package core

import (
	"fmt"
	"strings"
	"testing"
)

func TestCustomDecoderStrictnessAudit(t *testing.T) {
	cases := []struct {
		name    string
		kind    SyncEntityKind
		yaml    string
		wantErr string
	}{
		{
			name: "auth ref mixed with inline token",
			kind: EntityRequest,
			yaml: `schemaVersion: 1
id: r1
name: Test
http:
  method: GET
  url: http://example.com
auth:
  ref: bearer
  token: accidental-inline
`,
			wantErr: "auth with ref must not also define \"token\"",
		},
		{
			name: "auth ref with unknown field",
			kind: EntityRequest,
			yaml: `schemaVersion: 1
id: r1
name: Test
http:
  method: GET
  url: http://example.com
auth:
  ref: bearer
  bogus: 123
`,
			wantErr: "auth with ref must not also define \"bogus\"",
		},
		{
			name: "inline auth with unknown field",
			kind: EntityRequest,
			yaml: `schemaVersion: 1
id: r1
name: Test
http:
  method: GET
  url: http://example.com
auth:
  type: bearer
  token: "{{secret.t}}"
  unknownKey: should-fail
`,
			wantErr: "unknown auth field \"unknownKey\"",
		},
		{
			name: "duplicate key inside custom Body UnmarshalYAML",
			kind: EntityRequest,
			yaml: `schemaVersion: 1
id: r1
name: Test
http:
  method: POST
  url: http://example.com
  body:
    type: json
    type: text
    value: "hi"
`,
			wantErr: "duplicate YAML key \"type\"",
		},
		{
			name: "duplicate key inside nested JSON body.value",
			kind: EntityRequest,
			yaml: `schemaVersion: 1
id: r1
name: Test
http:
  method: POST
  url: http://example.com
  body:
    type: json
    value:
      user: alice
      user: bob
`,
			wantErr: "duplicate YAML key \"user\"",
		},
		{
			name: "body specifying both file and inline value",
			kind: EntityRequest,
			yaml: `schemaVersion: 1
id: r1
name: Test
http:
  method: POST
  url: http://example.com
  body:
    type: json
    file: payloads/a.json
    value:
      a: 1
`,
			wantErr: "cannot specify both file and inline value",
		},
		{
			name: "implicit YAML merge key << rejected",
			kind: EntityRequest,
			yaml: `schemaVersion: 1
id: r1
name: Test
http:
  method: POST
  url: http://example.com
  body:
    type: json
    value:
      base: &b
        x: 1
      merged:
        <<: *b
        y: 2
`,
			wantErr: "merge key '<<' is not supported",
		},
		{
			name: "higher schemaVersion rejected",
			kind: EntityRequest,
			yaml: `schemaVersion: 2
id: r1
name: Future
http:
  method: GET
  url: http://example.com
`,
			wantErr: "schemaVersion 2 unsupported",
		},
		{
			name: "non-mapping extensions rejected",
			kind: EntityRequest,
			yaml: `schemaVersion: 1
id: r1
name: BadExt
http:
  method: GET
  url: http://example.com
extensions: "not-a-map"
`,
			wantErr: "extensions must be a YAML mapping",
		},
		{
			name: "unknown top-level field outside extensions rejected",
			kind: EntityRequest,
			yaml: `schemaVersion: 1
id: r1
name: BadField
http:
  method: GET
  url: http://example.com
unexpectedRootField: true
`,
			wantErr: "field unexpectedRootField not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateEntityYAML(tc.kind, []byte(tc.yaml), YAMLLimits{})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestYAMLResourceLimitsDepthNodesAliasesAndSize(t *testing.T) {
	// 1. Byte size limit.
	big := "schemaVersion: 1\nid: x\nname: \"" + strings.Repeat("a", 2048) + "\"\nhttp:\n  method: GET\n  url: http://h\n"
	if _, err := ValidateEntityYAML(EntityRequest, []byte(big), YAMLLimits{MaxBytes: 512}); err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("expected byte size limit error, got %v", err)
	}

	// 2. Recursion depth limit.
	var deep strings.Builder
	deep.WriteString("schemaVersion: 1\nid: d\nname: Deep\nhttp:\n  method: POST\n  url: http://h\n  body:\n    type: json\n    value:\n")
	for i := 0; i < 20; i++ {
		deep.WriteString(strings.Repeat("  ", i+3))
		deep.WriteString(fmt.Sprintf("lvl%d:\n", i))
	}
	deep.WriteString(strings.Repeat("  ", 23) + "leaf: 1\n")
	if _, err := ValidateEntityYAML(EntityRequest, []byte(deep.String()), YAMLLimits{MaxDepth: 12}); err == nil || !strings.Contains(err.Error(), "depth exceeds limit") {
		t.Fatalf("expected depth limit error, got %v", err)
	}

	// 3. Node count limit.
	var many strings.Builder
	many.WriteString("schemaVersion: 1\nid: m\nname: Many\nhttp:\n  method: GET\n  url: http://h\n  headers:\n")
	for i := 0; i < 60; i++ {
		many.WriteString(fmt.Sprintf("    - name: X-%d\n      value: v\n", i))
	}
	if _, err := ValidateEntityYAML(EntityRequest, []byte(many.String()), YAMLLimits{MaxNodes: 50}); err == nil || !strings.Contains(err.Error(), "node count exceeds limit") {
		t.Fatalf("expected node count limit error, got %v", err)
	}

	// 4. Billion-laughs exponential alias expansion bomb caught immediately!
	var bomb strings.Builder
	bomb.WriteString("schemaVersion: 1\nid: b\nname: Bomb\nhttp:\n  method: POST\n  url: http://h\n  body:\n    type: json\n    value:\n")
	bomb.WriteString("      l0: &a0 [\"x\", \"x\", \"x\", \"x\", \"x\", \"x\", \"x\", \"x\"]\n")
	for i := 1; i <= 7; i++ {
		bomb.WriteString(fmt.Sprintf("      l%d: &a%d [*a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d]\n",
			i, i, i-1, i-1, i-1, i-1, i-1, i-1, i-1, i-1))
	}
	if _, err := ParseStrictYAMLTree([]byte(bomb.String()), YAMLLimits{MaxAliases: 500, MaxExpandedNodes: 5000}); err == nil || !strings.Contains(err.Error(), "alias expansion exceeds limit") {
		t.Fatalf("expected alias expansion bomb rejection, got %v", err)
	}

	// 5. Invalid UTF-8 & NUL byte rejection.
	if _, err := ParseStrictYAMLTree([]byte{0xff, 0xfe, '\n'}, YAMLLimits{}); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("expected invalid UTF-8 rejection, got %v", err)
	}
	if _, err := ParseStrictYAMLTree([]byte("a: b\x00c\n"), YAMLLimits{}); err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("expected NUL byte rejection, got %v", err)
	}
}

func TestScanDiskSnapshotEnforcesFileSizeAndFileCountLimits(t *testing.T) {
	dir, p := mustNewProject(t, "LimitsScan")
	gq := p.Dir

	// Write an oversized request file and extra requests past MaxTrackedFiles.
	oversize := "schemaVersion: 1\nid: big\nname: \"" + strings.Repeat("Z", 1024) + "\"\nhttp:\n  method: GET\n  url: http://h\n"
	writeRawProjectFile(t, gq, "collections/big.request.yaml", oversize)
	for i := 0; i < 6; i++ {
		writeRawProjectFile(t, gq, fmt.Sprintf("collections/r%d.request.yaml", i),
			fmt.Sprintf("schemaVersion: 1\nid: r%d\nname: R%d\nhttp:\n  method: GET\n  url: http://h\n", i, i))
	}

	snapSize, err := ScanDiskSnapshot(p.Store, StoreLimits{MaxYAMLBytes: 512, MaxDocBytes: 4096, MaxTrackedFiles: 100})
	if err != nil {
		t.Fatal(err)
	}
	hasSizeIssue := false
	for _, iss := range snapSize.Issues {
		if iss.Code == "file-size-limit" && iss.Path == "collections/big.request.yaml" {
			hasSizeIssue = true
		}
	}
	if !hasSizeIssue {
		t.Fatalf("expected file-size-limit issue, got %+v", snapSize.Issues)
	}

	snapCount, err := ScanDiskSnapshot(p.Store, StoreLimits{MaxYAMLBytes: 8192, MaxDocBytes: 8192, MaxTrackedFiles: 4})
	if err != nil {
		t.Fatal(err)
	}
	hasCountIssue := false
	for _, iss := range snapCount.Issues {
		if iss.Code == "file-count-limit" {
			hasCountIssue = true
		}
	}
	if !hasCountIssue || len(snapCount.Files) > 4 {
		t.Fatalf("expected file-count-limit issue and <=4 scanned files, got files=%d issues=%+v", len(snapCount.Files), snapCount.Issues)
	}

	// OpenProjectStrict surfaces strictness issues on child files.
	writeRawProjectFile(t, gq, "collections/bad-inline-auth.request.yaml",
		"schemaVersion: 1\nid: badauth\nname: Bad\nhttp:\n  method: GET\n  url: http://h\nauth:\n  type: bearer\n  token: x\n  extraSecret: nope\n")
	pStrict, err := OpenProjectStrict(dir, StoreLimits{}, YAMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	foundStrict := false
	for _, iss := range pStrict.Issues {
		if iss.Path == "collections/bad-inline-auth.request.yaml" && strings.Contains(iss.Message, "unknown auth field") {
			foundStrict = true
		}
	}
	if !foundStrict {
		t.Fatalf("expected OpenProjectStrict to report unknown inline auth field, got %+v", pStrict.Issues)
	}
}
