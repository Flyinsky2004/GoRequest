package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// CURLParsed holds the structured result of parsing a curl command in pure Go
// without invoking any shell or external process.
type CURLParsed struct {
	Request        *Request
	Variables      []VariableSourceNote
	SecretBindings []string
	LocalFileSlots []string
	Losses         []LossItem
	Blocked        bool
}

// CURLExportResult holds a redacted curl command ready for preview or clipboard
// copy, along with redaction and compatibility notes.
type CURLExportResult struct {
	Command        string     `json:"command" yaml:"command"`
	RedactedCount  int        `json:"redactedCount" yaml:"redactedCount"`
	SecretRefs     []string   `json:"secretRefs,omitempty" yaml:"secretRefs,omitempty"`
	LocalFileSlots []string   `json:"localFileSlots,omitempty" yaml:"localFileSlots,omitempty"`
	Notes          []LossItem `json:"notes,omitempty" yaml:"notes,omitempty"`
}

func looksLikeCURLInput(raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		lower := strings.ToLower(t)
		return lower == "curl" ||
			strings.HasPrefix(lower, "curl ") ||
			strings.HasPrefix(lower, "curl\t") ||
			strings.HasPrefix(lower, "curl\\") ||
			strings.HasPrefix(lower, "curl.exe ")
	}
	return false
}

// StageCURLImport parses a curl command string in pure Go and stages the
// resulting request creation/update into a ChangeSet preview.
func StageCURLImport(p *Project, raw string, opts ImportOptions) (*ImportPlan, error) {
	if p == nil || p.Store == nil {
		return nil, errors.New("target project is required")
	}
	opts = opts.normalized()
	targetDir, err := normalizeTargetCollectionFolder(opts.TargetFolderRel)
	if err != nil {
		return nil, fmt.Errorf("invalid target folder: %w", err)
	}

	parsed, err := ParseCURLCommand(raw, opts)
	if err != nil {
		return nil, err
	}

	req := parsed.Request
	if opts.DefaultRequestName != "" {
		req.Name = strings.TrimSpace(opts.DefaultRequestName)
	}
	slug := sanitizeSlug(req.Name, "curl-request")
	desiredID := "req-" + slug

	usedIDs := make(map[string]bool)
	usedPaths := make(map[string]bool)
	finalID, finalRel, action, baseline, conflictMsg := resolveEntityCollision(
		p,
		opts.ConflictPolicy,
		"request",
		desiredID,
		targetDir,
		".request.yaml",
		usedIDs,
		usedPaths,
	)
	req.ID = finalID
	req.SchemaVersion = SupportedSchemaVersion

	cs := p.Store.NewChangeSet()
	var conflicts []string
	if conflictMsg != "" {
		conflicts = append(conflicts, conflictMsg)
	}

	if !parsed.Blocked && action != "conflict" && action != "skip" {
		var existingBytes []byte
		if action == "update" && p.Store.Exists(finalRel) {
			existingBytes, _, _ = p.Store.Read(finalRel)
		}
		yamlBytes, err := MergeRequestYAML(existingBytes, req)
		if err != nil {
			return nil, fmt.Errorf("encode imported curl request: %w", err)
		}
		switch action {
		case "update":
			cs.Update(finalRel, yamlBytes, baseline)
		default:
			cs.Create(finalRel, yamlBytes)
		}
	}

	// If secrets were discovered and TargetEnvID is specified (or default env exists),
	// declare secret slots in that environment without writing secret values.
	if !parsed.Blocked && len(parsed.SecretBindings) > 0 {
		envID := strings.TrimSpace(opts.TargetEnvID)
		if envID == "" {
			envID = defaultProjectEnvID(p)
		}
		if envID != "" {
			if err := stageSecretSlotsOnEnv(p, cs, envID, parsed.SecretBindings, usedPaths); err != nil {
				parsed.Losses = append(parsed.Losses, LossItem{
					Code:     "curl-env-secret-slot-note",
					Severity: LossWarning,
					Message:  fmt.Sprintf("could not stage secret slot names into environment %q: %v", envID, err),
				})
			}
		}
	}

	changePreviews, err := cs.Preview()
	if err != nil {
		return nil, err
	}

	reqMethod := "GET"
	reqURL := ""
	if req.HTTP != nil {
		reqMethod = req.HTTP.Method
		reqURL = req.HTTP.URL
	}

	canApply := !parsed.Blocked && len(conflicts) == 0 && len(changePreviews) > 0
	summary := fmt.Sprintf("cURL import: %s %s -> %s", reqMethod, opts.Redactor.RedactString(reqURL), filepath.ToSlash(finalRel))
	if parsed.Blocked {
		summary = "cURL import blocked by unsafe shell construct or invalid invocation"
	} else if len(conflicts) > 0 {
		summary = fmt.Sprintf("cURL import blocked by %d conflict(s)", len(conflicts))
	}

	for i := range parsed.Losses {
		parsed.Losses[i].Message = opts.Redactor.RedactString(parsed.Losses[i].Message)
	}

	preview := ImportPreview{
		Format:         FormatCURL,
		FormatVersion:  "cli",
		Summary:        summary,
		ConflictPolicy: opts.ConflictPolicy,
		Changes:        changePreviews,
		IDMappings: []IDMapping{
			{
				EntityKind: "request",
				SourceID:   desiredID,
				TargetID:   finalID,
				SourcePath: "curl",
				TargetPath: filepath.ToSlash(finalRel),
				Action:     action,
				Renamed:    finalID != desiredID,
			},
		},
		Variables:      parsed.Variables,
		SecretBindings: parsed.SecretBindings,
		LocalFileSlots: parsed.LocalFileSlots,
		Losses:         parsed.Losses,
		Conflicts:      conflicts,
		CanApply:       canApply,
	}

	return &ImportPlan{
		project:      p,
		changeSet:    cs,
		preview:      preview,
		redactor:     opts.Redactor,
		importedReqs: []string{finalID},
	}, nil
}

// ParseCURLCommand tokenizes and interprets a curl command in pure Go without
// ever invoking a shell or external binary.
func ParseCURLCommand(raw string, opts ImportOptions) (*CURLParsed, error) {
	opts = opts.normalized()
	tokens, lexLosses, blocked, err := tokenizeCURL(raw)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, errors.New("empty curl command")
	}

	cmd := strings.ToLower(filepath.Base(tokens[0]))
	if cmd != "curl" && cmd != "curl.exe" {
		return nil, fmt.Errorf("expected curl command, got %q", tokens[0])
	}

	out := &CURLParsed{
		Losses:  lexLosses,
		Blocked: blocked,
	}

	var (
		explicitMethod string
		forceGet       bool
		forceHead      bool
		targetURL      string
		headers        []KV
		queryParams    []KV
		dataParts      []string
		urlEncodeParts []string
		formParts      []MultipartPart
		jsonPayload    string
		hasJSONFlag    bool
		binarySlot     string
		authRef        *AuthRef
	)

	secretSet := make(map[string]bool)
	slotSet := make(map[string]bool)
	addSecretSlot := func(slot, rawVal, scope, src string) string {
		slot = sanitizeSecretSlotName(slot, "imported_secret")
		if rawVal != "" {
			opts.Redactor.RegisterSecret(rawVal)
		}
		if !secretSet[slot] {
			secretSet[slot] = true
			out.SecretBindings = append(out.SecretBindings, slot)
		}
		ref := "{{secret." + slot + "}}"
		out.Variables = append(out.Variables, VariableSourceNote{
			Name:          slot,
			Scope:         scope,
			Source:        src,
			IsSecret:      true,
			MappedRef:     ref,
			MaskedPreview: redactedMask,
		})
		return ref
	}
	addLocalSlot := func(rawPath, field string) string {
		base := filepath.Base(strings.ReplaceAll(rawPath, "\\", "/"))
		slot := sanitizeSecretSlotName(strings.TrimSuffix(base, filepath.Ext(base)), "attachment")
		if !slotSet[slot] {
			slotSet[slot] = true
			out.LocalFileSlots = append(out.LocalFileSlots, slot)
		}
		out.Losses = append(out.Losses, LossItem{
			Code:     "curl-host-file-to-local-slot",
			Severity: LossSecurityScrubbed,
			Field:    field,
			Message:  fmt.Sprintf("host file reference %q converted to logical slot %s (bind local file before sending)", filepath.Base(rawPath), FormatLocalFileRef(slot)),
		})
		return slot
	}

	args := expandShortCURLFlags(tokens[1:])
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			for j := i + 1; j < len(args); j++ {
				if targetURL == "" {
					targetURL = args[j]
				}
			}
			break
		}

		var flagName, inlineVal string
		hasInline := false
		if strings.HasPrefix(arg, "--") {
			if eq := strings.IndexByte(arg, '='); eq > 2 {
				flagName = arg[:eq]
				inlineVal = arg[eq+1:]
				hasInline = true
			} else {
				flagName = arg
			}
		} else if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			flagName = arg
		}

		nextVal := func() (string, error) {
			if hasInline {
				return inlineVal, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("missing argument for curl option %s", flagName)
			}
			i++
			return args[i], nil
		}

		switch flagName {
		case "":
			if targetURL == "" {
				targetURL = arg
			} else {
				out.Losses = append(out.Losses, LossItem{
					Code:     "curl-extra-url-ignored",
					Severity: LossWarning,
					Message:  fmt.Sprintf("additional positional URL %q ignored; single-request import uses the first URL", arg),
				})
			}

		case "--url":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			if targetURL == "" {
				targetURL = v
			}

		case "-X", "--request":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			explicitMethod = strings.ToUpper(strings.TrimSpace(v))

		case "-G", "--get":
			forceGet = true

		case "-I", "--head":
			forceHead = true

		case "-H", "--header":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(strings.TrimSpace(v), "@") {
				out.Losses = append(out.Losses, LossItem{
					Code:     "curl-header-file-unsupported",
					Severity: LossUnsupported,
					Field:    flagName,
					Message:  fmt.Sprintf("reading headers from file (%s) is blocked for host safety", v),
				})
				continue
			}
			k, val, ok := parseHeaderLine(v)
			if !ok {
				out.Losses = append(out.Losses, LossItem{
					Code:     "curl-header-malformed",
					Severity: LossWarning,
					Field:    flagName,
					Message:  fmt.Sprintf("ignored malformed header %q", v),
				})
				continue
			}
			lowerK := strings.ToLower(k)
			if lowerK == "authorization" {
				if parsedAuth := parseAuthorizationHeaderToAuth(val, opts.ScrubSecretsToRefs, addSecretSlot, opts.Redactor); parsedAuth != nil {
					authRef = parsedAuth
					continue
				}
			}
			if lowerK == "cookie" {
				opts.Redactor.RegisterSecret(val)
				out.Losses = append(out.Losses, LossItem{
					Code:     "curl-cookie-scrubbed",
					Severity: LossSecurityScrubbed,
					Field:    "Cookie",
					Message:  "inline Cookie header scrubbed to secret reference; raw session cookies are excluded from shared project files",
				})
				if opts.ScrubSecretsToRefs && !strings.Contains(val, "{{") {
					val = addSecretSlot("cookie_header", val, "header", "curl-header")
				}
			} else if isSensitiveKeyName(lowerK) && opts.ScrubSecretsToRefs && !strings.Contains(val, "{{") && val != "" {
				opts.Redactor.RegisterSensitiveKey("header", k)
				slot := sanitizeSecretSlotName(lowerK, "header_secret")
				val = addSecretSlot(slot, val, "header", "curl-header")
			}
			headers = append(headers, KV{Name: k, Value: val})

		case "-b", "--cookie":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			opts.Redactor.RegisterSecret(v)
			out.Losses = append(out.Losses, LossItem{
				Code:     "curl-cookie-scrubbed",
				Severity: LossSecurityScrubbed,
				Field:    flagName,
				Message:  "curl -b/--cookie value scrubbed to secret reference; cookie jars and raw cookies are not persisted in shared requests",
			})
			ref := v
			if opts.ScrubSecretsToRefs && !strings.Contains(v, "{{") {
				ref = addSecretSlot("cookie_header", v, "header", "curl-cookie")
			}
			headers = append(headers, KV{Name: "Cookie", Value: ref})

		case "-u", "--user":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			user, pass, hasPass := strings.Cut(v, ":")
			opts.Redactor.RegisterBasicPair(user, pass)
			passField := pass
			if hasPass && pass != "" && opts.ScrubSecretsToRefs && !strings.Contains(pass, "{{") {
				passField = addSecretSlot("basic_password", pass, "request-auth", "curl-auth")
			}
			authRef = &AuthRef{
				Mode: "inline",
				Inline: &AuthScheme{
					Type:     "basic",
					Username: user,
					Password: passField,
				},
			}

		case "-A", "--user-agent":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			headers = upsertHeaderKV(headers, "User-Agent", v)

		case "-e", "--referer":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			headers = upsertHeaderKV(headers, "Referer", v)

		case "-d", "--data", "--data-raw", "--data-ascii", "--data-binary":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			if flagName != "--data-raw" && strings.HasPrefix(v, "@") && len(v) > 1 {
				filePath := v[1:]
				binarySlot = addLocalSlot(filePath, flagName)
				continue
			}
			dataParts = append(dataParts, v)

		case "--data-urlencode":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			urlEncodeParts = append(urlEncodeParts, v)

		case "--json":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			hasJSONFlag = true
			jsonPayload = v
			headers = ensureHeaderKV(headers, "Content-Type", "application/json")
			headers = ensureHeaderKV(headers, "Accept", "application/json")

		case "-F", "--form", "--form-string":
			v, err := nextVal()
			if err != nil {
				return nil, err
			}
			mf := parseCURLFormPart(v, flagName == "--form-string", addLocalSlot)
			formParts = append(formParts, mf)

		case "--compressed", "-s", "--silent", "-S", "--show-error", "-v", "--verbose", "-i", "--include":
			if flagName == "--compressed" {
				out.Losses = append(out.Losses, LossItem{
					Code:     "curl-compressed-note",
					Severity: LossInfo,
					Field:    flagName,
					Message:  "--compressed acknowledged; GoRequest transport negotiates compression automatically",
				})
			}

		case "-k", "--insecure", "-L", "--location", "--location-trusted",
			"-x", "--proxy", "--proxy-user", "-E", "--cert", "--key", "--cacert",
			"--capath", "--resolve", "--connect-to", "-o", "--output", "-O", "--remote-name",
			"-D", "--dump-header", "-c", "--cookie-jar", "-m", "--max-time", "--connect-timeout",
			"--retry", "--http1.1", "--http2", "--http3":
			if curlUnsupportedFlagTakesValue(flagName) && !hasInline && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			out.Losses = append(out.Losses, LossItem{
				Code:     "curl-flag-unsupported",
				Severity: LossUnsupported,
				Field:    flagName,
				Message:  fmt.Sprintf("curl option %s is not stored in shared Request YAML (transport/TLS/output options belong to runtime settings)", flagName),
			})

		default:
			if strings.HasPrefix(flagName, "-") {
				if !hasInline && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !looksLikeURLToken(args[i+1]) && targetURL != "" {
					i++
				}
				out.Losses = append(out.Losses, LossItem{
					Code:     "curl-flag-unsupported",
					Severity: LossUnsupported,
					Field:    flagName,
					Message:  fmt.Sprintf("unsupported curl option %s ignored", flagName),
				})
			}
		}
	}

	if targetURL == "" {
		return nil, errors.New("curl command does not contain a target URL")
	}

	baseURL, parsedQuery, urlLoss := splitCURLTargetURL(targetURL)
	if urlLoss != nil {
		out.Losses = append(out.Losses, *urlLoss)
	}
	queryParams = append(queryParams, parsedQuery...)

	if forceGet {
		for _, dp := range dataParts {
			queryParams = append(queryParams, parseFormURLEncodedKVs(dp)...)
		}
		for _, up := range urlEncodeParts {
			f := parseDataURLEncodeField(up, addLocalSlot)
			queryParams = append(queryParams, KV{Name: f.Name, Value: f.Value})
		}
		dataParts = nil
		urlEncodeParts = nil
	}

	method := explicitMethod
	if method == "" {
		switch {
		case forceHead:
			method = "HEAD"
		case forceGet:
			method = "GET"
		case hasJSONFlag || len(dataParts) > 0 || len(urlEncodeParts) > 0 || len(formParts) > 0 || binarySlot != "":
			method = "POST"
		default:
			method = "GET"
		}
	}

	bodyCfg, bodyErr := buildCURLBody(
		hasJSONFlag,
		jsonPayload,
		dataParts,
		urlEncodeParts,
		formParts,
		binarySlot,
		headers,
		addLocalSlot,
	)
	if bodyErr != nil {
		return nil, bodyErr
	}

	reqName := deriveRequestNameFromURL(method, baseURL)
	req := &Request{
		SchemaVersion: SupportedSchemaVersion,
		ID:            "req-" + sanitizeSlug(reqName, "curl-request"),
		Name:          reqName,
		Protocol:      "http",
		HTTP: &HTTPDef{
			Method:  method,
			URL:     baseURL,
			Query:   queryParams,
			Headers: headers,
			Body:    bodyCfg,
		},
		Auth: authRef,
	}

	out.Request = req
	return out, nil
}

// ExportRedactedCURL builds a POSIX-quoted curl command for entry with all
// secrets and sensitive fields scrubbed via Redactor (`***` or `{{secret.name}}`).
func ExportRedactedCURL(p *Project, entry *RequestEntry, envID string, secrets SecretProvider) (*CURLExportResult, error) {
	if entry == nil || entry.Request == nil || entry.Request.HTTP == nil {
		return nil, errors.New("HTTP request entry is required")
	}
	req := entry.Request
	httpDef := req.HTTP
	red := NewRedactor()

	var secretRefs []string
	var envVars map[string]string
	if p != nil {
		targetEnv := envID
		if targetEnv == "" {
			targetEnv = defaultProjectEnvID(p)
		}
		if merged, env, err := p.ResolveVars(entry, targetEnv); err == nil {
			envVars = merged
			if env != nil {
				for _, secName := range env.Secrets {
					if !slices.Contains(secretRefs, secName) {
						secretRefs = append(secretRefs, secName)
					}
					if secrets != nil {
						if val, ok := secrets.Get(secName); ok && val != "" {
							red.RegisterSecret(val)
						}
					}
				}
			}
		}
	}

	redactedCount := 0
	resolveSafe := func(raw string, keyHint string) string {
		if raw == "" {
			return ""
		}
		if isSensitiveKeyName(keyHint) && !strings.Contains(raw, "{{") {
			red.RegisterSecret(raw)
			redactedCount++
			return redactedMask
		}
		out := templatePattern.ReplaceAllStringFunc(raw, func(match string) string {
			sub := templatePattern.FindStringSubmatch(match)
			if len(sub) < 2 {
				return match
			}
			expr := strings.TrimSpace(sub[1])
			if strings.HasPrefix(expr, "secret.") {
				secName := strings.TrimSpace(strings.TrimPrefix(expr, "secret."))
				if !slices.Contains(secretRefs, secName) {
					secretRefs = append(secretRefs, secName)
				}
				redactedCount++
				return "{{secret." + secName + "}}"
			}
			if envVars != nil {
				if v, ok := envVars[expr]; ok {
					return red.RedactString(v)
				}
			}
			return match
		})
		scrubbed := red.RedactString(out)
		if scrubbed != out {
			redactedCount++
		}
		return scrubbed
	}

	var notes []LossItem
	var localSlots []string
	method := strings.ToUpper(strings.TrimSpace(httpDef.Method))
	if method == "" {
		method = "GET"
	}

	fullURL := resolveSafe(httpDef.URL, "url")
	var queryParts []string
	for _, q := range httpDef.Query {
		if !q.IsEnabled() || strings.TrimSpace(q.Name) == "" {
			continue
		}
		qk := url.QueryEscape(resolveSafe(q.Name, ""))
		qv := url.QueryEscape(resolveSafe(q.Value, q.Name))
		queryParts = append(queryParts, qk+"="+qv)
	}
	if len(queryParts) > 0 {
		sep := "?"
		if strings.Contains(fullURL, "?") {
			sep = "&"
		}
		fullURL += sep + strings.Join(queryParts, "&")
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("curl -X %s %s", posixQuote(method), posixQuote(fullURL)))

	// Resolve effective Auth across project -> folder -> request
	var effectiveScheme *AuthScheme
	if p != nil {
		if eff, err := p.ResolveEffectiveAuth(entry, req); err == nil && eff != nil && eff.Scheme != nil {
			effectiveScheme = eff.Scheme
		}
	}
	if effectiveScheme == nil && req.Auth != nil && req.Auth.Mode == "inline" {
		effectiveScheme = req.Auth.Inline
	}

	if effectiveScheme != nil {
		switch strings.ToLower(strings.TrimSpace(effectiveScheme.Type)) {
		case "bearer":
			tok := effectiveScheme.Token
			if tok == "" || !strings.Contains(tok, "{{secret.") {
				redactedCount++
				tok = redactedMask
			} else {
				tok = resolveSafe(tok, "authorization")
			}
			lines = append(lines, fmt.Sprintf("  -H %s", posixQuote("Authorization: Bearer "+tok)))
		case "basic":
			user := resolveSafe(effectiveScheme.Username, "username")
			pass := effectiveScheme.Password
			if pass == "" || !strings.Contains(pass, "{{secret.") {
				redactedCount++
				pass = redactedMask
			} else {
				pass = resolveSafe(pass, "password")
			}
			lines = append(lines, fmt.Sprintf("  -u %s", posixQuote(user+":"+pass)))
		case "apikey", "api-key":
			inLoc := NormalizeAPIKeyLocation(effectiveScheme.In)
			val := effectiveScheme.Value
			if val == "" || !strings.Contains(val, "{{secret.") {
				redactedCount++
				val = redactedMask
			} else {
				val = resolveSafe(val, effectiveScheme.Name)
			}
			if inLoc == "query" {
				notes = append(notes, LossItem{
					Code:     "curl-export-apikey-query",
					Severity: LossInfo,
					Field:    effectiveScheme.Name,
					Message:  fmt.Sprintf("API key %q in query is redacted as %s", effectiveScheme.Name, val),
				})
			} else if effectiveScheme.Name != "" {
				lines = append(lines, fmt.Sprintf("  -H %s", posixQuote(effectiveScheme.Name+": "+val)))
			}
		}
	}

	for _, h := range httpDef.Headers {
		if !h.IsEnabled() || strings.TrimSpace(h.Name) == "" {
			continue
		}
		hv := resolveSafe(h.Value, h.Name)
		lines = append(lines, fmt.Sprintf("  -H %s", posixQuote(h.Name+": "+hv)))
	}

	if httpDef.Body != nil {
		tb, _ := InspectBody(httpDef.Body)
		if tb != nil {
			switch tb.Kind {
			case BodyJSON:
				if tb.JSONNode != nil {
					var obj any
					if err := tb.JSONNode.Decode(&obj); err == nil {
						if b, err := json.Marshal(obj); err == nil {
							bodyText := resolveSafe(string(b), "")
							lines = append(lines, fmt.Sprintf("  --data-raw %s", posixQuote(bodyText)))
						}
					}
				}
			case BodyText, BodyXML:
				if tb.Text != "" {
					bodyText := resolveSafe(tb.Text, "")
					lines = append(lines, fmt.Sprintf("  --data-raw %s", posixQuote(bodyText)))
				}
			case BodyURLEncoded:
				for _, f := range tb.FormFields {
					if !f.IsEnabled() || strings.TrimSpace(f.Name) == "" {
						continue
					}
					fv := resolveSafe(f.Value, f.Name)
					lines = append(lines, fmt.Sprintf("  --data-urlencode %s", posixQuote(f.Name+"="+fv)))
				}
			case BodyFormData:
				for _, mp := range tb.MultipartParts {
					if !mp.IsEnabled() || strings.TrimSpace(mp.Name) == "" {
						continue
					}
					if mp.EffectiveKind() == "file" {
						fileRef := mp.File
						if mp.LocalFile != "" {
							localSlots = append(localSlots, mp.LocalFile)
							fileRef = "<local:" + mp.LocalFile + ">"
							notes = append(notes, LossItem{
								Code:     "curl-export-local-slot",
								Severity: LossSecurityScrubbed,
								Field:    mp.Name,
								Message:  fmt.Sprintf("multipart file field %q references local slot local:%s; host path omitted", mp.Name, mp.LocalFile),
							})
						} else if ref, err := ParseFileRef(mp.File); err == nil && ref.Kind == FileRefLocal {
							localSlots = append(localSlots, ref.Slot)
							fileRef = "<local:" + ref.Slot + ">"
							notes = append(notes, LossItem{
								Code:     "curl-export-local-slot",
								Severity: LossSecurityScrubbed,
								Field:    mp.Name,
								Message:  fmt.Sprintf("multipart file field %q references local slot local:%s; host path omitted", mp.Name, ref.Slot),
							})
						}
						part := mp.Name + "=@" + fileRef
						if mp.ContentType != "" {
							part += ";type=" + mp.ContentType
						}
						lines = append(lines, fmt.Sprintf("  -F %s", posixQuote(part)))
					} else {
						mv := resolveSafe(mp.Value, mp.Name)
						lines = append(lines, fmt.Sprintf("  -F %s", posixQuote(mp.Name+"="+mv)))
					}
				}
			case BodyBinary:
				fileRef := tb.File
				if tb.LocalFile != "" {
					localSlots = append(localSlots, tb.LocalFile)
					fileRef = "<local:" + tb.LocalFile + ">"
					notes = append(notes, LossItem{
						Code:     "curl-export-local-slot",
						Severity: LossSecurityScrubbed,
						Field:    "body.file",
						Message:  fmt.Sprintf("binary body references local slot local:%s; host path omitted", tb.LocalFile),
					})
				}
				if fileRef != "" {
					lines = append(lines, fmt.Sprintf("  --data-binary %s", posixQuote("@"+fileRef)))
				}
			}
		}
	}

	return &CURLExportResult{
		Command:        strings.Join(lines, " \\\n"),
		RedactedCount:  redactedCount,
		SecretRefs:     secretRefs,
		LocalFileSlots: localSlots,
		Notes:          notes,
	}, nil
}

func posixQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func tokenizeCURL(raw string) (tokens []string, losses []LossItem, blocked bool, err error) {
	src := strings.ReplaceAll(raw, "\r\n", "\n")

	var cur strings.Builder
	inToken := false
	inSingle := false
	inDouble := false
	inAnsiSingle := false

	for i := 0; i < len(src); i++ {
		ch := src[i]

		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(ch)
			}
			continue
		}

		if inAnsiSingle {
			if ch == '\'' {
				inAnsiSingle = false
				continue
			}
			if ch == '\\' && i+1 < len(src) {
				i++
				switch src[i] {
				case 'n':
					cur.WriteByte('\n')
				case 'r':
					cur.WriteByte('\r')
				case 't':
					cur.WriteByte('\t')
				case '\\', '\'', '"':
					cur.WriteByte(src[i])
				default:
					cur.WriteByte('\\')
					cur.WriteByte(src[i])
				}
				continue
			}
			cur.WriteByte(ch)
			continue
		}

		if inDouble {
			if ch == '"' {
				inDouble = false
				continue
			}
			if ch == '\\' && i+1 < len(src) {
				next := src[i+1]
				if next == '\n' {
					i++
					continue
				}
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					cur.WriteByte(next)
					i++
					continue
				}
			}
			if ch == '`' || (ch == '$' && i+1 < len(src) && src[i+1] == '(') {
				blocked = true
				losses = append(losses, LossItem{
					Code:     "curl-shell-construct-blocked",
					Severity: LossBlocked,
					Message:  "shell command substitution ($(...) or backticks) inside double quotes is blocked; GoRequest never executes shell subcommands",
				})
			}
			cur.WriteByte(ch)
			continue
		}

		if ch == '\\' {
			if i+1 < len(src) {
				if src[i+1] == '\n' {
					i++
					continue
				}
				i++
				inToken = true
				cur.WriteByte(src[i])
				continue
			}
			continue
		}

		if ch == '#' && !inToken {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}

		if ch == '$' && i+1 < len(src) && src[i+1] == '\'' {
			inToken = true
			inAnsiSingle = true
			i++
			continue
		}

		if ch == '`' || (ch == '$' && i+1 < len(src) && src[i+1] == '(') {
			blocked = true
			losses = append(losses, LossItem{
				Code:     "curl-shell-construct-blocked",
				Severity: LossBlocked,
				Message:  "shell command substitution ($(...) or backticks) is blocked; GoRequest never invokes a shell",
			})
			inToken = true
			cur.WriteByte(ch)
			continue
		}

		if ch == ';' || ch == '|' || ch == '>' || ch == '<' || (ch == '&' && i+1 < len(src) && src[i+1] == '&') {
			blocked = true
			losses = append(losses, LossItem{
				Code:     "curl-shell-construct-blocked",
				Severity: LossBlocked,
				Message:  fmt.Sprintf("unquoted shell operator %q is blocked; import only accepts a single curl invocation without pipes, redirections, or chained commands", string(ch)),
			})
			break
		}

		if ch == '\'' {
			inToken = true
			inSingle = true
			continue
		}
		if ch == '"' {
			inToken = true
			inDouble = true
			continue
		}

		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			if inToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inToken = false
			}
			continue
		}

		inToken = true
		cur.WriteByte(ch)
	}

	if inSingle || inDouble || inAnsiSingle {
		return nil, losses, true, errors.New("unterminated quote in curl command")
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, losses, blocked, nil
}

func expandShortCURLFlags(args []string) []string {
	valueTakingShort := map[byte]bool{
		'X': true,
		'H': true,
		'd': true,
		'F': true,
		'u': true,
		'A': true,
		'e': true,
		'b': true,
		'o': true,
		'D': true,
		'c': true,
		'x': true,
		'E': true,
		'm': true,
	}
	boolShort := map[byte]bool{
		'G': true,
		'I': true,
		'k': true,
		'L': true,
		's': true,
		'S': true,
		'v': true,
		'i': true,
		'O': true,
	}

	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") || len(a) <= 2 {
			out = append(out, a)
			continue
		}
		first := a[1]
		if valueTakingShort[first] {
			out = append(out, "-"+string(first), a[2:])
			continue
		}
		allBool := true
		for i := 1; i < len(a); i++ {
			if !boolShort[a[i]] {
				allBool = false
				break
			}
		}
		if allBool {
			for i := 1; i < len(a); i++ {
				out = append(out, "-"+string(a[i]))
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func curlUnsupportedFlagTakesValue(flag string) bool {
	switch flag {
	case "-x", "--proxy", "--proxy-user", "-E", "--cert", "--key", "--cacert",
		"--capath", "--resolve", "--connect-to", "-o", "--output",
		"-D", "--dump-header", "-c", "--cookie-jar", "-m", "--max-time",
		"--connect-timeout", "--retry":
		return true
	default:
		return false
	}
}

func looksLikeURLToken(tok string) bool {
	lower := strings.ToLower(tok)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "{{") ||
		strings.HasPrefix(lower, "localhost")
}

func parseHeaderLine(line string) (string, string, bool) {
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		if semi := strings.IndexByte(line, ';'); semi > 0 && semi == len(strings.TrimSpace(line))-1 {
			return strings.TrimSpace(line[:semi]), "", true
		}
		return "", "", false
	}
	k := strings.TrimSpace(line[:idx])
	v := strings.TrimSpace(line[idx+1:])
	if k == "" {
		return "", "", false
	}
	return k, v, true
}

func parseAuthorizationHeaderToAuth(
	val string,
	scrubToRef bool,
	addSecretSlot func(slot, rawVal, scope, src string) string,
	red *Redactor,
) *AuthRef {
	trimmed := strings.TrimSpace(val)
	parts := strings.SplitN(trimmed, " ", 2)
	if len(parts) != 2 {
		return nil
	}
	scheme := strings.ToLower(strings.TrimSpace(parts[0]))
	cred := strings.TrimSpace(parts[1])
	switch scheme {
	case "bearer":
		red.RegisterSecret(cred)
		tokenVal := cred
		if scrubToRef && cred != "" && !strings.Contains(cred, "{{") {
			tokenVal = addSecretSlot("bearer_token", cred, "request-auth", "curl-header")
		}
		return &AuthRef{
			Mode: "inline",
			Inline: &AuthScheme{
				Type:  "bearer",
				Token: tokenVal,
			},
		}
	case "basic":
		decoded, err := base64.StdEncoding.DecodeString(cred)
		if err == nil {
			user, pass, _ := strings.Cut(string(decoded), ":")
			red.RegisterBasicPair(user, pass)
			passVal := pass
			if scrubToRef && pass != "" && !strings.Contains(pass, "{{") {
				passVal = addSecretSlot("basic_password", pass, "request-auth", "curl-header")
			}
			return &AuthRef{
				Mode: "inline",
				Inline: &AuthScheme{
					Type:     "basic",
					Username: user,
					Password: passVal,
				},
			}
		}
	}
	return nil
}

func upsertHeaderKV(headers []KV, key, val string) []KV {
	for i := range headers {
		if strings.EqualFold(headers[i].Name, key) {
			headers[i].Value = val
			headers[i].Enabled = nil
			return headers
		}
	}
	return append(headers, KV{Name: key, Value: val})
}

func ensureHeaderKV(headers []KV, key, val string) []KV {
	for i := range headers {
		if strings.EqualFold(headers[i].Name, key) {
			return headers
		}
	}
	return append(headers, KV{Name: key, Value: val})
}

func splitCURLTargetURL(rawURL string) (string, []KV, *LossItem) {
	trimmed := strings.TrimSpace(rawURL)
	var loss *LossItem
	if !strings.Contains(trimmed, "://") && !strings.HasPrefix(trimmed, "{{") {
		trimmed = "http://" + trimmed
		loss = &LossItem{
			Code:     "curl-url-default-scheme",
			Severity: LossInfo,
			Field:    "url",
			Message:  fmt.Sprintf("URL %q had no scheme; defaulted to http://", rawURL),
		}
	}
	qIdx := strings.IndexByte(trimmed, '?')
	if qIdx < 0 {
		return trimmed, nil, loss
	}
	base := trimmed[:qIdx]
	queryRaw := trimmed[qIdx+1:]
	if fragIdx := strings.IndexByte(queryRaw, '#'); fragIdx >= 0 {
		queryRaw = queryRaw[:fragIdx]
	}
	kvs := parseFormURLEncodedKVs(queryRaw)
	return base, kvs, loss
}

func parseFormURLEncodedKVs(raw string) []KV {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []KV
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		kRaw, vRaw, hasEq := strings.Cut(pair, "=")
		k, err1 := url.QueryUnescape(kRaw)
		if err1 != nil {
			k = kRaw
		}
		v := ""
		if hasEq {
			if uv, err2 := url.QueryUnescape(vRaw); err2 == nil {
				v = uv
			} else {
				v = vRaw
			}
		}
		out = append(out, KV{Name: k, Value: v})
	}
	return out
}

func parseDataURLEncodeField(expr string, addLocalSlot func(rawPath, field string) string) FormField {
	if idx := strings.IndexByte(expr, '='); idx >= 0 {
		name := expr[:idx]
		val := expr[idx+1:]
		if name == "" {
			return FormField{Name: "value", Value: val}
		}
		return FormField{Name: name, Value: val}
	}
	if idx := strings.IndexByte(expr, '@'); idx >= 0 {
		name := expr[:idx]
		filePath := expr[idx+1:]
		if name == "" {
			name = "file"
		}
		slot := addLocalSlot(filePath, "--data-urlencode")
		return FormField{Name: name, Value: FormatLocalFileRef(slot)}
	}
	return FormField{Name: expr, Value: ""}
}

func parseCURLFormPart(expr string, forceString bool, addLocalSlot func(rawPath, field string) string) MultipartPart {
	name, rest, hasEq := strings.Cut(expr, "=")
	if !hasEq {
		return MultipartPart{
			Name:  strings.TrimSpace(expr),
			Kind:  "text",
			Value: "",
		}
	}
	name = strings.TrimSpace(name)
	if forceString {
		return MultipartPart{
			Name:  name,
			Kind:  "text",
			Value: rest,
		}
	}

	segments := strings.Split(rest, ";")
	mainVal := segments[0]
	contentType := ""
	fileName := ""
	for _, seg := range segments[1:] {
		seg = strings.TrimSpace(seg)
		lowerSeg := strings.ToLower(seg)
		if strings.HasPrefix(lowerSeg, "type=") {
			contentType = strings.Trim(strings.TrimSpace(seg[5:]), `"'`)
		} else if strings.HasPrefix(lowerSeg, "filename=") {
			fileName = strings.Trim(strings.TrimSpace(seg[9:]), `"'`)
		}
	}

	if (strings.HasPrefix(mainVal, "@") || strings.HasPrefix(mainVal, "<")) && len(mainVal) > 1 {
		filePath := mainVal[1:]
		slot := addLocalSlot(filePath, "-F "+name)
		return MultipartPart{
			Name:        name,
			Kind:        "file",
			LocalFile:   slot,
			FileName:    fileName,
			ContentType: contentType,
		}
	}

	return MultipartPart{
		Name:        name,
		Kind:        "text",
		Value:       mainVal,
		ContentType: contentType,
	}
}

func buildCURLBody(
	hasJSONFlag bool,
	jsonPayload string,
	dataParts []string,
	urlEncodeParts []string,
	formParts []MultipartPart,
	binarySlot string,
	headers []KV,
	addLocalSlot func(rawPath, field string) string,
) (*Body, error) {
	if len(formParts) > 0 {
		tb := &TypedBody{
			Kind:           BodyFormData,
			MultipartParts: formParts,
		}
		return tb.ToBody()
	}
	if binarySlot != "" {
		tb := &TypedBody{
			Kind:      BodyBinary,
			LocalFile: binarySlot,
		}
		return tb.ToBody()
	}
	if hasJSONFlag {
		node, err := parseJSONOrScalarYAMLNode(jsonPayload)
		if err != nil {
			return nil, err
		}
		tb := &TypedBody{
			Kind:           BodyJSON,
			JSONNode:       node,
			HasInlineValue: true,
		}
		return tb.ToBody()
	}
	if len(urlEncodeParts) > 0 && len(dataParts) == 0 {
		var fields []FormField
		for _, up := range urlEncodeParts {
			fields = append(fields, parseDataURLEncodeField(up, addLocalSlot))
		}
		tb := &TypedBody{
			Kind:           BodyURLEncoded,
			FormFields:     fields,
			HasInlineValue: true,
		}
		return tb.ToBody()
	}
	if len(dataParts) == 0 && len(urlEncodeParts) == 0 {
		return nil, nil
	}

	combined := strings.Join(dataParts, "&")
	ct := strings.ToLower(findHeaderValue(headers, "Content-Type"))

	if strings.Contains(ct, "json") || (!strings.Contains(ct, "urlencoded") && looksLikeJSONObjectOrArray(combined)) {
		node, err := parseJSONOrScalarYAMLNode(combined)
		if err == nil {
			tb := &TypedBody{
				Kind:           BodyJSON,
				JSONNode:       node,
				HasInlineValue: true,
			}
			return tb.ToBody()
		}
	}
	if strings.Contains(ct, "xml") {
		tb := &TypedBody{
			Kind:           BodyXML,
			Text:           combined,
			HasInlineValue: true,
		}
		return tb.ToBody()
	}
	if (strings.Contains(ct, "urlencoded") || (ct == "" && strings.Contains(combined, "="))) && !strings.Contains(combined, "\n") {
		kvs := parseFormURLEncodedKVs(combined)
		var fields []FormField
		for _, kv := range kvs {
			fields = append(fields, FormField{Name: kv.Name, Value: kv.Value})
		}
		for _, up := range urlEncodeParts {
			fields = append(fields, parseDataURLEncodeField(up, addLocalSlot))
		}
		if len(fields) > 0 {
			tb := &TypedBody{
				Kind:           BodyURLEncoded,
				FormFields:     fields,
				HasInlineValue: true,
			}
			return tb.ToBody()
		}
	}
	tb := &TypedBody{
		Kind:           BodyText,
		Text:           combined,
		HasInlineValue: true,
	}
	return tb.ToBody()
}

func parseJSONOrScalarYAMLNode(raw string) (*yaml.Node, error) {
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader([]byte(raw)))
	if err := dec.Decode(&doc); err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: raw}, nil
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0], nil
	}
	return &doc, nil
}

func findHeaderValue(headers []KV, key string) string {
	for _, h := range headers {
		if h.IsEnabled() && strings.EqualFold(h.Name, key) {
			return h.Value
		}
	}
	return ""
}

func looksLikeJSONObjectOrArray(s string) bool {
	t := strings.TrimSpace(s)
	if (strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}")) ||
		(strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")) {
		var js any
		return json.Unmarshal([]byte(t), &js) == nil
	}
	return false
}

func deriveRequestNameFromURL(method, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil && u.Path != "" && u.Path != "/" {
		clean := strings.Trim(u.Path, "/")
		return method + " " + clean
	}
	if err == nil && u.Host != "" {
		return method + " " + u.Host
	}
	return method + " request"
}

func isSensitiveKeyName(k string) bool {
	lower := strings.ToLower(strings.TrimSpace(k))
	for _, needle := range []string{
		"authorization",
		"proxy-authorization",
		"x-api-key",
		"api-key",
		"apikey",
		"api_key",
		"secret",
		"token",
		"password",
		"passwd",
		"private-key",
		"private_key",
		"client-secret",
		"client_secret",
		"access-key",
		"access_key",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func stageSecretSlotsOnEnv(p *Project, cs *ChangeSet, envID string, secretSlots []string, usedPaths map[string]bool) error {
	if p == nil || cs == nil || len(secretSlots) == 0 {
		return nil
	}
	rel := environmentRelPath(envID)
	if p.EnvEntries != nil {
		if existing, ok := p.EnvEntries[envID]; ok && existing != nil && existing.RelPath != "" {
			rel = existing.RelPath
		}
	}
	if usedPaths[rel] {
		return nil
	}
	var env *Environment
	var rawExisting []byte
	var baseline string
	if p.Store != nil && p.Store.Exists(rel) {
		if b, d, err := p.Store.Read(rel); err == nil {
			rawExisting = b
			baseline = d
			var parsed Environment
			if err := decodeStrict(b, &parsed); err == nil {
				env = &parsed
			}
		}
	}
	if env == nil {
		if existing, ok := p.Environments[envID]; ok && existing != nil {
			cp := *existing
			cp.Secrets = slices.Clone(existing.Secrets)
			env = &cp
		} else {
			env = &Environment{
				SchemaVersion: SupportedSchemaVersion,
				ID:            envID,
				Variables:     map[string]string{},
			}
		}
	}
	changed := false
	for _, s := range secretSlots {
		if !slices.Contains(env.Secrets, s) {
			env.Secrets = append(env.Secrets, s)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	encoded, err := MergeEnvironmentYAML(rawExisting, env)
	if err != nil {
		return err
	}
	usedPaths[rel] = true
	if baseline != "" {
		cs.Update(rel, encoded, baseline)
	} else {
		cs.Create(rel, encoded)
	}
	return nil
}
