package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"weak"

	"github.com/egoist/mygo/ui"
	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

// Import source kind constants for the Import modal segmented switch.
const (
	ImportSourceCURL     = 0
	ImportSourcePostman  = 1
	ImportSourcePortable = 2
)

// Export kind constants for the Export modal segmented switch.
const (
	ExportKindRedactedCURL   = 0
	ExportKindPortableReq    = 1
	ExportKindPortableFolder = 2
	ExportKindPortableProj   = 3
	ExportKindPostmanCol     = 4
	ExportKindPostmanEnv     = 5
)

// InteropState holds the per-window state for the B08 Import and Export modals.
// It is bound via weak.Pointer[App] + runtime.AddCleanup (or directly on App
// after applying B08-integration.patch) so it never leaks strong App references.
type InteropState struct {
	// Import modal state
	ImportOpen         bool
	ImportSourceKind   int // ImportSourceCURL | ImportSourcePostman | ImportSourcePortable
	CURLInput          string
	ImportFilePath     string
	ImportTargetFolder string
	ImportTargetEnv    string
	ImportRequestName  string
	ConflictPolicyIdx  int // 0: fail, 1: rename, 2: overwrite, 3: skip
	StagedPlan         *core.ImportPlan
	LastReceipt        *core.ImportReceipt
	ImportErr          string
	ImportNotice       string

	// Export modal state
	ExportOpen         bool
	ExportKind         int // ExportKindRedactedCURL..ExportKindPostmanEnv
	ExportTargetFolder string
	ExportTargetReqRel string
	ExportTargetEnvID  string
	ExportOutputPath   string
	ExportOverwrite    bool
	ExportPreviewText  string
	ExportLosses       []core.LossItem
	ExportManifest     *core.PortableManifest
	ExportCURL         *core.CURLExportResult
	ExportErr          string
	ExportNotice       string
}

var (
	interopStateMu  sync.RWMutex
	interopByWindow = map[weak.Pointer[App]]*InteropState{}
)

func (a *App) peekInteropState() *InteropState {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	interopStateMu.RLock()
	st := interopByWindow[key]
	interopStateMu.RUnlock()
	return st
}

// Interop returns or lazily initializes the InteropState for this App window.
func (a *App) Interop() *InteropState {
	if a == nil {
		return &InteropState{}
	}
	key := weak.Make(a)
	interopStateMu.RLock()
	st := interopByWindow[key]
	interopStateMu.RUnlock()
	if st != nil {
		return st
	}

	interopStateMu.Lock()
	defer interopStateMu.Unlock()
	if st = interopByWindow[key]; st != nil {
		return st
	}
	st = &InteropState{}
	interopByWindow[key] = st
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		interopStateMu.Lock()
		delete(interopByWindow, k)
		interopStateMu.Unlock()
	}, key)
	return st
}

func conflictPolicyFromIdx(idx int) core.ConflictPolicy {
	switch idx {
	case 1:
		return core.ConflictRenameNewID
	case 2:
		return core.ConflictOverwrite
	case 3:
		return core.ConflictSkip
	default:
		return core.ConflictFail
	}
}

// OpenImportModal opens the B08 Import modal defaulting to the selected folder.
func (a *App) OpenImportModal(sourceKind int) {
	if a == nil || a.ps == nil {
		return
	}
	st := a.Interop()
	st.ImportSourceKind = sourceKind
	st.ImportTargetFolder = a.ps.selectedFolder()
	st.ImportTargetEnv = a.ps.currentEnv()
	st.ImportErr = ""
	st.ImportNotice = ""
	st.StagedPlan = nil
	st.ImportOpen = true
}

// OpenImportCURLModal opens the B08 Import modal in cURL mode.
func (a *App) OpenImportCURLModal() {
	a.OpenImportModal(ImportSourceCURL)
}

// OpenImportFileModal opens the B08 Import modal prefilled with a file path and
// immediately stages a preview if path is non-empty.
func (a *App) OpenImportFileModal(filePath string) {
	if a == nil || a.ps == nil {
		return
	}
	lower := strings.ToLower(strings.TrimSpace(filePath))
	kind := ImportSourcePostman
	if strings.HasSuffix(lower, ".zip") {
		kind = ImportSourcePortable
	}
	a.OpenImportModal(kind)
	st := a.Interop()
	st.ImportFilePath = strings.TrimSpace(filePath)
	if st.ImportFilePath != "" {
		a.StageImportPreview()
	}
}

// StageImportPreview runs the B08 staging pipeline without touching disk and
// populates StagedPlan for user inspection in the Import modal.
func (a *App) StageImportPreview() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.Interop()
	st.ImportErr = ""
	st.ImportNotice = ""
	st.StagedPlan = nil

	opts := core.ImportOptions{
		TargetFolderRel:    strings.TrimSpace(st.ImportTargetFolder),
		DefaultRequestName: strings.TrimSpace(st.ImportRequestName),
		TargetEnvID:        strings.TrimSpace(st.ImportTargetEnv),
		ConflictPolicy:     conflictPolicyFromIdx(st.ConflictPolicyIdx),
		ScrubSecretsToRefs: true,
		ScrubSecretsSet:    true,
	}

	if st.ImportSourceKind == ImportSourceCURL {
		raw := strings.TrimSpace(st.CURLInput)
		if raw == "" {
			st.ImportErr = "Please enter a cURL command to preview."
			return
		}
		plan, err := core.StageCURLImport(a.ps.project, raw, opts)
		if err != nil {
			st.ImportErr = err.Error()
			return
		}
		st.StagedPlan = plan
		return
	}

	filePath := strings.TrimSpace(st.ImportFilePath)
	if filePath == "" {
		st.ImportErr = "Please enter or browse to an import file (.json, .yaml, or .gorequest-bundle.zip)."
		return
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		st.ImportErr = fmt.Sprintf("Read import file: %v", err)
		return
	}
	plan, err := core.StageAutoImport(a.ps.project, data, filepath.Base(filePath), opts)
	if err != nil {
		st.ImportErr = err.Error()
		return
	}
	st.StagedPlan = plan
}

// ConfirmAndApplyImport executes the staged ImportPlan atomically, refreshes
// the workspace's open tabs and tree, and stores the ImportReceipt for Undo.
func (a *App) ConfirmAndApplyImport() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.Interop()
	if st.StagedPlan == nil {
		st.ImportErr = "Generate a preview before applying."
		return
	}
	receipt, err := st.StagedPlan.Apply()
	if err != nil {
		st.ImportErr = err.Error()
		return
	}
	importedIDs := st.StagedPlan.ImportedRequestIDs()
	st.LastReceipt = receipt
	st.StagedPlan = nil
	st.ImportErr = ""

	// Synchronize file sync engine if active so external-change watcher baselines stay current
	if peekFileSync(a.ps) != nil {
		a.SyncNow()
	}

	// Ensure environment selector stays valid if a new environment was imported
	if a.ps.envSel == "" || a.ps.envSel == "(none)" {
		if len(a.ps.project.EnvOrder) > 0 {
			a.ps.envSel = a.ps.project.EnvOrder[0]
		}
	}

	// Open the first imported request in a tab for immediate visibility
	if len(importedIDs) > 0 {
		firstID := importedIDs[0]
		if entry, ok := a.ps.project.Requests[firstID]; ok && entry != nil {
			a.ps.openRequest(entry)
			a.ps.sel = "req:" + firstID
			a.ps.expandChain(filepath.ToSlash(filepath.Dir(entry.RelPath)))
		}
	}

	im := a.interopMsg()
	changeCount := len(receipt.Preview().Changes)
	msg := fmt.Sprintf(im.ImportAppliedStatus, changeCount)
	st.ImportNotice = msg
	a.status = msg
}

// RollbackLastImport reverts the most recent import receipt and synchronizes
// the workspace tabs and collection tree.
func (a *App) RollbackLastImport() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.Interop()
	if st.LastReceipt == nil {
		st.ImportErr = "No applied import receipt available to roll back."
		return
	}
	if err := st.LastReceipt.Rollback(); err != nil {
		st.ImportErr = err.Error()
		return
	}
	st.LastReceipt = nil
	st.ImportErr = ""

	// Close any open request tabs whose request no longer exists after rollback
	var keptTabs []*Tab
	for _, tab := range a.ps.tabs {
		if tab.Kind == "request" && tab.Entry != nil && tab.Entry.Request != nil {
			if _, ok := a.ps.project.Requests[tab.Entry.Request.ID]; !ok {
				continue
			}
		}
		keptTabs = append(keptTabs, tab)
	}
	a.ps.tabs = keptTabs
	if a.ps.active >= len(a.ps.tabs) {
		a.ps.active = len(a.ps.tabs) - 1
	}

	if peekFileSync(a.ps) != nil {
		a.SyncNow()
	}

	im := a.interopMsg()
	st.ImportNotice = im.ImportRolledBack
	a.status = im.ImportRolledBack
}

// OpenExportModal opens the B08 Export modal for the given export kind and
// generates the initial redacted preview.
func (a *App) OpenExportModal(kind int) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.Interop()
	st.ExportKind = kind
	st.ExportErr = ""
	st.ExportNotice = ""
	st.ExportTargetEnvID = a.ps.currentEnv()

	if tab := a.ps.activeTab(); tab != nil && tab.Kind == "request" && tab.Entry != nil {
		st.ExportTargetReqRel = tab.Entry.RelPath
	} else if strings.HasPrefix(a.ps.sel, "req:") {
		reqID := strings.TrimPrefix(a.ps.sel, "req:")
		if e := a.ps.project.Requests[reqID]; e != nil {
			st.ExportTargetReqRel = e.RelPath
		}
	} else if len(a.ps.project.Order) > 0 {
		if e := a.ps.project.Requests[a.ps.project.Order[0]]; e != nil {
			st.ExportTargetReqRel = e.RelPath
		}
	}

	if selFolder := a.ps.selectedFolder(); selFolder != "" {
		st.ExportTargetFolder = "collections/" + selFolder
	} else if len(a.ps.project.Collections) > 0 {
		st.ExportTargetFolder = a.ps.project.Collections[0].RelPath
	} else {
		st.ExportTargetFolder = "collections"
	}

	st.ExportOpen = true
	a.RefreshExportPreview()
}

// OpenExportCURLForActiveRequest opens the Export modal on the Redacted cURL
// view for the currently active request tab.
func (a *App) OpenExportCURLForActiveRequest() {
	a.OpenExportModal(ExportKindRedactedCURL)
}

// OpenExportForRequest opens the Export modal targeting a specific request ID.
func (a *App) OpenExportForRequest(reqID string, kind int) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	a.OpenExportModal(kind)
	if e := a.ps.project.Requests[reqID]; e != nil {
		st := a.Interop()
		st.ExportTargetReqRel = e.RelPath
		a.RefreshExportPreview()
	}
}

// OpenExportForFolder opens the Export modal targeting a specific folder path.
func (a *App) OpenExportForFolder(folderRel string, kind int) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	a.OpenExportModal(kind)
	st := a.Interop()
	clean := strings.Trim(filepath.ToSlash(strings.TrimSpace(folderRel)), "/")
	if clean != "" && !strings.HasPrefix(clean, "collections/") && clean != "collections" {
		clean = "collections/" + clean
	}
	st.ExportTargetFolder = clean
	a.RefreshExportPreview()
}

// RefreshExportPreview computes the redacted preview for the selected export
// format and scope without writing to disk.
func (a *App) RefreshExportPreview() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.Interop()
	st.ExportErr = ""
	st.ExportNotice = ""
	st.ExportLosses = nil
	st.ExportManifest = nil
	st.ExportCURL = nil
	st.ExportPreviewText = ""

	p := a.ps.project

	switch st.ExportKind {
	case ExportKindRedactedCURL:
		entry := a.resolveExportRequestEntry(st.ExportTargetReqRel)
		if entry == nil {
			st.ExportErr = "Select or open an HTTP request to generate a redacted cURL command."
			return
		}
		res, err := core.ExportRedactedCURL(p, entry, st.ExportTargetEnvID, nil)
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportCURL = res
		st.ExportLosses = res.Notes
		st.ExportPreviewText = res.Command

	case ExportKindPortableReq:
		if strings.TrimSpace(st.ExportTargetReqRel) == "" {
			st.ExportErr = "Select a request to export as a Portable Bundle."
			return
		}
		_, manifest, losses, err := core.ExportPortableBundle(p, core.PortableExportOptions{
			Scope:         core.ScopeRequest,
			TargetRelPath: st.ExportTargetReqRel,
		})
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportManifest = manifest
		st.ExportLosses = losses
		mb, _ := yaml.Marshal(manifest)
		st.ExportPreviewText = string(mb)

	case ExportKindPortableFolder:
		_, manifest, losses, err := core.ExportPortableBundle(p, core.PortableExportOptions{
			Scope:         core.ScopeFolder,
			TargetRelPath: st.ExportTargetFolder,
		})
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportManifest = manifest
		st.ExportLosses = losses
		mb, _ := yaml.Marshal(manifest)
		st.ExportPreviewText = string(mb)

	case ExportKindPortableProj:
		_, manifest, losses, err := core.ExportPortableBundle(p, core.PortableExportOptions{
			Scope: core.ScopeProject,
		})
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportManifest = manifest
		st.ExportLosses = losses
		mb, _ := yaml.Marshal(manifest)
		st.ExportPreviewText = string(mb)

	case ExportKindPostmanCol:
		b, losses, err := core.ExportPostmanCollection(p, core.PostmanExportOptions{
			Scope: core.ScopeProject,
		})
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportLosses = losses
		st.ExportPreviewText = string(b)

	case ExportKindPostmanEnv:
		envID := strings.TrimSpace(st.ExportTargetEnvID)
		if envID == "" && len(p.EnvOrder) > 0 {
			envID = p.EnvOrder[0]
		}
		b, losses, err := core.ExportPostmanEnvironment(p, envID)
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportLosses = losses
		st.ExportPreviewText = string(b)
	}
}

func (a *App) resolveExportRequestEntry(relOrID string) *core.RequestEntry {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return nil
	}
	p := a.ps.project
	norm := filepath.ToSlash(strings.TrimSpace(relOrID))
	if norm != "" {
		if e, ok := p.Requests[norm]; ok && e != nil {
			return e
		}
		for _, id := range p.Order {
			if e := p.Requests[id]; e != nil && filepath.ToSlash(e.RelPath) == norm {
				return e
			}
		}
	}
	if tab := a.ps.activeTab(); tab != nil && tab.Kind == "request" && tab.Entry != nil {
		return tab.Entry
	}
	if len(p.Order) > 0 {
		return p.Requests[p.Order[0]]
	}
	return nil
}

// CopyExportPreviewToClipboard copies the current redacted export preview text
// to the system clipboard.
func (a *App) CopyExportPreviewToClipboard(c *ui.Context) {
	if a == nil {
		return
	}
	st := a.Interop()
	if st.ExportPreviewText == "" {
		a.RefreshExportPreview()
	}
	if st.ExportPreviewText == "" {
		return
	}
	if c != nil {
		c.WriteClipboard(st.ExportPreviewText)
	}
	st.ExportNotice = a.msg().Copied
	a.status = a.msg().Copied
}

// ExecuteExportToFile writes the selected export payload to ExportOutputPath,
// enforcing overwrite protection unless ExportOverwrite is enabled.
func (a *App) ExecuteExportToFile() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.Interop()
	st.ExportErr = ""
	st.ExportNotice = ""

	targetPath := strings.TrimSpace(st.ExportOutputPath)
	if targetPath == "" {
		st.ExportErr = "Please enter an export file path."
		return
	}
	if !st.ExportOverwrite {
		if _, err := os.Stat(targetPath); err == nil {
			st.ExportErr = fmt.Sprintf("Target file %q already exists (enable 'Allow Overwrite' to replace it).", targetPath)
			return
		}
	}

	p := a.ps.project
	switch st.ExportKind {
	case ExportKindRedactedCURL:
		a.RefreshExportPreview()
		if st.ExportErr != "" {
			return
		}
		if err := writeExportBytes(targetPath, []byte(st.ExportPreviewText+"\n")); err != nil {
			st.ExportErr = err.Error()
			return
		}
	case ExportKindPortableReq:
		_, losses, err := core.ExportPortableBundleToFile(p, targetPath, st.ExportOverwrite, core.PortableExportOptions{
			Scope:         core.ScopeRequest,
			TargetRelPath: st.ExportTargetReqRel,
		})
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportLosses = losses
	case ExportKindPortableFolder:
		_, losses, err := core.ExportPortableBundleToFile(p, targetPath, st.ExportOverwrite, core.PortableExportOptions{
			Scope:         core.ScopeFolder,
			TargetRelPath: st.ExportTargetFolder,
		})
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportLosses = losses
	case ExportKindPortableProj:
		_, losses, err := core.ExportPortableBundleToFile(p, targetPath, st.ExportOverwrite, core.PortableExportOptions{
			Scope: core.ScopeProject,
		})
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportLosses = losses
	case ExportKindPostmanCol:
		data, losses, err := core.ExportPostmanCollection(p, core.PostmanExportOptions{
			Scope: core.ScopeProject,
		})
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportLosses = losses
		if err := writeExportBytes(targetPath, data); err != nil {
			st.ExportErr = err.Error()
			return
		}
	case ExportKindPostmanEnv:
		envID := strings.TrimSpace(st.ExportTargetEnvID)
		if envID == "" && len(p.EnvOrder) > 0 {
			envID = p.EnvOrder[0]
		}
		data, losses, err := core.ExportPostmanEnvironment(p, envID)
		if err != nil {
			st.ExportErr = err.Error()
			return
		}
		st.ExportLosses = losses
		if err := writeExportBytes(targetPath, data); err != nil {
			st.ExportErr = err.Error()
			return
		}
	}

	im := a.interopMsg()
	msg := fmt.Sprintf(im.ExportSavedStatus, filepath.Base(targetPath))
	st.ExportNotice = msg
	a.status = msg
}

func writeExportBytes(targetPath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(targetPath, data, 0o644)
}

func _keepSlicesRef() {
	_ = slices.Clone[[]string]
}
