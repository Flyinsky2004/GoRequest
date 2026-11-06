package core

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// This file implements the B02 cross-workspace request transfer used by the
// "copy/move between a project and the global workspace" actions. It is pure
// filesystem and data logic; the desktop layer only previews and confirms.
//
// Rules:
//   - copy  = the destination gets a fresh stable ID and a fresh file path;
//   - move  = the stable ID is preserved; an ID already present in the
//     destination refuses the move rather than overwriting anything;
//   - the source is verified against its baseline digest, and a move removes
//     the source only after the target write succeeded (rolling the target back
//     if removal fails);
//   - inline credentials and literal Cookie / Authorization values block the
//     transfer, because copying a real secret or cookie across workspaces is
//     never silent. Secret references ({{secret.name}}) are kept as references;
//     their values live in the device's credential store and are not copied;
//   - local-machine file mapping slots are kept as slot names (never absolute
//     paths); the destination must bind them itself, which the preview warns
//     about.

// Transfer modes.
const (
	TransferCopy = "copy"
	TransferMove = "move"
)

// Transfer warning codes. The desktop maps each code to a localized sentence;
// Detail carries only non-sensitive context (a name, path, scheme id).
const (
	WarnInlineCredential   = "inline-credential"
	WarnUnresolvedAuth     = "unresolved-auth"
	WarnUnresolvedVariable = "unresolved-variable"
	WarnMissingDoc         = "missing-doc"
	WarnLocalFileBinding   = "local-file-binding"
	WarnTargetDefaultAuth  = "target-default-auth"
)

// TransferWarning is one preview finding. It never contains a secret value.
type TransferWarning struct {
	Code   string
	Detail string
}

// RequestTransfer is a prepared copy/move between two loaded workspaces. Build
// it with PlanRequestTransfer, inspect it in a preview, then call Apply.
type RequestTransfer struct {
	Mode         string
	Source       *Project
	Target       *Project
	Entry        *RequestEntry
	TargetID     string
	TargetRel    string
	TargetFolder string // collections-relative folder ("" for the root)
	KeepID       bool
	// Blocked is set when the request carries inline credentials or literal
	// cookie/authorization values. The preview must not offer to apply it.
	Blocked    bool
	Warnings   []TransferWarning
	SecretRefs []string
	VarRefs    []string
	DocRefs    []string

	request *Request
	content []byte
}

// PlanRequestTransfer validates a transfer and returns its preview. It reads
// the source file (so a stale baseline is a visible conflict) but writes
// nothing. targetCollection is relative to the destination's collections/
// directory ("" means its root).
func PlanRequestTransfer(src, dst *Project, entry *RequestEntry, targetCollection, mode string) (*RequestTransfer, error) {
	if src == nil || dst == nil {
		return nil, configErrorf("transfer needs a source and a destination workspace")
	}
	if entry == nil || entry.Request == nil {
		return nil, configErrorf("no request to transfer")
	}
	if mode != TransferCopy && mode != TransferMove {
		return nil, configErrorf("unknown transfer mode %q", mode)
	}
	targetRel, err := sanitizeCollectionRel(targetCollection)
	if err != nil {
		return nil, err
	}
	if targetRel != "" && !dst.Store.Exists(collectionsDirOf(targetRel)) {
		return nil, configErrorf("target folder %q does not exist in the destination", targetRel)
	}

	content, diskDigest, err := src.Store.Read(entry.RelPath)
	if err != nil {
		return nil, fmt.Errorf("reading source request: %w", err)
	}
	if entry.BaselineDigest != "" && diskDigest != entry.BaselineDigest {
		return nil, &ConflictError{Path: entry.RelPath, Baseline: entry.BaselineDigest, Disk: diskDigest}
	}

	tr := &RequestTransfer{
		Mode:         mode,
		Source:       src,
		Target:       dst,
		Entry:        entry,
		TargetFolder: targetRel,
		KeepID:       mode == TransferMove,
		content:      content,
	}

	reqCopy := *entry.Request
	if mode == TransferCopy {
		id, rel := uniqueRequestIdentity(dst, targetRel, entry.Request.Name, "", entry.Request.ID)
		tr.TargetID, tr.TargetRel = id, rel
		reqCopy.ID = id
		tr.request = &reqCopy
	} else {
		if existing := dst.Requests[entry.Request.ID]; existing != nil {
			return nil, configErrorf("destination already has request id %q", entry.Request.ID)
		}
		_, rel := uniqueRequestIdentity(dst, targetRel, entry.Request.Name, entry.Request.ID, "")
		tr.TargetID, tr.TargetRel = entry.Request.ID, rel
		tr.request = &reqCopy
	}

	tr.SecretRefs = secretRefs(requestTexts(entry.Request)...)
	tr.DocRefs = append([]string(nil), entry.Request.Documentation...)
	tr.VarRefs = templateVariableRefs(requestTexts(entry.Request)...)

	blocked, sensitive := sensitiveTransferFindings(entry.Request)
	tr.Blocked = blocked
	for _, detail := range sensitive {
		tr.Warnings = append(tr.Warnings, TransferWarning{Code: WarnInlineCredential, Detail: detail})
	}
	tr.Warnings = append(tr.Warnings, transferReferenceWarnings(dst, entry.Request, targetRel, tr.VarRefs, tr.DocRefs)...)
	return tr, nil
}

// Apply performs the prepared transfer. It re-validates the source baseline,
// writes the destination first, and removes the source afterwards for a move.
func (t *RequestTransfer) Apply() error {
	if t == nil || t.Source == nil || t.Target == nil || t.Entry == nil || t.request == nil {
		return configErrorf("transfer is not prepared")
	}
	if t.Blocked {
		return configErrorf("transfer is blocked: inline credentials must be replaced by {{secret.name}} references")
	}
	if t.Target.Store.Exists(t.TargetRel) {
		return configErrorf("destination file %q already exists", t.TargetRel)
	}

	// Re-check the source right before writing so an edit between preview and
	// confirmation is reported instead of being silently overwritten/removed.
	_, diskDigest, err := t.Source.Store.Read(t.Entry.RelPath)
	if err != nil {
		return fmt.Errorf("reading source request: %w", err)
	}
	if t.Entry.BaselineDigest != "" && diskDigest != t.Entry.BaselineDigest {
		return &ConflictError{Path: t.Entry.RelPath, Baseline: t.Entry.BaselineDigest, Disk: diskDigest}
	}

	var data []byte
	if t.Mode == TransferMove {
		// Moving preserves the file's own content byte for byte (the ID is
		// unchanged), so comments and ordering survive.
		data = t.content
	} else {
		data, err = t.Target.EncodeRequest(t.request)
		if err != nil {
			return err
		}
	}
	digest := Digest(data)
	if err := t.Target.Store.SaveChecked(t.TargetRel, data, ""); err != nil {
		return err
	}

	if t.Mode == TransferMove {
		if _, err := t.Source.DeleteRequest(t.Entry); err != nil {
			// Roll the newly written target back so a failed move never leaves
			// two copies behind.
			_ = t.Target.Store.RemoveChecked(t.TargetRel, digest)
			return fmt.Errorf("removing source request: %w", err)
		}
	}

	req := t.request
	if t.Mode == TransferMove {
		req = t.Entry.Request
	}
	entry := &RequestEntry{
		Request:        req,
		RelPath:        t.TargetRel,
		BaselineDigest: digest,
		CollectionVars: t.Target.collectionVarsFor(t.TargetRel, t.Target.colByDir),
	}
	t.Target.Requests[req.ID] = entry
	t.Target.Order = append(t.Target.Order, req.ID)
	t.Target.rebuildTree()
	return nil
}

// uniqueRequestIdentity picks a stable ID and file path in p's target folder.
// forcedID keeps that exact ID (move); otherwise the ID is derived from the
// name with the folder prefix used by NewRequest and suffixed until unique and
// distinct from avoidID, so a copy never silently reuses the source ID.
func uniqueRequestIdentity(p *Project, collectionRel, name, forcedID, avoidID string) (id, rel string) {
	base := Slug(name)
	if base == "" {
		base = "request"
	}
	dir := collectionsDirOf(collectionRel)
	prefix := ""
	if collectionRel != "" {
		prefix = Slug(filepath.Base(collectionRel)) + "."
	}
	if forcedID != "" {
		id = forcedID
	} else {
		id = prefix + base
		for i := 2; ; i++ {
			_, exists := p.Requests[id]
			if !exists && id != avoidID {
				break
			}
			id = fmt.Sprintf("%s%s-%d", prefix, base, i)
		}
	}
	rel = filepath.ToSlash(filepath.Join(dir, base+".request.yaml"))
	for i := 2; p.Store.Exists(rel); i++ {
		rel = filepath.ToSlash(filepath.Join(dir, fmt.Sprintf("%s-%d.request.yaml", base, i)))
	}
	return id, rel
}

// sensitiveTransferFindings reports inline credentials that must not be copied
// into another workspace. Details name the location, never the value.
func sensitiveTransferFindings(req *Request) (bool, []string) {
	if req == nil {
		return false, nil
	}
	var findings []string
	add := func(where string) { findings = append(findings, where) }

	if req.Auth != nil {
		scheme := req.Auth.Inline
		if req.Auth.Mode == "inline" && scheme != nil {
			switch scheme.Type {
			case "bearer":
				if literalSecret(scheme.Token) {
					add("auth bearer token")
				}
			case "basic":
				if literalSecret(scheme.Password) {
					add("auth basic password")
				}
			case "apikey":
				if literalSecret(scheme.Value) {
					add("auth api key value")
				}
			}
		}
	}

	if req.HTTP != nil {
		if password, ok := urlUserInfoPassword(req.HTTP.URL); ok && literalSecret(password) {
			add("URL userinfo password")
		}
		for _, kv := range req.HTTP.Headers {
			name := strings.ToLower(strings.TrimSpace(kv.Name))
			switch name {
			case "authorization", "cookie", "proxy-authorization", "x-api-key":
				if literalSecret(kv.Value) {
					add("literal " + kv.Name + " header")
				}
			}
		}
	}
	sort.Strings(findings)
	return len(findings) > 0, findings
}

// literalSecret reports whether a value carries a real secret rather than a
// secret reference. An empty value or one containing {{secret. is not treated
// as literal; a header such as "Bearer {{secret.token}}" is a reference.
func literalSecret(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return false
	}
	return !strings.Contains(v, "{{secret.")
}

func urlUserInfoPassword(raw string) (string, bool) {
	rest, ok := strings.CutPrefix(raw, "http://")
	if !ok {
		rest, ok = strings.CutPrefix(raw, "https://")
	}
	if !ok {
		return "", false
	}
	at := strings.Index(rest, "@")
	if at < 0 {
		return "", false
	}
	userinfo := rest[:at]
	colon := strings.Index(userinfo, ":")
	if colon < 0 || colon == len(userinfo)-1 {
		return "", false
	}
	return userinfo[colon+1:], true
}

// transferReferenceWarnings previews references that the destination does not
// satisfy. They are warnings, not failures: environments or device bindings may
// still provide the value after the transfer.
func transferReferenceWarnings(dst *Project, req *Request, targetRel string, varRefs, docRefs []string) []TransferWarning {
	var out []TransferWarning
	if dst == nil || req == nil {
		return out
	}
	if req.Auth != nil && req.Auth.Mode == "ref" {
		if _, ok := dst.AuthSchemes[req.Auth.Ref]; !ok {
			out = append(out, TransferWarning{Code: WarnUnresolvedAuth, Detail: req.Auth.Ref})
		}
	}
	if dst.File != nil && dst.File.Defaults != nil && dst.File.Defaults.Auth != "" {
		out = append(out, TransferWarning{Code: WarnTargetDefaultAuth, Detail: dst.File.Defaults.Auth})
	}
	defined := destinationVariables(dst, targetRel)
	for _, name := range varRefs {
		if !defined[name] {
			out = append(out, TransferWarning{Code: WarnUnresolvedVariable, Detail: name})
		}
	}
	docs := map[string]bool{}
	for _, d := range dst.Docs {
		docs[d.RelPath] = true
	}
	for _, rel := range docRefs {
		if !docs[rel] {
			out = append(out, TransferWarning{Code: WarnMissingDoc, Detail: rel})
		}
	}
	for _, slot := range localFileSlots(req) {
		out = append(out, TransferWarning{Code: WarnLocalFileBinding, Detail: slot})
	}
	return out
}

func destinationVariables(dst *Project, targetRel string) map[string]bool {
	defined := map[string]bool{}
	if dst == nil {
		return defined
	}
	if dst.File != nil {
		for k := range dst.File.Variables {
			defined[k] = true
		}
	}
	for k := range dst.collectionVarsFor(collectionsDirOf(targetRel)+"/placeholder.request.yaml", dst.colByDir) {
		defined[k] = true
	}
	for _, env := range dst.Environments {
		for k := range env.Variables {
			defined[k] = true
		}
	}
	return defined
}

func localFileSlots(req *Request) []string {
	if req == nil || req.HTTP == nil || req.HTTP.Body == nil {
		return nil
	}
	tb, err := InspectBody(req.HTTP.Body)
	if err != nil {
		return nil
	}
	var slots []string
	if tb.LocalFile != "" {
		slots = append(slots, tb.LocalFile)
	}
	for _, part := range tb.MultipartParts {
		if part.LocalFile != "" {
			slots = append(slots, part.LocalFile)
		}
	}
	sort.Strings(slots)
	return slots
}
