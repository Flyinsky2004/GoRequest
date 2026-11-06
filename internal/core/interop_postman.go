package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	postmanSchemaV21 = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
	postmanSchemaV20 = "https://schema.getpostman.com/json/collection/v2.0.0/collection.json"
)

// PostmanExportOptions configures exporting a GoRequest project or subtree to
// Postman Collection v2.1.0 JSON.
type PostmanExportOptions struct {
	Scope         PortableScope
	TargetRelPath string
}

// StagePostmanCollectionImport parses a Postman Collection JSON (v2.1.0 or v2.0.0)
// and stages all folders, requests, and collection variables into a ChangeSet
// without executing any scripts or touching disk before Apply.
func StagePostmanCollectionImport(p *Project, data []byte, opts ImportOptions) (*ImportPlan, error) {
	if p == nil || p.Store == nil {
		return nil, errors.New("target project is required")
	}
	opts = opts.normalized()

	var rawTop map[string]any
	if err := json.Unmarshal(data, &rawTop); err != nil {
		return nil, fmt.Errorf("invalid Postman Collection JSON: %w", err)
	}

	if oa, ok := rawTop["openapi"].(string); ok && oa != "" {
		return buildOpenAPITriagePlan(p, FormatOpenAPI, oa, "collection.json", opts), nil
	}
	if sw, ok := rawTop["swagger"].(string); ok && sw != "" {
		return buildOpenAPITriagePlan(p, FormatSwagger2, sw, "collection.json", opts), nil
	}

	if _, hasReqs := rawTop["requests"].([]any); hasReqs {
		if _, hasInfo := rawTop["info"]; !hasInfo {
			msg := "Postman Collection v1.0 legacy format is not supported; please export as Postman Collection v2.1.0 or v2.0.0"
			return &ImportPlan{
				project:  p,
				redactor: opts.Redactor,
				preview: ImportPreview{
					Format:         FormatPostmanCollection,
					FormatVersion:  "v1.0",
					Summary:        msg,
					ConflictPolicy: opts.ConflictPolicy,
					Losses: []LossItem{
						{
							Code:     "postman-v1-unsupported",
							Severity: LossBlocked,
							Message:  msg,
						},
					},
					CanApply: false,
				},
			}, nil
		}
	}

	var col postmanCollectionRaw
	if err := json.Unmarshal(data, &col); err != nil {
		return nil, fmt.Errorf("parse Postman Collection structure: %w", err)
	}

	version := detectPostmanCollectionSchemaVersion(col.Info.Schema)
	if version != "v2.1.0" && version != "v2.0.0" {
		msg := fmt.Sprintf("unsupported Postman Collection schema %q (supported versions: v2.1.0, v2.0.0)", col.Info.Schema)
		return &ImportPlan{
			project:  p,
			redactor: opts.Redactor,
			preview: ImportPreview{
				Format:         FormatPostmanCollection,
				FormatVersion:  version,
				Summary:        msg,
				ConflictPolicy: opts.ConflictPolicy,
				Losses: []LossItem{
					{
						Code:     "postman-schema-unsupported",
						Severity: LossBlocked,
						Field:    "info.schema",
						Message:  msg,
					},
				},
				CanApply: false,
			},
		}, nil
	}

	targetBaseDir, err := normalizeTargetCollectionFolder(opts.TargetFolderRel)
	if err != nil {
		return nil, fmt.Errorf("invalid target folder: %w", err)
	}

	cs := p.Store.NewChangeSet()
	usedIDs := make(map[string]bool)
	usedPaths := make(map[string]bool)
	var (
		mappings       []IDMapping
		variables      []VariableSourceNote
		secretBindings []string
		localSlots     []string
		losses         []LossItem
		conflicts      []string
		importedReqs   []string
	)
	secretSet := make(map[string]bool)
	slotSet := make(map[string]bool)
	secretVarRename := make(map[string]string)

	addSecret := func(slot, rawVal, scope, src string) string {
		slot = sanitizeSecretSlotName(slot, "postman_secret")
		if rawVal != "" {
			opts.Redactor.RegisterSecret(rawVal)
		}
		if !secretSet[slot] {
			secretSet[slot] = true
			secretBindings = append(secretBindings, slot)
		}
		ref := "{{secret." + slot + "}}"
		variables = append(variables, VariableSourceNote{
			Name:          slot,
			Scope:         scope,
			Source:        src,
			IsSecret:      true,
			MappedRef:     ref,
			MaskedPreview: redactedMask,
		})
		return ref
	}

	addLocalSlot := func(srcPath, sourceLoc, field string) string {
		base := filepath.Base(strings.ReplaceAll(srcPath, "\\", "/"))
		slot := sanitizeSecretSlotName(strings.TrimSuffix(base, filepath.Ext(base)), "file")
		if !slotSet[slot] {
			slotSet[slot] = true
			localSlots = append(localSlots, slot)
		}
		losses = append(losses, LossItem{
			Code:       "postman-file-to-local-slot",
			Severity:   LossSecurityScrubbed,
			SourcePath: sourceLoc,
			Field:      field,
			Message:    fmt.Sprintf("Postman file reference %q converted to logical slot %s (host path not imported)", filepath.Base(srcPath), FormatLocalFileRef(slot)),
		})
		return slot
	}

	recordPostmanEvents(col.Event, col.Info.Name, "", &losses)
	if len(col.ProtocolProfileBehavior) > 0 {
		losses = append(losses, LossItem{
			Code:       "postman-protocol-profile-ignored",
			Severity:   LossUnsupported,
			SourcePath: col.Info.Name,
			Field:      "protocolProfileBehavior",
			Message:    "collection protocolProfileBehavior is not mapped to GoRequest YAML",
		})
	}

	colName := strings.TrimSpace(col.Info.Name)
	if colName == "" {
		colName = "Postman Collection"
	}
	colSlug := sanitizeSlug(colName, "postman-collection")

	if len(col.Variable) > 0 {
		envDesiredID := strings.TrimSpace(opts.TargetEnvID)
		if envDesiredID == "" {
			envDesiredID = colSlug + "-env"
		} else {
			envDesiredID = sanitizeSlug(envDesiredID, colSlug+"-env")
		}
		envObj := &Environment{
			SchemaVersion: SupportedSchemaVersion,
			ID:            envDesiredID,
			Variables:     map[string]string{},
		}
		for _, v := range col.Variable {
			if v.Disabled {
				continue
			}
			key := strings.TrimSpace(v.Key)
			if key == "" {
				continue
			}
			valStr := fmt.Sprint(v.Value)
			if v.Value == nil {
				valStr = ""
			}
			isSec := strings.EqualFold(v.Type, "secret") || (opts.ScrubSecretsToRefs && isSensitiveKeyName(key) && valStr != "" && !strings.Contains(valStr, "{{"))
			if isSec {
				slot := sanitizeSecretSlotName(key, "secret")
				if valStr != "" {
					opts.Redactor.RegisterSecret(valStr)
				}
				if !slices.Contains(envObj.Secrets, slot) {
					envObj.Secrets = append(envObj.Secrets, slot)
				}
				if !secretSet[slot] {
					secretSet[slot] = true
					secretBindings = append(secretBindings, slot)
				}
				secretVarRename[key] = slot
				variables = append(variables, VariableSourceNote{
					Name:          key,
					Scope:         "collection",
					Source:        "postman-collection",
					TargetEnvID:   envDesiredID,
					IsSecret:      true,
					MappedRef:     "{{secret." + slot + "}}",
					MaskedPreview: redactedMask,
				})
			} else {
				envObj.Variables[key] = valStr
				variables = append(variables, VariableSourceNote{
					Name:          key,
					Scope:         "collection",
					Source:        "postman-collection",
					TargetEnvID:   envDesiredID,
					IsSecret:      false,
					MappedRef:     "{{" + key + "}}",
					MaskedPreview: opts.Redactor.RedactString(valStr),
				})
			}
		}

		finalEnvID, finalEnvRel, envAction, envBaseline, envConflict := resolveEntityCollision(
			p,
			opts.ConflictPolicy,
			"environment",
			envDesiredID,
			"environments",
			".yaml",
			usedIDs,
			usedPaths,
		)
		envObj.ID = finalEnvID
		if envConflict != "" {
			conflicts = append(conflicts, envConflict)
		}
		mappings = append(mappings, IDMapping{
			EntityKind: "environment",
			SourceID:   envDesiredID,
			TargetID:   finalEnvID,
			SourcePath: colName + "/variable",
			TargetPath: filepath.ToSlash(finalEnvRel),
			Action:     envAction,
			Renamed:    finalEnvID != envDesiredID,
		})
		if envAction != "conflict" && envAction != "skip" {
			var existingEnvBytes []byte
			if envAction == "update" {
				if p.Store.Exists(finalEnvRel) {
					existingEnvBytes, _, _ = p.Store.Read(finalEnvRel)
				}
				if existing, ok := p.Environments[finalEnvID]; ok && existing != nil {
					mergedVars := make(map[string]string, len(existing.Variables)+len(envObj.Variables))
					for k, v := range existing.Variables {
						mergedVars[k] = v
					}
					for k, v := range envObj.Variables {
						mergedVars[k] = v
					}
					envObj.Variables = mergedVars
					for _, s := range existing.Secrets {
						if !slices.Contains(envObj.Secrets, s) {
							envObj.Secrets = append(envObj.Secrets, s)
						}
					}
				}
			}
			envBytes, err := MergeEnvironmentYAML(existingEnvBytes, envObj)
			if err != nil {
				return nil, fmt.Errorf("encode collection environment: %w", err)
			}
			if envAction == "update" {
				cs.Update(finalEnvRel, envBytes, envBaseline)
			} else {
				cs.Create(finalEnvRel, envBytes)
			}
		}
	}

	rootFolderDir := path.Join(targetBaseDir, colSlug)
	if targetBaseDir != "collections" && strings.HasSuffix(targetBaseDir, "/"+colSlug) {
		rootFolderDir = targetBaseDir
	}

	if col.Auth != nil {
		fAuth := convertPostmanAuth(col.Auth, colName, "", opts.ScrubSecretsToRefs, addSecret, secretVarRename, &losses)
		if fAuth != nil {
			stageFolderCollectionChange(p, cs, opts.ConflictPolicy, rootFolderDir, colSlug, colName, fAuth, usedIDs, usedPaths, &mappings, &conflicts)
		}
	}

	var walkItems func(items []postmanItemRaw, parentDir, sourcePrefix string) error
	walkItems = func(items []postmanItemRaw, parentDir, sourcePrefix string) error {
		for idx, it := range items {
			itemName := strings.TrimSpace(it.Name)
			if itemName == "" {
				itemName = fmt.Sprintf("item-%d", idx+1)
			}
			itemSource := itemName
			if sourcePrefix != "" {
				itemSource = sourcePrefix + "/" + itemName
			}

			if len(it.Item) > 0 || len(it.Request) == 0 {
				folderSlug := sanitizeSlug(itemName, fmt.Sprintf("folder-%d", idx+1))
				folderDir := path.Join(parentDir, folderSlug)
				recordPostmanEvents(it.Event, itemSource, folderDir, &losses)
				if len(it.ProtocolProfileBehavior) > 0 {
					losses = append(losses, LossItem{
						Code:       "postman-protocol-profile-ignored",
						Severity:   LossUnsupported,
						SourcePath: itemSource,
						TargetPath: folderDir,
						Field:      "protocolProfileBehavior",
						Message:    "folder protocolProfileBehavior is not mapped to GoRequest YAML",
					})
				}
				if it.Auth != nil {
					fAuth := convertPostmanAuth(it.Auth, itemSource, folderDir, opts.ScrubSecretsToRefs, addSecret, secretVarRename, &losses)
					if fAuth != nil {
						stageFolderCollectionChange(p, cs, opts.ConflictPolicy, folderDir, folderSlug, itemName, fAuth, usedIDs, usedPaths, &mappings, &conflicts)
					}
				}
				if err := walkItems(it.Item, folderDir, itemSource); err != nil {
					return err
				}
				continue
			}

			reqSlug := sanitizeSlug(itemName, fmt.Sprintf("request-%d", idx+1))
			desiredID := "req-" + reqSlug
			finalID, finalRel, action, baseline, conflictMsg := resolveEntityCollision(
				p,
				opts.ConflictPolicy,
				"request",
				desiredID,
				parentDir,
				".request.yaml",
				usedIDs,
				usedPaths,
			)
			if conflictMsg != "" {
				conflicts = append(conflicts, conflictMsg)
			}
			mappings = append(mappings, IDMapping{
				EntityKind: "request",
				SourceID:   desiredID,
				TargetID:   finalID,
				SourcePath: itemSource,
				TargetPath: filepath.ToSlash(finalRel),
				Action:     action,
				Renamed:    finalID != desiredID,
			})

			recordPostmanEvents(it.Event, itemSource, finalRel, &losses)
			if len(it.Response) > 0 {
				losses = append(losses, LossItem{
					Code:       "postman-saved-response-ignored",
					Severity:   LossUnsupported,
					SourcePath: itemSource,
					TargetPath: finalRel,
					Field:      "response",
					Message:    fmt.Sprintf("%d saved response example(s) omitted; GoRequest stores request definitions only", len(it.Response)),
				})
			}
			if len(it.ProtocolProfileBehavior) > 0 {
				losses = append(losses, LossItem{
					Code:       "postman-protocol-profile-ignored",
					Severity:   LossUnsupported,
					SourcePath: itemSource,
					TargetPath: finalRel,
					Field:      "protocolProfileBehavior",
					Message:    "request protocolProfileBehavior omitted",
				})
			}

			reqObj, err := convertPostmanRequest(
				it.Request,
				finalID,
				itemName,
				itemSource,
				finalRel,
				opts,
				addSecret,
				addLocalSlot,
				secretVarRename,
				&losses,
			)
			if err != nil {
				return fmt.Errorf("convert request %q: %w", itemSource, err)
			}

			if action != "conflict" && action != "skip" {
				var existingBytes []byte
				if action == "update" && p.Store.Exists(finalRel) {
					existingBytes, _, _ = p.Store.Read(finalRel)
				}
				yamlBytes, err := MergeRequestYAML(existingBytes, reqObj)
				if err != nil {
					return fmt.Errorf("encode request %q: %w", finalID, err)
				}
				if action == "update" {
					cs.Update(finalRel, yamlBytes, baseline)
				} else {
					cs.Create(finalRel, yamlBytes)
				}
				importedReqs = append(importedReqs, finalID)
			}
		}
		return nil
	}

	if err := walkItems(col.Item, rootFolderDir, colName); err != nil {
		return nil, err
	}

	if len(col.Variable) == 0 && len(secretBindings) > 0 {
		envID := strings.TrimSpace(opts.TargetEnvID)
		if envID == "" {
			envID = defaultProjectEnvID(p)
		}
		if envID != "" {
			_ = stageSecretSlotsOnEnv(p, cs, envID, secretBindings, usedPaths)
		}
	}

	changePreviews, err := cs.Preview()
	if err != nil {
		return nil, err
	}

	for i := range losses {
		losses[i].Message = opts.Redactor.RedactString(losses[i].Message)
	}

	canApply := len(conflicts) == 0 && len(changePreviews) > 0
	summary := fmt.Sprintf("Postman Collection (%s) %q: %d file change(s), %d loss/warning note(s)", version, colName, len(changePreviews), len(losses))
	if len(conflicts) > 0 {
		summary = fmt.Sprintf("Postman Collection (%s) %q blocked by %d conflict(s)", version, colName, len(conflicts))
	}

	return &ImportPlan{
		project:      p,
		changeSet:    cs,
		importedReqs: importedReqs,
		redactor:     opts.Redactor,
		preview: ImportPreview{
			Format:         FormatPostmanCollection,
			FormatVersion:  version,
			Summary:        summary,
			ConflictPolicy: opts.ConflictPolicy,
			Changes:        changePreviews,
			IDMappings:     mappings,
			Variables:      variables,
			SecretBindings: secretBindings,
			LocalFileSlots: localSlots,
			Losses:         losses,
			Conflicts:      conflicts,
			CanApply:       canApply,
		},
	}, nil
}

// StagePostmanEnvironmentImport parses a Postman Environment JSON and stages
// an Environment file (.gorequest/environments/<id>.yaml), keeping secret
// variables in `secrets` without writing raw secret values to disk.
func StagePostmanEnvironmentImport(p *Project, data []byte, opts ImportOptions) (*ImportPlan, error) {
	if p == nil || p.Store == nil {
		return nil, errors.New("target project is required")
	}
	opts = opts.normalized()

	var rawEnv postmanEnvironmentRaw
	if err := json.Unmarshal(data, &rawEnv); err != nil {
		return nil, fmt.Errorf("invalid Postman Environment JSON: %w", err)
	}
	envName := strings.TrimSpace(rawEnv.Name)
	if envName == "" {
		envName = "Imported Environment"
	}
	desiredID := strings.TrimSpace(opts.TargetEnvID)
	if desiredID == "" {
		desiredID = sanitizeSlug(envName, "imported-env")
	} else {
		desiredID = sanitizeSlug(desiredID, "imported-env")
	}

	usedIDs := make(map[string]bool)
	usedPaths := make(map[string]bool)
	finalID, finalRel, action, baseline, conflictMsg := resolveEntityCollision(
		p,
		opts.ConflictPolicy,
		"environment",
		desiredID,
		"environments",
		".yaml",
		usedIDs,
		usedPaths,
	)

	envObj := &Environment{
		SchemaVersion: SupportedSchemaVersion,
		ID:            finalID,
		Variables:     map[string]string{},
	}

	var (
		variables      []VariableSourceNote
		secretBindings []string
		losses         []LossItem
		conflicts      []string
	)
	if conflictMsg != "" {
		conflicts = append(conflicts, conflictMsg)
	}

	for _, valEntry := range rawEnv.Values {
		if !valEntry.EnabledIsTrue() {
			losses = append(losses, LossItem{
				Code:       "postman-env-disabled-var-omitted",
				Severity:   LossInfo,
				SourcePath: envName,
				TargetPath: finalRel,
				Field:      valEntry.Key,
				Message:    fmt.Sprintf("disabled environment variable %q omitted", valEntry.Key),
			})
			continue
		}
		key := strings.TrimSpace(valEntry.Key)
		if key == "" {
			continue
		}
		valStr := fmt.Sprint(valEntry.Value)
		if valEntry.Value == nil {
			valStr = ""
		}
		isSec := strings.EqualFold(valEntry.Type, "secret") || (opts.ScrubSecretsToRefs && isSensitiveKeyName(key) && valStr != "" && !strings.Contains(valStr, "{{"))
		if isSec {
			slot := sanitizeSecretSlotName(key, "secret")
			if valStr != "" {
				opts.Redactor.RegisterSecret(valStr)
			}
			if !slices.Contains(envObj.Secrets, slot) {
				envObj.Secrets = append(envObj.Secrets, slot)
			}
			if !slices.Contains(secretBindings, slot) {
				secretBindings = append(secretBindings, slot)
			}
			variables = append(variables, VariableSourceNote{
				Name:          key,
				Scope:         "environment",
				Source:        "postman-env",
				TargetEnvID:   finalID,
				IsSecret:      true,
				MappedRef:     "{{secret." + slot + "}}",
				MaskedPreview: redactedMask,
			})
			losses = append(losses, LossItem{
				Code:       "postman-secret-value-scrubbed",
				Severity:   LossSecurityScrubbed,
				SourcePath: envName,
				TargetPath: finalRel,
				Field:      key,
				Message:    fmt.Sprintf("secret variable %q value excluded from shared YAML; declared slot %q in secrets list", key, slot),
			})
		} else {
			envObj.Variables[key] = valStr
			variables = append(variables, VariableSourceNote{
				Name:          key,
				Scope:         "environment",
				Source:        "postman-env",
				TargetEnvID:   finalID,
				IsSecret:      false,
				MappedRef:     "{{" + key + "}}",
				MaskedPreview: opts.Redactor.RedactString(valStr),
			})
		}
	}

	cs := p.Store.NewChangeSet()
	if action != "conflict" && action != "skip" {
		var existingBytes []byte
		if action == "update" && p.Store.Exists(finalRel) {
			existingBytes, _, _ = p.Store.Read(finalRel)
		}
		encoded, err := MergeEnvironmentYAML(existingBytes, envObj)
		if err != nil {
			return nil, fmt.Errorf("encode environment %q: %w", finalID, err)
		}
		if action == "update" {
			cs.Update(finalRel, encoded, baseline)
		} else {
			cs.Create(finalRel, encoded)
		}
	}

	changePreviews, err := cs.Preview()
	if err != nil {
		return nil, err
	}

	canApply := len(conflicts) == 0 && len(changePreviews) > 0
	summary := fmt.Sprintf("Postman Environment %q -> %s (%d variable(s), %d secret slot(s))", envName, filepath.ToSlash(finalRel), len(envObj.Variables), len(envObj.Secrets))
	if len(conflicts) > 0 {
		summary = fmt.Sprintf("Postman Environment %q blocked by conflict", envName)
	}

	return &ImportPlan{
		project:   p,
		changeSet: cs,
		redactor:  opts.Redactor,
		preview: ImportPreview{
			Format:         FormatPostmanEnvironment,
			FormatVersion:  "v2.1.0",
			Summary:        summary,
			ConflictPolicy: opts.ConflictPolicy,
			Changes:        changePreviews,
			IDMappings: []IDMapping{
				{
					EntityKind: "environment",
					SourceID:   desiredID,
					TargetID:   finalID,
					SourcePath: envName,
					TargetPath: filepath.ToSlash(finalRel),
					Action:     action,
					Renamed:    finalID != desiredID,
				},
			},
			Variables:      variables,
			SecretBindings: secretBindings,
			Losses:         losses,
			Conflicts:      conflicts,
			CanApply:       canApply,
		},
	}, nil
}

// ExportPostmanCollection exports a GoRequest project (or a folder/request scope)
// to Postman Collection v2.1.0 JSON, scrubbing any inline secrets via Redactor
// and never exporting local host file paths.
func ExportPostmanCollection(p *Project, opts PostmanExportOptions) ([]byte, []LossItem, error) {
	if p == nil {
		return nil, nil, errors.New("project is required")
	}
	scope := opts.Scope
	if scope == "" {
		scope = ScopeProject
	}

	var losses []LossItem
	red := NewRedactor()

	buildReqItem := func(entry *RequestEntry) postmanExportItem {
		req := entry.Request
		httpDef := req.HTTP
		if httpDef == nil {
			httpDef = &HTTPDef{Method: "GET"}
		}
		var headers []postmanExportHeader
		for _, h := range httpDef.Headers {
			val := h.Value
			if isSensitiveKeyName(h.Name) && !strings.Contains(val, "{{") && val != "" {
				red.RegisterSecret(val)
				val = redactedMask
				losses = append(losses, LossItem{
					Code:       "postman-export-secret-scrubbed",
					Severity:   LossSecurityScrubbed,
					SourcePath: entry.RelPath,
					Field:      "headers." + h.Name,
					Message:    fmt.Sprintf("sensitive header %q scrubbed to %s", h.Name, redactedMask),
				})
			}
			headers = append(headers, postmanExportHeader{
				Key:      h.Name,
				Value:    val,
				Disabled: !h.IsEnabled(),
			})
		}

		var queries []postmanExportQuery
		var rawQueryParts []string
		for _, q := range httpDef.Query {
			val := q.Value
			if isSensitiveKeyName(q.Name) && !strings.Contains(val, "{{") && val != "" {
				val = redactedMask
			}
			queries = append(queries, postmanExportQuery{
				Key:      q.Name,
				Value:    val,
				Disabled: !q.IsEnabled(),
			})
			if q.IsEnabled() && q.Name != "" {
				rawQueryParts = append(rawQueryParts, q.Name+"="+val)
			}
		}

		rawURL := httpDef.URL
		if len(rawQueryParts) > 0 {
			sep := "?"
			if strings.Contains(rawURL, "?") {
				sep = "&"
			}
			rawURL += sep + strings.Join(rawQueryParts, "&")
		}

		var bodyOut *postmanExportBody
		if httpDef.Body != nil {
			if tb, _ := InspectBody(httpDef.Body); tb != nil {
				switch tb.Kind {
				case BodyJSON:
					rawText := ""
					if tb.JSONNode != nil {
						var obj any
						if err := tb.JSONNode.Decode(&obj); err == nil {
							if b, err := json.MarshalIndent(obj, "", "  "); err == nil {
								rawText = string(b)
							}
						}
					}
					bodyOut = &postmanExportBody{
						Mode: "raw",
						Raw:  rawText,
						Options: map[string]any{
							"raw": map[string]any{"language": "json"},
						},
					}
				case BodyXML:
					bodyOut = &postmanExportBody{
						Mode: "raw",
						Raw:  tb.Text,
						Options: map[string]any{
							"raw": map[string]any{"language": "xml"},
						},
					}
				case BodyText:
					bodyOut = &postmanExportBody{
						Mode: "raw",
						Raw:  tb.Text,
						Options: map[string]any{
							"raw": map[string]any{"language": "text"},
						},
					}
				case BodyURLEncoded:
					var ue []postmanExportKV
					for _, f := range tb.FormFields {
						ue = append(ue, postmanExportKV{
							Key:      f.Name,
							Value:    f.Value,
							Type:     "text",
							Disabled: !f.IsEnabled(),
						})
					}
					bodyOut = &postmanExportBody{
						Mode:       "urlencoded",
						URLEncoded: ue,
					}
				case BodyFormData:
					var fd []postmanExportFormPart
					for _, mp := range tb.MultipartParts {
						if mp.EffectiveKind() == "file" {
							srcRef := mp.File
							if mp.LocalFile != "" {
								srcRef = FormatLocalFileRef(mp.LocalFile)
								losses = append(losses, LossItem{
									Code:       "postman-export-local-slot",
									Severity:   LossSecurityScrubbed,
									SourcePath: entry.RelPath,
									Field:      mp.Name,
									Message:    fmt.Sprintf("multipart file %q exported as logical slot %s without host path", mp.Name, srcRef),
								})
							}
							fd = append(fd, postmanExportFormPart{
								Key:         mp.Name,
								Type:        "file",
								Src:         srcRef,
								ContentType: mp.ContentType,
								Disabled:    !mp.IsEnabled(),
							})
						} else {
							fd = append(fd, postmanExportFormPart{
								Key:         mp.Name,
								Type:        "text",
								Value:       mp.Value,
								ContentType: mp.ContentType,
								Disabled:    !mp.IsEnabled(),
							})
						}
					}
					bodyOut = &postmanExportBody{
						Mode:     "formdata",
						FormData: fd,
					}
				case BodyBinary:
					srcRef := tb.File
					if tb.LocalFile != "" {
						srcRef = FormatLocalFileRef(tb.LocalFile)
					}
					bodyOut = &postmanExportBody{
						Mode: "file",
						File: &postmanExportFile{Src: srcRef},
					}
				}
			}
		}

		var authOut *postmanExportAuth
		if req.Auth != nil {
			authOut = convertAuthRefToPostmanExport(p, req.Auth, entry.RelPath, &losses)
		}

		return postmanExportItem{
			Name: req.Name,
			Request: &postmanExportRequest{
				Method: httpDef.Method,
				Header: headers,
				URL: postmanExportURL{
					Raw:   rawURL,
					Query: queries,
				},
				Body: bodyOut,
				Auth: authOut,
			},
		}
	}

	var walkEntry func(ce *CollectionEntry) *postmanExportItem
	walkEntry = func(ce *CollectionEntry) *postmanExportItem {
		if ce == nil {
			return nil
		}
		var children []postmanExportItem
		for _, ch := range ce.Children {
			if converted := walkEntry(ch); converted != nil {
				children = append(children, *converted)
			}
		}
		for _, r := range ce.Requests {
			if r != nil && r.Request != nil {
				children = append(children, buildReqItem(r))
			}
		}
		var fAuthOut *postmanExportAuth
		if fAuth := p.FolderAuth(ce.RelPath); fAuth != nil {
			fAuthOut = convertAuthRefToPostmanExport(p, fAuth, ce.RelPath, &losses)
		}
		name := ce.Name
		if name == "" {
			name = filepath.Base(ce.RelPath)
		}
		return &postmanExportItem{
			Name: name,
			Item: children,
			Auth: fAuthOut,
		}
	}

	var rootItems []postmanExportItem
	colTitle := "GoRequest Export"
	if p.File != nil && p.File.Name != "" {
		colTitle = p.File.Name
	}

	switch scope {
	case ScopeRequest:
		norm := filepath.ToSlash(strings.TrimSpace(opts.TargetRelPath))
		var targetEntry *RequestEntry
		for _, id := range p.Order {
			r := p.Requests[id]
			if r != nil && (filepath.ToSlash(r.RelPath) == norm || r.Request.ID == norm) {
				targetEntry = r
				break
			}
		}
		if targetEntry == nil {
			return nil, nil, fmt.Errorf("request %q not found in project", opts.TargetRelPath)
		}
		colTitle = targetEntry.Request.Name
		rootItems = append(rootItems, buildReqItem(targetEntry))
	case ScopeFolder:
		norm := strings.Trim(filepath.ToSlash(strings.TrimSpace(opts.TargetRelPath)), "/")
		if !strings.HasPrefix(norm, "collections/") && norm != "collections" {
			norm = "collections/" + norm
		}
		targetCol := findCollectionEntryByRel(p.Collections, norm)
		if targetCol == nil {
			return nil, nil, fmt.Errorf("folder %q not found in project", opts.TargetRelPath)
		}
		if targetCol.Name != "" {
			colTitle = targetCol.Name
		} else {
			colTitle = filepath.Base(targetCol.RelPath)
		}
		if converted := walkEntry(targetCol); converted != nil {
			rootItems = converted.Item
		}
	default:
		for _, ce := range p.Collections {
			if ce.RelPath == "collections" {
				for _, ch := range ce.Children {
					if converted := walkEntry(ch); converted != nil {
						rootItems = append(rootItems, *converted)
					}
				}
				for _, r := range ce.Requests {
					if r != nil && r.Request != nil {
						rootItems = append(rootItems, buildReqItem(r))
					}
				}
			} else if converted := walkEntry(ce); converted != nil {
				rootItems = append(rootItems, *converted)
			}
		}
	}

	doc := postmanExportCollection{
		Info: postmanExportInfo{
			Name:   colTitle,
			Schema: postmanSchemaV21,
		},
		Item: rootItems,
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return encoded, losses, nil
}

// ExportPostmanEnvironment exports a GoRequest Environment to Postman
// Environment JSON format, emitting secret slots with empty value (`""`) and
// `type: "secret"` so real secret values are never leaked.
func ExportPostmanEnvironment(p *Project, envID string) ([]byte, []LossItem, error) {
	if p == nil {
		return nil, nil, errors.New("project is required")
	}
	if envID == "" {
		envID = defaultProjectEnvID(p)
	}
	env, ok := p.Environments[envID]
	if !ok || env == nil {
		return nil, nil, fmt.Errorf("environment %q not found", envID)
	}

	var values []postmanEnvValueRaw
	var losses []LossItem

	var varKeys []string
	for k := range env.Variables {
		varKeys = append(varKeys, k)
	}
	slices.Sort(varKeys)
	for _, k := range varKeys {
		v := env.Variables[k]
		if isSensitiveKeyName(k) && v != "" && !strings.Contains(v, "{{") {
			values = append(values, postmanEnvValueRaw{
				Key:     k,
				Value:   "",
				Type:    "secret",
				Enabled: BoolPtr(true),
			})
			losses = append(losses, LossItem{
				Code:       "postman-env-export-secret-scrubbed",
				Severity:   LossSecurityScrubbed,
				SourcePath: envID,
				Field:      k,
				Message:    fmt.Sprintf("variable %q matched sensitive key pattern; exported as secret with empty value", k),
			})
			continue
		}
		values = append(values, postmanEnvValueRaw{
			Key:     k,
			Value:   v,
			Type:    "default",
			Enabled: BoolPtr(true),
		})
	}

	for _, sec := range env.Secrets {
		values = append(values, postmanEnvValueRaw{
			Key:     sec,
			Value:   "",
			Type:    "secret",
			Enabled: BoolPtr(true),
		})
		losses = append(losses, LossItem{
			Code:       "postman-env-export-secret-slot",
			Severity:   LossSecurityScrubbed,
			SourcePath: envID,
			Field:      sec,
			Message:    fmt.Sprintf("secret slot %q exported with empty value (type=secret)", sec),
		})
	}

	out := map[string]any{
		"name":                    env.ID,
		"values":                  values,
		"_postman_variable_scope": "environment",
	}
	b, err := json.MarshalIndent(out, "", "  ")
	return b, losses, err
}

// --- Internal Postman structures & helpers ---

type postmanCollectionRaw struct {
	Info                    postmanInfoRaw    `json:"info"`
	Item                    []postmanItemRaw  `json:"item"`
	Event                   []postmanEventRaw `json:"event"`
	Variable                []postmanVarRaw   `json:"variable"`
	Auth                    *postmanAuthRaw   `json:"auth"`
	ProtocolProfileBehavior map[string]any    `json:"protocolProfileBehavior"`
}

type postmanInfoRaw struct {
	PostmanID string `json:"_postman_id"`
	Name      string `json:"name"`
	Schema    string `json:"schema"`
}

type postmanItemRaw struct {
	Name                    string            `json:"name"`
	Item                    []postmanItemRaw  `json:"item"`
	Request                 json.RawMessage   `json:"request"`
	Response                []json.RawMessage `json:"response"`
	Event                   []postmanEventRaw `json:"event"`
	Auth                    *postmanAuthRaw   `json:"auth"`
	ProtocolProfileBehavior map[string]any    `json:"protocolProfileBehavior"`
}

type postmanEventRaw struct {
	Listen string           `json:"listen"`
	Script postmanScriptRaw `json:"script"`
}

type postmanScriptRaw struct {
	Type string `json:"type"`
	Exec any    `json:"exec"`
}

type postmanVarRaw struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled"`
}

type postmanAuthRaw struct {
	Type   string          `json:"type"`
	Bearer json.RawMessage `json:"bearer"`
	Basic  json.RawMessage `json:"basic"`
	APIKey json.RawMessage `json:"apikey"`
}

type postmanEnvironmentRaw struct {
	ID     string               `json:"id"`
	Name   string               `json:"name"`
	Values []postmanEnvValueRaw `json:"values"`
	Scope  string               `json:"_postman_variable_scope"`
}

type postmanEnvValueRaw struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Type    string `json:"type"`
	Enabled *bool  `json:"enabled,omitempty"`
}

func (v postmanEnvValueRaw) EnabledIsTrue() bool {
	return v.Enabled == nil || *v.Enabled
}

func detectPostmanCollectionSchemaVersion(schema string) string {
	s := strings.ToLower(strings.TrimSpace(schema))
	switch {
	case strings.Contains(s, "/collection/v2.1.0/"):
		return "v2.1.0"
	case strings.Contains(s, "/collection/v2.0.0/"):
		return "v2.0.0"
	case s == "":
		return "unknown"
	default:
		return schema
	}
}

func recordPostmanEvents(events []postmanEventRaw, sourcePath, targetPath string, losses *[]LossItem) {
	for _, ev := range events {
		lines := countPostmanScriptLines(ev.Script.Exec)
		if lines == 0 {
			continue
		}
		listen := strings.TrimSpace(ev.Listen)
		if listen == "" {
			listen = "script"
		}
		*losses = append(*losses, LossItem{
			Code:       "postman-script-ignored",
			Severity:   LossUnsupported,
			SourcePath: sourcePath,
			TargetPath: targetPath,
			Field:      "event." + listen,
			Message:    fmt.Sprintf("Postman %s script (%d line(s)) was not imported and never executed", listen, lines),
		})
	}
}

func countPostmanScriptLines(exec any) int {
	switch v := exec.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return 0
		}
		return len(strings.Split(v, "\n"))
	case []any:
		nonEmpty := 0
		for _, item := range v {
			if strings.TrimSpace(fmt.Sprint(item)) != "" {
				nonEmpty++
			}
		}
		return nonEmpty
	default:
		return 0
	}
}

func parsePostmanAuthAttributes(raw json.RawMessage) map[string]string {
	out := make(map[string]string)
	if len(raw) == 0 {
		return out
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var arr []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(raw, &arr); err == nil {
			for _, entry := range arr {
				if entry.Key != "" && entry.Value != nil {
					out[entry.Key] = fmt.Sprint(entry.Value)
				}
			}
		}
	} else if strings.HasPrefix(trimmed, "{") {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err == nil {
			for k, v := range m {
				if v != nil {
					out[k] = fmt.Sprint(v)
				}
			}
		}
	}
	return out
}

func rewriteSecretVars(s string, secretVarRename map[string]string) string {
	if s == "" || len(secretVarRename) == 0 {
		return s
	}
	return templatePattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := templatePattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		key := strings.TrimSpace(sub[1])
		if slot, ok := secretVarRename[key]; ok {
			return "{{secret." + slot + "}}"
		}
		return match
	})
}

func convertPostmanAuth(
	auth *postmanAuthRaw,
	sourcePath, targetPath string,
	scrubSecrets bool,
	addSecret func(slot, rawVal, scope, src string) string,
	secretVarRename map[string]string,
	losses *[]LossItem,
) *AuthRef {
	if auth == nil {
		return nil
	}
	t := strings.ToLower(strings.TrimSpace(auth.Type))
	switch t {
	case "", "inherit":
		return nil
	case "noauth":
		return &AuthRef{Mode: "none"}
	case "bearer":
		attrs := parsePostmanAuthAttributes(auth.Bearer)
		token := rewriteSecretVars(attrs["token"], secretVarRename)
		if token == "" {
			token = rewriteSecretVars(attrs["bearer"], secretVarRename)
		}
		if scrubSecrets && token != "" && !strings.Contains(token, "{{") {
			slot := sanitizeSecretSlotName(filepath.Base(sourcePath)+"_bearer_token", "bearer_token")
			token = addSecret(slot, token, "request-auth", "postman-collection")
		}
		return &AuthRef{
			Mode: "inline",
			Inline: &AuthScheme{
				Type:  "bearer",
				Token: token,
			},
		}
	case "basic":
		attrs := parsePostmanAuthAttributes(auth.Basic)
		user := rewriteSecretVars(attrs["username"], secretVarRename)
		pass := rewriteSecretVars(attrs["password"], secretVarRename)
		if scrubSecrets && pass != "" && !strings.Contains(pass, "{{") {
			slot := sanitizeSecretSlotName(filepath.Base(sourcePath)+"_basic_password", "basic_password")
			pass = addSecret(slot, pass, "request-auth", "postman-collection")
		}
		return &AuthRef{
			Mode: "inline",
			Inline: &AuthScheme{
				Type:     "basic",
				Username: user,
				Password: pass,
			},
		}
	case "apikey":
		attrs := parsePostmanAuthAttributes(auth.APIKey)
		key := attrs["key"]
		val := rewriteSecretVars(attrs["value"], secretVarRename)
		inLoc := NormalizeAPIKeyLocation(attrs["in"])
		if inLoc == "" {
			inLoc = "header"
		}
		if scrubSecrets && val != "" && !strings.Contains(val, "{{") {
			slot := sanitizeSecretSlotName(key, "api_key")
			val = addSecret(slot, val, "request-auth", "postman-collection")
		}
		return &AuthRef{
			Mode: "inline",
			Inline: &AuthScheme{
				Type:  "apikey",
				Name:  key,
				Value: val,
				In:    inLoc,
			},
		}
	default:
		*losses = append(*losses, LossItem{
			Code:       "postman-auth-unsupported",
			Severity:   LossUnsupported,
			SourcePath: sourcePath,
			TargetPath: targetPath,
			Field:      "auth." + t,
			Message:    fmt.Sprintf("Postman auth type %q is not supported in B08 basic converter; downgraded to none", auth.Type),
		})
		return &AuthRef{Mode: "none"}
	}
}

func stageFolderCollectionChange(
	p *Project,
	cs *ChangeSet,
	policy ConflictPolicy,
	folderDir string,
	folderSlug string,
	folderName string,
	fAuth *AuthRef,
	usedIDs map[string]bool,
	usedPaths map[string]bool,
	mappings *[]IDMapping,
	conflicts *[]string,
) {
	rel := path.Join(folderDir, "collection.yaml")
	if usedPaths[rel] {
		return
	}
	var existingBytes []byte
	var baseline string
	if p != nil && p.Store != nil && p.Store.Exists(rel) {
		if b, d, err := p.Store.Read(rel); err == nil {
			existingBytes = b
			baseline = d
		}
	}
	action := "create"
	if baseline != "" {
		switch policy {
		case ConflictSkip:
			*mappings = append(*mappings, IDMapping{
				EntityKind: "folder",
				SourceID:   folderSlug,
				TargetID:   folderSlug,
				TargetPath: rel,
				Action:     "skip",
			})
			return
		case ConflictFail:
			*conflicts = append(*conflicts, fmt.Sprintf("folder collection metadata already exists at %s", rel))
			*mappings = append(*mappings, IDMapping{
				EntityKind: "folder",
				SourceID:   folderSlug,
				TargetID:   folderSlug,
				TargetPath: rel,
				Action:     "conflict",
			})
			return
		default:
			action = "update"
		}
	}
	colObj := &Collection{
		SchemaVersion: SupportedSchemaVersion,
		ID:            folderSlug,
		Name:          folderName,
		Auth:          fAuth,
	}
	encoded, err := MergeCollectionYAML(existingBytes, colObj)
	if err != nil || len(encoded) == 0 {
		return
	}
	usedPaths[rel] = true
	usedIDs["folder:"+rel] = true
	if action == "update" {
		cs.Update(rel, encoded, baseline)
	} else {
		cs.Create(rel, encoded)
	}
	*mappings = append(*mappings, IDMapping{
		EntityKind: "folder",
		SourceID:   folderSlug,
		TargetID:   folderSlug,
		TargetPath: rel,
		Action:     action,
	})
}

func convertPostmanRequest(
	rawReq json.RawMessage,
	reqID, reqName, sourcePath, targetPath string,
	opts ImportOptions,
	addSecret func(slot, rawVal, scope, src string) string,
	addLocalSlot func(srcPath, sourceLoc, field string) string,
	secretVarRename map[string]string,
	losses *[]LossItem,
) (*Request, error) {
	trimmed := strings.TrimSpace(string(rawReq))
	if strings.HasPrefix(trimmed, "\"") {
		var urlStr string
		if err := json.Unmarshal(rawReq, &urlStr); err != nil {
			return nil, err
		}
		baseURL, q, _ := splitCURLTargetURL(rewriteSecretVars(urlStr, secretVarRename))
		return &Request{
			SchemaVersion: SupportedSchemaVersion,
			ID:            reqID,
			Name:          reqName,
			Protocol:      "http",
			HTTP: &HTTPDef{
				Method: "GET",
				URL:    baseURL,
				Query:  q,
			},
		}, nil
	}

	var pr struct {
		Method      string          `json:"method"`
		Header      json.RawMessage `json:"header"`
		URL         json.RawMessage `json:"url"`
		Body        *postmanBodyRaw `json:"body"`
		Auth        *postmanAuthRaw `json:"auth"`
		Description any             `json:"description"`
	}
	if err := json.Unmarshal(rawReq, &pr); err != nil {
		return nil, err
	}

	method := strings.ToUpper(strings.TrimSpace(pr.Method))
	if method == "" {
		method = "GET"
	}

	baseURL, queryKVs := parsePostmanURL(pr.URL, sourcePath, targetPath, secretVarRename, losses)
	headers := parsePostmanHeaders(pr.Header, sourcePath, targetPath, opts.ScrubSecretsToRefs, addSecret, secretVarRename, losses)
	authRef := convertPostmanAuth(pr.Auth, sourcePath, targetPath, opts.ScrubSecretsToRefs, addSecret, secretVarRename, losses)
	bodyCfg, err := convertPostmanBody(pr.Body, headers, sourcePath, targetPath, addLocalSlot, secretVarRename, losses)
	if err != nil {
		return nil, err
	}

	return &Request{
		SchemaVersion: SupportedSchemaVersion,
		ID:            reqID,
		Name:          reqName,
		Protocol:      "http",
		HTTP: &HTTPDef{
			Method:  method,
			URL:     baseURL,
			Headers: headers,
			Query:   queryKVs,
			Body:    bodyCfg,
		},
		Auth: authRef,
	}, nil
}

type postmanBodyRaw struct {
	Mode       string          `json:"mode"`
	Raw        string          `json:"raw"`
	URLEncoded []postmanKVRaw  `json:"urlencoded"`
	FormData   []postmanFDRaw  `json:"formdata"`
	File       *postmanFileRaw `json:"file"`
	GraphQL    *struct {
		Query     string `json:"query"`
		Variables string `json:"variables"`
	} `json:"graphql"`
	Options struct {
		Raw struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options"`
	Disabled bool `json:"disabled"`
}

type postmanKVRaw struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Disabled bool   `json:"disabled"`
}

type postmanFDRaw struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Type        string `json:"type"`
	Src         any    `json:"src"`
	ContentType string `json:"contentType"`
	Disabled    bool   `json:"disabled"`
}

type postmanFileRaw struct {
	Src any `json:"src"`
}

func parsePostmanURL(
	rawURL json.RawMessage,
	sourcePath, targetPath string,
	secretVarRename map[string]string,
	losses *[]LossItem,
) (string, []KV) {
	if len(rawURL) == 0 {
		return "", nil
	}
	trimmed := strings.TrimSpace(string(rawURL))
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(rawURL, &s); err == nil {
			base, q, _ := splitCURLTargetURL(rewriteSecretVars(s, secretVarRename))
			return base, q
		}
	}

	var uObj struct {
		Raw      string         `json:"raw"`
		Protocol string         `json:"protocol"`
		Host     any            `json:"host"`
		Port     string         `json:"port"`
		Path     any            `json:"path"`
		Query    []postmanKVRaw `json:"query"`
		Variable []postmanKVRaw `json:"variable"`
	}
	if err := json.Unmarshal(rawURL, &uObj); err != nil {
		return "", nil
	}

	var queryOut []KV
	for _, q := range uObj.Query {
		if strings.TrimSpace(q.Key) == "" && q.Value == nil {
			continue
		}
		valStr := ""
		if q.Value != nil {
			valStr = rewriteSecretVars(fmt.Sprint(q.Value), secretVarRename)
		}
		var enabledPtr *bool
		if q.Disabled {
			enabledPtr = BoolPtr(false)
		}
		queryOut = append(queryOut, KV{
			Name:    rewriteSecretVars(q.Key, secretVarRename),
			Value:   valStr,
			Enabled: enabledPtr,
		})
	}

	if len(uObj.Variable) > 0 {
		*losses = append(*losses, LossItem{
			Code:       "postman-path-variable-note",
			Severity:   LossInfo,
			SourcePath: sourcePath,
			TargetPath: targetPath,
			Field:      "url.variable",
			Message:    fmt.Sprintf("request URL contains %d Postman path variable definition(s)", len(uObj.Variable)),
		})
	}

	raw := strings.TrimSpace(uObj.Raw)
	if raw != "" {
		base := raw
		if qIdx := strings.IndexByte(base, '?'); qIdx >= 0 {
			if len(queryOut) == 0 {
				_, parsedQ, _ := splitCURLTargetURL(base)
				queryOut = parsedQ
			}
			base = base[:qIdx]
		}
		return rewriteSecretVars(base, secretVarRename), queryOut
	}

	host := joinPostmanParts(uObj.Host, ".")
	urlPath := joinPostmanParts(uObj.Path, "/")
	proto := strings.TrimSuffix(strings.TrimSpace(uObj.Protocol), "://")
	var built strings.Builder
	if proto != "" {
		built.WriteString(proto)
		built.WriteString("://")
	}
	built.WriteString(host)
	if uObj.Port != "" {
		built.WriteByte(':')
		built.WriteString(uObj.Port)
	}
	if urlPath != "" {
		if !strings.HasPrefix(urlPath, "/") {
			built.WriteByte('/')
		}
		built.WriteString(urlPath)
	}
	return rewriteSecretVars(built.String(), secretVarRename), queryOut
}

func joinPostmanParts(v any, sep string) string {
	switch val := v.(type) {
	case string:
		return val
	case []any:
		var parts []string
		for _, item := range val {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, sep)
	default:
		return ""
	}
}

func parsePostmanHeaders(
	rawHeader json.RawMessage,
	sourcePath, targetPath string,
	scrubSecrets bool,
	addSecret func(slot, rawVal, scope, src string) string,
	secretVarRename map[string]string,
	losses *[]LossItem,
) []KV {
	if len(rawHeader) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(rawHeader))
	if strings.HasPrefix(trimmed, "\"") {
		var hdrBlock string
		if err := json.Unmarshal(rawHeader, &hdrBlock); err != nil {
			return nil
		}
		var out []KV
		for _, line := range strings.Split(hdrBlock, "\n") {
			k, v, ok := parseHeaderLine(line)
			if !ok {
				continue
			}
			v = rewriteSecretVars(v, secretVarRename)
			if scrubSecrets && isSensitiveKeyName(k) && v != "" && !strings.Contains(v, "{{") {
				slot := sanitizeSecretSlotName(k, "header_secret")
				v = addSecret(slot, v, "header", "postman-collection")
			}
			out = append(out, KV{Name: k, Value: v})
		}
		return out
	}

	var arr []postmanKVRaw
	if err := json.Unmarshal(rawHeader, &arr); err != nil {
		return nil
	}
	var out []KV
	for _, h := range arr {
		k := strings.TrimSpace(h.Key)
		if k == "" {
			continue
		}
		v := ""
		if h.Value != nil {
			v = rewriteSecretVars(fmt.Sprint(h.Value), secretVarRename)
		}
		if strings.EqualFold(k, "Cookie") && v != "" && !strings.Contains(v, "{{") {
			*losses = append(*losses, LossItem{
				Code:       "postman-cookie-scrubbed",
				Severity:   LossSecurityScrubbed,
				SourcePath: sourcePath,
				TargetPath: targetPath,
				Field:      "header.Cookie",
				Message:    "Cookie header value scrubbed to secret reference",
			})
			if scrubSecrets {
				v = addSecret("cookie_header", v, "header", "postman-collection")
			}
		} else if scrubSecrets && isSensitiveKeyName(k) && v != "" && !strings.Contains(v, "{{") {
			slot := sanitizeSecretSlotName(k, "header_secret")
			v = addSecret(slot, v, "header", "postman-collection")
		}
		var enabledPtr *bool
		if h.Disabled {
			enabledPtr = BoolPtr(false)
		}
		out = append(out, KV{
			Name:    k,
			Value:   v,
			Enabled: enabledPtr,
		})
	}
	return out
}

func convertPostmanBody(
	b *postmanBodyRaw,
	headers []KV,
	sourcePath, targetPath string,
	addLocalSlot func(srcPath, sourceLoc, field string) string,
	secretVarRename map[string]string,
	losses *[]LossItem,
) (*Body, error) {
	if b == nil || b.Disabled {
		return nil, nil
	}
	mode := strings.ToLower(strings.TrimSpace(b.Mode))
	switch mode {
	case "", "none":
		return nil, nil
	case "raw":
		lang := strings.ToLower(strings.TrimSpace(b.Options.Raw.Language))
		content := rewriteSecretVars(b.Raw, secretVarRename)
		ct := strings.ToLower(findHeaderValue(headers, "Content-Type"))
		switch {
		case lang == "json" || strings.Contains(ct, "json") || (lang == "" && looksLikeJSONObjectOrArray(content)):
			node, err := parseJSONOrScalarYAMLNode(content)
			if err != nil {
				return nil, err
			}
			tb := &TypedBody{Kind: BodyJSON, JSONNode: node, HasInlineValue: true}
			return tb.ToBody()
		case lang == "xml" || strings.Contains(ct, "xml"):
			tb := &TypedBody{Kind: BodyXML, Text: content, HasInlineValue: true}
			return tb.ToBody()
		default:
			tb := &TypedBody{Kind: BodyText, Text: content, HasInlineValue: true}
			return tb.ToBody()
		}
	case "urlencoded":
		var fields []FormField
		for _, f := range b.URLEncoded {
			val := ""
			if f.Value != nil {
				val = rewriteSecretVars(fmt.Sprint(f.Value), secretVarRename)
			}
			var enabledPtr *bool
			if f.Disabled {
				enabledPtr = BoolPtr(false)
			}
			fields = append(fields, FormField{
				Name:    rewriteSecretVars(f.Key, secretVarRename),
				Value:   val,
				Enabled: enabledPtr,
			})
		}
		tb := &TypedBody{Kind: BodyURLEncoded, FormFields: fields, HasInlineValue: true}
		return tb.ToBody()
	case "formdata":
		var parts []MultipartPart
		for _, fd := range b.FormData {
			fType := strings.ToLower(strings.TrimSpace(fd.Type))
			var enabledPtr *bool
			if fd.Disabled {
				enabledPtr = BoolPtr(false)
			}
			if fType == "file" {
				srcStr := extractPostmanFileSrc(fd.Src)
				slot := addLocalSlot(srcStr, sourcePath, "body.formdata."+fd.Key)
				parts = append(parts, MultipartPart{
					Name:        fd.Key,
					Kind:        "file",
					LocalFile:   slot,
					ContentType: fd.ContentType,
					Enabled:     enabledPtr,
				})
			} else {
				val := ""
				if fd.Value != nil {
					val = rewriteSecretVars(fmt.Sprint(fd.Value), secretVarRename)
				}
				parts = append(parts, MultipartPart{
					Name:        fd.Key,
					Kind:        "text",
					Value:       val,
					ContentType: fd.ContentType,
					Enabled:     enabledPtr,
				})
			}
		}
		tb := &TypedBody{Kind: BodyFormData, MultipartParts: parts, HasInlineValue: true}
		return tb.ToBody()
	case "file":
		srcStr := ""
		if b.File != nil {
			srcStr = extractPostmanFileSrc(b.File.Src)
		}
		slot := addLocalSlot(srcStr, sourcePath, "body.file")
		tb := &TypedBody{Kind: BodyBinary, LocalFile: slot}
		return tb.ToBody()
	case "graphql":
		q := ""
		v := ""
		if b.GraphQL != nil {
			q = b.GraphQL.Query
			v = b.GraphQL.Variables
		}
		payload := map[string]any{"query": q}
		if strings.TrimSpace(v) != "" {
			var parsedVars any
			if err := json.Unmarshal([]byte(v), &parsedVars); err == nil {
				payload["variables"] = parsedVars
			} else {
				payload["variables"] = v
			}
		}
		encoded, _ := json.MarshalIndent(payload, "", "  ")
		*losses = append(*losses, LossItem{
			Code:       "postman-graphql-converted-to-json",
			Severity:   LossInfo,
			SourcePath: sourcePath,
			TargetPath: targetPath,
			Field:      "body.graphql",
			Message:    "GraphQL body converted to JSON request body payload",
		})
		node, err := parseJSONOrScalarYAMLNode(string(encoded))
		if err != nil {
			return nil, err
		}
		tb := &TypedBody{Kind: BodyJSON, JSONNode: node, HasInlineValue: true}
		return tb.ToBody()
	default:
		*losses = append(*losses, LossItem{
			Code:       "postman-body-mode-unsupported",
			Severity:   LossUnsupported,
			SourcePath: sourcePath,
			TargetPath: targetPath,
			Field:      "body.mode",
			Message:    fmt.Sprintf("unsupported Postman body mode %q omitted", b.Mode),
		})
		return nil, nil
	}
}

func extractPostmanFileSrc(src any) string {
	switch v := src.(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			return fmt.Sprint(v[0])
		}
		return "attachment"
	default:
		return "attachment"
	}
}

// --- Postman Export JSON structs ---

type postmanExportCollection struct {
	Info postmanExportInfo   `json:"info"`
	Item []postmanExportItem `json:"item"`
}

type postmanExportInfo struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
}

type postmanExportItem struct {
	Name    string                `json:"name"`
	Item    []postmanExportItem   `json:"item,omitempty"`
	Request *postmanExportRequest `json:"request,omitempty"`
	Auth    *postmanExportAuth    `json:"auth,omitempty"`
}

type postmanExportRequest struct {
	Method string                `json:"method"`
	Header []postmanExportHeader `json:"header,omitempty"`
	URL    postmanExportURL      `json:"url"`
	Body   *postmanExportBody    `json:"body,omitempty"`
	Auth   *postmanExportAuth    `json:"auth,omitempty"`
}

type postmanExportHeader struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled,omitempty"`
}

type postmanExportURL struct {
	Raw   string               `json:"raw"`
	Query []postmanExportQuery `json:"query,omitempty"`
}

type postmanExportQuery struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled,omitempty"`
}

type postmanExportBody struct {
	Mode       string                  `json:"mode"`
	Raw        string                  `json:"raw,omitempty"`
	URLEncoded []postmanExportKV       `json:"urlencoded,omitempty"`
	FormData   []postmanExportFormPart `json:"formdata,omitempty"`
	File       *postmanExportFile      `json:"file,omitempty"`
	Options    map[string]any          `json:"options,omitempty"`
}

type postmanExportKV struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled,omitempty"`
}

type postmanExportFormPart struct {
	Key         string `json:"key"`
	Value       string `json:"value,omitempty"`
	Type        string `json:"type"`
	Src         string `json:"src,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
}

type postmanExportFile struct {
	Src string `json:"src"`
}

type postmanExportAuth struct {
	Type   string                  `json:"type"`
	Bearer []postmanExportAuthAttr `json:"bearer,omitempty"`
	Basic  []postmanExportAuthAttr `json:"basic,omitempty"`
	APIKey []postmanExportAuthAttr `json:"apikey,omitempty"`
}

type postmanExportAuthAttr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

func scrubExportSecretField(val, sourcePath, field string, losses *[]LossItem) string {
	if val == "" {
		return ""
	}
	if strings.Contains(val, "{{") {
		return val
	}
	*losses = append(*losses, LossItem{
		Code:       "postman-export-secret-scrubbed",
		Severity:   LossSecurityScrubbed,
		SourcePath: sourcePath,
		Field:      field,
		Message:    fmt.Sprintf("literal credential in %s scrubbed to %s", field, redactedMask),
	})
	return redactedMask
}

func convertAuthRefToPostmanExport(p *Project, auth *AuthRef, sourcePath string, losses *[]LossItem) *postmanExportAuth {
	if auth == nil {
		return nil
	}
	if auth.Mode == "none" {
		return &postmanExportAuth{Type: "noauth"}
	}
	var scheme *AuthScheme
	switch auth.Mode {
	case "ref":
		if p != nil {
			scheme = p.AuthSchemes[auth.Ref]
		}
	case "inline":
		scheme = auth.Inline
	}
	if scheme == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(scheme.Type)) {
	case "bearer":
		tok := scrubExportSecretField(scheme.Token, sourcePath, "auth.token", losses)
		return &postmanExportAuth{
			Type: "bearer",
			Bearer: []postmanExportAuthAttr{
				{Key: "token", Value: tok, Type: "string"},
			},
		}
	case "basic":
		pass := scrubExportSecretField(scheme.Password, sourcePath, "auth.password", losses)
		return &postmanExportAuth{
			Type: "basic",
			Basic: []postmanExportAuthAttr{
				{Key: "username", Value: scheme.Username, Type: "string"},
				{Key: "password", Value: pass, Type: "string"},
			},
		}
	case "apikey", "api-key":
		val := scrubExportSecretField(scheme.Value, sourcePath, "auth.value", losses)
		inLoc := NormalizeAPIKeyLocation(scheme.In)
		if inLoc == "" {
			inLoc = "header"
		}
		return &postmanExportAuth{
			Type: "apikey",
			APIKey: []postmanExportAuthAttr{
				{Key: "key", Value: scheme.Name, Type: "string"},
				{Key: "value", Value: val, Type: "string"},
				{Key: "in", Value: inLoc, Type: "string"},
			},
		}
	default:
		return nil
	}
}

func findCollectionEntryByRel(entries []*CollectionEntry, rel string) *CollectionEntry {
	for _, ce := range entries {
		if ce == nil {
			continue
		}
		if filepath.ToSlash(ce.RelPath) == rel {
			return ce
		}
		if found := findCollectionEntryByRel(ce.Children, rel); found != nil {
			return found
		}
	}
	return nil
}
