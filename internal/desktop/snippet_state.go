package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"weak"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/snippet"
)

// SnippetState holds the per-window state for the C04 request-snippet modal.
// It is bound via weak.Pointer[App] + runtime.AddCleanup (the established
// in-module pattern) so no global map keeps a window alive after close.
type SnippetState struct {
	Open        bool
	TargetRel   string
	EnvID       string
	LanguageIdx int
	// Preview fields for the current language
	Result *snippet.Result
	Err    string
	Notice string

	// Export-to-file fields (explicit export; never overwrites by default).
	OutputPath string
	Overwrite  bool
}

var (
	snippetStateMu  sync.RWMutex
	snippetByWindow = map[weak.Pointer[App]]*SnippetState{}
)

func (a *App) peekSnippetState() *SnippetState {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	snippetStateMu.RLock()
	st := snippetByWindow[key]
	snippetStateMu.RUnlock()
	return st
}

// Snippet returns or lazily initializes the SnippetState for this window.
func (a *App) Snippet() *SnippetState {
	if a == nil {
		return &SnippetState{}
	}
	key := weak.Make(a)
	snippetStateMu.RLock()
	st := snippetByWindow[key]
	snippetStateMu.RUnlock()
	if st != nil {
		return st
	}

	snippetStateMu.Lock()
	defer snippetStateMu.Unlock()
	if st = snippetByWindow[key]; st != nil {
		return st
	}
	st = &SnippetState{}
	snippetByWindow[key] = st
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		snippetStateMu.Lock()
		delete(snippetByWindow, k)
		snippetStateMu.Unlock()
	}, key)
	return st
}

// snippetLanguage returns the language selected by the modal index.
func snippetLanguage(idx int) snippet.Language {
	langs := snippet.Languages()
	if idx < 0 || idx >= len(langs) {
		return langs[0]
	}
	return langs[idx]
}

// snippetEntryFor resolves a request entry by rel path (or id).
func (a *App) snippetEntryFor(rel string) *core.RequestEntry {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return nil
	}
	clean := strings.TrimSpace(rel)
	if clean == "" {
		return nil
	}
	for _, e := range a.ps.project.Requests {
		if e != nil && e.RelPath == clean {
			return e
		}
	}
	return nil
}

// OpenSnippetModal opens the snippet modal on the active/selected request,
// defaulting to the current environment snapshot and cURL.
func (a *App) OpenSnippetModal() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.Snippet()
	st.Err = ""
	st.Notice = ""
	st.EnvID = a.ps.currentEnv()

	st.TargetRel = ""
	if tab := a.ps.activeTab(); tab != nil && tab.Kind == "request" && tab.Entry != nil {
		st.TargetRel = tab.Entry.RelPath
	} else if strings.HasPrefix(a.ps.sel, "req:") {
		reqID := strings.TrimPrefix(a.ps.sel, "req:")
		if e := a.ps.project.Requests[reqID]; e != nil {
			st.TargetRel = e.RelPath
		}
	} else if len(a.ps.project.Order) > 0 {
		if e := a.ps.project.Requests[a.ps.project.Order[0]]; e != nil {
			st.TargetRel = e.RelPath
		}
	}

	st.Open = true
	a.RefreshSnippetPreview()
}

// RefreshSnippetPreview regenerates the snippet for the selected language.
// Copying or exporting never writes anywhere without this step.
func (a *App) RefreshSnippetPreview() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.Snippet()
	st.Err = ""
	entry := a.snippetEntryFor(st.TargetRel)
	if entry == nil {
		st.Result = nil
		st.Err = "Select or open an HTTP request to generate a snippet."
		return
	}
	res, err := snippet.Generate(a.ps.project, entry, snippetLanguage(st.LanguageIdx), snippet.Options{EnvID: st.EnvID})
	if err != nil {
		st.Result = nil
		st.Err = err.Error()
		return
	}
	st.Result = res
}

// SelectSnippetLanguage switches the preview language and refreshes.
func (a *App) SelectSnippetLanguage(idx int) {
	st := a.Snippet()
	st.LanguageIdx = idx
	a.RefreshSnippetPreview()
}

// CopySnippetToClipboard copies the reviewed snippet text. It never executes
// anything; clipboard only.
func (a *App) CopySnippetToClipboard(c *ui.Context) {
	st := a.peekSnippetState()
	if st == nil || st.Result == nil || st.Result.Code == "" {
		if a != nil {
			a.status = a.snippetMsg().CopyNothingStatus
		}
		return
	}
	if c == nil {
		return
	}
	c.WriteClipboard(st.Result.Code)
	if a != nil {
		a.status = a.snippetMsg().CopiedStatus
		a.notifyCopied(st.Result.Code)
	}
}

// ExecuteSnippetExportToFile writes the current snippet to the explicit user
// path. Existing files are never overwritten unless the checkbox is on, and
// the write happens only after a fresh preview exists.
func (a *App) ExecuteSnippetExportToFile() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.Snippet()
	st.Err = ""
	st.Notice = ""
	if st.Result == nil || st.Result.Code == "" || st.Result.Blocked != "" {
		st.Err = a.snippetMsg().NeedRefreshStatus
		return
	}
	targetPath := strings.TrimSpace(st.OutputPath)
	if targetPath == "" {
		st.Err = "Enter an export file path."
		return
	}
	if !st.Overwrite {
		if _, err := os.Stat(targetPath); err == nil {
			st.Err = fmt.Sprintf("Target file %q already exists (enable 'Allow Overwrite' to replace it).", targetPath)
			return
		}
	}
	if err := writeSnippetBytes(targetPath, []byte(st.Result.Code+"\n")); err != nil {
		st.Err = err.Error()
		return
	}
	st.Notice = fmt.Sprintf(a.snippetMsg().SnippetExportSaved, targetPath)
	a.status = st.Notice
	a.notifySuccess(a.feedbackMsg().ExportOK)
}

func writeSnippetBytes(targetPath string, data []byte) error {
	if dir := filepath.Dir(targetPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(targetPath, data, 0o644)
}
