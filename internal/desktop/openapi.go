package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"weak"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// Tab indices for the OpenAPI & Schema Studio modal.
const (
	OpenAPITabImportSync        = 0
	OpenAPITabExportCoverage    = 1
	OpenAPITabContractValidator = 2
)

// OpenAPIState holds per-window state for the A03 OpenAPI 3.1/3.2 & Schema
// Studio modal and active request contract editor. Bound via weak.Pointer[App]
// + runtime.AddCleanup so it never retains strong App references.
type OpenAPIState struct {
	ModalOpen bool
	ActiveTab int // OpenAPITabImportSync | OpenAPITabExportCoverage | OpenAPITabContractValidator

	// Tab 0: Import / Migrate / 3-Way Sync
	SpecFilePath      string
	SpecInlineInput   string
	SourceModeIdx     int  // 0: native, 1: external-spec
	SyncActionIdx     int  // 0: Import/Migrate, 1: 3-Way Sync Preview
	StrictLossReject  bool // reject import if any lossy/unsupported feature is present
	GroupByTag        bool
	ConflictPolicyIdx int // 0: fail, 1: rename, 2: overwrite, 3: skip
	TargetFolderRel   string
	TargetEnvID       string
	ValidationReport  *core.OpenAPIValidationReport
	SyncPreview       *core.OpenAPISyncPreview
	StagedPlan        *core.ImportPlan
	LastReceipt       *core.ImportReceipt
	ImportErr         string
	ImportNotice      string

	// Tab 1: Export 3.1/3.2 + Compatibility + Coverage
	ExportVersionIdx  int  // 0: 3.1.2, 1: 3.2.1
	ExportFormatIdx   int  // 0: yaml, 1: json
	ExportStrict      bool // default true
	ExportAllowLossy  bool
	ExportIncludeExs  bool
	ExportOutputPath  string
	ExportOverwrite   bool
	ExportResult      *core.OpenAPIExportResult
	CoverageReport    *core.OpenAPICoverageReport
	ExportPreviewText string
	ExportErr         string
	ExportNotice      string

	// Tab 2: Request Operation Contract & Response Schema Validator
	LoadedReqID        string
	ContractOpID       string
	ContractSummary    string
	ContractDesc       string
	ContractPathTmpl   string
	ContractDeprecated bool
	DraftStatusKey     string
	DraftMediaType     string
	DraftSchemaJSON    string
	WorkingContract    *core.OperationContract
	ContractErr        string
	ContractNotice     string

	TestStatusCode   string
	TestContentType  string
	TestBodyJSON     string
	ValidationResult *core.SchemaValidationResult
}

var (
	openAPIStateMu  sync.RWMutex
	openAPIByWindow = map[weak.Pointer[App]]*OpenAPIState{}
)

func (a *App) peekOpenAPIState() *OpenAPIState {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	openAPIStateMu.RLock()
	st := openAPIByWindow[key]
	openAPIStateMu.RUnlock()
	return st
}

// OpenAPI returns or lazily initializes the per-window OpenAPIState.
func (a *App) OpenAPI() *OpenAPIState {
	if a == nil {
		return &OpenAPIState{ExportStrict: true, ExportIncludeExs: true, GroupByTag: true}
	}
	key := weak.Make(a)
	openAPIStateMu.RLock()
	st := openAPIByWindow[key]
	openAPIStateMu.RUnlock()
	if st != nil {
		return st
	}

	openAPIStateMu.Lock()
	defer openAPIStateMu.Unlock()
	if st = openAPIByWindow[key]; st != nil {
		return st
	}
	st = &OpenAPIState{
		GroupByTag:       true,
		ExportStrict:     true,
		ExportIncludeExs: true,
		DraftStatusKey:   "200",
		DraftMediaType:   "application/json",
		TestStatusCode:   "200",
		TestContentType:  "application/json",
	}
	openAPIByWindow[key] = st
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		openAPIStateMu.Lock()
		delete(openAPIByWindow, k)
		openAPIStateMu.Unlock()
	}, key)
	return st
}

// OpenOpenAPIModal opens the OpenAPI & Schema Studio modal on the specified tab.
func (a *App) OpenOpenAPIModal(tab int) {
	if a == nil || a.ps == nil {
		return
	}
	st := a.OpenAPI()
	st.ActiveTab = tab
	st.ModalOpen = true
	if st.TargetFolderRel == "" {
		st.TargetFolderRel = a.ps.selectedFolder()
	}
	if st.TargetEnvID == "" {
		st.TargetEnvID = a.ps.currentEnv()
	}
	switch tab {
	case OpenAPITabExportCoverage:
		a.RefreshOpenAPIExportAndCoverage()
	case OpenAPITabContractValidator:
		a.LoadActiveRequestContract()
	}
}

// OpenOpenAPIImportFile opens the OpenAPI modal on the Import/Sync tab prefilled
// with filePath and immediately stages a preview.
func (a *App) OpenOpenAPIImportFile(filePath string) {
	if a == nil || a.ps == nil {
		return
	}
	a.OpenOpenAPIModal(OpenAPITabImportSync)
	st := a.OpenAPI()
	st.SpecFilePath = strings.TrimSpace(filePath)
	if st.SpecFilePath != "" {
		a.StageOpenAPIPreview()
	}
}

func (st *OpenAPIState) sourceMode() core.SourceMode {
	if st.SourceModeIdx == 1 {
		return core.SourceModeExternalSpec
	}
	return core.SourceModeNative
}

func (st *OpenAPIState) exportTargetVersion() string {
	if st.ExportVersionIdx == 1 {
		return core.OpenAPIVersion32
	}
	return core.OpenAPIVersion31
}

func (st *OpenAPIState) exportFormat() string {
	if st.ExportFormatIdx == 1 {
		return "json"
	}
	return "yaml"
}

// StageOpenAPIPreview validates the input OpenAPI 3.1/3.2/3.0 or Swagger 2.0
// specification and stages either a fresh import/migration plan or a 3-way
// external-spec sync preview without mutating disk.
func (a *App) StageOpenAPIPreview() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.OpenAPI()
	st.ImportErr = ""
	st.ImportNotice = ""
	st.ValidationReport = nil
	st.SyncPreview = nil
	st.StagedPlan = nil

	var rawBytes []byte
	var baseDir string
	var filename string

	filePath := strings.TrimSpace(st.SpecFilePath)
	inlineText := strings.TrimSpace(st.SpecInlineInput)
	switch {
	case filePath != "":
		b, err := os.ReadFile(filePath)
		if err != nil {
			st.ImportErr = fmt.Sprintf("Read specification file: %v", err)
			return
		}
		rawBytes = b
		baseDir = filepath.Dir(filePath)
		filename = filepath.Base(filePath)
	case inlineText != "":
		rawBytes = []byte(inlineText)
		filename = "inline-openapi.yaml"
	default:
		st.ImportErr = "Please enter an OpenAPI/Swagger file path or paste specification content."
		return
	}

	valRep, err := core.ValidateOpenAPISpec(rawBytes, core.OpenAPIValidationOptions{
		BaseDir:        baseDir,
		CurrentFileRel: filename,
	})
	if err != nil {
		st.ImportErr = err.Error()
		return
	}
	st.ValidationReport = valRep

	oaOpts := core.OpenAPIImportOptions{
		BaseDir:          baseDir,
		SourceFileRel:    filename,
		SourceMode:       st.sourceMode(),
		StrictLossReject: st.StrictLossReject,
		GroupByTag:       st.GroupByTag,
		InteropOptions: core.ImportOptions{
			TargetFolderRel:    strings.TrimSpace(st.TargetFolderRel),
			TargetEnvID:        strings.TrimSpace(st.TargetEnvID),
			ConflictPolicy:     conflictPolicyFromIdx(st.ConflictPolicyIdx),
			ScrubSecretsToRefs: true,
			ScrubSecretsSet:    true,
		},
	}

	if st.SyncActionIdx == 1 {
		plan, syncPrev, err := core.StageOpenAPISync(a.ps.project, rawBytes, filename, oaOpts)
		if err != nil {
			st.ImportErr = err.Error()
			return
		}
		st.StagedPlan = plan
		st.SyncPreview = syncPrev
		return
	}

	plan, err := core.StageOpenAPIImport(a.ps.project, rawBytes, filename, oaOpts)
	if err != nil {
		st.ImportErr = err.Error()
		return
	}
	st.StagedPlan = plan
}

// ConfirmAndApplyOpenAPI executes the staged OpenAPI ImportPlan atomically after
// checking for dirty buffers, updates open workspace tabs, and records LastReceipt.
func (a *App) ConfirmAndApplyOpenAPI() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.OpenAPI()
	if st.StagedPlan == nil {
		st.ImportErr = "Stage a preview before applying."
		return
	}
	if err := a.checkImportBuffers(st.StagedPlan.Preview()); err != nil {
		st.ImportErr = err.Error()
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

	if peekFileSync(a.ps) != nil {
		a.SyncNow()
	}

	if a.ps.envSel == "" || a.ps.envSel == "(none)" {
		if len(a.ps.project.EnvOrder) > 0 {
			a.ps.envSel = a.ps.project.EnvOrder[0]
		}
	}

	// Refresh any existing open request tabs whose underlying files were updated
	for _, tab := range a.ps.tabs {
		if tab.Kind == "request" && tab.Entry != nil && tab.Entry.Request != nil && !tab.Dirty && !tab.Sending {
			if updatedEntry, ok := a.ps.project.Requests[tab.Entry.Request.ID]; ok && updatedEntry != nil {
				tab.Entry = updatedEntry
				tab.Baseline = updatedEntry.BaselineDigest
				tab.Title = updatedEntry.Request.Name
				tab.Method = updatedEntry.Request.HTTP.Method
				tab.URL = updatedEntry.Request.HTTP.URL
			}
		}
	}

	if len(importedIDs) > 0 {
		firstID := importedIDs[0]
		if entry, ok := a.ps.project.Requests[firstID]; ok && entry != nil {
			a.ps.openRequest(entry)
			a.ps.sel = "req:" + firstID
			a.ps.expandChain(filepath.ToSlash(filepath.Dir(entry.RelPath)))
		}
	}

	om := a.openAPIMsg()
	msg := fmt.Sprintf(om.AppliedStatusFmt, len(receipt.Preview().Changes))
	st.ImportNotice = msg
	a.status = msg
}

// RollbackLastOpenAPI reverts the most recent OpenAPI import/sync receipt.
func (a *App) RollbackLastOpenAPI() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.OpenAPI()
	if st.LastReceipt == nil {
		st.ImportErr = "No applied OpenAPI receipt available to roll back."
		return
	}
	if err := a.checkImportBuffers(st.LastReceipt.Preview()); err != nil {
		st.ImportErr = err.Error()
		return
	}
	if err := st.LastReceipt.Rollback(); err != nil {
		st.ImportErr = err.Error()
		return
	}
	st.LastReceipt = nil
	st.ImportErr = ""

	for _, tab := range append([]*Tab(nil), a.ps.tabs...) {
		if tab.Kind == "request" && tab.Entry != nil && tab.Entry.Request != nil {
			if _, ok := a.ps.project.Requests[tab.Entry.Request.ID]; !ok && !tab.Dirty && !tab.Sending {
				a.ps.removeTab(tab)
			}
		}
	}

	if peekFileSync(a.ps) != nil {
		a.SyncNow()
	}

	om := a.openAPIMsg()
	st.ImportNotice = om.RolledBackStatus
	a.status = om.RolledBackStatus
}

// RefreshOpenAPIExportAndCoverage computes the OpenAPI 3.1.2 / 3.2.1 export
// preview, version compatibility report, and documentation coverage report.
func (a *App) RefreshOpenAPIExportAndCoverage() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.OpenAPI()
	st.ExportErr = ""
	st.ExportNotice = ""
	st.ExportResult = nil
	st.ExportPreviewText = ""

	cov, err := core.ComputeOpenAPICoverage(a.ps.project)
	if err != nil {
		st.ExportErr = err.Error()
		return
	}
	st.CoverageReport = cov

	res, err := core.ExportOpenAPI(a.ps.project, core.OpenAPIExportOptions{
		TargetVersion:   st.exportTargetVersion(),
		Format:          st.exportFormat(),
		Strict:          st.ExportStrict,
		AllowLossy:      st.ExportAllowLossy,
		IncludeExamples: st.ExportIncludeExs,
		EnvID:           a.ps.currentEnv(),
	})
	st.ExportResult = res
	if err != nil {
		st.ExportErr = err.Error()
		return
	}
	st.ExportPreviewText = string(res.Bytes)
}

// CopyOpenAPIExportToClipboard copies the serialized OpenAPI specification to
// the system clipboard.
func (a *App) CopyOpenAPIExportToClipboard(c *ui.Context) {
	if a == nil {
		return
	}
	st := a.OpenAPI()
	if st.ExportPreviewText == "" {
		a.RefreshOpenAPIExportAndCoverage()
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

// ExecuteOpenAPIExportToFile writes the OpenAPI 3.1.2 / 3.2.1 specification to
// disk with atomic write and overwrite protection.
func (a *App) ExecuteOpenAPIExportToFile() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.OpenAPI()
	st.ExportErr = ""
	st.ExportNotice = ""

	targetPath := strings.TrimSpace(st.ExportOutputPath)
	if targetPath == "" {
		st.ExportErr = "Please enter an export file path."
		return
	}

	res, err := core.ExportOpenAPIToFile(a.ps.project, targetPath, st.ExportOverwrite, core.OpenAPIExportOptions{
		TargetVersion:   st.exportTargetVersion(),
		Format:          st.exportFormat(),
		Strict:          st.ExportStrict,
		AllowLossy:      st.ExportAllowLossy,
		IncludeExamples: st.ExportIncludeExs,
		EnvID:           a.ps.currentEnv(),
	})
	st.ExportResult = res
	if err != nil {
		st.ExportErr = err.Error()
		return
	}
	st.ExportPreviewText = string(res.Bytes)
	msg := fmt.Sprintf(a.openAPIMsg().ExportSavedFmt, filepath.Base(targetPath))
	st.ExportNotice = msg
	a.status = msg
}

func (a *App) activeHTTPRequestTabAndEntry() (*Tab, *core.RequestEntry) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return nil, nil
	}
	if tab := a.ps.activeTab(); tab != nil && tab.Kind == "request" && tab.Entry != nil && tab.Entry.Request != nil {
		return tab, tab.Entry
	}
	if strings.HasPrefix(a.ps.sel, "req:") {
		id := strings.TrimPrefix(a.ps.sel, "req:")
		if e := a.ps.project.Requests[id]; e != nil && e.Request != nil {
			return nil, e
		}
	}
	if len(a.ps.project.Order) > 0 {
		if e := a.ps.project.Requests[a.ps.project.Order[0]]; e != nil && e.Request != nil {
			return nil, e
		}
	}
	return nil, nil
}

// LoadActiveRequestContract loads the OperationContract of the currently active
// HTTP request into OpenAPIState for editing and response schema validation.
func (a *App) LoadActiveRequestContract() {
	st := a.OpenAPI()
	st.ContractErr = ""
	st.ContractNotice = ""
	st.ValidationResult = nil

	tab, entry := a.activeHTTPRequestTabAndEntry()
	if entry == nil || entry.Request == nil {
		st.LoadedReqID = ""
		st.WorkingContract = nil
		return
	}
	req := entry.Request
	st.LoadedReqID = req.ID

	contract, err := core.GetRequestOpenAPI(req)
	if err != nil {
		st.ContractErr = err.Error()
	}
	if contract == nil {
		contract = &core.OperationContract{
			OperationID: req.ID,
			Summary:     req.Name,
			Responses:   map[string]core.ResponseContract{},
		}
	}
	if contract.Responses == nil {
		contract.Responses = map[string]core.ResponseContract{}
	}
	st.WorkingContract = contract
	st.ContractOpID = contract.OperationID
	st.ContractSummary = contract.Summary
	st.ContractDesc = contract.Description
	st.ContractPathTmpl = contract.PathTemplate
	st.ContractDeprecated = contract.Deprecated

	if st.DraftStatusKey == "" {
		st.DraftStatusKey = "200"
	}
	if st.DraftMediaType == "" {
		st.DraftMediaType = "application/json"
	}
	if rc, ok := contract.Responses[st.DraftStatusKey]; ok {
		if mt, ok := rc.Content[st.DraftMediaType]; ok && len(mt.Schema) > 0 {
			if b, err := json.MarshalIndent(mt.Schema, "", "  "); err == nil {
				st.DraftSchemaJSON = string(b)
			}
		}
	}

	if tab != nil && tab.Resp != nil {
		a.LoadLatestLiveResponseIntoValidator()
	}
}

// StageDraftResponseContractEntry parses DraftSchemaJSON and adds/updates the
// response contract entry for (DraftStatusKey, DraftMediaType) in WorkingContract.
func (a *App) StageDraftResponseContractEntry() {
	st := a.OpenAPI()
	st.ContractErr = ""
	st.ContractNotice = ""
	if st.WorkingContract == nil {
		st.WorkingContract = &core.OperationContract{
			Responses: map[string]core.ResponseContract{},
		}
	}
	if st.WorkingContract.Responses == nil {
		st.WorkingContract.Responses = map[string]core.ResponseContract{}
	}
	statusKey := strings.TrimSpace(st.DraftStatusKey)
	if statusKey == "" {
		statusKey = "200"
	}
	mediaType := strings.TrimSpace(st.DraftMediaType)
	if mediaType == "" {
		mediaType = "application/json"
	}

	var schemaMap map[string]any
	rawSchema := strings.TrimSpace(st.DraftSchemaJSON)
	if rawSchema != "" {
		if err := json.Unmarshal([]byte(rawSchema), &schemaMap); err != nil {
			st.ContractErr = fmt.Sprintf("Invalid JSON Schema: %v", err)
			return
		}
	}

	rc := st.WorkingContract.Responses[statusKey]
	if rc.Description == "" {
		rc.Description = "Response " + statusKey
	}
	if rc.Content == nil {
		rc.Content = map[string]core.MediaTypeContract{}
	}
	mt := rc.Content[mediaType]
	mt.Schema = schemaMap
	rc.Content[mediaType] = mt
	st.WorkingContract.Responses[statusKey] = rc
	st.ContractNotice = fmt.Sprintf("Staged response contract %s (%s)", statusKey, mediaType)
}

// SaveActiveRequestContract persists WorkingContract to the active request's
// YAML file using core.SaveRequestWithOpenAPI (preserving comments & checking baseline).
func (a *App) SaveActiveRequestContract() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.OpenAPI()
	st.ContractErr = ""
	st.ContractNotice = ""

	tab, entry := a.activeHTTPRequestTabAndEntry()
	if entry == nil || entry.Request == nil {
		st.ContractErr = a.openAPIMsg().NoActiveHTTPRequest
		return
	}
	if tab != nil && (tab.Dirty || tab.Sending) {
		st.ContractErr = fmt.Sprintf(a.interopMsg().ImportBufferBusy, entry.RelPath)
		return
	}

	if strings.TrimSpace(st.DraftSchemaJSON) != "" {
		a.StageDraftResponseContractEntry()
		if st.ContractErr != "" {
			return
		}
	}
	if st.WorkingContract == nil {
		st.WorkingContract = &core.OperationContract{}
	}
	st.WorkingContract.OperationID = strings.TrimSpace(st.ContractOpID)
	st.WorkingContract.Summary = strings.TrimSpace(st.ContractSummary)
	st.WorkingContract.Description = strings.TrimSpace(st.ContractDesc)
	st.WorkingContract.PathTemplate = strings.TrimSpace(st.ContractPathTmpl)
	st.WorkingContract.Deprecated = st.ContractDeprecated

	newDigest, err := core.SaveRequestWithOpenAPI(a.ps.project, entry, entry.Request, st.WorkingContract, entry.BaselineDigest)
	if err != nil {
		st.ContractErr = err.Error()
		return
	}
	if tab != nil {
		tab.Baseline = newDigest
	}
	if peekFileSync(a.ps) != nil {
		a.SyncNow()
	}

	om := a.openAPIMsg()
	st.ContractNotice = om.ContractSavedStatus
	a.status = om.ContractSavedStatus
}

// LoadLatestLiveResponseIntoValidator populates TestStatusCode, TestContentType,
// and TestBodyJSON from the active tab's latest HTTP response (`tab.Resp`).
func (a *App) LoadLatestLiveResponseIntoValidator() {
	st := a.OpenAPI()
	tab, _ := a.activeHTTPRequestTabAndEntry()
	if tab == nil || tab.Resp == nil {
		return
	}
	st.TestStatusCode = strconv.Itoa(tab.Resp.Status)
	ct := "application/json"
	for _, h := range tab.Resp.Headers {
		if strings.EqualFold(h[0], "Content-Type") && strings.TrimSpace(h[1]) != "" {
			ct = strings.TrimSpace(h[1])
			break
		}
	}
	st.TestContentType = ct
	st.TestBodyJSON = string(tab.Resp.Body)
}

// ValidateActiveResponseAgainstContract validates the response status, headers,
// and JSON body in Tab 2 against the active request's OperationContract.
func (a *App) ValidateActiveResponseAgainstContract() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	st := a.OpenAPI()
	st.ContractErr = ""
	st.ValidationResult = nil

	_, entry := a.activeHTTPRequestTabAndEntry()
	if entry == nil || entry.Request == nil {
		st.ContractErr = a.openAPIMsg().NoActiveHTTPRequest
		return
	}

	if strings.TrimSpace(st.DraftSchemaJSON) != "" {
		a.StageDraftResponseContractEntry()
		if st.ContractErr != "" {
			return
		}
	}

	// Build a shallow request copy carrying the latest WorkingContract so users
	// can test schema validation even before clicking Save
	reqCopy := *entry.Request
	if st.WorkingContract != nil {
		_ = core.SetRequestOpenAPI(&reqCopy, st.WorkingContract)
	}

	statusCode, err := strconv.Atoi(strings.TrimSpace(st.TestStatusCode))
	if err != nil || statusCode <= 0 {
		statusCode = 200
	}
	headers := []core.KV{
		{Name: "Content-Type", Value: strings.TrimSpace(st.TestContentType)},
	}
	if tab, _ := a.activeHTTPRequestTabAndEntry(); tab != nil && tab.Resp != nil {
		for _, h := range tab.Resp.Headers {
			if !strings.EqualFold(h[0], "Content-Type") {
				headers = append(headers, core.KV{Name: h[0], Value: h[1]})
			}
		}
	}

	res, err := core.ValidateHTTPResponseContract(
		a.ps.project,
		&reqCopy,
		statusCode,
		headers,
		[]byte(st.TestBodyJSON),
		core.SchemaValidationOptions{ValidateFormats: true},
	)
	if err != nil {
		st.ContractErr = err.Error()
		return
	}
	st.ValidationResult = res
}
