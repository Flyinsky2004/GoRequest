package desktop

import (
	"github.com/egoist/mygo/ui"
	"gorequest/internal/docedit"
	"gorequest/internal/markdown"
	"strings"
	"testing"
)

func TestAuthoringIntegratedTableEditUndo(t *testing.T) {
	for _, gap := range []string{"\n", "\n\n"} {
		t.Run(strings.ReplaceAll(gap, "\n", "n"), func(t *testing.T) {
			a, ps := openTestProject(t, "Integrated Table")
			src := "intro" + gap + "| a | b |\n| --- | --- |\n| c | d |\n\nafter\n"
			tab := openDocForTest(t, ps, "docs/table.md", src)
			blocks := docedit.Parse(src)
			if len(blocks) != 3 || blocks[1].Kind != markdown.Table || !strings.HasPrefix(blocks[1].Source, "| a |") {
				t.Fatalf("blocks: %+v", blocks)
			}
			tt := ui.NewTester(a.View, 1200, 820)
			tt.Frame()
			if err := tt.Click("Table cell 2,1"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("X")
			tt.Frame()
			want := strings.Replace(src, "| c | d |", "| X | d |", 1)
			if got := tab.DocEditor.Buffer.String(); got != want {
				t.Fatalf("table changed surrounding document: %q", got)
			}
			tt.Key(ui.Cmd, ui.KeyZ)
			tt.Frame()
			if got := tab.DocEditor.Buffer.String(); got != src {
				t.Fatalf("undo: %q", got)
			}
			// The renderer must also adopt the undo, not keep its old cell cache.
			tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			tt.Frame()
			if got := tab.DocEditor.Buffer.String(); got != want {
				t.Fatalf("redo: %q", got)
			}
		})
	}
}

func TestAuthoringIntegratedMentionAndHeaderSave(t *testing.T) {
	a, ps := openTestProject(t, "Integrated Mention")
	req := addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	tab := openDocForTest(t, ps, "docs/mention.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(a.docLiveMsg().EmptyHint); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	if !tt.HasText("@auth/用户登录") {
		t.Fatalf("missing integrated completion: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	want := "[@auth/用户登录](gorequest://request/" + req.Request.ID + ")"
	if !strings.Contains(tab.DocEditor.Buffer.String(), want) {
		t.Fatalf("mention: %q", tab.DocEditor.Buffer.String())
	}
	tt.Type(" last")
	// Save through U04's shared file-sync path without another document frame.
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatal(err)
	}
	data, _, err := ps.project.Store.Read(tab.DocRel)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), " last") {
		t.Fatalf("last input lost on save: %q", data)
	}
	tt.Frame()
	if tab.Dirty {
		t.Fatal("save did not clear dirty state")
	}
	if tt.HasText(a.msg().SaveDoc) {
		t.Fatal("duplicate document save control")
	}
}

func TestAuthoringPendingEditSurvivesRequestTabSwitch(t *testing.T) {
	a, ps := openTestProject(t, "Integrated Switch")
	req := addMentionRequest(t, ps, "", "Request", "GET", "/x")
	tab := openDocForTest(t, ps, "docs/switch.md", "one\n")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, "one\n", 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type(" last")
	ps.openRequest(req)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "one last\n" {
		t.Fatalf("last input lost on tab switch: %q", got)
	}
}

func TestAuthoringSourceAndSplitUseMentionHooks(t *testing.T) {
	for _, section := range []int{1, 0} {
		a, ps := openTestProject(t, "Integrated Source")
		one := addMentionRequest(t, ps, "", "One", "GET", "/one")
		two := addMentionRequest(t, ps, "", "Two", "GET", "/two")
		src := "[@One](gorequest://request/" + one.Request.ID + ") [@Two](gorequest://request/" + two.Request.ID + ")"
		tab := openDocForTest(t, ps, "docs/source.md", src)
		tt := ui.NewTester(a.View, 1200, 820)
		tt.Frame()
		tab.Section = section
		tt.Frame()
		tt.Frame()
		if docMentionSessionFor(tab.DocEditor) == nil {
			t.Fatalf("section %d has no mention hooks", section)
		}
		tab.DocEditor.OnDefinition(strings.Index(src, "[@Two]"))
		if active := ps.activeTab(); active == nil || active.Entry != two {
			t.Fatalf("section %d opened wrong request: %+v", section, active)
		}
	}
}

func TestAuthoringMentionCacheFollowsRequestDeletion(t *testing.T) {
	a, ps := openTestProject(t, "Integrated Ref Cache")
	req := addMentionRequest(t, ps, "", "One", "GET", "/one")
	tab := openDocForTest(t, ps, "docs/cache.md", "[@One](gorequest://request/"+req.Request.ID+")")
	a.installDocMentionHooks(tab, tab.DocEditor)
	if refs := a.docMentionRefs(tab, tab.DocEditor); len(refs) != 1 || !refs[0].Found {
		t.Fatalf("refs: %+v", refs)
	}
	delete(ps.project.Requests, req.Request.ID)
	if refs := a.docMentionRefs(tab, tab.DocEditor); len(refs) != 1 || refs[0].Found {
		t.Fatalf("deleted ref still resolves: %+v", refs)
	}
}
