package desktop

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"weak"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/sdkgen"
)

// SDKState holds the per-window state of the C04-phase2 Go SDK generator modal.
// It is bound via weak.Pointer[App] + runtime.AddCleanup so no global map keeps
// a window alive after close.
type SDKState struct {
	Open bool

	// Inputs.
	SpecVersionIdx     int // 0: OpenAPI 3.1.2, 1: OpenAPI 3.2.1
	PackageName        string
	ModulePath         string
	TargetDir          string
	IncludeUnsupported bool
	AllowOverwrite     bool
	AllowDeleteStale   bool

	// Preview / feedback.
	Plan   *sdkgen.Plan
	Err    string
	Notice string
	Busy   bool

	generation uint64
	// previewDone is closed when the in-flight preview completes. Tests use it
	// to join the background goroutine; production ignores it.
	previewDone chan struct{}
}

var (
	sdkStateMu  sync.RWMutex
	sdkByWindow = map[weak.Pointer[App]]*SDKState{}
)

func (a *App) peekSDKState() *SDKState {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	sdkStateMu.RLock()
	st := sdkByWindow[key]
	sdkStateMu.RUnlock()
	return st
}

// SDK returns or lazily initializes the SDKState for this window.
func (a *App) SDK() *SDKState {
	if a == nil {
		return &SDKState{}
	}
	key := weak.Make(a)
	sdkStateMu.RLock()
	st := sdkByWindow[key]
	sdkStateMu.RUnlock()
	if st != nil {
		return st
	}
	sdkStateMu.Lock()
	defer sdkStateMu.Unlock()
	if st = sdkByWindow[key]; st != nil {
		return st
	}
	st = &SDKState{}
	sdkByWindow[key] = st
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		sdkStateMu.Lock()
		delete(sdkByWindow, k)
		sdkStateMu.Unlock()
	}, key)
	return st
}

// OpenSDKModal opens the generator modal with sensible defaults. Opening the
// modal never generates, writes, or touches the network.
func (a *App) OpenSDKModal() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.SDK()
	st.Open = true
	st.Err = ""
	st.Notice = ""
	st.Plan = nil
	st.Busy = false
	if strings.TrimSpace(st.PackageName) == "" {
		st.PackageName = defaultSDKPackageName(a.ps.project.File.Name)
	}
	if strings.TrimSpace(st.ModulePath) == "" {
		st.ModulePath = "example.com/" + st.PackageName
	}
}

// CloseSDKModal dismisses the modal and invalidates any in-flight preview.
func (a *App) CloseSDKModal() {
	st := a.peekSDKState()
	if st == nil {
		return
	}
	st.Open = false
	st.generation++
	st.Busy = false
}

// sdkSnapshot exports the current project contract as a strict JSON snapshot.
// It runs on the UI thread so it reads a consistent project state; the pure
// generator then runs off-thread.
func (a *App) sdkSnapshot(st *SDKState) ([]byte, error) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return nil, fmt.Errorf("no project is open")
	}
	version := core.OpenAPIVersion31
	if st.SpecVersionIdx == 1 {
		version = core.OpenAPIVersion32
	}
	res, err := core.ExportOpenAPI(a.ps.project, core.OpenAPIExportOptions{
		TargetVersion:   version,
		Format:          "json",
		Strict:          true,
		AllowLossy:      false,
		IncludeExamples: false,
		EnvID:           a.ps.currentEnv(),
	})
	if err != nil {
		return nil, err
	}
	return res.Bytes, nil
}

func (a *App) sdkOptions(st *SDKState) sdkgen.Options {
	root := ""
	if a != nil && a.ps != nil {
		root = a.ps.root
	}
	return sdkgen.Options{
		PackageName:        strings.TrimSpace(st.PackageName),
		ModulePath:         strings.TrimSpace(st.ModulePath),
		TargetDir:          strings.TrimSpace(st.TargetDir),
		ProjectRoot:        root,
		IncludeUnsupported: st.IncludeUnsupported,
		AllowOverwrite:     st.AllowOverwrite,
		AllowDeleteStale:   st.AllowDeleteStale,
	}
}

// RefreshSDKPreview exports the contract and runs sdkgen.Preview off the UI
// thread. The result is delivered back to the owning window and dropped when a
// newer generation or a closed modal has superseded it.
func (a *App) RefreshSDKPreview() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.SDK()
	st.Err = ""
	st.Notice = ""
	st.Plan = nil
	if strings.TrimSpace(st.TargetDir) == "" {
		st.Err = a.sdkMsg().TargetEmpty
		return
	}
	snapshot, err := a.sdkSnapshot(st)
	if err != nil {
		st.Err = err.Error()
		return
	}
	opts := a.sdkOptions(st)
	st.generation++
	gen := st.generation
	st.Busy = true
	done := make(chan struct{})
	st.previewDone = done
	go func() {
		defer close(done)
		plan, perr := sdkgen.Preview(snapshot, opts)
		if a.win == nil || a.win.IsDestroyed() {
			return
		}
		a.win.Update(func() {
			if st.generation != gen || !st.Open {
				return
			}
			st.Busy = false
			if perr != nil {
				st.Plan = nil
				st.Err = perr.Error()
				return
			}
			st.Plan = plan
		})
	}()
}

// ApplySDK writes the previewed plan. It never writes when the plan is missing
// or has conflicts, and it never executes the generated code.
func (a *App) ApplySDK() {
	if a == nil {
		return
	}
	st := a.SDK()
	st.Err = ""
	st.Notice = ""
	if st.Plan == nil {
		st.Err = a.sdkMsg().NeedPreview
		return
	}
	if len(st.Plan.Conflicts) > 0 {
		st.Err = a.sdkMsg().ConflictsBlock
		return
	}
	res, err := sdkgen.Apply(st.Plan)
	if err != nil {
		st.Err = err.Error()
		return
	}
	st.Plan = nil
	st.Notice = fmt.Sprintf(a.sdkMsg().AppliedFmt, len(res.Created)+len(res.Updated), len(res.Deleted))
	a.status = st.Notice
	a.notifySuccess(a.feedbackMsg().ExportOK)
}

// PickSDKTargetDir opens the native directory picker off the UI thread and
// lands the choice on the modal state.
func (a *App) PickSDKTargetDir() {
	if a == nil {
		return
	}
	st := a.SDK()
	var pw *mygo.Window
	if nw, ok := a.win.(nativeWindower); ok {
		pw = nw.nativeWindow()
	}
	if pw == nil {
		st.Err = a.docMsg().DialogUnavailable
		return
	}
	sm := a.sdkMsg()
	go func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: pw, Title: sm.TargetLabel, ButtonLabel: sm.Browse,
			Directory: true, CreateDirectories: true,
		})
		if err != nil {
			if a.win != nil && !a.win.IsDestroyed() {
				a.win.Update(func() { st.Err = err.Error() })
			}
			return
		}
		if len(dirs) == 0 {
			return
		}
		if a.win != nil && !a.win.IsDestroyed() {
			a.win.Update(func() {
				st.TargetDir = dirs[0]
				st.Plan = nil
			})
		}
	}()
}

// defaultSDKPackageName derives a conservative Go package name from the
// project name; the user can edit it before previewing.
func defaultSDKPackageName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	pkg := b.String()
	if pkg == "" {
		return "api"
	}
	if pkg[0] >= '0' && pkg[0] <= '9' {
		pkg = "api" + pkg
	}
	if pkg == "main" || pkg == "type" || pkg == "func" || pkg == "var" || pkg == "const" || pkg == "package" || pkg == "map" || pkg == "struct" || pkg == "interface" || pkg == "go" || pkg == "select" || pkg == "range" || pkg == "return" || pkg == "if" || pkg == "else" || pkg == "for" || pkg == "switch" || pkg == "case" || pkg == "default" || pkg == "import" {
		pkg = "api" + pkg
	}
	return pkg
}

// sdkView wraps a.View and renders the modal afterwards so headless UI tests
// exercise the full flow while shared files stay on the integration baseline.
func (a *App) SDKView(c *ui.Context) {
	a.View(c)
	a.SDKModals(c, c.Theme())
}
