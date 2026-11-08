package core

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const duplicateCURL = `curl -X POST https://api.example.com/v1/orders -H 'Content-Type: application/json' -d '{"item":"book"}'`

func newDuplicateCURLProject(t *testing.T) *Project {
	t.Helper()
	dir := t.TempDir()
	p, err := NewProject(dir, "CURL Duplicates")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	return p
}

func stageDuplicateCURL(t *testing.T, p *Project, opts ImportOptions) *ImportPlan {
	t.Helper()
	plan, err := StageCURLImport(p, duplicateCURL, opts)
	if err != nil {
		t.Fatalf("StageCURLImport: %v", err)
	}
	return plan
}

func applyDuplicateCURL(t *testing.T, p *Project, opts ImportOptions) (*ImportPlan, *ImportReceipt, string, string) {
	t.Helper()
	plan := stageDuplicateCURL(t, p, opts)
	prev := plan.Preview()
	if !prev.CanApply {
		t.Fatalf("expected CanApply=true, conflicts=%v summary=%q", prev.Conflicts, prev.Summary)
	}
	if len(plan.ImportedRequestIDs()) != 1 || len(prev.IDMappings) != 1 {
		t.Fatalf("expected exactly one imported request, ids=%v mappings=%v", plan.ImportedRequestIDs(), prev.IDMappings)
	}
	id := plan.ImportedRequestIDs()[0]
	rel := prev.IDMappings[0].TargetPath
	receipt, err := plan.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return plan, receipt, id, rel
}

func readStoreBytes(t *testing.T, p *Project, rel string) []byte {
	t.Helper()
	data, _, err := p.Store.Read(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return data
}

// Repeated imports of the same command must create independent files and IDs
// (at least three times) without touching the first copy.
func TestCURLRepeatedImportsAllocateIndependentCopies(t *testing.T) {
	p := newDuplicateCURLProject(t)
	opts := ImportOptions{TargetFolderRel: "orders", DefaultRequestName: "Create Order"}

	const copies = 4
	ids := make([]string, 0, copies)
	rels := make([]string, 0, copies)
	var firstDigest string

	for i := 0; i < copies; i++ {
		plan, _, id, rel := applyDuplicateCURL(t, p, opts)
		prev := plan.Preview()
		if slices.Contains(ids, id) {
			t.Fatalf("import %d reused ID %q; all copies must be independent (ids=%v)", i, id, ids)
		}
		if slices.Contains(rels, rel) {
			t.Fatalf("import %d reused path %q; all copies must be independent (rels=%v)", i, rel, rels)
		}
		if i > 0 && !prev.IDMappings[0].Renamed {
			t.Fatalf("import %d should be marked renamed, mapping=%+v", i, prev.IDMappings[0])
		}
		if !p.Store.Exists(rel) {
			t.Fatalf("import %d file %q missing after Apply", i, rel)
		}
		if i == 0 {
			firstDigest = Digest(readStoreBytes(t, p, rel))
		} else if got := Digest(readStoreBytes(t, p, rels[0])); got != firstDigest {
			t.Fatalf("original copy %q changed after import %d", rels[0], i)
		}
		ids = append(ids, id)
		rels = append(rels, rel)
	}

	wantIDs := []string{
		"req-create-order",
		"req-create-order-copy",
		"req-create-order-copy-2",
		"req-create-order-copy-3",
	}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("unexpected copy IDs: got %v want %v", ids, wantIDs)
	}

	// Reopening the project must recover every copy with its stable ID.
	reopened, err := OpenProject(p.Dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	for _, id := range ids {
		if reopened.Requests[id] == nil {
			t.Fatalf("reopened project lost request %q (have %v)", id, reopened.Order)
		}
	}
	if len(reopened.Requests) != copies {
		t.Fatalf("expected %d requests after reopen, got %d (%v)", copies, len(reopened.Requests), reopened.Order)
	}
}

// The same custom Chinese name in the same and in different folders must still
// produce independent copies; the display name is preserved.
func TestCURLRepeatedChineseNameAcrossFolders(t *testing.T) {
	p := newDuplicateCURLProject(t)
	folders := []string{"订单", "备份", "订单"}
	ids := make([]string, 0, len(folders))
	rels := make([]string, 0, len(folders))

	for i, folder := range folders {
		_, _, id, rel := applyDuplicateCURL(t, p, ImportOptions{
			TargetFolderRel:    folder,
			DefaultRequestName: "创建订单",
		})
		if slices.Contains(ids, id) {
			t.Fatalf("folder %q import %d reused ID %q (ids=%v)", folder, i, id, ids)
		}
		if slices.Contains(rels, rel) {
			t.Fatalf("folder %q import %d reused path %q", folder, i, rel)
		}
		entry := p.Requests[id]
		if entry == nil || entry.Request.Name != "创建订单" {
			t.Fatalf("expected Chinese display name preserved, got %+v", entry)
		}
		if !strings.HasPrefix(rel, "collections/"+folder+"/") {
			t.Fatalf("expected file under collections/%s, got %q", folder, rel)
		}
		ids = append(ids, id)
		rels = append(rels, rel)
	}

	reopened, err := OpenProject(p.Dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if len(reopened.Requests) != len(folders) {
		t.Fatalf("expected %d Chinese-named copies, got %d", len(folders), len(reopened.Requests))
	}
	for _, id := range ids {
		if reopened.Requests[id] == nil {
			t.Fatalf("reopened project lost Chinese-named copy %q", id)
		}
	}
}

// A request file that exists on disk but was not reloaded into memory must
// still reserve its ID, even when the new import targets another folder.
func TestCURLImportDetectsDiskRequestIDsWhenMemoryStale(t *testing.T) {
	p := newDuplicateCURLProject(t)
	existing := &Request{
		SchemaVersion: SupportedSchemaVersion,
		ID:            "req-create-order",
		Name:          "Existing On Disk",
		Protocol:      "http",
		HTTP:          &HTTPDef{Method: "GET", URL: "https://example.com/existing"},
	}
	data, err := p.EncodeRequest(existing)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	existingRel := "collections/other/req-create-order.request.yaml"
	if err := p.Store.WriteAtomic(existingRel, data); err != nil {
		t.Fatalf("write existing request: %v", err)
	}
	if _, ok := p.Requests["req-create-order"]; ok {
		t.Fatalf("test setup expected stale in-memory project without req-create-order")
	}

	_, _, id, rel := applyDuplicateCURL(t, p, ImportOptions{
		TargetFolderRel:    "target",
		DefaultRequestName: "Create Order",
	})
	if id == "req-create-order" {
		t.Fatalf("import reused an ID that already exists on disk")
	}
	if rel == existingRel {
		t.Fatalf("import reused the on-disk request path %q", existingRel)
	}
	if got := Digest(readStoreBytes(t, p, existingRel)); got != Digest(data) {
		t.Fatalf("existing on-disk request was modified by the import")
	}

	reopened, err := OpenProject(p.Dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if reopened.Requests["req-create-order"] == nil {
		t.Fatalf("existing request lost after reopen")
	}
	if reopened.Requests[id] == nil {
		t.Fatalf("new copy %q lost after reopen", id)
	}
	if len(reopened.Requests) != 2 {
		t.Fatalf("expected 2 distinct requests, got %d (%v)", len(reopened.Requests), reopened.Order)
	}
}

// If a file appears at the previewed path before confirmation, Apply must
// refuse to overwrite it and a fresh plan must allocate a different copy.
func TestCURLStalePreviewRejectedWithoutOverwrite(t *testing.T) {
	p := newDuplicateCURLProject(t)
	opts := ImportOptions{TargetFolderRel: "orders", DefaultRequestName: "Create Order"}

	plan := stageDuplicateCURL(t, p, opts)
	prev := plan.Preview()
	if !prev.CanApply {
		t.Fatalf("expected initial preview to be applicable, conflicts=%v", prev.Conflicts)
	}
	rel := prev.IDMappings[0].TargetPath

	intruder := &Request{
		SchemaVersion: SupportedSchemaVersion,
		ID:            "req-intruder",
		Name:          "Intruder",
		Protocol:      "http",
		HTTP:          &HTTPDef{Method: "GET", URL: "https://example.com/intruder"},
	}
	intruderBytes, err := p.EncodeRequest(intruder)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if err := p.Store.WriteAtomic(rel, intruderBytes); err != nil {
		t.Fatalf("write intruder: %v", err)
	}

	if _, err := plan.Apply(); !IsConflict(err) {
		t.Fatalf("expected stale preview Apply to return a conflict, got %v", err)
	}
	if got := readStoreBytes(t, p, rel); Digest(got) != Digest(intruderBytes) {
		t.Fatalf("stale preview overwrote the file that appeared on disk")
	}

	replanned := stageDuplicateCURL(t, p, opts)
	replannedPrev := replanned.Preview()
	if !replannedPrev.CanApply {
		t.Fatalf("expected replanned preview to be applicable, conflicts=%v", replannedPrev.Conflicts)
	}
	if replannedPrev.IDMappings[0].TargetPath == rel {
		t.Fatalf("replanned import reused the occupied path %q", rel)
	}
	if _, err := replanned.Apply(); err != nil {
		t.Fatalf("apply replanned import: %v", err)
	}
	if got := readStoreBytes(t, p, rel); Digest(got) != Digest(intruderBytes) {
		t.Fatalf("replanned import modified the pre-existing file")
	}
	if !p.Store.Exists(replannedPrev.IDMappings[0].TargetPath) {
		t.Fatalf("replanned copy was not written")
	}
}

// Explicit overwrite keeps its baseline guard: a stale preview must not clobber
// a file edited after staging.
func TestCURLOverwriteStaleBaselineRejected(t *testing.T) {
	p := newDuplicateCURLProject(t)
	baseOpts := ImportOptions{TargetFolderRel: "orders", DefaultRequestName: "Create Order"}
	_, _, _, rel := applyDuplicateCURL(t, p, baseOpts)

	overwritePlan := stageDuplicateCURL(t, p, ImportOptions{
		TargetFolderRel:    "orders",
		DefaultRequestName: "Create Order",
		ConflictPolicy:     ConflictOverwrite,
	})
	prev := overwritePlan.Preview()
	if prev.IDMappings[0].Action != "update" {
		t.Fatalf("expected overwrite plan to update existing request, got %+v", prev.IDMappings[0])
	}

	edited := append(readStoreBytes(t, p, rel), []byte("\n# edited after preview\n")...)
	if err := p.Store.WriteAtomic(rel, edited); err != nil {
		t.Fatalf("external edit: %v", err)
	}
	if _, err := overwritePlan.Apply(); !IsConflict(err) {
		t.Fatalf("expected stale overwrite to be rejected, got %v", err)
	}
	if got := readStoreBytes(t, p, rel); Digest(got) != Digest(edited) {
		t.Fatalf("stale overwrite clobbered the externally edited file")
	}

	// A fresh overwrite plan against the current baseline is still allowed.
	fresh := stageDuplicateCURL(t, p, ImportOptions{
		TargetFolderRel:    "orders",
		DefaultRequestName: "Create Order",
		ConflictPolicy:     ConflictOverwrite,
	})
	if _, err := fresh.Apply(); err != nil {
		t.Fatalf("fresh overwrite apply: %v", err)
	}
	if len(p.Requests) != 1 {
		t.Fatalf("overwrite should keep a single request, got %d (%v)", len(p.Requests), p.Order)
	}
}

// Explicit fail and skip strategies must keep working for cURL imports.
func TestCURLExplicitFailAndSkipStillWork(t *testing.T) {
	p := newDuplicateCURLProject(t)
	baseOpts := ImportOptions{TargetFolderRel: "orders", DefaultRequestName: "Create Order"}
	_, _, id, rel := applyDuplicateCURL(t, p, baseOpts)
	original := readStoreBytes(t, p, rel)

	failPlan := stageDuplicateCURL(t, p, ImportOptions{
		TargetFolderRel:    "orders",
		DefaultRequestName: "Create Order",
		ConflictPolicy:     ConflictFail,
	})
	failPrev := failPlan.Preview()
	if failPrev.CanApply || len(failPrev.Conflicts) == 0 {
		t.Fatalf("explicit fail must block the duplicate, preview=%+v", failPrev)
	}
	if _, err := failPlan.Apply(); !errors.Is(err, ErrImportBlocked) {
		t.Fatalf("expected ErrImportBlocked for explicit fail, got %v", err)
	}

	skipPlan := stageDuplicateCURL(t, p, ImportOptions{
		TargetFolderRel:    "orders",
		DefaultRequestName: "Create Order",
		ConflictPolicy:     ConflictSkip,
	})
	skipPrev := skipPlan.Preview()
	if len(skipPrev.Changes) != 0 {
		t.Fatalf("skip must not stage file changes, got %+v", skipPrev.Changes)
	}
	if _, err := skipPlan.Apply(); !errors.Is(err, ErrImportBlocked) {
		t.Fatalf("expected skip to apply nothing, got %v", err)
	}
	if got := readStoreBytes(t, p, rel); Digest(got) != Digest(original) {
		t.Fatalf("explicit fail/skip modified the original request")
	}
	if len(p.Requests) != 1 {
		t.Fatalf("explicit fail/skip created extra requests: %v", p.Order)
	}
	if p.Requests[id] == nil {
		t.Fatalf("original request %q disappeared", id)
	}
}

// Undoing the last duplicate or deleting one copy must leave the other copies
// and their IDs intact.
func TestCURLDuplicateCopiesSurviveUndoAndDeleteOfAnother(t *testing.T) {
	p := newDuplicateCURLProject(t)
	opts := ImportOptions{TargetFolderRel: "orders", DefaultRequestName: "Create Order"}

	ids := make([]string, 0, 3)
	receipts := make([]*ImportReceipt, 0, 3)
	for i := 0; i < 3; i++ {
		_, receipt, id, _ := applyDuplicateCURL(t, p, opts)
		ids = append(ids, id)
		receipts = append(receipts, receipt)
	}

	if err := receipts[2].Rollback(); err != nil {
		t.Fatalf("rollback last copy: %v", err)
	}
	for _, id := range ids[:2] {
		if p.Requests[id] == nil {
			t.Fatalf("rollback of last copy removed %q", id)
		}
	}
	if p.Requests[ids[2]] != nil {
		t.Fatalf("rollback left %q behind", ids[2])
	}

	middle := p.Requests[ids[1]]
	if middle == nil {
		t.Fatalf("middle copy missing")
	}
	if err := p.Store.RemoveChecked(middle.RelPath, middle.BaselineDigest); err != nil {
		t.Fatalf("delete middle copy: %v", err)
	}
	if err := ReloadProjectInMemory(p); err != nil {
		t.Fatalf("reload after delete: %v", err)
	}
	if p.Requests[ids[0]] == nil {
		t.Fatalf("deleting one copy removed the first copy")
	}
	if p.Requests[ids[1]] != nil {
		t.Fatalf("deleted copy %q still present", ids[1])
	}

	// A new import after the deletions gets its own fresh copy.
	_, _, newID, _ := applyDuplicateCURL(t, p, opts)
	if newID == ids[0] {
		t.Fatalf("new import reused the surviving copy ID %q", newID)
	}
	if p.Requests[ids[0]] == nil || p.Requests[newID] == nil {
		t.Fatalf("expected surviving and new copies, have %v", p.Order)
	}
}

// Two previews of the same command in different folders must not be able to
// commit the same ID; the stale plan is rejected and can be re-planned.
func TestCURLStalePlanCannotDuplicateIDAcrossFolders(t *testing.T) {
	p := newDuplicateCURLProject(t)
	planA := stageDuplicateCURL(t, p, ImportOptions{TargetFolderRel: "a", DefaultRequestName: "Create Order"})
	prevA := planA.Preview()
	if !prevA.CanApply {
		t.Fatalf("plan A not applicable: %+v", prevA)
	}
	idA := planA.ImportedRequestIDs()[0]
	relA := prevA.IDMappings[0].TargetPath

	planB := stageDuplicateCURL(t, p, ImportOptions{TargetFolderRel: "b", DefaultRequestName: "Create Order"})
	if _, err := planB.Apply(); err != nil {
		t.Fatalf("apply plan B: %v", err)
	}
	if planB.ImportedRequestIDs()[0] != idA {
		t.Fatalf("test setup expected both previews to allocate %q, B got %q", idA, planB.ImportedRequestIDs()[0])
	}
	if p.Store.Exists(relA) {
		t.Fatalf("plan A file unexpectedly exists before its Apply")
	}

	if _, err := planA.Apply(); !errors.Is(err, ErrRequestIDConflict) {
		t.Fatalf("expected stale plan to be rejected for duplicate ID, got %v", err)
	}
	if p.Store.Exists(relA) {
		t.Fatalf("rejected plan A wrote its file anyway")
	}
	if p.Requests[idA] == nil {
		t.Fatalf("rejected plan A removed the request created by plan B")
	}

	replanned := stageDuplicateCURL(t, p, ImportOptions{TargetFolderRel: "a", DefaultRequestName: "Create Order"})
	if replanned.ImportedRequestIDs()[0] == idA {
		t.Fatalf("replanned import reused the occupied ID %q", idA)
	}
	if _, err := replanned.Apply(); err != nil {
		t.Fatalf("apply replanned import: %v", err)
	}

	reopened, err := OpenProject(p.Dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if len(reopened.Requests) != 2 {
		t.Fatalf("expected 2 distinct requests, got %d (%v)", len(reopened.Requests), reopened.Order)
	}
}

// Duplicate imports that declare secret slots must keep the environment slot
// declarations when the last copy is undone.
func TestCURLDuplicateWithSecretsUndoLastKeepsOthers(t *testing.T) {
	p := newDuplicateCURLProject(t)
	raw := `curl https://api.example.com/v1/orders -H 'Authorization: Bearer top_secret_dup_123'`
	opts := ImportOptions{TargetFolderRel: "orders", DefaultRequestName: "Create Order"}

	ids := make([]string, 0, 3)
	receipts := make([]*ImportReceipt, 0, 3)
	for i := 0; i < 3; i++ {
		plan, err := StageCURLImport(p, raw, opts)
		if err != nil {
			t.Fatalf("stage %d: %v", i, err)
		}
		if !plan.Preview().CanApply {
			t.Fatalf("stage %d not applicable: %+v", i, plan.Preview())
		}
		id := plan.ImportedRequestIDs()[0]
		receipt, err := plan.Apply()
		if err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
		ids = append(ids, id)
		receipts = append(receipts, receipt)
	}

	env := p.Environments["dev"]
	if env == nil || !slices.Contains(env.Secrets, "bearer_token") {
		t.Fatalf("expected dev environment to declare bearer_token, got %+v", env)
	}

	if err := receipts[2].Rollback(); err != nil {
		t.Fatalf("rollback last secret copy: %v", err)
	}
	for _, id := range ids[:2] {
		if p.Requests[id] == nil {
			t.Fatalf("rollback of last secret copy removed %q", id)
		}
	}
	if p.Requests[ids[2]] != nil {
		t.Fatalf("rollback left %q behind", ids[2])
	}
	env = p.Environments["dev"]
	if env == nil || !slices.Contains(env.Secrets, "bearer_token") {
		t.Fatalf("rollback of the last copy removed the shared secret slot: %+v", env)
	}

	reopened, err := OpenProject(p.Dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if len(reopened.Requests) != 2 {
		t.Fatalf("expected 2 surviving secret copies, got %d", len(reopened.Requests))
	}
	if env := reopened.Environments["dev"]; env == nil || !slices.Contains(env.Secrets, "bearer_token") {
		t.Fatalf("reopened environment lost the shared secret slot: %+v", env)
	}
}

// StageAutoImport must apply the same cURL default while leaving Postman's
// fail-on-conflict default untouched.
func TestStageAutoImportCURLDefaultsToCopiesAndPostmanKeepsFail(t *testing.T) {
	p := newDuplicateCURLProject(t)
	rawCURL := []byte(duplicateCURL)
	for i := 0; i < 3; i++ {
		plan, err := StageAutoImport(p, rawCURL, "request.curl", ImportOptions{DefaultRequestName: "Auto"})
		if err != nil {
			t.Fatalf("StageAutoImport cURL %d: %v", i, err)
		}
		if !plan.Preview().CanApply {
			t.Fatalf("StageAutoImport cURL %d preview not applicable: %+v", i, plan.Preview())
		}
		if _, err := plan.Apply(); err != nil {
			t.Fatalf("apply cURL %d: %v", i, err)
		}
	}
	if len(p.Requests) != 3 {
		t.Fatalf("expected 3 cURL copies, got %d (%v)", len(p.Requests), p.Order)
	}

	postman := []byte(`{
  "info": {"name": "Orders", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "item": [{"name": "List Orders", "request": {"method": "GET", "url": "https://api.example.com/orders"}}]
}`)
	first, err := StageAutoImport(p, postman, "orders.postman_collection.json", ImportOptions{})
	if err != nil {
		t.Fatalf("StageAutoImport Postman first: %v", err)
	}
	if _, err := first.Apply(); err != nil {
		t.Fatalf("apply Postman first: %v", err)
	}
	second, err := StageAutoImport(p, postman, "orders.postman_collection.json", ImportOptions{})
	if err != nil {
		t.Fatalf("StageAutoImport Postman second: %v", err)
	}
	secondPrev := second.Preview()
	if secondPrev.CanApply || len(secondPrev.Conflicts) == 0 {
		t.Fatalf("Postman duplicate must keep the fail-on-conflict default, preview=%+v", secondPrev)
	}
}

// Files written by the import must stay confined to the project root; this is
// a guard that the duplicate allocation does not escape through crafted names.
func TestCURLDuplicateAllocationStaysInsideProject(t *testing.T) {
	p := newDuplicateCURLProject(t)
	opts := ImportOptions{TargetFolderRel: "orders", DefaultRequestName: "../../escape"}
	_, _, _, rel := applyDuplicateCURL(t, p, opts)
	if strings.Contains(rel, "..") {
		t.Fatalf("allocated path escaped the project: %q", rel)
	}
	abs := filepath.Join(p.Dir, filepath.FromSlash(rel))
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("expected imported file under project root: %v", err)
	}
}
