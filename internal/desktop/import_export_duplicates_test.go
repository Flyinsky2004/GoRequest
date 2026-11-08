package desktop

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gorequest/internal/core"
)

const desktopDuplicateCURL = `curl -X POST https://api.example.com/v1/orders -H "Content-Type: application/json" -d '{"item":"book"}'`

func prepareDesktopCURLImport(t *testing.T, a *App) *InteropState {
	t.Helper()
	a.OpenImportCURLModal()
	st := a.Interop()
	st.CURLInput = desktopDuplicateCURL
	st.ImportRequestName = "Create Order"
	st.ImportTargetFolder = "orders"
	return st
}

// Importing the same cURL through the real modal entry point three times must
// keep three independent requests, and Undo/delete must not disturb the others.
func TestDesktopRepeatedCURLImportsCreateIndependentCopies(t *testing.T) {
	a, ps := openTestProject(t, "Desktop CURL Duplicates")
	st := prepareDesktopCURLImport(t, a)
	if st.ConflictPolicyIdx != 1 {
		t.Fatalf("cURL import modal should default to Copy with New ID, got index %d", st.ConflictPolicyIdx)
	}

	ids := make([]string, 0, 3)
	var firstDigest string
	for i := 0; i < 3; i++ {
		a.StageImportPreview()
		if st.ImportErr != "" {
			t.Fatalf("stage %d: %s", i, st.ImportErr)
		}
		if st.StagedPlan == nil || !st.StagedPlan.Preview().CanApply {
			t.Fatalf("stage %d preview not applicable", i)
		}
		id := st.StagedPlan.ImportedRequestIDs()[0]
		if slices.Contains(ids, id) {
			t.Fatalf("import %d reused ID %q (ids=%v)", i, id, ids)
		}
		ids = append(ids, id)

		a.ConfirmAndApplyImport()
		if st.ImportErr != "" {
			t.Fatalf("apply %d: %s", i, st.ImportErr)
		}
		entry := ps.project.Requests[id]
		if entry == nil {
			t.Fatalf("import %d did not create request %q (have %v)", i, id, ps.project.Order)
		}
		data, _, err := ps.project.Store.Read(entry.RelPath)
		if err != nil {
			t.Fatalf("read imported file %q: %v", entry.RelPath, err)
		}
		if i == 0 {
			firstDigest = core.Digest(data)
			continue
		}
		firstEntry := ps.project.Requests[ids[0]]
		if firstEntry == nil {
			t.Fatalf("first copy disappeared after import %d", i)
		}
		firstData, _, err := ps.project.Store.Read(firstEntry.RelPath)
		if err != nil {
			t.Fatalf("read first copy: %v", err)
		}
		if core.Digest(firstData) != firstDigest {
			t.Fatalf("first copy changed after import %d", i)
		}
	}
	if len(ps.project.Requests) != 3 {
		t.Fatalf("expected 3 independent copies, got %d (%v)", len(ps.project.Requests), ps.project.Order)
	}

	reopened, err := core.OpenProject(ps.project.Dir)
	if err != nil {
		t.Fatalf("reopen project: %v", err)
	}
	for _, id := range ids {
		if reopened.Requests[id] == nil {
			t.Fatalf("reopened project lost %q", id)
		}
	}

	a.RollbackLastImport()
	if st.ImportErr != "" {
		t.Fatalf("undo last import: %s", st.ImportErr)
	}
	if ps.project.Requests[ids[2]] != nil {
		t.Fatalf("undo left the last copy %q behind", ids[2])
	}
	for _, id := range ids[:2] {
		if ps.project.Requests[id] == nil {
			t.Fatalf("undo of the last copy removed %q", id)
		}
	}

	middle := ps.project.Requests[ids[1]]
	if middle == nil {
		t.Fatalf("middle copy missing before delete")
	}
	if err := os.Remove(filepath.Join(ps.project.Dir, filepath.FromSlash(middle.RelPath))); err != nil {
		t.Fatalf("delete middle copy: %v", err)
	}
	if err := core.ReloadProjectInMemory(ps.project); err != nil {
		t.Fatalf("reload after delete: %v", err)
	}
	if ps.project.Requests[ids[0]] == nil {
		t.Fatalf("deleting one copy removed the surviving copy")
	}
	if ps.project.Requests[ids[1]] != nil {
		t.Fatalf("deleted copy %q still present", ids[1])
	}
}

// A file appearing at the previewed path before confirmation must trigger a
// fresh preview, never a silent overwrite.
func TestDesktopCURLStalePreviewReplansInsteadOfOverwriting(t *testing.T) {
	a, ps := openTestProject(t, "Desktop CURL Stale Preview")
	st := prepareDesktopCURLImport(t, a)

	a.StageImportPreview()
	if st.ImportErr != "" || st.StagedPlan == nil {
		t.Fatalf("initial preview failed: err=%q plan=%v", st.ImportErr, st.StagedPlan)
	}
	rel := st.StagedPlan.Preview().IDMappings[0].TargetPath

	intruder := &core.Request{
		SchemaVersion: core.SupportedSchemaVersion,
		ID:            "req-intruder",
		Name:          "Intruder",
		Protocol:      "http",
		HTTP:          &core.HTTPDef{Method: "GET", URL: "https://example.com/intruder"},
	}
	intruderBytes, err := ps.project.EncodeRequest(intruder)
	if err != nil {
		t.Fatalf("encode intruder: %v", err)
	}
	if err := ps.project.Store.WriteAtomic(rel, intruderBytes); err != nil {
		t.Fatalf("write intruder: %v", err)
	}

	a.ConfirmAndApplyImport()
	if st.ImportErr != "" {
		t.Fatalf("stale confirm should re-plan, got error %q", st.ImportErr)
	}
	if st.ImportNotice == "" {
		t.Fatalf("expected a re-plan notice")
	}
	if st.StagedPlan == nil {
		t.Fatalf("expected a fresh staged plan after re-planning")
	}
	onDisk, _, err := ps.project.Store.Read(rel)
	if err != nil {
		t.Fatalf("read intruder after re-plan: %v", err)
	}
	if core.Digest(onDisk) != core.Digest(intruderBytes) {
		t.Fatalf("stale confirm overwrote the file that appeared on disk")
	}
	if st.StagedPlan.Preview().IDMappings[0].TargetPath == rel {
		t.Fatalf("fresh plan reused the occupied path %q", rel)
	}

	a.ConfirmAndApplyImport()
	if st.ImportErr != "" {
		t.Fatalf("apply fresh plan: %s", st.ImportErr)
	}
	if !ps.project.Store.Exists(rel) {
		t.Fatalf("pre-existing file disappeared after applying the fresh plan")
	}
	// Reloading after Apply sees both the pre-existing file and the new copy.
	if len(ps.project.Requests) != 2 {
		t.Fatalf("expected the pre-existing request plus one imported copy, got %d (%v)", len(ps.project.Requests), ps.project.Order)
	}
	for id, entry := range ps.project.Requests {
		if entry.RelPath == rel && id != "req-intruder" {
			t.Fatalf("fresh plan reused the occupied path for %q", id)
		}
	}
}

// The generic source selector keeps the conservative fail default for
// collection/bundle imports.
func TestDesktopImportModalDefaultPolicyDependsOnSource(t *testing.T) {
	a, _ := openTestProject(t, "Desktop Import Defaults")
	a.OpenImportModal(ImportSourcePostman)
	if got := a.Interop().ConflictPolicyIdx; got != 0 {
		t.Fatalf("Postman import should default to fail-on-conflict, got index %d", got)
	}
	a.OpenImportCURLModal()
	if got := a.Interop().ConflictPolicyIdx; got != 1 {
		t.Fatalf("cURL import should default to Copy with New ID, got index %d", got)
	}
}

// The re-plan notice must follow the configured UI language.
func TestDesktopImportReplannedNoticeIsLocalized(t *testing.T) {
	a, _ := openTestProject(t, "Desktop Import Notice")
	if got := a.importReplannedNotice(); !strings.Contains(strings.ToLower(got), "preview") {
		t.Fatalf("unexpected English re-plan notice: %q", got)
	}
	if a.mgr != nil && a.mgr.cfg != nil {
		a.mgr.cfg.cfg.Language = string(langSimplifiedChinese)
	}
	if got := a.importReplannedNotice(); !strings.Contains(got, "预览") {
		t.Fatalf("unexpected Chinese re-plan notice: %q", got)
	}
}
