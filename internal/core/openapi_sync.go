package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SyncDiffKind classifies how an operation in an external OpenAPI specification
// relates to the current GoRequest project state during 3-way sync preview.
type SyncDiffKind string

const (
	SyncAdded        SyncDiffKind = "added"
	SyncUpdated      SyncDiffKind = "updated"
	SyncUnchanged    SyncDiffKind = "unchanged"
	SyncLocalOnly    SyncDiffKind = "local-only"
	SyncDiffConflict SyncDiffKind = "conflict"
)

// SyncConflictItem describes a 3-way conflict where both the local request and
// the upstream OpenAPI specification diverged from the baseline contract.
type SyncConflictItem struct {
	RequestID     string `json:"requestId" yaml:"requestId"`
	OperationKey  string `json:"operationKey" yaml:"operationKey"`
	Field         string `json:"field" yaml:"field"`
	BaselineValue string `json:"baselineValue,omitempty" yaml:"baselineValue,omitempty"`
	LocalValue    string `json:"localValue,omitempty" yaml:"localValue,omitempty"`
	UpstreamValue string `json:"upstreamValue,omitempty" yaml:"upstreamValue,omitempty"`
	Message       string `json:"message" yaml:"message"`
}

// OperationSyncItem summarizes the 3-way sync disposition for a single operation.
type OperationSyncItem struct {
	OperationKey         string             `json:"operationKey" yaml:"operationKey"`
	OperationID          string             `json:"operationId,omitempty" yaml:"operationId,omitempty"`
	RequestID            string             `json:"requestId" yaml:"requestId"`
	RelPath              string             `json:"relPath" yaml:"relPath"`
	Kind                 SyncDiffKind       `json:"kind" yaml:"kind"`
	Summary              string             `json:"summary,omitempty" yaml:"summary,omitempty"`
	PreservedLocalFields []string           `json:"preservedLocalFields,omitempty" yaml:"preservedLocalFields,omitempty"`
	Conflicts            []SyncConflictItem `json:"conflicts,omitempty" yaml:"conflicts,omitempty"`
}

// OpenAPISyncPreview summarizes a 3-way contract sync between an external
// OpenAPI specification, its recorded baseline, and local project requests.
type OpenAPISyncPreview struct {
	SourceMode         SourceMode          `json:"sourceMode" yaml:"sourceMode"`
	SpecPath           string              `json:"specPath,omitempty" yaml:"specPath,omitempty"`
	PreviousSpecDigest string              `json:"previousSpecDigest,omitempty" yaml:"previousSpecDigest,omitempty"`
	UpstreamSpecDigest string              `json:"upstreamSpecDigest" yaml:"upstreamSpecDigest"`
	SpecVersion        string              `json:"specVersion" yaml:"specVersion"`
	AddedCount         int                 `json:"addedCount" yaml:"addedCount"`
	UpdatedCount       int                 `json:"updatedCount" yaml:"updatedCount"`
	UnchangedCount     int                 `json:"unchangedCount" yaml:"unchangedCount"`
	LocalOnlyCount     int                 `json:"localOnlyCount" yaml:"localOnlyCount"`
	ConflictCount      int                 `json:"conflictCount" yaml:"conflictCount"`
	Operations         []OperationSyncItem `json:"operations,omitempty" yaml:"operations,omitempty"`
	Conflicts          []SyncConflictItem  `json:"conflicts,omitempty" yaml:"conflicts,omitempty"`
}

// StageOpenAPISyncFromFile reads an updated external OpenAPI specification file
// from disk and stages a 3-way sync preview against p.
func StageOpenAPISyncFromFile(p *Project, specFilePath string, opts OpenAPIImportOptions) (*ImportPlan, *OpenAPISyncPreview, error) {
	cleanPath := filepath.Clean(specFilePath)
	info, err := os.Stat(cleanPath)
	if err != nil {
		return nil, nil, err
	}
	if info.Size() > DefaultMaxSchemaBytes {
		return nil, nil, configErrorf("OpenAPI specification %q exceeds size limit (%d > %d bytes)", cleanPath, info.Size(), DefaultMaxSchemaBytes)
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, nil, err
	}
	if opts.BaseDir == "" {
		opts.BaseDir = filepath.Dir(cleanPath)
	}
	if opts.SourceFileRel == "" {
		opts.SourceFileRel = filepath.Base(cleanPath)
	}
	return StageOpenAPISync(p, data, filepath.Base(cleanPath), opts)
}

// StageOpenAPISync stages a 3-way synchronization preview between p and an
// updated upstream OpenAPI specification, preserving non-conflicting local
// execution parameters (timeout, variables, extra assertions, {{var}} bindings)
// while detecting 3-way contract/local conflicts.
func StageOpenAPISync(p *Project, upstreamData []byte, filename string, opts OpenAPIImportOptions) (*ImportPlan, *OpenAPISyncPreview, error) {
	if p == nil || p.Store == nil || p.File == nil {
		return nil, nil, configErrorf("project or store is nil")
	}
	opts = opts.normalized()
	opts.SourceMode = SourceModeExternalSpec

	red := opts.InteropOptions.Redactor
	if red == nil {
		red = NewRedactor()
		opts.InteropOptions.Redactor = red
	}

	valReport, err := ValidateOpenAPISpec(upstreamData, OpenAPIValidationOptions{
		BaseDir:        opts.BaseDir,
		CurrentFileRel: opts.SourceFileRel,
	})
	if err != nil {
		return nil, nil, err
	}

	// Stage a raw import with ConflictOverwrite first so we can inspect all upstream operations cleanly
	stageOpts := opts
	stageOpts.InteropOptions.ConflictPolicy = ConflictOverwrite
	basePlan, err := StageOpenAPIImport(p, upstreamData, filename, stageOpts)
	if err != nil {
		return basePlan, nil, err
	}

	prevMeta, _ := GetProjectOpenAPI(p.File)
	prevDigest := ""
	specPath := opts.SourceFileRel
	if specPath == "" {
		specPath = filename
	}
	if prevMeta != nil && prevMeta.ExternalSpec != nil {
		prevDigest = prevMeta.ExternalSpec.ContentDigest
		if specPath == "" {
			specPath = prevMeta.ExternalSpec.SourcePath
		}
	}

	preview := &OpenAPISyncPreview{
		SourceMode:         SourceModeExternalSpec,
		SpecPath:           specPath,
		PreviousSpecDigest: prevDigest,
		UpstreamSpecDigest: Digest(upstreamData),
		SpecVersion:        valReport.Version,
	}

	if !valReport.Valid || len(valReport.MissingDependencies) > 0 || basePlan.changeSet == nil {
		return basePlan, preview, nil
	}

	// Build lookup table of existing local requests by operationKey ("METHOD /path"), operationId, and requestId
	existingByOpKey := map[string]*RequestEntry{}
	existingByOpID := map[string]*RequestEntry{}
	for _, reqID := range sortedMapKeys(p.Requests) {
		entry := p.Requests[reqID]
		if entry == nil || entry.Request == nil || !isHTTPProtocol(entry.Request.Protocol) || entry.Request.HTTP == nil {
			continue
		}
		contract, _ := GetRequestOpenAPI(entry.Request)
		m := strings.ToUpper(strings.TrimSpace(entry.Request.HTTP.Method))
		opPath := deriveOperationPath(entry.Request, contract)
		if contract != nil && contract.Source != nil {
			if contract.Source.Path != "" && contract.Source.Method != "" {
				existingByOpKey[strings.ToUpper(contract.Source.Method)+" "+contract.Source.Path] = entry
			}
			if contract.Source.OperationID != "" {
				existingByOpID[contract.Source.OperationID] = entry
			}
		}
		existingByOpKey[m+" "+opPath] = entry
		if contract != nil && contract.OperationID != "" {
			existingByOpID[contract.OperationID] = entry
		}
	}

	matchedLocalReqIDs := map[string]bool{}
	rebuiltCS := p.Store.NewChangeSet()
	var idMappings []IDMapping
	var conflictStrings []string
	var losses []LossItem
	losses = append(losses, basePlan.preview.Losses...)

	for _, fc := range basePlan.changeSet.changes {
		if !strings.HasPrefix(fc.RelPath, "collections/") || !strings.HasSuffix(fc.RelPath, ".request.yaml") {
			rebuiltCS.Add(fc)
			continue
		}

		var upReq Request
		if err := yaml.Unmarshal(fc.NewContent, &upReq); err != nil || upReq.HTTP == nil {
			rebuiltCS.Add(fc)
			continue
		}
		upContract, _ := GetRequestOpenAPI(&upReq)
		method := strings.ToUpper(strings.TrimSpace(upReq.HTTP.Method))
		opPath := deriveOperationPath(&upReq, upContract)
		opKey := method + " " + opPath
		opID := ""
		if upContract != nil {
			opID = upContract.OperationID
		}

		var localEntry *RequestEntry
		if opID != "" {
			localEntry = existingByOpID[opID]
		}
		if localEntry == nil {
			localEntry = existingByOpKey[opKey]
		}
		if localEntry == nil {
			localEntry = p.Requests[upReq.ID]
		}

		if localEntry == nil || localEntry.Request == nil {
			// Newly added operation in upstream spec
			preview.AddedCount++
			preview.Operations = append(preview.Operations, OperationSyncItem{
				OperationKey: opKey,
				OperationID:  opID,
				RequestID:    upReq.ID,
				RelPath:      fc.RelPath,
				Kind:         SyncAdded,
				Summary:      upReq.Name,
			})
			rebuiltCS.Add(fc)
			idMappings = append(idMappings, IDMapping{
				EntityKind: "request",
				SourceID:   opKey,
				TargetID:   upReq.ID,
				TargetPath: fc.RelPath,
				Action:     "create",
			})
			continue
		}

		matchedLocalReqIDs[localEntry.Request.ID] = true
		localReq := localEntry.Request
		localContract, _ := GetRequestOpenAPI(localReq)

		mergedReq, mergedContract, preserved, conflicts, changed := mergeThreeWayOperation(
			localReq, localContract, &upReq, upContract, opKey,
		)

		if len(conflicts) > 0 {
			preview.ConflictCount += len(conflicts)
			preview.Conflicts = append(preview.Conflicts, conflicts...)
			preview.Operations = append(preview.Operations, OperationSyncItem{
				OperationKey:         opKey,
				OperationID:          opID,
				RequestID:            localReq.ID,
				RelPath:              localEntry.RelPath,
				Kind:                 SyncDiffConflict,
				Summary:              localReq.Name,
				PreservedLocalFields: preserved,
				Conflicts:            conflicts,
			})

			switch opts.InteropOptions.ConflictPolicy {
			case ConflictSkip:
				idMappings = append(idMappings, IDMapping{
					EntityKind: "request",
					SourceID:   opKey,
					TargetID:   localReq.ID,
					TargetPath: localEntry.RelPath,
					Action:     "skip",
				})
				continue

			case ConflictOverwrite:
				// Proceed and stage mergedReq (which uses upstream contract + preserved non-conflicting local execution settings)
			default:
				for _, c := range conflicts {
					conflictStrings = append(conflictStrings, c.Message)
					losses = append(losses, LossItem{
						Code:       "openapi-sync-conflict",
						Severity:   LossBlocked,
						SourcePath: opKey,
						TargetPath: localEntry.RelPath,
						Field:      c.Field,
						Message:    c.Message,
					})
				}
			}
		} else if !changed {
			preview.UnchangedCount++
			preview.Operations = append(preview.Operations, OperationSyncItem{
				OperationKey:         opKey,
				OperationID:          opID,
				RequestID:            localReq.ID,
				RelPath:              localEntry.RelPath,
				Kind:                 SyncUnchanged,
				Summary:              localReq.Name,
				PreservedLocalFields: preserved,
			})
			continue
		} else {
			preview.UpdatedCount++
			preview.Operations = append(preview.Operations, OperationSyncItem{
				OperationKey:         opKey,
				OperationID:          opID,
				RequestID:            localReq.ID,
				RelPath:              localEntry.RelPath,
				Kind:                 SyncUpdated,
				Summary:              mergedReq.Name,
				PreservedLocalFields: preserved,
			})
		}

		// Serialize mergedReq preserving comments on localEntry.RelPath
		if err := SetRequestOpenAPI(mergedReq, mergedContract); err != nil {
			return nil, nil, err
		}
		var diskBytes []byte
		if p.Store.Exists(localEntry.RelPath) {
			diskBytes, _, _ = p.Store.ReadBounded(localEntry.RelPath, p.Store.EffectiveLimits().MaxYAMLBytes)
		}
		mergedBytes, merr := MergeRequestYAML(diskBytes, mergedReq)
		if merr != nil {
			return nil, nil, merr
		}
		mergedBytes, merr = patchTopLevelExtensionsAST(mergedBytes, mergedReq.Extensions)
		if merr != nil {
			return nil, nil, merr
		}
		rebuiltCS.Update(localEntry.RelPath, mergedBytes, localEntry.BaselineDigest)
		idMappings = append(idMappings, IDMapping{
			EntityKind: "request",
			SourceID:   opKey,
			TargetID:   mergedReq.ID,
			TargetPath: localEntry.RelPath,
			Action:     "update",
		})
	}

	// Identify local-only HTTP requests (not present in upstream spec)
	for _, reqID := range sortedMapKeys(p.Requests) {
		if matchedLocalReqIDs[reqID] {
			continue
		}
		entry := p.Requests[reqID]
		if entry == nil || entry.Request == nil || !isHTTPProtocol(entry.Request.Protocol) || entry.Request.HTTP == nil {
			continue
		}
		contract, _ := GetRequestOpenAPI(entry.Request)
		m := strings.ToUpper(strings.TrimSpace(entry.Request.HTTP.Method))
		opPath := deriveOperationPath(entry.Request, contract)
		preview.LocalOnlyCount++
		preview.Operations = append(preview.Operations, OperationSyncItem{
			OperationKey: m + " " + opPath,
			RequestID:    reqID,
			RelPath:      entry.RelPath,
			Kind:         SyncLocalOnly,
			Summary:      entry.Request.Name,
		})
	}

	csPreviews, err := rebuiltCS.Preview()
	if err != nil {
		return nil, nil, err
	}

	blockingConflicts := preview.ConflictCount > 0 &&
		opts.InteropOptions.ConflictPolicy != ConflictOverwrite &&
		opts.InteropOptions.ConflictPolicy != ConflictSkip

	canApply := !blockingConflicts &&
		len(conflictStrings) == 0 &&
		(!opts.StrictLossReject || len(basePlan.preview.Losses) == 0) &&
		len(csPreviews) > 0

	summaryMsg := fmt.Sprintf("Sync OpenAPI %s (%s): +%d added, ~%d updated, =%d unchanged, %d local-only, !%d conflict(s)",
		valReport.Version, filename, preview.AddedCount, preview.UpdatedCount, preview.UnchangedCount, preview.LocalOnlyCount, preview.ConflictCount)

	basePlan.changeSet = rebuiltCS
	basePlan.preview.Summary = summaryMsg
	basePlan.preview.ConflictPolicy = opts.InteropOptions.ConflictPolicy
	basePlan.preview.Changes = csPreviews
	basePlan.preview.IDMappings = idMappings
	basePlan.preview.Losses = losses
	basePlan.preview.Conflicts = conflictStrings
	basePlan.preview.CanApply = canApply

	return basePlan, preview, nil
}

func mergeThreeWayOperation(
	localReq *Request,
	localContract *OperationContract,
	upReq *Request,
	upContract *OperationContract,
	opKey string,
) (*Request, *OperationContract, []string, []SyncConflictItem, bool) {
	merged := *upReq
	merged.ID = localReq.ID
	if upContract == nil {
		upContract = &OperationContract{}
	}
	mergedContract := *upContract

	var preserved []string
	var conflicts []SyncConflictItem

	var base *ContractSourceRef
	if localContract != nil {
		base = localContract.Source
	}

	// 1. Preserve local execution-only settings (Timeout, Variables, extra Assertions)
	if localReq.Timeout != "" {
		merged.Timeout = localReq.Timeout
		preserved = append(preserved, "timeout")
	}
	if len(localReq.Variables) > 0 {
		varsCopy := make(map[string]string, len(localReq.Variables))
		for k, v := range localReq.Variables {
			varsCopy[k] = v
		}
		merged.Variables = varsCopy
		preserved = append(preserved, "variables")
	}
	if len(localReq.Assert) > 0 {
		merged.Assert = mergePreservedAssertions(localReq.Assert, upReq.Assert)
		preserved = append(preserved, "assert")
	}
	if localReq.Auth != nil && upReq.Auth == nil {
		merged.Auth = CloneAuthRef(localReq.Auth)
		preserved = append(preserved, "auth")
	}

	// 2. Check Summary / Name 3-way conflict
	baseSummary := ""
	if base != nil {
		baseSummary = strings.TrimSpace(base.BaselineSummary)
	}
	localSummary := strings.TrimSpace(localReq.Name)
	if localContract != nil && strings.TrimSpace(localContract.Summary) != "" {
		localSummary = strings.TrimSpace(localContract.Summary)
	}
	upSummary := strings.TrimSpace(upReq.Name)
	if upContract != nil && strings.TrimSpace(upContract.Summary) != "" {
		upSummary = strings.TrimSpace(upContract.Summary)
	}

	if base != nil && baseSummary != "" {
		localChangedSummary := localSummary != baseSummary
		upChangedSummary := upSummary != baseSummary
		if localChangedSummary && upChangedSummary && localSummary != upSummary {
			conflicts = append(conflicts, SyncConflictItem{
				RequestID:     localReq.ID,
				OperationKey:  opKey,
				Field:         "summary",
				BaselineValue: baseSummary,
				LocalValue:    localSummary,
				UpstreamValue: upSummary,
				Message:       fmt.Sprintf("3-way conflict on %s summary: local modified %q -> %q while upstream modified to %q", opKey, baseSummary, localSummary, upSummary),
			})
		} else if localChangedSummary && !upChangedSummary {
			merged.Name = localReq.Name
			mergedContract.Summary = localSummary
			preserved = append(preserved, "summary")
		}
	}

	// 3. Merge Query parameters (preserve local {{var}} bindings and local extra query params; detect 3-way conflict)
	if localReq.HTTP != nil && merged.HTTP != nil {
		var baseQuery map[string]string
		if base != nil {
			baseQuery = base.BaselineQuery
		}
		qMerged, qPreserved, qConflicts := mergeThreeWayKVs(
			localReq.ID, opKey, "query",
			baseQuery, localReq.HTTP.Query, merged.HTTP.Query,
		)
		merged.HTTP.Query = qMerged
		if qPreserved {
			preserved = append(preserved, "http.query")
		}
		conflicts = append(conflicts, qConflicts...)

		// 4. Merge Header parameters
		var baseHeaders map[string]string
		if base != nil {
			baseHeaders = base.BaselineHeaders
		}
		hMerged, hPreserved, hConflicts := mergeThreeWayKVs(
			localReq.ID, opKey, "headers",
			baseHeaders, localReq.HTTP.Headers, merged.HTTP.Headers,
		)
		merged.HTTP.Headers = hMerged
		if hPreserved {
			preserved = append(preserved, "http.headers")
		}
		conflicts = append(conflicts, hConflicts...)

		// 5. Check RequestBody 3-way conflict
		localBodyType := ""
		localBodyText := ""
		if localReq.HTTP.Body != nil {
			localBodyType = localReq.HTTP.Body.Type
			localBodyText = BodyEditText(localReq.HTTP.Body)
		}
		upBodyType := ""
		upBodyText := ""
		if merged.HTTP.Body != nil {
			upBodyType = merged.HTTP.Body.Type
			upBodyText = BodyEditText(merged.HTTP.Body)
		}
		if base != nil && (base.BaselineBodyType != "" || base.BaselineBodyHash != "") {
			localBodyHash := ""
			if localBodyType != "" {
				localBodyHash = Digest([]byte(localBodyText))
			}
			upBodyHash := ""
			if upBodyType != "" {
				upBodyHash = Digest([]byte(upBodyText))
			}
			localChangedBody := localBodyType != base.BaselineBodyType || localBodyHash != base.BaselineBodyHash
			upChangedBody := upBodyType != base.BaselineBodyType || upBodyHash != base.BaselineBodyHash
			if localChangedBody && upChangedBody && (localBodyType != upBodyType || localBodyHash != upBodyHash) {
				conflicts = append(conflicts, SyncConflictItem{
					RequestID:     localReq.ID,
					OperationKey:  opKey,
					Field:         "http.body",
					BaselineValue: base.BaselineBodyType,
					LocalValue:    localBodyType,
					UpstreamValue: upBodyType,
					Message:       fmt.Sprintf("3-way conflict on %s request body: both local request and upstream OpenAPI spec modified the request body", opKey),
				})
			} else if localChangedBody && !upChangedBody && localReq.HTTP.Body != nil {
				merged.HTTP.Body = CloneBody(localReq.HTTP.Body)
				preserved = append(preserved, "http.body")
			}
		}
	}

	// Update ContractSourceRef baseline on mergedContract so subsequent syncs use the new baseline
	specPath := ""
	specDigest := ""
	specVersion := ""
	if mergedContract.Source != nil {
		specPath = mergedContract.Source.SpecPath
		specDigest = mergedContract.Source.SpecDigest
		specVersion = mergedContract.Source.SpecVersion
	}
	mergedContract.Source = buildContractSourceRef(
		specPath,
		specDigest,
		specVersion,
		deriveOperationPath(&merged, &mergedContract),
		strings.ToUpper(merged.HTTP.Method),
		mergedContract.OperationID,
		&merged,
		&mergedContract,
	)

	changed := didOperationChange(localReq, localContract, &merged, &mergedContract)
	return &merged, &mergedContract, preserved, conflicts, changed
}

func mergeThreeWayKVs(
	reqID, opKey, fieldPrefix string,
	baseMap map[string]string,
	localKVs []KV,
	upKVs []KV,
) ([]KV, bool, []SyncConflictItem) {
	localByName := map[string]KV{}
	for _, kv := range localKVs {
		localByName[kv.Name] = kv
	}
	upSeen := map[string]bool{}
	var out []KV
	var conflicts []SyncConflictItem
	preservedAny := false

	for _, upKV := range upKVs {
		upSeen[upKV.Name] = true
		locKV, hasLocal := localByName[upKV.Name]
		if !hasLocal {
			out = append(out, upKV)
			continue
		}
		baseVal, hasBase := baseMap[upKV.Name]
		if !hasBase && baseMap != nil {
			// Parameter was added in both local and upstream
			if locKV.Value != upKV.Value && locKV.Value != "" && upKV.Value != "" {
				conflicts = append(conflicts, SyncConflictItem{
					RequestID:     reqID,
					OperationKey:  opKey,
					Field:         fieldPrefix + "." + upKV.Name,
					BaselineValue: "",
					LocalValue:    locKV.Value,
					UpstreamValue: upKV.Value,
					Message:       fmt.Sprintf("3-way conflict on %s.%s: added locally as %q and upstream as %q", fieldPrefix, upKV.Name, locKV.Value, upKV.Value),
				})
			}
			out = append(out, upKV)
			continue
		}

		localChanged := hasBase && locKV.Value != baseVal
		upChanged := hasBase && upKV.Value != baseVal
		if localChanged && upChanged && locKV.Value != upKV.Value {
			conflicts = append(conflicts, SyncConflictItem{
				RequestID:     reqID,
				OperationKey:  opKey,
				Field:         fieldPrefix + "." + upKV.Name,
				BaselineValue: baseVal,
				LocalValue:    locKV.Value,
				UpstreamValue: upKV.Value,
				Message:       fmt.Sprintf("3-way conflict on %s.%s: local modified %q -> %q while upstream modified to %q", fieldPrefix, upKV.Name, baseVal, locKV.Value, upKV.Value),
			})
			out = append(out, upKV)
			continue
		}

		// Preserve local execution value if local changed it (or bound it to {{var}}) and upstream didn't conflict
		if localChanged || strings.Contains(locKV.Value, "{{") {
			preservedAny = true
			out = append(out, locKV)
		} else {
			out = append(out, upKV)
		}
	}

	// Keep local-only execution KVs (that were not removed by upstream from baseline)
	for _, locKV := range localKVs {
		if upSeen[locKV.Name] {
			continue
		}
		if baseMap != nil {
			if _, existedInBaseline := baseMap[locKV.Name]; existedInBaseline {
				// Upstream intentionally removed this parameter from the contract
				continue
			}
		}
		preservedAny = true
		out = append(out, locKV)
	}

	return out, preservedAny, conflicts
}

func mergePreservedAssertions(localAsserts, upAsserts []yaml.Node) []yaml.Node {
	if len(upAsserts) == 0 {
		return slices.Clone(localAsserts)
	}
	out := slices.Clone(upAsserts)
	for _, la := range localAsserts {
		var m map[string]any
		if err := la.Decode(&m); err == nil {
			t := strings.ToLower(stringField(m, "type"))
			if t == "schema" || t == "status" {
				// Upstream schema/status assertion is already in upAsserts
				continue
			}
		}
		out = append(out, *cloneYAMLNode(&la, true))
	}
	return out
}

func didOperationChange(
	localReq *Request,
	localContract *OperationContract,
	mergedReq *Request,
	mergedContract *OperationContract,
) bool {
	if localReq.Name != mergedReq.Name {
		return true
	}
	if localReq.HTTP != nil && mergedReq.HTTP != nil {
		if localReq.HTTP.Method != mergedReq.HTTP.Method || localReq.HTTP.URL != mergedReq.HTTP.URL {
			return true
		}
		if !kvsEqual(localReq.HTTP.Query, mergedReq.HTTP.Query) {
			return true
		}
		if !kvsEqual(localReq.HTTP.Headers, mergedReq.HTTP.Headers) {
			return true
		}
	}
	lcCopy := cloneContractWithoutDigest(localContract)
	mcCopy := cloneContractWithoutDigest(mergedContract)
	b1, _ := json.Marshal(lcCopy)
	b2, _ := json.Marshal(mcCopy)
	return !bytes.Equal(b1, b2)
}

func kvsEqual(a, b []KV) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Value != b[i].Value || a[i].IsEnabled() != b[i].IsEnabled() {
			return false
		}
	}
	return true
}

func cloneContractWithoutDigest(c *OperationContract) OperationContract {
	if c == nil {
		return OperationContract{}
	}
	cp := *c
	cp.Source = nil
	return cp
}
