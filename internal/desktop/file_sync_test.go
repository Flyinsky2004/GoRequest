package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

// queuedTestWindow is a thread-safe headless window whose Update queues UI
// callbacks onto a channel drained by the test goroutine, matching real
// mygo.Window UI-thread dispatch under -race.
type queuedTestWindow struct {
	mu        sync.Mutex
	title     string
	shown     bool
	destroyed bool
	queue     []func()
	onClose   []func(*mygo.CloseEvent)
	onClosed  []func()
	notifyCh  chan struct{}
}

func newQueuedTestWindow(title string) *queuedTestWindow {
	return &queuedTestWindow{
		title:    title,
		notifyCh: make(chan struct{}, 1),
	}
}

func (w *queuedTestWindow) Show() {
	w.mu.Lock()
	if !w.destroyed {
		w.shown = true
	}
	w.mu.Unlock()
}

func (w *queuedTestWindow) Hide() {
	w.mu.Lock()
	w.shown = false
	w.mu.Unlock()
}

func (w *queuedTestWindow) Focus() {}

func (w *queuedTestWindow) SetTitle(t string) {
	w.mu.Lock()
	w.title = t
	w.mu.Unlock()
}

func (w *queuedTestWindow) Update(fn func()) {
	w.mu.Lock()
	if w.destroyed {
		w.mu.Unlock()
		return
	}
	w.queue = append(w.queue, fn)
	w.mu.Unlock()
	select {
	case w.notifyCh <- struct{}{}:
	default:
	}
}

func (w *queuedTestWindow) drainUI() int {
	n := 0
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return n
		}
		batch := w.queue
		w.queue = nil
		w.mu.Unlock()
		for _, fn := range batch {
			if fn != nil {
				fn()
				n++
			}
		}
	}
}

func (w *queuedTestWindow) OnClose(fn func(*mygo.CloseEvent)) func() {
	w.mu.Lock()
	w.onClose = append(w.onClose, fn)
	w.mu.Unlock()
	return func() {}
}

func (w *queuedTestWindow) OnClosed(fn func()) func() {
	w.mu.Lock()
	w.onClosed = append(w.onClosed, fn)
	w.mu.Unlock()
	return func() {}
}

func (w *queuedTestWindow) IsDestroyed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.destroyed
}

func (w *queuedTestWindow) IsVisible() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.shown
}

func (w *queuedTestWindow) Close() {
	w.mu.Lock()
	if w.destroyed {
		w.mu.Unlock()
		return
	}
	closeHandlers := make([]func(*mygo.CloseEvent), len(w.onClose))
	copy(closeHandlers, w.onClose)
	w.mu.Unlock()

	ev := &mygo.CloseEvent{}
	for _, fn := range closeHandlers {
		fn(ev)
	}
	if ev.DefaultPrevented() {
		return
	}

	w.mu.Lock()
	if w.destroyed {
		w.mu.Unlock()
		return
	}
	w.destroyed = true
	w.shown = false
	closedHandlers := make([]func(), len(w.onClosed))
	copy(closedHandlers, w.onClosed)
	w.mu.Unlock()

	for _, fn := range closedHandlers {
		fn()
	}
}

func TestFileSyncI18nCatalogComplete(t *testing.T) {
	typ := reflect.TypeOf(fileSyncMessages{})
	zh := reflect.ValueOf(fileSyncMessagesZh)
	en := reflect.ValueOf(fileSyncMessagesEn)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		zhStr := strings.TrimSpace(zh.Field(i).String())
		enStr := strings.TrimSpace(en.Field(i).String())
		if zhStr == "" {
			t.Errorf("fileSyncMessagesZh.%s is empty", f.Name)
		}
		if enStr == "" {
			t.Errorf("fileSyncMessagesEn.%s is empty", f.Name)
		}
		if zhStr == enStr {
			t.Errorf("fileSyncMessagesZh.%s equals English (%q)", f.Name, zhStr)
		}
	}
	if fileSyncMessagesFor("") != &fileSyncMessagesZh {
		t.Fatal("empty language should resolve to Simplified Chinese")
	}
	if fileSyncMessagesFor(langEnglish) != &fileSyncMessagesEn {
		t.Fatal("en should resolve to English")
	}
}

func TestWorkspaceIdleWithFileSyncRequestsNoFramesAndNeverRewritesOnOpen(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	dir := t.TempDir()
	p, err := core.NewProject(dir, "NoRewrite")
	if err != nil {
		t.Fatal(err)
	}
	commentedReq := strings.Join([]string{
		"# Preserve me on open",
		"schemaVersion: 1",
		"id: req-open-check",
		"name: Open Check",
		"http:",
		"  method: GET",
		"  url: https://api.example.com/v1/items # inline comment",
		"",
	}, "\n")
	reqAbs := filepath.Join(p.Store.Root, "collections", "open-check.request.yaml")
	if err := os.WriteFile(reqAbs, []byte(commentedReq), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager(t)
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}
	entry := a.ps.project.Requests["req-open-check"]
	if entry == nil {
		t.Fatal("expected req-open-check loaded")
	}
	a.ps.openRequest(entry)

	tt := ui.NewTester(a.View, 1180, 820)
	tt.Frame()
	// Advance past initial UnderlineTabs / button mount springs so workspace settles.
	tt.Advance(2 * time.Second)
	if tt.FramePending() {
		t.Fatal("workspace with clean file-sync requested animation frames while idle")
	}
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("workspace requested animation frames on second idle redraw")
	}

	afterBytes, err := os.ReadFile(reqAbs)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBytes) != commentedReq {
		t.Fatalf("opening project and rendering tab rewrote file!\ngot:\n%s\nwant:\n%s", string(afterBytes), commentedReq)
	}
}

func TestSaveControlPreservesYAMLCommentsOrderAndExtensions(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	dir := t.TempDir()
	p, err := core.NewProject(dir, "PreserveUI")
	if err != nil {
		t.Fatal(err)
	}
	originalYAML := strings.Join([]string{
		"# Header comment kept across UI Save",
		"id: req-preserve-ui",
		"schemaVersion: 1",
		"name: Preserve UI Request",
		"http:",
		"  url: https://api.example.com/old # url inline comment",
		"  method: POST",
		"  headers:",
		"    - name: X-Custom",
		"      value: keep-me",
		"      enabled: true",
		"  body:",
		"    type: json",
		"    value:",
		"      # nested comment inside JSON block YAML",
		"      z_first: 1",
		"      a_second: true",
		"extensions:",
		"  x-owner: platform-team",
		"# Footer comment",
		"",
	}, "\n")
	reqAbs := filepath.Join(p.Store.Root, "collections", "preserve.request.yaml")
	if err := os.WriteFile(reqAbs, []byte(originalYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager(t)
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}
	entry := a.ps.project.Requests["req-preserve-ui"]
	if entry == nil {
		t.Fatal("expected req-preserve-ui loaded")
	}
	a.ps.openRequest(entry)
	tab := a.ps.activeTab()

	// Only edit URL in the UI tab; leave headers, JSON body, comments, and extensions untouched.
	tab.URL = "https://api.example.com/new-endpoint"
	tab.Dirty = true

	tt := ui.NewTester(a.View, 1180, 820)
	tt.Frame()
	if err := tt.Click("Save ⌘S"); err != nil {
		t.Fatalf("click Save ⌘S: %v", err)
	}
	if tab.Dirty {
		t.Fatalf("tab should be clean after Save; status=%q", a.status)
	}

	savedRaw, err := os.ReadFile(reqAbs)
	if err != nil {
		t.Fatal(err)
	}
	saved := string(savedRaw)
	for _, want := range []string{
		"# Header comment kept across UI Save",
		"# url inline comment",
		"https://api.example.com/new-endpoint",
		"enabled: true",
		"# nested comment inside JSON block YAML",
		"z_first: 1",
		"x-owner: platform-team",
		"# Footer comment",
	} {
		if !strings.Contains(saved, want) {
			t.Fatalf("saved YAML lost %q:\n%s", want, saved)
		}
	}
	// Key ordering: id was before schemaVersion and z_first before a_second.
	if strings.Index(saved, "id: req-preserve-ui") > strings.Index(saved, "schemaVersion: 1") {
		t.Fatalf("top-level key order was not preserved:\n%s", saved)
	}
	if strings.Index(saved, "z_first: 1") > strings.Index(saved, "a_second: true") {
		t.Fatalf("JSON block map key order was not preserved:\n%s", saved)
	}
}

func TestCleanTabsRefreshRenameAndDeleteWithoutResurrecting(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	a, ps := openTestProject(t, "CleanSync")
	reqTab := openRequestTabs(t, ps, "Alpha")[0]
	docEntry, err := ps.newDoc()
	if err != nil {
		t.Fatal(err)
	}
	docTab := ps.activeTab()
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}

	// Render body tab once so reqTab.bodyEditor is populated before external refresh.
	ps.activateTab(reqTab)
	reqTab.Section = 3 // Body section
	tt := ui.NewTester(a.View, 1180, 820)
	tt.Frame()

	// 1. External modification of both clean request and clean doc.
	reqAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(reqTab.Entry.RelPath))
	updatedReqYAML := strings.Join([]string{
		"schemaVersion: 1",
		"id: " + reqTab.Entry.Request.ID,
		"name: Alpha External",
		"http:",
		"  method: POST",
		"  url: https://external.example.com/v2",
		"  body:",
		"    type: json",
		"    value:",
		"      refreshed: true",
		"",
	}, "\n")
	if err := os.WriteFile(reqAbs, []byte(updatedReqYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	docAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(docEntry.RelPath))
	if err := os.WriteFile(docAbs, []byte("# Updated Externally\n\nBody v2.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if reqTab.Dirty || reqTab.URL != "https://external.example.com/v2" || reqTab.Method != "POST" {
		t.Fatalf("clean request tab did not refresh cleanly: dirty=%v method=%q url=%q", reqTab.Dirty, reqTab.Method, reqTab.URL)
	}
	if !strings.Contains(reqTab.BodyText, `"refreshed": true`) {
		t.Fatalf("clean request body did not refresh: %q", reqTab.BodyText)
	}
	// Render another frame on the Body tab to verify cached bodyEditor rebuilt cleanly without marking Dirty.
	tt.Frame()
	if reqTab.Dirty {
		t.Fatal("rendering refreshed Body tab must not mark clean tab Dirty")
	}
	if docTab.Dirty || !strings.Contains(docTab.DocEditor.Buffer.String(), "Updated Externally") {
		t.Fatalf("clean doc tab did not refresh: dirty=%v text=%q", docTab.Dirty, docTab.DocEditor.Buffer.String())
	}

	// 2. External rename of both clean files.
	newReqRel := "collections/v2/alpha-moved.request.yaml"
	newReqAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(newReqRel))
	if err := os.MkdirAll(filepath.Dir(newReqAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(reqAbs, newReqAbs); err != nil {
		t.Fatal(err)
	}
	newDocRel := "docs/handbook.md"
	newDocAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(newDocRel))
	if err := os.Rename(docAbs, newDocAbs); err != nil {
		t.Fatal(err)
	}

	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if reqTab.Entry.RelPath != newReqRel {
		t.Fatalf("request tab did not follow rename: got %q, want %q", reqTab.Entry.RelPath, newReqRel)
	}
	if docTab.DocRel != newDocRel || docTab.Title != "handbook.md" {
		t.Fatalf("doc tab did not follow rename: DocRel=%q Title=%q", docTab.DocRel, docTab.Title)
	}
	if _, err := os.Stat(reqAbs); !os.IsNotExist(err) {
		t.Fatal("old request path must remain absent")
	}
	if _, err := os.Stat(docAbs); !os.IsNotExist(err) {
		t.Fatal("old doc path must remain absent")
	}

	// 3. External deletion of both clean files closes tabs without resurrecting.
	if err := os.Remove(newReqAbs); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(newDocAbs); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if len(ps.tabs) != 0 {
		t.Fatalf("clean deleted tabs should close automatically, remaining=%d", len(ps.tabs))
	}
	if _, err := os.Stat(newReqAbs); !os.IsNotExist(err) {
		t.Fatal("deleted request file was resurrected")
	}
	if _, err := os.Stat(newDocAbs); !os.IsNotExist(err) {
		t.Fatal("deleted doc file was resurrected")
	}
}

func TestDirtyModifyConflictBannerAndThreeVersionModalUI(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	dir := t.TempDir()
	p, err := core.NewProject(dir, "ConflictUI")
	if err != nil {
		t.Fatal(err)
	}
	baseYAML := strings.Join([]string{
		"# Keep team comment",
		"schemaVersion: 1",
		"id: req-3way",
		"name: Three Way Demo",
		"http:",
		"  method: GET",
		"  url: https://api.example.com/v1/users",
		"timeout: 5s",
		"",
	}, "\n")
	reqAbs := filepath.Join(p.Store.Root, "collections", "three-way.request.yaml")
	if err := os.WriteFile(reqAbs, []byte(baseYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager(t)
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}
	a.ps.openRequest(a.ps.project.Requests["req-3way"])
	tab := a.ps.activeTab()

	// User edits URL locally (line 7), leaving timeout untouched.
	tab.URL = "https://api.example.com/v2/users"
	tab.Dirty = true

	// External editor changes timeout on disk (line 8), leaving URL untouched.
	diskYAML := strings.Replace(baseYAML, "timeout: 5s", "timeout: 30s", 1)
	if err := os.WriteFile(reqAbs, []byte(diskYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if a.ConflictForTab(tab) == nil {
		t.Fatal("expected three-version conflict for dirty tab")
	}
	// Disk file and user edit must BOTH remain untouched before resolution.
	rawOnDisk, _ := os.ReadFile(reqAbs)
	if string(rawOnDisk) != diskYAML {
		t.Fatal("SyncNow must not overwrite external disk changes")
	}
	if tab.URL != "https://api.example.com/v2/users" || !tab.Dirty {
		t.Fatal("SyncNow must preserve user's unsaved tab edits")
	}

	tt := ui.NewTester(a.View, 1280, 860)
	tt.Frame()
	for _, want := range []string{"External edit conflict:", "Compare 3 Versions…", "Reload Disk", "Save As Copy"} {
		if !tt.HasText(want) {
			t.Fatalf("missing English conflict banner text %q; texts=%v", want, tt.Texts())
		}
	}

	// Verify Simplified Chinese rendering of banner and modal too.
	m.cfg.cfg.Language = string(langSimplifiedChinese)
	tt.Frame()
	if !tt.HasText("外部修改冲突") || !tt.HasText("查看三版本…") {
		t.Fatalf("missing Chinese conflict banner; texts=%v", tt.Texts())
	}
	if err := tt.Click("查看三版本…"); err != nil {
		t.Fatalf("click 查看三版本…: %v", err)
	}
	for _, want := range []string{"处理外部文件修改冲突", "读取基线（只读）", "磁盘版本（只读）", "编辑 / 合并结果（可编辑）", "三方自动合并", "应用并保存"} {
		if !tt.HasText(want) {
			t.Fatalf("missing Chinese modal text %q; texts=%v", want, tt.Texts())
		}
	}

	// Switch back to English and exercise DiffView modes + 3-Way Auto-Merge + Save Resolution.
	m.cfg.cfg.Language = string(langEnglish)
	tt.Frame()
	if err := tt.Click("Baseline ↔ Disk"); err != nil {
		t.Fatalf("switch to Baseline ↔ Disk diff: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Disk ↔ Edited Merge"); err != nil {
		t.Fatalf("switch to Disk ↔ Edited Merge diff: %v", err)
	}
	tt.Frame()
	if err := tt.Click("3-Way Auto-Merge"); err != nil {
		t.Fatalf("click 3-Way Auto-Merge: %v", err)
	}
	if !tt.HasText("Clean 3-way merge generated") {
		t.Fatalf("expected clean merge note; texts=%v", tt.Texts())
	}
	if err := tt.Click("Save Resolution"); err != nil {
		t.Fatalf("click Save Resolution: %v", err)
	}

	if a.ConflictForTab(tab) != nil {
		t.Fatal("conflict should be cleared after Save Resolution")
	}
	if tab.Dirty {
		t.Fatal("tab should be clean after Save Resolution")
	}
	if tab.URL != "https://api.example.com/v2/users" || tab.Timeout != "30s" {
		t.Fatalf("tab did not reflect merged values: URL=%q Timeout=%q", tab.URL, tab.Timeout)
	}
	mergedDisk, err := os.ReadFile(reqAbs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Keep team comment", "https://api.example.com/v2/users", "timeout: 30s"} {
		if !strings.Contains(string(mergedDisk), want) {
			t.Fatalf("merged disk file missing %q:\n%s", want, string(mergedDisk))
		}
	}
}

func TestConflictModalRejectsGitMarkersAndSupportsSaveAsCopy(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	a, ps := openTestProject(t, "OverlapAndCopy")
	tab := openRequestTabs(t, ps, "Orders")[0]
	origID := tab.Entry.Request.ID
	origRel := tab.Entry.RelPath
	reqAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(origRel))

	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}

	// Both user and external disk change the exact same URL line differently.
	tab.URL = "https://local.example.com/orders"
	tab.Dirty = true

	diskYAML := strings.Join([]string{
		"schemaVersion: 1",
		"id: " + origID,
		"name: Orders",
		"http:",
		"  method: GET",
		"  url: https://disk.example.com/orders",
		"",
	}, "\n")
	if err := os.WriteFile(reqAbs, []byte(diskYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Clicking Save ⌘S before any watcher poll must intercept the conflict and open the 3-version modal!
	tt := ui.NewTester(a.View, 1280, 860)
	tt.Frame()
	if err := tt.Click("Save ⌘S"); err != nil {
		t.Fatalf("click Save ⌘S: %v", err)
	}
	if !tt.HasText("Resolve External File Modification") {
		t.Fatalf("Save ⌘S on diverged file should open conflict modal; texts=%v", tt.Texts())
	}

	// Running 3-Way Auto-Merge on overlapping URL edits produces conflict markers.
	if err := tt.Click("3-Way Auto-Merge"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Overlapping edits marked") {
		t.Fatalf("expected overlap warning; texts=%v", tt.Texts())
	}

	// Attempting Save Resolution while conflict markers remain MUST fail safely without writing to disk.
	if err := tt.Click("Save Resolution"); err != nil {
		t.Fatal(err)
	}
	if !tab.Dirty || a.ConflictForTab(tab) == nil {
		t.Fatal("rejected Save Resolution must keep tab dirty and conflict active")
	}
	if !tt.HasText("unresolved Git merge conflict markers") {
		t.Fatalf("expected unresolved Git conflict marker error in modal; texts=%v", tt.Texts())
	}
	stillDisk, _ := os.ReadFile(reqAbs)
	if string(stillDisk) != diskYAML {
		t.Fatal("failed resolution must not modify disk file")
	}

	// Dismiss and reopen modal (resets merge buffer to clean EditedBytes), then click Save As Copy.
	if err := tt.Click("Keep Editing"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Compare 3 Versions…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Save As Copy"); err != nil {
		t.Fatalf("click Save As Copy: %v", err)
	}

	if len(ps.tabs) != 2 {
		t.Fatalf("Save As Copy should keep original tab and open copy tab, tabs=%d", len(ps.tabs))
	}
	copyTab := ps.activeTab()
	if copyTab == tab {
		t.Fatal("newly created copy tab should become active")
	}
	if copyTab.Entry.Request.ID == origID {
		t.Fatalf("copy request must get a distinct stable ID, got %q", copyTab.Entry.Request.ID)
	}
	if copyTab.URL != "https://local.example.com/orders" || copyTab.Dirty {
		t.Fatalf("copy tab state unexpected: URL=%q Dirty=%v", copyTab.URL, copyTab.Dirty)
	}
	if tab.URL != "https://disk.example.com/orders" || tab.Dirty {
		t.Fatalf("original tab should reload clean disk version: URL=%q Dirty=%v", tab.URL, tab.Dirty)
	}
}

func TestDirtyExternalDeleteNeverResurrectsOnAutoSaveOrSaveUntilExplicitRestore(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	a, ps := openTestProject(t, "DeleteProtect")
	reqTab := openRequestTabs(t, ps, "Ephemeral")[0]
	docEntry, err := ps.newDoc()
	if err != nil {
		t.Fatal(err)
	}
	docTab := ps.activeTab()
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}

	// Dirty both tabs.
	reqTab.URL = "https://keep.example.com/unsaved"
	reqTab.Dirty = true
	docTab.DocEditor.Buffer.Set("# Unsaved Doc Content\n\nStill editing.\n")
	docTab.Dirty = true

	// Delete both files externally on disk.
	reqAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(reqTab.Entry.RelPath))
	docAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(docEntry.RelPath))
	if err := os.Remove(reqAbs); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(docAbs); err != nil {
		t.Fatal(err)
	}

	// AutoSaveTick MUST skip both and NEVER resurrect deleted files.
	saved, skipped := a.AutoSaveTick()
	if saved != 0 || skipped != 2 {
		t.Fatalf("AutoSaveTick = (%d, %d), want (0, 2)", saved, skipped)
	}
	if _, err := os.Stat(reqAbs); !os.IsNotExist(err) {
		t.Fatal("AutoSaveTick resurrected deleted request file!")
	}
	if _, err := os.Stat(docAbs); !os.IsNotExist(err) {
		t.Fatal("AutoSaveTick resurrected deleted doc file!")
	}

	// Activate dirty request tab and press Save ⌘S: must NOT resurrect, must open deletion modal.
	ps.activateTab(reqTab)
	tt := ui.NewTester(a.View, 1280, 860)
	tt.Frame()
	if err := tt.Click("Save ⌘S"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(reqAbs); !os.IsNotExist(err) {
		t.Fatal("Save ⌘S resurrected deleted request file without confirmation!")
	}
	if !tt.HasText("Resolve External File Deletion") {
		t.Fatalf("expected deletion modal; texts=%v", tt.Texts())
	}

	// Choose Accept Delete on request tab -> tab closes, file stays absent.
	if err := tt.Click("Accept Delete"); err != nil {
		t.Fatal(err)
	}
	if ps.indexOfTab(reqTab) >= 0 {
		t.Fatal("request tab should close on Accept Delete")
	}
	if _, err := os.Stat(reqAbs); !os.IsNotExist(err) {
		t.Fatal("Accept Delete resurrected deleted request file!")
	}

	// Remaining active tab is docTab: open modal and click explicit Restore File.
	tt.Frame()
	if err := tt.Click("Compare 3 Versions…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Restore File"); err != nil {
		t.Fatal(err)
	}
	restoredDoc, err := os.ReadFile(docAbs)
	if err != nil {
		t.Fatalf("explicit Restore File should recreate doc: %v", err)
	}
	if !strings.Contains(string(restoredDoc), "Unsaved Doc Content") || docTab.Dirty {
		t.Fatalf("restored doc mismatch: dirty=%v content=%q", docTab.Dirty, string(restoredDoc))
	}
}

func TestDirtyExternalRenameAndModifyFollowsNewPathWithoutResurrectingOld(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	a, ps := openTestProject(t, "DirtyRename")
	tab := openRequestTabs(t, ps, "Billing")[0]
	reqID := tab.Entry.Request.ID
	oldRel := tab.Entry.RelPath
	oldAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(oldRel))

	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}

	tab.URL = "https://billing.example.com/v2/charge"
	tab.Dirty = true

	// Externally move AND edit name + timeout at new path, starting from actual on-disk bytes.
	origDiskBytes, err := os.ReadFile(oldAbs)
	if err != nil {
		t.Fatal(err)
	}
	newRel := "collections/finance/billing-charge.request.yaml"
	newAbs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(newRel))
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	movedYAML := strings.Replace(string(origDiskBytes), "name: Billing", "name: Billing Charge", 1) + "timeout: 15s\n"
	if err := os.WriteFile(newAbs, []byte(movedYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(oldAbs); err != nil {
		t.Fatal(err)
	}
	_ = reqID

	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	c := a.ConflictForTab(tab)
	if c == nil || c.ConflictKind != core.ConflictRenamedModified {
		t.Fatalf("expected ConflictRenamedModified, got %+v", c)
	}
	if c.OldPath != oldRel || c.RelPath != newRel {
		t.Fatalf("paths = (%q -> %q), want (%q -> %q)", c.OldPath, c.RelPath, oldRel, newRel)
	}

	tt := ui.NewTester(a.View, 1280, 860)
	tt.Frame()
	if err := tt.Click("Compare 3 Versions…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("3-Way Auto-Merge"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Save Resolution"); err != nil {
		t.Fatal(err)
	}
	if tab.Dirty || a.ConflictForTab(tab) != nil {
		t.Fatalf("expected conflict resolved cleanly; dirty=%v status=%q", tab.Dirty, a.status)
	}

	if _, err := os.Stat(oldAbs); !os.IsNotExist(err) {
		t.Fatal("old renamed path was resurrected!")
	}
	finalRaw, err := os.ReadFile(newAbs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(finalRaw), "https://billing.example.com/v2/charge") ||
		!strings.Contains(string(finalRaw), "timeout: 15s") {
		t.Fatalf("merged renamed file missing expected content:\n%s", string(finalRaw))
	}
}

func TestGitConflictMarkersBlockCanSendTab(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	a, ps := openTestProject(t, "GitSendGuard")
	tab := openRequestTabs(t, ps, "Ping")[0]
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}

	// Inject unresolved Git conflict markers into environments/dev.yaml on disk.
	devAbs := filepath.Join(ps.project.Store.Root, "environments", "dev.yaml")
	conflictedEnv := strings.Join([]string{
		"schemaVersion: 1",
		"id: dev",
		"variables:",
		"<<<<<<< HEAD",
		"  baseUrl: https://staging.example.com",
		"=======",
		"  baseUrl: https://prod.example.com",
		">>>>>>> main",
		"",
	}, "\n")
	if err := os.WriteFile(devAbs, []byte(conflictedEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if err := a.CanSendTab(tab); err == nil {
		t.Fatal("CanSendTab must block sending when active environment has Git conflict markers")
	}
}

func TestBackgroundStartFileSyncStormCoalescingAndWindowClose(t *testing.T) {
	t.Cleanup(StopAllFileSync)

	var createdWin *queuedTestWindow
	mgr := &Manager{
		cfg:     newConfigStore(filepath.Join(t.TempDir(), "config.json")),
		secrets: platform.NewEnvSecrets(),
		newWindow: func(s windowSpec) window {
			createdWin = newQueuedTestWindow(s.Title)
			return createdWin
		},
	}
	mgr.cfg.cfg.Language = string(langEnglish)

	dir := writeProject(t, "StormProject")
	if err := mgr.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := mgr.workspaces[0]
	tab := openRequestTabs(t, a.ps, "Rapid")[0]
	reqID := tab.Entry.Request.ID
	reqAbs := filepath.Join(a.ps.project.Store.Root, filepath.FromSlash(tab.Entry.RelPath))

	err := a.StartFileSync(core.WatchOptions{
		PollInterval:   20 * time.Millisecond,
		DebounceWindow: 40 * time.Millisecond,
		MaxBatchWait:   200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Rapidly write 25 revisions plus temp/swap noise files.
	tmpNoise := filepath.Join(filepath.Dir(reqAbs), ".tmp-editor-noise")
	swpNoise := reqAbs + ".swp"
	_ = os.WriteFile(tmpNoise, []byte("partial"), 0o644)
	_ = os.WriteFile(swpNoise, []byte("vim-swap"), 0o644)
	for i := 0; i < 25; i++ {
		body := strings.Join([]string{
			"schemaVersion: 1",
			"id: " + reqID,
			"name: Rapid",
			"http:",
			"  method: GET",
			"  url: https://storm.example.com/rev-" + strconv.Itoa(i),
			"",
		}, "\n")
		if err := os.WriteFile(reqAbs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Remove(tmpNoise)
	_ = os.Remove(swpNoise)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-createdWin.notifyCh:
			createdWin.drainUI()
		case <-time.After(25 * time.Millisecond):
			createdWin.drainUI()
		}
		if tab.URL == "https://storm.example.com/rev-24" {
			break
		}
	}
	if tab.URL != "https://storm.example.com/rev-24" {
		t.Fatalf("tab URL after storm = %q, want rev-24", tab.URL)
	}
	stats := a.FileSyncWatcherStats()
	if stats.CoalescedBatches == 0 || stats.CoalescedBatches > 5 {
		t.Fatalf("expected storm of 25 writes to coalesce into 1..5 batches, got %+v", stats)
	}

	// Close window and stop file sync; subsequent writes must not panic or race.
	createdWin.Close()
	a.StopFileSync()
	_ = os.WriteFile(reqAbs, []byte("schemaVersion: 1\nid: "+reqID+"\nname: AfterClose\nhttp:\n  method: GET\n  url: https://after-close.local\n"), 0o644)
	time.Sleep(60 * time.Millisecond)
	createdWin.drainUI()
}
