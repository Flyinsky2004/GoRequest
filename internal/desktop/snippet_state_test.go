package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// NOTE (C04-phase1): per the standing user decision this batch does not run
// go test. These cases exercise the snippet modal state machine headlessly
// (the established fake-window harness); they are delivered unexecuted and
// recorded as pending in docs/task-results/C04-phase1.md.

// snippetFixup points the default scratch request at a plain URL without
// variables so generation is unblocked for every language.
func snippetFixup(t *testing.T, ps *projectState) *Tab {
	t.Helper()
	tabs := openRequestTabs(t, ps, "SnippetTarget")
	tab := tabs[0]
	entry := tab.Entry
	entry.Request.HTTP.Method = "POST"
	entry.Request.HTTP.URL = "https://api.example.com/v1/ping"
	return tab
}

func TestSnippetModalGeneratesEveryLanguage(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tab := snippetFixup(t, ps)
	ps.openRequest(tab.Entry)

	a.OpenSnippetModal()
	st := a.Snippet()
	if !st.Open {
		t.Fatal("snippet modal did not open")
	}
	if st.TargetRel != tab.Entry.RelPath {
		t.Fatalf("target mismatch: %q vs %q", st.TargetRel, tab.Entry.RelPath)
	}
	if st.Result == nil || st.Result.Code == "" {
		t.Fatalf("default preview missing: %v", st.Err)
	}
	for i := 1; i < 5; i++ {
		a.SelectSnippetLanguage(i)
		if st.Result == nil || st.Result.Code == "" {
			blocked := ""
			if st.Result != nil {
				blocked = st.Result.Blocked
			}
			t.Fatalf("language %d produced no code (err=%s blocked=%s)", i, st.Err, blocked)
		}
		if strings.Contains(st.Result.Code, "127.0.0.1:4010") {
			t.Fatalf("language %d emitted scratch default url instead of the request url", i)
		}
		if !strings.Contains(st.Result.Code, "api.example.com/v1/ping") {
			t.Fatalf("language %d lost the request url", i)
		}
	}
}

func TestSnippetUndefinedVariableBlocks(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "SnippetUndef")
	tab := tabs[0]
	tab.Entry.Request.HTTP.URL = "{{mysteryHost}}/ping"

	a.OpenSnippetModal()
	st := a.Snippet()
	if st.Result == nil {
		t.Fatalf("expected a blocked result, got nil (err=%s)", st.Err)
	}
	if st.Result.Blocked == "" {
		t.Fatal("undefined variable must block generation")
	}
	if st.Result.Code != "" {
		t.Fatal("blocked generation must not carry code")
	}
}

func TestSnippetExportRefusesOverwriteThenWrites(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tab := snippetFixup(t, ps)
	ps.openRequest(tab.Entry)

	a.OpenSnippetModal()
	st := a.Snippet()
	if st.Result == nil || st.Result.Code == "" {
		t.Fatalf("preview missing: %s", st.Err)
	}
	target := filepath.Join(t.TempDir(), "snippet.sh")
	if err := os.WriteFile(target, []byte("user data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.OutputPath = target
	st.Overwrite = false
	a.ExecuteSnippetExportToFile()
	if st.Err == "" {
		t.Fatal("export must refuse to overwrite an existing file")
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "user data\n" {
		t.Fatalf("user file was modified: %v %q", err, raw)
	}

	st.Overwrite = true
	a.ExecuteSnippetExportToFile()
	if st.Err != "" {
		t.Fatalf("overwrite export failed: %s", st.Err)
	}
	raw, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), st.Result.Code) {
		t.Fatal("exported content must equal the reviewed preview")
	}
	if st.Notice == "" {
		t.Fatal("export notice missing")
	}
}
