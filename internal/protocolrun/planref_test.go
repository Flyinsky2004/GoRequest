package protocolrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorequest/internal/core"
)

// writeFile writes data at root/rel, creating parent directories.
func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRefExtensionRoundTrip(t *testing.T) {
	req := &core.Request{}
	if _, ok := PlanRefFromRequest(req); ok {
		t.Fatal("empty request must have no plan ref")
	}

	ref := PlanRef{Path: "protocols/ticks.json", Digest: "abc123"}
	SetPlanRef(req, ref)
	got, ok := PlanRefFromRequest(req)
	if !ok || got != ref {
		t.Fatalf("round trip mismatch: %+v ok=%v", got, ok)
	}

	// A YAML-decoded extension bag arrives as map[any]any; both shapes must read.
	req.Extensions[PlanExtensionKey] = map[any]any{"path": "a/b.json", "digest": "d1"}
	got, ok = PlanRefFromRequest(req)
	if !ok || got.Path != "a/b.json" || got.Digest != "d1" {
		t.Fatalf("map[any]any decode failed: %+v", got)
	}

	// A bare string path (older hand-written YAML) is accepted without a digest.
	req.Extensions[PlanExtensionKey] = "plain.json"
	got, ok = PlanRefFromRequest(req)
	if !ok || got.Path != "plain.json" || got.Digest != "" {
		t.Fatalf("string decode failed: %+v", got)
	}

	SetPlanRef(req, PlanRef{})
	if _, exists := req.Extensions[PlanExtensionKey]; exists {
		t.Fatal("empty path must clear the extension key")
	}
}

func TestLoadAndReadPlanFromProject(t *testing.T) {
	dir := t.TempDir()
	doc := `{"version":1,"name":"p","steps":[{"action":"connect","url":"ws://example.test/ws"}]}`
	writeFile(t, dir, "plans/ticks.json", doc)

	p, digest, err := LoadPlanFile(dir + "/plans/ticks.json")
	if err != nil || p.Name != "p" || digest == "" {
		t.Fatalf("LoadPlanFile: %v name=%q digest=%q", err, p.Name, digest)
	}

	opener := core.NewDirFileOpener(dir)
	p2, digest2, err := ReadPlanFromProject(context.Background(), opener, "plans/ticks.json")
	if err != nil || p2.Name != "p" || digest2 != digest {
		t.Fatalf("ReadPlanFromProject: %v name=%q digest=%q/%q", err, p2.Name, digest2, digest)
	}

	// Path traversal must be rejected before the opener is consulted.
	if _, _, err := ReadPlanFromProject(context.Background(), opener, "../escape.json"); err == nil {
		t.Fatal("traversal path must be rejected")
	}

	// Oversize plans fail before the whole body is retained.
	big := `{"version":1,"name":"` + strings.Repeat("x", MaxPlanBytes) + `","steps":[]}`
	writeFile(t, dir, "plans/big.json", big)
	if _, _, err := ReadPlanFromProject(context.Background(), opener, "plans/big.json"); err == nil {
		t.Fatal("oversize plan must be rejected")
	}
}
