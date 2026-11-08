package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
)

// mentionEditorView renders one editor alone, focused, so the native
// completion overlay is built exactly as it is in a document tab.
func mentionEditorView(ed *editor.State) func(c *ui.Context) {
	return func(c *ui.Context) {
		ui.Column(c).Grow(1).Children(func() {
			editor.View(c, ed).Grow(1).Focus()
		})
	}
}

func addMentionRequest(t *testing.T, ps *projectState, folder, name, method, url string) *core.RequestEntry {
	t.Helper()
	entry, err := ps.project.NewRequest(folder, name)
	if err != nil {
		t.Fatalf("NewRequest(%q, %q): %v", folder, name, err)
	}
	entry.Request.Name = name
	if entry.Request.HTTP == nil {
		entry.Request.HTTP = &core.HTTPDef{}
	}
	entry.Request.HTTP.Method = method
	entry.Request.HTTP.URL = url
	return entry
}

func TestDocMentionCompletionShowsAndInsertsLink(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	req := addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	tab := openDocForTest(t, ps, "docs/mentions.md", "")
	a.installDocMentionHooks(tab, tab.DocEditor)

	tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("@auth/用户登录") {
		t.Fatalf("typing @ did not show the completion: %v", tt.Texts())
	}
	if !tt.HasText("POST") || !tt.HasText("/users/login") {
		t.Fatalf("completion row lacks method/address: %v", tt.Texts())
	}

	tt.Type("auth")
	tt.Frame()
	if !tt.HasText("@auth/用户登录") {
		t.Fatalf("typing a folder query lost the completion: %v", tt.Texts())
	}

	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	want := "[@auth/用户登录](gorequest://request/" + req.Request.ID + ")"
	if got := tab.DocEditor.Buffer.String(); got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	if tab.DocEditor.Buffer.Modified() != true {
		t.Fatal("inserting a mention must mark the document modified")
	}
}

func TestDocMentionDoesNotTriggerInEmailCodeOrLink(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	label := "@auth/用户登录"

	cases := []struct {
		name string
		src  string
		text string
	}{
		{"email", "", "user@example.com"},
		{"fenced code", "```\n", "@"},
		{"inline code", "`", "@"},
		{"link destination", "[x](", "@"},
		{"escaped", `\`, "@"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tab := openDocForTest(t, ps, "docs/"+strings.ReplaceAll(c.name, " ", "-")+".md", c.src)
			a.installDocMentionHooks(tab, tab.DocEditor)
			tab.DocEditor.Select(tab.DocEditor.Buffer.Len(), tab.DocEditor.Buffer.Len())
			tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
			tt.Frame()
			tt.Type(c.text)
			tt.Frame()
			if tt.HasText(label) {
				t.Fatalf("%s: mention completion must not trigger: %v", c.name, tt.Texts())
			}
		})
	}
}

func TestDocMentionChineseIMECommitAndNavigation(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	first := addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	second := addMentionRequest(t, ps, "auth", "用户登录2", "GET", "/users/login2")
	tab := openDocForTest(t, ps, "docs/ime.md", "")
	a.installDocMentionHooks(tab, tab.DocEditor)

	tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	tt.Frame()

	// Compose and commit the Chinese query the way an input method does.
	tt.Compose("用", 1)
	tt.Frame()
	tt.Type("用")
	tt.Frame()
	tt.Compose("户", 1)
	tt.Frame()
	tt.Type("户")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "@用户" {
		t.Fatalf("IME query buffer = %q, want %q", got, "@用户")
	}
	if !tt.HasText("@auth/用户登录") || !tt.HasText("@auth/用户登录2") {
		t.Fatalf("Chinese query did not match both requests: %v", tt.Texts())
	}

	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	want := "[@auth/用户登录2](gorequest://request/" + second.Request.ID + ")"
	if got := tab.DocEditor.Buffer.String(); got != want {
		t.Fatalf("Down+Enter inserted %q, want %q (first %q)", got, want, first.Request.ID)
	}
}

func TestDocMentionEscapeClosesCompletion(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	tab := openDocForTest(t, ps, "docs/escape.md", "")
	a.installDocMentionHooks(tab, tab.DocEditor)

	tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("@auth/用户登录") {
		t.Fatalf("completion missing before Escape: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("@auth/用户登录") {
		t.Fatalf("Escape did not close the completion: %v", tt.Texts())
	}
}

func TestDocMentionDisambiguatesSameName(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	one := addMentionRequest(t, ps, "auth", "login", "GET", "/one")
	two := addMentionRequest(t, ps, "auth", "login", "GET", "/two")

	tab := openDocForTest(t, ps, "docs/same.md", "")
	a.installDocMentionHooks(tab, tab.DocEditor)
	tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	tt.Frame()
	tt.Type("login")
	tt.Frame()
	tt.Frame()

	if !tt.HasText("@auth/login · "+one.Request.ID) || !tt.HasText("@auth/login · "+two.Request.ID) {
		t.Fatalf("same-name requests are not distinguishable: %v", tt.Texts())
	}
}

func TestDocMentionReportsInvalidIDAndNeverOffersIt(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	bad := addMentionRequest(t, ps, "auth", "bad", "GET", "/bad")
	delete(ps.project.Requests, bad.Request.ID)
	bad.Request.ID = "用户接口"
	ps.project.Requests[bad.Request.ID] = bad

	tab := openDocForTest(t, ps, "docs/invalid.md", "")
	a.installDocMentionHooks(tab, tab.DocEditor)
	tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	tt.Frame()
	tt.Type("用户")
	tt.Frame()
	tt.Frame()

	if tt.HasText("@auth/bad") || tt.HasText("gorequest://request/用户接口") {
		t.Fatalf("an invalid ID must not be offered as a link: %v", tt.Texts())
	}
	dm := a.docMentionMsg()
	if !tt.HasText(fmt.Sprintf(dm.InvalidOnly, 1)) {
		t.Fatalf("invalid ID was not reported: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "@用户" {
		t.Fatalf("accepting the hint must not change the text: %q", got)
	}
}

func TestDocMentionStaleStripRepicksDestinationOnly(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	req := addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	src := "See [@auth/用户登录](gorequest://request/missing.id).\n"
	tab := openDocForTest(t, ps, "docs/stale.md", src)
	a.installDocMentionHooks(tab, tab.DocEditor)

	tt := ui.NewTester(func(c *ui.Context) {
		root := ui.Column(c).Grow(1)
		a.renderDocMentionTools(c, c.Theme(), root, tab, tab.DocEditor)
		editor.View(c, tab.DocEditor).Grow(1)
	}, 900, 600)
	tt.Frame()
	dm := a.docMentionMsg()
	if !tt.HasText(fmt.Sprintf(dm.StaleCount, 1)) {
		t.Fatalf("stale reference not reported: %v", tt.Texts())
	}
	repick := fmt.Sprintf(dm.RepickFor, "@auth/用户登录")
	if !tt.HasText(repick) {
		t.Fatalf("re-pick entry missing: %v", tt.Texts())
	}
	if err := tt.Click(repick); err != nil {
		t.Fatalf("open re-pick menu: %v", err)
	}
	tt.Frame()
	if err := tt.Click("@auth/用户登录 · " + req.Request.ID); err != nil {
		t.Fatalf("choose replacement: %v (texts %v)", err, tt.Texts())
	}
	tt.Frame()
	want := "See [@auth/用户登录](gorequest://request/" + req.Request.ID + ").\n"
	if got := tab.DocEditor.Buffer.String(); got != want {
		t.Fatalf("repair rewrote more than the destination: %q, want %q", got, want)
	}
}

func TestDocMentionOpenFocusesExistingTabWithoutTouchingSource(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	req := addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	src := "See [@auth/用户登录](gorequest://request/" + req.Request.ID + ").\n"
	tab := openDocForTest(t, ps, "docs/open.md", src)
	dest := "gorequest://request/" + req.Request.ID

	if !a.docMentionOpenLink(tab, dest) {
		t.Fatal("opening a known mention link failed")
	}
	opened := ps.activeTab()
	if opened == nil || opened.Kind != "request" {
		t.Fatalf("mention link did not open the request tab: %+v", opened)
	}
	tabs := len(ps.tabs)
	if !a.docMentionOpenLink(tab, dest) {
		t.Fatal("reopening a known mention link failed")
	}
	if len(ps.tabs) != tabs {
		t.Fatalf("reopening created a tab: %d -> %d", tabs, len(ps.tabs))
	}

	// A rename keeps the ID reachable and leaves the label snapshot alone.
	req.Request.Name = "改名后的接口"
	if !a.docMentionOpenLink(tab, dest) {
		t.Fatal("renamed request is no longer reachable by ID")
	}
	if ps.activeTab() != opened {
		t.Fatal("renamed request did not focus its existing tab")
	}
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("navigation changed the source: %q", got)
	}
	info := a.docMentionHover(tab, dest)
	if info.Stale || info.Title != "改名后的接口" || info.Method != "POST" {
		t.Fatalf("hover does not show the current request: %+v", info)
	}

	// Moving the request changes its folder but not its ID.
	req.RelPath = "collections/moved/login.request.yaml"
	if !a.docMentionOpenLink(tab, dest) {
		t.Fatal("moved request is no longer reachable by ID")
	}
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("moving changed the label snapshot: %q", got)
	}

	// A deleted request is reported, not opened, and adds no tab.
	if !a.docMentionOpenLink(tab, "gorequest://request/gone.id") {
		t.Fatal("a gorequest link with a missing ID must be handled, not fall through")
	}
	if len(ps.tabs) != tabs {
		t.Fatalf("a missing request opened a tab: %d -> %d", tabs, len(ps.tabs))
	}
}

func TestDocMentionInstallIsIdempotent(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	tab := openDocForTest(t, ps, "docs/idempotent.md", "")

	a.installDocMentionHooks(tab, tab.DocEditor)
	s := docMentionSessionFor(tab.DocEditor)
	if s == nil {
		t.Fatal("hooks were not installed")
	}
	if s.prevInput != nil || s.prevCompletion != nil || s.prevRender != nil {
		t.Fatal("a fresh editor must have no previous callbacks to compose")
	}
	a.installDocMentionHooks(tab, tab.DocEditor)
	if again := docMentionSessionFor(tab.DocEditor); again != s {
		t.Fatal("a second install replaced the session")
	}
	if again := docMentionSessionFor(tab.DocEditor); again.prevInput != nil {
		t.Fatal("a second install wrapped the callbacks again")
	}

	tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "@" {
		t.Fatalf("double install typed the trigger twice: %q", got)
	}
}

func TestDocMentionUsesOwnerProjectOnly(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "One")); err != nil {
		t.Fatal(err)
	}
	if err := m.openProject(writeProject(t, "Two")); err != nil {
		t.Fatal(err)
	}
	one, two := m.workspaces[0], m.workspaces[1]
	own := addMentionRequest(t, one.ps, "auth", "本项目接口", "GET", "/one")
	addMentionRequest(t, two.ps, "auth", "别的项目接口", "GET", "/two")

	tab := openDocForTest(t, one.ps, "docs/owner.md", "")
	one.installDocMentionHooks(tab, tab.DocEditor)
	tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("@auth/本项目接口") {
		t.Fatalf("owner project request missing: %v", tt.Texts())
	}
	if tt.HasText("别的项目接口") {
		t.Fatalf("another project's request leaked into the completion: %v", tt.Texts())
	}
	if own.Request.ID == "" {
		t.Fatal("fixture request has no ID")
	}
}

func TestDocMentionWorksOnAnyEditorState(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	req := addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	tab := openDocForTest(t, ps, "docs/any-editor.md", "canonical\n")

	// A block editor of U06's canvas, not the tab's canonical DocEditor.
	ed := &editor.State{Buffer: editor.NewBuffer("")}
	a.installDocMentionHooks(tab, ed)
	tt := ui.NewTester(mentionEditorView(ed), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("@auth/用户登录") {
		t.Fatalf("completion did not install on the passed editor: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	want := "[@auth/用户登录](gorequest://request/" + req.Request.ID + ")"
	if got := ed.Buffer.String(); got != want {
		t.Fatalf("passed editor buffer = %q, want %q", got, want)
	}
	if got := tab.DocEditor.Buffer.String(); got != "canonical\n" {
		t.Fatalf("canonical DocEditor was modified: %q", got)
	}
}

func TestDocMentionComposesExistingCallbacks(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	tab := openDocForTest(t, ps, "docs/compose.md", "")
	ed := tab.DocEditor
	inputs, completions, definitions := 0, 0, 0
	ed.OnInput = func(ui.InputEvent) bool { inputs++; return false }
	ed.OnCompletion = func(editor.CompletionRequest) { completions++ }
	ed.OnDefinition = func(int) { definitions++ }
	a.installDocMentionHooks(tab, ed)
	s := docMentionSessionFor(ed)
	if s == nil || s.prevInput == nil || s.prevCompletion == nil || s.prevDefinition == nil {
		t.Fatal("existing callbacks were not captured for composition")
	}

	tt := ui.NewTester(mentionEditorView(ed), 900, 600)
	tt.Frame()
	tt.Type("x ")
	tt.Frame()
	if inputs == 0 {
		t.Fatal("a plain keystroke did not reach the previous OnInput")
	}
	before := inputs
	tt.Type("@")
	tt.Frame()
	if inputs != before {
		t.Fatal("the mention trigger was also passed to the previous OnInput")
	}

	// A completion request that is not a mention goes to the previous hook.
	ed.OnCompletion(editor.CompletionRequest{Offset: ed.Cursor()})
	if completions == 0 {
		t.Fatal("a non-mention completion request did not reach the previous OnCompletion")
	}
	ed.OnDefinition(0)
	if definitions == 0 {
		t.Fatal("a non-mention definition did not reach the previous OnDefinition")
	}
}

func TestDocMentionDefinitionOpensSourceLink(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	req := addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	src := "See [@auth/用户登录](gorequest://request/" + req.Request.ID + ").\n"
	tab := openDocForTest(t, ps, "docs/definition.md", src)
	a.installDocMentionHooks(tab, tab.DocEditor)

	tab.DocEditor.OnDefinition(strings.Index(src, "@auth"))
	opened := ps.activeTab()
	if opened == nil || opened.Kind != "request" {
		t.Fatalf("Cmd+click on a mention did not open its request: %+v", opened)
	}
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("Cmd+click changed the source: %q", got)
	}
}

func TestDocMentionChipOpensAndMarksStale(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	req := addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	tab := openDocForTest(t, ps, "docs/chip.md", "")
	dest := "gorequest://request/" + req.Request.ID

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() {
			a.docMentionChip(c, c.Theme(), tab, "@auth/用户登录", dest)
		})
	}, 700, 200)
	tt.Frame()
	if !tt.HasText("@auth/用户登录") || !tt.HasText("POST") {
		t.Fatalf("chip lacks label or method: %v", tt.Texts())
	}
	if err := tt.Click("@auth/用户登录"); err != nil {
		t.Fatalf("click chip: %v", err)
	}
	tt.Frame()
	if opened := ps.activeTab(); opened == nil || opened.Kind != "request" {
		t.Fatalf("chip click did not open the request: %+v", opened)
	}

	tabs := len(ps.tabs)
	tt2 := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() {
			a.docMentionChip(c, c.Theme(), tab, "@auth/用户登录", "gorequest://request/gone.id")
		})
	}, 700, 200)
	tt2.Frame()
	if !tt2.HasText("!") {
		t.Fatalf("stale chip has no warning mark: %v", tt2.Texts())
	}
	if err := tt2.Click("@auth/用户登录"); err != nil {
		t.Fatalf("click stale chip: %v", err)
	}
	tt2.Frame()
	if len(ps.tabs) != tabs {
		t.Fatalf("a stale chip opened a tab: %d -> %d", tabs, len(ps.tabs))
	}
	if !strings.Contains(a.status, "gone.id") {
		t.Fatalf("a stale chip did not report the missing request: %q", a.status)
	}
}

func TestDocMentionEmptyProjectAndNoMatch(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	tab := openDocForTest(t, ps, "docs/empty.md", "")
	a.installDocMentionHooks(tab, tab.DocEditor)
	dm := a.docMentionMsg()

	tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	tt.Frame()
	if !tt.HasText(dm.NoRequests) {
		t.Fatalf("empty project has no prompt: %v", tt.Texts())
	}

	addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	tt.Type("zzz")
	tt.Frame()
	tt.Frame()
	if !tt.HasText(dm.NoMatch) {
		t.Fatalf("no-match query has no prompt: %v", tt.Texts())
	}
}

func TestDocMentionMouseChooseInsertsLink(t *testing.T) {
	a, ps := openTestProject(t, "Mentions")
	req := addMentionRequest(t, ps, "auth", "用户登录", "POST", "/users/login")
	tab := openDocForTest(t, ps, "docs/mouse.md", "")
	a.installDocMentionHooks(tab, tab.DocEditor)

	tt := ui.NewTester(mentionEditorView(tab.DocEditor), 900, 600)
	tt.Frame()
	tt.Type("@")
	tt.Frame()
	tt.Frame()
	if err := tt.Click("@auth/用户登录"); err != nil {
		t.Fatalf("click completion row: %v", err)
	}
	tt.Frame()
	want := "[@auth/用户登录](gorequest://request/" + req.Request.ID + ")"
	if got := tab.DocEditor.Buffer.String(); got != want {
		t.Fatalf("mouse choice inserted %q, want %q", got, want)
	}
}
