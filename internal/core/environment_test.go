package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findEnvFile returns the absolute path of an environment file by project
// relative path, failing the test when it is missing.
func mustReadFile(t *testing.T, dir, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".gorequest", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

func TestNewEnvironmentRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := p.NewEnvironment(&Environment{
		ID:        "staging",
		Variables: map[string]string{"baseUrl": "http://staging.local", "empty": "", "tpl": "{{baseUrl}}/v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.RelPath != "environments/staging.yaml" {
		t.Fatalf("rel path = %q", entry.RelPath)
	}
	raw := mustReadFile(t, dir, entry.RelPath)
	if !strings.Contains(string(raw), "schemaVersion: 1") {
		t.Fatalf("missing schemaVersion:\n%s", raw)
	}
	if strings.Contains(string(raw), "enabled") {
		t.Fatalf("environment schema must not add an enabled field:\n%s", raw)
	}
	if got := p.EnvOrder; strings.Join(got, ",") != "dev,staging" {
		t.Fatalf("EnvOrder = %v, want dev,staging (stable sort)", got)
	}

	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	env, ok := p2.Environments["staging"]
	if !ok {
		t.Fatal("new environment not reloaded")
	}
	if env.Variables["baseUrl"] != "http://staging.local" || env.Variables["tpl"] != "{{baseUrl}}/v1" {
		t.Fatalf("variables not preserved: %+v", env.Variables)
	}
	if v, present := env.Variables["empty"]; !present || v != "" {
		t.Fatalf("empty value not preserved (present=%v value=%q)", present, v)
	}
}

func TestSaveEnvironmentRoundTripAndConsecutiveSaves(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	e1 := &Environment{SchemaVersion: 1, ID: "dev", Variables: map[string]string{"baseUrl": "http://a", "x": "1"}}
	if err := p.SaveEnvironment("dev", e1); err != nil {
		t.Fatalf("first save: %v", err)
	}
	e2 := &Environment{SchemaVersion: 1, ID: "dev", Variables: map[string]string{"baseUrl": "http://a", "x": "2"}}
	if err := p.SaveEnvironment("dev", e2); err != nil {
		t.Fatalf("second save (baseline must update): %v", err)
	}
	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := p2.Environments["dev"].Variables["x"]; got != "2" {
		t.Fatalf("x = %q, want 2 after consecutive saves", got)
	}
}

func TestSaveEnvironmentPreservesValuesTemplatesAndSecrets(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"url":    "http://h:8080/path?a=b&c:d",
		"empty":  "",
		"spacey": "  keep me  ",
		"tpl":    "{{baseUrl}}/x",
		"sec":    "{{secret.token}}",
	}
	if _, err := p.NewEnvironment(&Environment{ID: "qa", Variables: want, Secrets: []string{"token", "loginPassword"}}); err != nil {
		t.Fatal(err)
	}
	// Edit only one variable; the declared secret names must survive untouched.
	if err := p.SaveEnvironment("qa", &Environment{
		ID:        "qa",
		Variables: map[string]string{"url": "http://h:8080/path?a=b&c:d", "extra": "v"},
		Secrets:   []string{"token", "loginPassword"},
	}); err != nil {
		t.Fatal(err)
	}
	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	env := p2.Environments["qa"]
	if env.Variables["extra"] != "v" {
		t.Fatalf("edited variable missing: %+v", env.Variables)
	}
	if got := strings.Join(env.Secrets, ","); got != "token,loginPassword" {
		t.Fatalf("secrets lost on variable edit: %v", env.Secrets)
	}
}

func TestEnvironmentRoundTripExactValues(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"url":    "http://h:8080/path?a=b&c:d",
		"empty":  "",
		"spacey": "  keep me  ",
		"tpl":    "{{baseUrl}}/x",
		"sec":    "{{secret.token}}",
	}
	if _, err := p.NewEnvironment(&Environment{ID: "exact", Variables: want}); err != nil {
		t.Fatal(err)
	}
	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := p2.Environments["exact"].Variables
	for k, v := range want {
		if got[k] != v {
			t.Errorf("variable %q = %q, want %q", k, got[k], v)
		}
	}
}

func TestNewEnvironmentRejectsDuplicateAndExistingFile(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewEnvironment(&Environment{ID: "staging"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewEnvironment(&Environment{ID: "staging"}); err == nil {
		t.Fatal("expected duplicate id rejection")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate error = %v", err)
	}

	// A file already on disk under the would-be name (with a different id) must
	// not be overwritten.
	existRel := "environments/prod.yaml"
	if err := os.WriteFile(filepath.Join(dir, ".gorequest", "environments", "prod.yaml"),
		[]byte("schemaVersion: 1\nid: production\nvariables:\n  baseUrl: http://prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = p.NewEnvironment(&Environment{ID: "prod", Variables: map[string]string{"baseUrl": "http://other"}})
	if err == nil {
		t.Fatal("expected conflict when target file exists")
	}
	if !IsConflict(err) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	raw := mustReadFile(t, dir, existRel)
	if !strings.Contains(string(raw), "id: production") {
		t.Fatalf("existing file was overwritten:\n%s", raw)
	}
	if _, ok := p.EnvEntries["prod"]; ok {
		t.Fatal("failed create must not register an in-memory entry")
	}
}

func TestEnvironmentRealYMLPathDifferentFromID(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewProject(dir, "Demo"); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, ".gorequest", "environments", "prod.yml")
	if err := os.WriteFile(envFile, []byte("schemaVersion: 1\nid: production\nvariables:\n  baseUrl: http://prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := p.EnvEntries["production"]
	if !ok {
		t.Fatal("environment with filename != id not loaded")
	}
	if entry.RelPath != "environments/prod.yml" {
		t.Fatalf("rel path = %q, want environments/prod.yml", entry.RelPath)
	}
	if err := p.SaveEnvironment("production", &Environment{
		ID:        "production",
		Variables: map[string]string{"baseUrl": "http://prod2"},
	}); err != nil {
		t.Fatal(err)
	}
	raw := mustReadFile(t, dir, "environments/prod.yml")
	if !strings.Contains(string(raw), "http://prod2") {
		t.Fatalf(".yml file not updated:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gorequest", "environments", "production.yaml")); !os.IsNotExist(err) {
		t.Fatal("save must not create a new production.yaml")
	}
}

func TestSaveEnvironmentConflictOnExternalChangeOrDelete(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	orig := p.Environments["dev"].Variables["baseUrl"]
	devFile := filepath.Join(dir, ".gorequest", "environments", "dev.yaml")

	// External modification.
	if err := os.WriteFile(devFile, []byte("schemaVersion: 1\nid: dev\nvariables:\n  baseUrl: http://external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = p.SaveEnvironment("dev", &Environment{ID: "dev", Variables: map[string]string{"baseUrl": "http://mine"}})
	if !IsConflict(err) {
		t.Fatalf("expected conflict on external change, got %v", err)
	}
	if got := p.Environments["dev"].Variables["baseUrl"]; got != orig {
		t.Fatalf("failed save polluted memory: baseUrl = %q, want %q", got, orig)
	}

	// External deletion.
	if err := os.Remove(devFile); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveEnvironment("dev", &Environment{ID: "dev"}); !IsConflict(err) {
		t.Fatalf("expected conflict on external delete, got %v", err)
	}
}

func TestNewEnvironmentRejectsUnsafeIDs(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	unsafe := []string{"", "   ", "(none)", "../evil", "a/b", `a\b`, "/abs/path", "x..y", "a<b", "bad\x01name"}
	for _, id := range unsafe {
		if err := ValidateEnvironmentID(id); err == nil {
			t.Errorf("ValidateEnvironmentID(%q) should fail", id)
		}
		if _, err := p.NewEnvironment(&Environment{ID: id}); err == nil {
			t.Errorf("NewEnvironment(%q) should fail", id)
		}
	}
	if len(p.Environments) != 1 {
		t.Fatalf("unsafe ids must not register environments: %v", p.EnvEntries)
	}
	// The environments directory must contain only the scaffolded dev.yaml.
	entries, err := os.ReadDir(filepath.Join(dir, ".gorequest", "environments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "dev.yaml" {
		t.Fatalf("unexpected files written for unsafe ids: %v", entries)
	}
}

func TestResolveVarsAndBuildPlanUseSavedEnvironment(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := p.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewEnvironment(&Environment{ID: "blue", Variables: map[string]string{"baseUrl": "http://blue.local:9000"}}); err != nil {
		t.Fatal(err)
	}
	vars, env, err := p.ResolveVars(entry, "blue")
	if err != nil {
		t.Fatal(err)
	}
	if env == nil || env.ID != "blue" {
		t.Fatalf("resolved env = %+v", env)
	}
	if vars["baseUrl"] != "http://blue.local:9000" {
		t.Fatalf("vars baseUrl = %q", vars["baseUrl"])
	}
	req := *entry.Request
	req.HTTP = &HTTPDef{Method: "GET", URL: "{{baseUrl}}/ping"}
	plan, err := BuildPlan(&req, vars, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.URL != "http://blue.local:9000/ping" {
		t.Fatalf("plan url = %q", plan.URL)
	}

	// After an edit the new value is used immediately.
	if err := p.SaveEnvironment("blue", &Environment{ID: "blue", Variables: map[string]string{"baseUrl": "http://blue2.local"}}); err != nil {
		t.Fatal(err)
	}
	vars2, _, err := p.ResolveVars(entry, "blue")
	if err != nil {
		t.Fatal(err)
	}
	req.HTTP.URL = "{{baseUrl}}/ping"
	plan2, err := BuildPlan(&req, vars2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan2.URL != "http://blue2.local/ping" {
		t.Fatalf("plan2 url = %q", plan2.URL)
	}
}

func TestValidateEnvironmentIDAcceptsNormalIDs(t *testing.T) {
	for _, id := range []string{"staging", "dev", "qa-2", "prod_eu", "env.with.dots"} {
		if err := ValidateEnvironmentID(id); err != nil {
			t.Errorf("ValidateEnvironmentID(%q) = %v, want nil", id, err)
		}
	}
}

func TestEnvironmentRejectsInvalidVariableKeys(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id  string
		key string
	}{
		{"k-empty", ""},
		{"k-brace", "bad{key}"},
	}
	for _, c := range cases {
		if _, err := p.NewEnvironment(&Environment{ID: c.id, Variables: map[string]string{c.key: "v"}}); err == nil {
			t.Errorf("NewEnvironment with key %q should fail", c.key)
		}
		if _, ok := p.Environments[c.id]; ok {
			t.Errorf("invalid environment %q must not be registered", c.id)
		}
	}
}
