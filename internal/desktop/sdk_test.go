package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/sdkgen"
)

// waitSDKPreview joins the background preview goroutine started by
// RefreshSDKPreview. The fake test window runs Update synchronously, so once
// the channel closes the state fields are visible to the test goroutine.
func waitSDKPreview(t *testing.T, st *SDKState) {
	t.Helper()
	done := st.previewDone
	if done == nil {
		t.Fatalf("preview did not start: %s", st.Err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("preview timed out")
	}
}

func sdkTestRequest(t *testing.T, ps *projectState) *Tab {
	t.Helper()
	tab := openRequestTabs(t, ps, "SDKReq")[0]
	tab.Entry.Request.HTTP.Method = "GET"
	tab.Entry.Request.HTTP.URL = "/pets/{petId}"
	return tab
}

func TestSDKModalPreviewApplyCancel(t *testing.T) {
	a, ps := openTestProject(t, "SDKDemo")
	sdkTestRequest(t, ps)

	a.OpenSDKModal()
	st := a.SDK()
	if !st.Open {
		t.Fatal("SDK modal did not open")
	}
	if st.Plan != nil {
		t.Fatal("opening the modal must not generate a preview")
	}
	if st.PackageName == "" || st.ModulePath == "" {
		t.Fatalf("defaults missing: package=%q module=%q", st.PackageName, st.ModulePath)
	}

	st.TargetDir = filepath.Join(t.TempDir(), "sdk")
	a.RefreshSDKPreview()
	waitSDKPreview(t, st)
	if st.Err != "" {
		t.Fatalf("preview failed: %s", st.Err)
	}
	if st.Plan == nil || st.Plan.Result == nil {
		t.Fatal("preview produced no plan")
	}
	if len(st.Plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", st.Plan.Conflicts)
	}
	if len(st.Plan.Result.Operations) == 0 {
		t.Fatal("preview produced no operations")
	}
	for _, action := range st.Plan.Actions {
		if action.Kind != sdkgen.ActionCreate {
			t.Fatalf("first preview should create files, got %+v", action)
		}
	}

	a.ApplySDK()
	if st.Err != "" {
		t.Fatalf("apply failed: %s", st.Err)
	}
	if st.Notice == "" {
		t.Fatal("apply produced no success notice")
	}
	for _, name := range []string{"client.go", "operations.go", "README.md", "go.mod", sdkgen.ManifestName} {
		if _, err := os.Stat(filepath.Join(st.TargetDir, name)); err != nil {
			t.Fatalf("expected %s on disk: %v", name, err)
		}
	}

	// Re-preview: the generated set is unchanged.
	a.RefreshSDKPreview()
	waitSDKPreview(t, st)
	if st.Err != "" {
		t.Fatalf("second preview failed: %s", st.Err)
	}
	for _, action := range st.Plan.Actions {
		if action.Kind != sdkgen.ActionUnchanged {
			t.Fatalf("re-generation should be unchanged, got %+v", action)
		}
	}

	a.CloseSDKModal()
	if st.Open {
		t.Fatal("modal did not close")
	}
}

func TestSDKModalUnsafeTargetShowsError(t *testing.T) {
	a, ps := openTestProject(t, "SDKDemo")
	sdkTestRequest(t, ps)

	a.OpenSDKModal()
	st := a.SDK()
	st.TargetDir = filepath.Join(ps.root, "generated-sdk")
	a.RefreshSDKPreview()
	waitSDKPreview(t, st)
	if st.Err == "" || !strings.Contains(st.Err, "inside the project") {
		t.Fatalf("expected an inside-project error, got %q", st.Err)
	}
	if st.Plan != nil {
		t.Fatal("an unsafe target must not produce a plan")
	}
}

func TestSDKModalTwoWindowsIsolated(t *testing.T) {
	m, _ := newTestManager(t)
	dir1 := writeProject(t, "SDKOne")
	dir2 := writeProject(t, "SDKTwo")
	if err := m.openProject(dir1); err != nil {
		t.Fatalf("openProject one: %v", err)
	}
	if err := m.openProject(dir2); err != nil {
		t.Fatalf("openProject two: %v", err)
	}
	a1, a2 := m.workspaces[0], m.workspaces[1]
	a1.OpenSDKModal()
	a2.OpenSDKModal()
	if a1.SDK() == a2.SDK() {
		t.Fatal("two windows must not share SDK state")
	}
	a1.SDK().PackageName = "one"
	a2.SDK().PackageName = "two"
	if a1.SDK().PackageName != "one" || a2.SDK().PackageName != "two" {
		t.Fatal("SDK state leaked between windows")
	}
	a1.CloseSDKModal()
	if !a2.SDK().Open {
		t.Fatal("closing one window's modal must not close the other")
	}
}

func TestSDKModalRendersHeadless(t *testing.T) {
	a, ps := openTestProject(t, "SDKDemo")
	sdkTestRequest(t, ps)

	a.OpenSDKModal()
	st := a.SDK()
	st.TargetDir = filepath.Join(t.TempDir(), "sdk")
	a.RefreshSDKPreview()
	waitSDKPreview(t, st)
	if st.Err != "" {
		t.Fatalf("preview failed: %s", st.Err)
	}

	tt := ui.NewTester(a.SDKView, 1200, 820)
	tt.Frame()
	if _, ok := tt.Find(a.sdkMsg().ModalTitle); !ok {
		t.Fatalf("SDK modal title not rendered; texts=%v", tt.Texts())
	}
	if _, ok := tt.Find(a.sdkMsg().ApplyAction); !ok {
		t.Fatalf("apply action not rendered; texts=%v", tt.Texts())
	}
}
