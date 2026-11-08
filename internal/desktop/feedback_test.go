package desktop

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

func u32CountText(texts []string, target string) int {
	n := 0
	for _, s := range texts {
		if s == target {
			n++
		}
	}
	return n
}

func rectPixelsChanged(before, after []byte, stride int, r ui.Rect) bool {
	x0 := max(0, int(r.X)-4)
	y0 := max(0, int(r.Y)-4)
	x1 := int(r.X+r.W) + 4
	y1 := int(r.Y+r.H) + 4
	for y := y0; y < y1; y++ {
		row := y * stride
		if row+x1*4 > len(before) || row+x1*4 > len(after) {
			break
		}
		for x := x0; x < x1; x++ {
			off := row + x*4
			if before[off] != after[off] || before[off+1] != after[off+1] || before[off+2] != after[off+2] {
				return true
			}
		}
	}
	return false
}

func TestU32ToastRendersPixelsDedupesExpiresAndIdleBudget(t *testing.T) {
	for _, dark := range []bool{false, true} {
		mode := "light"
		if dark {
			mode = "dark"
		}
		for _, lang := range []language{langSimplifiedChinese, langEnglish} {
			t.Run(mode+"/"+string(lang), func(t *testing.T) {
				a, ps := openTestProject(t, "ToastPixels")
				a.mgr.cfg.cfg.Language = string(lang)
				tab := openRequestTabs(t, ps, "ToastReq")[0]
				tab.URL = "https://api.example.com/v1/orders"
				tab.Resp = &core.ExecResult{
					Status:   200,
					Proto:    "HTTP/1.1",
					Body:     []byte(`{"copied":true}`),
					Pretty:   "{\n  \"copied\": true\n}",
					BodySize: 15,
				}
				tab.respVersion = 1

				tt := ui.NewTester(a.View, 1200, 820)
				tt.SetDark(dark)
				tt.Frame()
				before := append([]byte(nil), tt.Image().Pix...)

				// Click Copy Response on the toolbar.
				if err := tt.Click(a.msg().CopyResponse); err != nil {
					t.Fatalf("click CopyResponse: %v", err)
				}
				if got := tt.Clipboard(); got != tab.Resp.Pretty {
					t.Fatalf("clipboard = %q, want %q", got, tab.Resp.Pretty)
				}

				wantToast := a.feedbackMsg().CopySuccess
				toastRect, ok := tt.Find(wantToast)
				if !ok {
					t.Fatalf("missing toast %q on click frame; texts=%v", wantToast, tt.Texts())
				}
				// Toast must sit in the floating bottom-center toast viewport above
				// the bottom status bar (which is at X≈12 on the far left), not be
				// mistaken for a.status.
				if toastRect.X < 200 || toastRect.Y > 820-28 {
					t.Fatalf("toast %q not positioned in floating viewport above status bar: %+v", wantToast, toastRect)
				}
				after := tt.Image()
				if !rectPixelsChanged(before, after.Pix, after.Stride, toastRect) {
					t.Fatalf("toast %q was added to tree (%+v) but painted zero pixels on frame 0", wantToast, toastRect)
				}
				// Also verify toast remains visibly painted at +500ms steady state.
				tt.Advance(500 * time.Millisecond)
				mid := tt.Image()
				if !rectPixelsChanged(before, mid.Pix, mid.Stride, toastRect) {
					t.Fatalf("toast %q not painted at +500ms", wantToast)
				}

				// Rapid repeated clicks must replace in-place (never stack) and keep
				// pixels visible on every click.
				for i := 0; i < 4; i++ {
					if err := tt.Click(a.feedbackMsg().CopyURLTooltip); err != nil {
						t.Fatalf("repeat click Copy URL #%d: %v", i, err)
					}
					if got := u32CountText(tt.Texts(), wantToast); got != 1 {
						t.Fatalf("repeat click #%d stacked %d toasts; texts=%v", i, got, tt.Texts())
					}
					cur := tt.Image()
					if !rectPixelsChanged(before, cur.Pix, cur.Stride, toastRect) {
						t.Fatalf("repeat click #%d blanked toast pixels", i)
					}
				}

				// After timeout + fade, the toast closes and requests zero idle frames.
				tt.Advance(feedbackToastTimeout + 500*time.Millisecond)
				tt.Frame()
				if tt.HasText(wantToast) {
					t.Fatalf("toast %q did not expire after timeout; texts=%v", wantToast, tt.Texts())
				}
				tt.Frame()
				if tt.FramePending() {
					t.Fatal("expired toast viewport still requests animation frames while idle")
				}
			})
		}
	}
}

func TestU32ToastPassThroughNeverBlocksRepeatedClicksUnderneath(t *testing.T) {
	a, _ := openTestProject(t, "PassThrough")
	a.mgr.cfg.cfg.Language = string(langSimplifiedChinese)

	clicks := 0
	view := func(c *ui.Context) {
		a.inView = true
		defer func() { a.inView = false }()
		a.applyTheme(c)
		th := c.Theme()
		// Place a button at the exact bottom-center spot where the toast renders.
		ui.Column(c).Fill().Justify(ui.End).AlignItems(ui.Center).Padding(0, 0, 34, 0).Children(func() {
			if ui.Button(c, "BottomAction").Clicked() {
				clicks++
				a.notifyCopied("payload")
			}
		})
		a.drainFeedback(c)
		a.renderFeedbackViewport(c, th)
	}

	tt := ui.NewTester(view, 800, 600)
	tt.Frame()
	btnRect, ok := tt.Find("BottomAction")
	if !ok {
		t.Fatal("BottomAction button missing")
	}

	// Click 5 times at the button's center without moving the pointer away:
	// click #1 spawns the toast directly over btnRect, and clicks #2..#5 must
	// pass straight through the toast to BottomAction.
	cx, cy := btnRect.X+btnRect.W/2, btnRect.Y+btnRect.H/2
	for i := 1; i <= 5; i++ {
		tt.ClickAt(cx, cy)
		if clicks != i {
			t.Fatalf("click #%d blocked by toast overlay: clicks=%d want=%d", i, clicks, i)
		}
		toastRect, has := tt.Find("复制成功")
		if !has {
			t.Fatalf("click #%d missing toast", i)
		}
		// Confirm the test truly overlapped the toast and the button.
		if toastRect.Y >= btnRect.Y+btnRect.H || btnRect.Y >= toastRect.Y+toastRect.H {
			t.Fatalf("expected toast %+v to overlap button %+v vertically", toastRect, btnRect)
		}
	}

	// Even with the pointer still resting at (cx, cy), the PassThrough toast
	// must not freeze on hover — it must expire on schedule.
	tt.Move(cx, cy)
	tt.Advance(feedbackToastTimeout + 500*time.Millisecond)
	tt.Frame()
	if tt.HasText("复制成功") {
		t.Fatal("PassThrough toast froze under hovered pointer instead of expiring")
	}
}

func TestU32ToastInsideModalAndNeverClaimsFalseSuccess(t *testing.T) {
	a, ps := openTestProject(t, "TruthfulFeedback")
	a.mgr.cfg.cfg.Language = string(langSimplifiedChinese)
	entry, err := ps.createRequestIn("", "OrderAPI")
	if err != nil {
		t.Fatal(err)
	}
	ps.openRequest(entry)
	tab := ps.activeTab()
	tab.URL = ""
	tab.Resp = &core.ExecResult{Status: 204, Proto: "HTTP/1.1", Body: nil}
	tab.respVersion = 1

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	// Empty URL copy and empty 204 response body copy must not claim success.
	if err := tt.Click(a.feedbackMsg().CopyURLTooltip); err != nil {
		t.Fatalf("click empty Copy URL: %v", err)
	}
	if tt.HasText("复制成功") || a.status == a.msg().Copied {
		t.Fatalf("empty URL copy claimed success: status=%q texts=%v", a.status, tt.Texts())
	}
	if err := tt.Click(a.msg().CopyResponse); err != nil {
		t.Fatalf("click empty CopyResponse: %v", err)
	}
	if tt.HasText("复制成功") || a.status == a.msg().Copied {
		t.Fatalf("empty 204 response copy claimed success: status=%q texts=%v", a.status, tt.Texts())
	}

	// Nil-context programmatic calls must never claim clipboard success.
	a.Interop().ExportPreviewText = "curl https://example.com"
	a.CopyExportPreviewToClipboard(nil)
	a.OpenAPI().ExportPreviewText = "openapi: 3.1.0"
	a.CopyOpenAPIExportToClipboard(nil)
	if a.status == a.msg().Copied || a.feedbackPending != nil {
		t.Fatalf("nil context claimed clipboard copy: status=%q pending=%+v", a.status, a.feedbackPending)
	}

	// Copying inside an open modal (Snippet modal) must drain in the same frame
	// and render above the modal.
	tab.URL = "https://api.example.com/items"
	a.OpenSnippetModal()
	tt.Frame()
	if err := tt.Click(a.snippetMsg().CopyButton); err != nil {
		t.Fatalf("click snippet CopyButton: %v", err)
	}
	if !tt.HasText("复制成功") {
		t.Fatalf("modal copy did not show toast in same frame; texts=%v", tt.Texts())
	}
	tt.Advance(feedbackToastTimeout + 500*time.Millisecond)
	tt.Frame()
	a.Snippet().Open = false
	tt.Frame()

	// Real save must show SaveSuccess; subsequent conflicting save must show
	// SaveFailed and never SaveSuccess.
	tab.Dirty = true
	a.saveActiveWithFeedback()
	tt.Frame()
	if !tt.HasText(a.feedbackMsg().SaveSuccess) {
		t.Fatalf("real save did not show SaveSuccess toast; texts=%v", tt.Texts())
	}
	tt.Advance(feedbackToastTimeout + 500*time.Millisecond)
	tt.Frame()

	raw, _, err := ps.project.Store.Read(tab.Entry.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.project.Store.WriteAtomic(tab.Entry.RelPath, append(raw, []byte("\n# external edit\n")...)); err != nil {
		t.Fatal(err)
	}
	tab.URL = "https://api.example.com/local-dirty"
	tab.Dirty = true
	a.saveActiveWithFeedback()
	tt.Frame()
	if tt.HasText(a.feedbackMsg().SaveSuccess) {
		t.Fatalf("conflicting save falsely reported SaveSuccess; texts=%v", tt.Texts())
	}
	if !tt.HasText(a.feedbackMsg().SaveFailed) {
		t.Fatalf("conflicting save did not surface SaveFailed toast; texts=%v", tt.Texts())
	}
}

func TestU32HeaderPathPositionUniquenessStatesAndNarrowTruncation(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		t.Run(string(lang), func(t *testing.T) {
			a, ps := openTestProject(t, "PathGeoProj")
			a.mgr.cfg.cfg.Language = string(lang)
			// Hide sidebar so tree folder/item labels cannot mask path uniqueness.
			a.sidebarHidden = true

			tt := ui.NewTester(a.View, 1200, 820)
			tt.Frame()
			hm := a.headerPathMsg()
			m := a.msg()

			projRect, ok := tt.Find("PathGeoProj")
			if !ok {
				t.Fatalf("project MenuButton missing; texts=%v", tt.Texts())
			}
			envRect, ok := tt.Find(m.EnvLabel)
			if !ok {
				t.Fatalf("Env label missing; texts=%v", tt.Texts())
			}

			// 1. No active tab state: shown right after project button, before Env.
			noTabRect, ok := tt.Find(hm.NoActive)
			if !ok {
				t.Fatalf("no-active-tab hint %q missing; texts=%v", hm.NoActive, tt.Texts())
			}
			if noTabRect.X < projRect.X+projRect.W-0.5 || noTabRect.X+noTabRect.W > envRect.X+0.5 {
				t.Fatalf("no-tab hint not between project button and Env: proj=%+v noTab=%+v env=%+v",
					projRect, noTabRect, envRect)
			}
			if math.Abs(float64((noTabRect.Y+noTabRect.H/2)-(projRect.Y+projRect.H/2))) > 2.0 {
				t.Fatalf("no-tab hint not vertically aligned with project button: proj=%+v noTab=%+v",
					projRect, noTabRect)
			}

			// 2. Active Request tab in nested folder: Request › OrdersFolder › GetOrderDetail.
			folder, err := ps.createFolder("", "OrdersFolder")
			if err != nil {
				t.Fatal(err)
			}
			entry, err := ps.createRequestIn(folder.RelPath, "GetOrderDetail")
			if err != nil {
				t.Fatal(err)
			}
			ps.openRequest(entry)
			tt.Frame()

			if tt.HasText(hm.NoActive) {
				t.Fatal("no-active-tab hint persisted after opening a request")
			}
			projRect, _ = tt.Find("PathGeoProj")
			envRect, _ = tt.Find(m.EnvLabel)
			kindRect, ok := tt.Find(hm.RequestKind)
			if !ok {
				t.Fatalf("request kind %q missing; texts=%v", hm.RequestKind, tt.Texts())
			}
			folderRect, ok := tt.Find("OrdersFolder")
			if !ok {
				t.Fatalf("folder crumb missing; texts=%v", tt.Texts())
			}
			// Find the header title rect (the tab bar also renders the tab title below Y=46).
			if folderRect.Y >= 46 {
				t.Fatalf("folder crumb rendered outside top 46px header: %+v", folderRect)
			}
			// Uniqueness: with sidebar hidden, "OrdersFolder" must appear
			// ONLY ONCE (in the top header), proving the old breadcrumb row
			// between the tab bar and request editor is gone.
			if got := u32CountText(tt.Texts(), "OrdersFolder"); got != 1 {
				t.Fatalf("OrdersFolder appeared %d times (want 1 in top header only); texts=%v", got, tt.Texts())
			}
			if got := u32CountText(tt.Texts(), "GetOrderDetail"); got != 2 {
				// Exactly 1 in headerPath + 1 on the open tab pill (not a 3rd old breadcrumb).
				t.Fatalf("GetOrderDetail appeared %d times (want 2: headerPath + tab bar); texts=%v", got, tt.Texts())
			}

			// Horizontal order & centerline in header: Project -> RequestKind -> Folder -> Env.
			if kindRect.X < projRect.X+projRect.W-0.5 {
				t.Fatalf("request kind not after project button: proj=%+v kind=%+v", projRect, kindRect)
			}
			if folderRect.X < kindRect.X+kindRect.W-0.5 {
				t.Fatalf("folder crumb not after request kind: kind=%+v folder=%+v", kindRect, folderRect)
			}
			if folderRect.X+folderRect.W > envRect.X+0.5 {
				t.Fatalf("folder crumb intrudes into Env area: folder=%+v env=%+v", folderRect, envRect)
			}
			projMid := projRect.Y + projRect.H/2
			for name, r := range map[string]ui.Rect{"kind": kindRect, "folder": folderRect} {
				if math.Abs(float64((r.Y+r.H/2)-projMid)) > 2.0 {
					t.Fatalf("%s not on project button centerline: proj=%+v %s=%+v", name, projRect, name, r)
				}
			}

			// Clicking the folder crumb still selects the folder in projectState.
			if err := tt.Click("OrdersFolder"); err != nil {
				t.Fatalf("click folder crumb: %v", err)
			}
			if ps.sel != "folder:"+folder.RelPath {
				t.Fatalf("ps.sel = %q, want %q", ps.sel, "folder:"+folder.RelPath)
			}

			// 3. Active Document tab: Document › guide.md, not Request.
			docTab := openDocForTest(t, ps, "docs/guide.md", "# Guide\n")
			ps.activateTab(docTab)
			tt.Frame()
			docKindRect, ok := tt.Find(hm.DocKind)
			if !ok {
				t.Fatalf("doc kind %q missing for active document; texts=%v", hm.DocKind, tt.Texts())
			}
			if docKindRect.X < projRect.X+projRect.W-0.5 || docKindRect.X+docKindRect.W > envRect.X+0.5 || docKindRect.Y >= 46 {
				t.Fatalf("doc kind out of header position: proj=%+v docKind=%+v env=%+v", projRect, docKindRect, envRect)
			}
			if tt.HasText("OrdersFolder") {
				t.Fatal("request folder crumb leaked into document header path")
			}

			// 4. Narrow window truncation: deep folders + long request title must
			// clip cleanly between Project and Env without pushing Env or + out.
			sub, err := ps.createFolder(folder.RelPath, "DeepNestedSubfolderAlpha")
			if err != nil {
				t.Fatal(err)
			}
			longEntry, err := ps.createRequestIn(sub.RelPath, "ExtremelyLongRequestNameThatMustTruncateInNarrowHeader")
			if err != nil {
				t.Fatal(err)
			}
			ps.openRequest(longEntry)

			for _, narrowW := range []int{520, 440} {
				tt.SetSize(narrowW, 600)
				tt.Frame()
				pRect, okP := tt.Find("PathGeoProj")
				eRect, okE := tt.Find(m.EnvLabel)
				nRect, okN := tt.Find(m.NewRequest)
				if !okP || !okE || !okN {
					t.Fatalf("narrow %d: chrome control missing (proj=%v env=%v new=%v); texts=%v",
						narrowW, okP, okE, okN, tt.Texts())
				}
				if pRect.W < 40 {
					t.Fatalf("narrow %d: project button crushed: %+v", narrowW, pRect)
				}
				if nRect.X+nRect.W > float32(narrowW)+0.5 {
					t.Fatalf("narrow %d: + menu button pushed outside window: %+v", narrowW, nRect)
				}
				if eRect.X < pRect.X+pRect.W-0.5 {
					t.Fatalf("narrow %d: Env overlapped project button: proj=%+v env=%+v", narrowW, pRect, eRect)
				}
			}
		})
	}
}
