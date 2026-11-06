package core

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

func init() {
	encodeRequestPreservingHook = MergeRequestYAML
}

type yamlPathCtx int

const (
	ctxRoot yamlPathCtx = iota
	ctxGeneral
	ctxHTTP
	ctxBody
	ctxJSONValue
	ctxKVSeq
	ctxKVItem
)

// MergeRequestYAML updates original YAML bytes to reflect updated while
// preserving comments (header, inline, footer), key ordering, flow/quote styles
// on untouched fields, explicit enabled:true on KV items, block YAML style in
// JSON body values, and top-level extensions. It refuses to downgrade files
// with unsupported higher schemaVersion.
func MergeRequestYAML(original []byte, updated *Request) ([]byte, error) {
	if updated == nil {
		return nil, configErrorf("request is nil")
	}
	cp := *updated
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	if cp.SchemaVersion != SupportedSchemaVersion {
		return nil, configErrorf("unsupported request schemaVersion %d (this build writes %d)", cp.SchemaVersion, SupportedSchemaVersion)
	}
	return mergeEntityYAML(EntityRequest, original, &cp)
}

// MergeEnvironmentYAML updates original environment YAML while preserving
// comments, variable key order, and top-level extensions.
func MergeEnvironmentYAML(original []byte, updated *Environment) ([]byte, error) {
	if updated == nil {
		return nil, configErrorf("environment is nil")
	}
	cp := *updated
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	if cp.SchemaVersion != SupportedSchemaVersion {
		return nil, configErrorf("unsupported environment schemaVersion %d (this build writes %d)", cp.SchemaVersion, SupportedSchemaVersion)
	}
	return mergeEntityYAML(EntityEnvironment, original, &cp)
}

// MergeCollectionYAML updates original collection.yaml while preserving
// comments, variable key order, and top-level extensions.
func MergeCollectionYAML(original []byte, updated *Collection) ([]byte, error) {
	if updated == nil {
		return nil, configErrorf("collection is nil")
	}
	cp := *updated
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	if cp.SchemaVersion != SupportedSchemaVersion {
		return nil, configErrorf("unsupported collection schemaVersion %d (this build writes %d)", cp.SchemaVersion, SupportedSchemaVersion)
	}
	return mergeEntityYAML(EntityCollection, original, &cp)
}

// MergeProjectFileYAML updates original project.yaml while preserving comments,
// variable key order, and top-level extensions.
func MergeProjectFileYAML(original []byte, updated *ProjectFile) ([]byte, error) {
	if updated == nil {
		return nil, configErrorf("project file is nil")
	}
	cp := *updated
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	if cp.SchemaVersion != SupportedSchemaVersion {
		return nil, configErrorf("unsupported project schemaVersion %d (this build writes %d)", cp.SchemaVersion, SupportedSchemaVersion)
	}
	return mergeEntityYAML(EntityProject, original, &cp)
}

func mergeEntityYAML(kind SyncEntityKind, original []byte, targetStruct any) ([]byte, error) {
	if len(bytes.TrimSpace(original)) == 0 {
		return encodeYAML(targetStruct)
	}
	origDoc, err := ValidateEntityYAML(kind, original, YAMLLimits{})
	if err != nil {
		return nil, err
	}
	targetDoc, err := marshalStructToNode(targetStruct)
	if err != nil {
		return nil, err
	}
	origRoot := origDoc.Content[0]
	targetRoot := targetDoc.Content[0]

	// Fast no-op path: if zero semantic fields changed, keep original bytes
	// intact (normalized to UTF-8 LF with trailing newline per storage-format §3).
	if nodesSemanticallyEqual(origRoot, targetRoot, ctxRoot) {
		return ensureTrailingNewline(NormalizeLF(original)), nil
	}

	patchMapping(origRoot, targetRoot, ctxRoot)
	return encodeDocumentNode(origDoc)
}

func marshalStructToNode(v any) (*yaml.Node, error) {
	raw, err := encodeYAML(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func encodeDocumentNode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return ensureTrailingNewline(NormalizeLF(buf.Bytes())), nil
}

func ensureTrailingNewline(b []byte) []byte {
	if len(b) == 0 {
		return []byte{'\n'}
	}
	if b[len(b)-1] != '\n' {
		out := make([]byte, len(b)+1)
		copy(out, b)
		out[len(b)] = '\n'
		return out
	}
	return b
}

func childYAMLContext(parent yamlPathCtx, key string, parentOrig, parentTarget *yaml.Node) yamlPathCtx {
	switch parent {
	case ctxRoot:
		if key == "http" {
			return ctxHTTP
		}
		return ctxGeneral
	case ctxHTTP:
		switch key {
		case "query", "headers":
			return ctxKVSeq
		case "body":
			return ctxBody
		}
		return ctxGeneral
	case ctxBody:
		if key == "value" {
			bodyType := ""
			if tn := mappingValue(parentTarget, "type"); tn != nil {
				bodyType = strings.ToLower(strings.TrimSpace(tn.Value))
			} else if on := mappingValue(parentOrig, "type"); on != nil {
				bodyType = strings.ToLower(strings.TrimSpace(on.Value))
			}
			if bodyType == "json" {
				return ctxJSONValue
			}
		}
		return ctxGeneral
	case ctxJSONValue:
		return ctxJSONValue
	default:
		return ctxGeneral
	}
}

func nodesSemanticallyEqual(orig, target *yaml.Node, ctx yamlPathCtx) bool {
	if orig == nil && target == nil {
		return true
	}
	if orig == nil || target == nil {
		return false
	}
	orig = derefAlias(orig)
	target = derefAlias(target)
	if orig.Kind != target.Kind {
		return false
	}

	switch orig.Kind {
	case yaml.ScalarNode:
		return scalarsSemanticallyEqual(orig, target)

	case yaml.SequenceNode:
		if len(orig.Content) != len(target.Content) {
			return false
		}
		elemCtx := ctxGeneral
		switch ctx {
		case ctxKVSeq:
			elemCtx = ctxKVItem
		case ctxJSONValue:
			elemCtx = ctxJSONValue
		}
		for i := range orig.Content {
			if !nodesSemanticallyEqual(orig.Content[i], target.Content[i], elemCtx) {
				return false
			}
		}
		return true

	case yaml.MappingNode:
		if ctx == ctxKVItem {
			return kvItemsSemanticallyEqual(orig, target)
		}
		origKeys := map[string]*yaml.Node{}
		for i := 0; i+1 < len(orig.Content); i += 2 {
			k := orig.Content[i].Value
			if ctx == ctxRoot && k == "extensions" {
				continue
			}
			origKeys[k] = orig.Content[i+1]
		}
		targetKeys := map[string]*yaml.Node{}
		for i := 0; i+1 < len(target.Content); i += 2 {
			k := target.Content[i].Value
			if ctx == ctxRoot && k == "extensions" {
				continue
			}
			targetKeys[k] = target.Content[i+1]
		}
		if len(origKeys) != len(targetKeys) {
			return false
		}
		for k, tv := range targetKeys {
			ov, ok := origKeys[k]
			if !ok {
				return false
			}
			cctx := childYAMLContext(ctx, k, orig, target)
			if !nodesSemanticallyEqual(ov, tv, cctx) {
				return false
			}
		}
		return true
	}
	return false
}

func derefAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

func scalarsSemanticallyEqual(a, b *yaml.Node) bool {
	tagA := a.ShortTag()
	tagB := b.ShortTag()
	if tagA != tagB {
		return false
	}
	return a.Value == b.Value
}

func kvItemsSemanticallyEqual(orig, target *yaml.Node) bool {
	if orig.Kind != yaml.MappingNode || target.Kind != yaml.MappingNode {
		return false
	}
	origName := scalarVal(mappingValue(orig, "name"))
	targName := scalarVal(mappingValue(target, "name"))
	if origName != targName {
		return false
	}
	origVal := scalarVal(mappingValue(orig, "value"))
	targVal := scalarVal(mappingValue(target, "value"))
	if origVal != targVal {
		return false
	}
	return kvNodeEnabled(orig) == kvNodeEnabled(target)
}

func scalarVal(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

func kvNodeEnabled(m *yaml.Node) bool {
	en := mappingValue(m, "enabled")
	if en == nil {
		return true
	}
	return strings.ToLower(strings.TrimSpace(en.Value)) != "false"
}

func patchMapping(orig, target *yaml.Node, ctx yamlPathCtx) {
	if orig == nil || target == nil || orig.Kind != yaml.MappingNode || target.Kind != yaml.MappingNode {
		return
	}
	// Preserve block style when orig was block YAML (important when target came
	// from JSON unmarshaling in ParseBodyText, which sets FlowStyle!).
	if orig.Style == 0 && target.Style == yaml.FlowStyle {
		orig.Style = 0
	}

	targetByKey := make(map[string]*yaml.Node, len(target.Content)/2)
	targetOrder := make([]string, 0, len(target.Content)/2)
	for i := 0; i+1 < len(target.Content); i += 2 {
		k := target.Content[i].Value
		targetByKey[k] = target.Content[i+1]
		targetOrder = append(targetOrder, k)
	}

	var firstDroppedHeadComment string
	seenInOrig := map[string]bool{}
	newContent := make([]*yaml.Node, 0, len(orig.Content)+len(target.Content))

	for i := 0; i+1 < len(orig.Content); i += 2 {
		origK := orig.Content[i]
		origV := orig.Content[i+1]
		key := origK.Value

		// Keep unmanaged extensions intact!
		if ctx == ctxRoot && key == "extensions" {
			if _, managed := targetByKey[key]; !managed {
				newContent = append(newContent, origK, origV)
				continue
			}
		}

		// In KV items, if orig explicitly wrote `enabled: true` and target is
		// enabled (where Go struct omitempty omitted `enabled`), keep `enabled: true`!
		if ctx == ctxKVItem && key == "enabled" {
			if _, hasTargetEnabled := targetByKey["enabled"]; !hasTargetEnabled && kvNodeEnabled(orig) && kvNodeEnabled(target) {
				newContent = append(newContent, origK, origV)
				seenInOrig[key] = true
				continue
			}
		}

		targetV, keep := targetByKey[key]
		if !keep {
			if i == 0 && origK.HeadComment != "" {
				firstDroppedHeadComment = origK.HeadComment
			}
			continue
		}
		seenInOrig[key] = true
		cctx := childYAMLContext(ctx, key, orig, target)
		if !nodesSemanticallyEqual(origV, targetV, cctx) {
			patchNode(origV, targetV, cctx)
		}
		newContent = append(newContent, origK, origV)
	}

	for _, key := range targetOrder {
		if seenInOrig[key] {
			continue
		}
		targetV := targetByKey[key]
		kNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
		vNode := cloneYAMLNode(targetV, ctx == ctxJSONValue || ctx == ctxBody)
		newContent = append(newContent, kNode, vNode)
	}

	if firstDroppedHeadComment != "" && len(newContent) > 0 {
		if newContent[0].HeadComment == "" {
			newContent[0].HeadComment = firstDroppedHeadComment
		} else {
			newContent[0].HeadComment = firstDroppedHeadComment + "\n" + newContent[0].HeadComment
		}
	}
	orig.Content = newContent
}

func patchNode(orig, target *yaml.Node, ctx yamlPathCtx) {
	if orig == nil || target == nil {
		return
	}
	if orig.Kind == yaml.AliasNode {
		// Replace alias with concrete patched target rather than mutating shared anchor.
		repl := cloneYAMLNode(target, ctx == ctxJSONValue)
		copyNodeComments(repl, orig)
		*orig = *repl
		return
	}
	if orig.Kind != target.Kind {
		repl := cloneYAMLNode(target, ctx == ctxJSONValue)
		copyNodeComments(repl, orig)
		*orig = *repl
		return
	}

	switch orig.Kind {
	case yaml.MappingNode:
		patchMapping(orig, target, ctx)

	case yaml.SequenceNode:
		elemCtx := ctxGeneral
		switch ctx {
		case ctxKVSeq:
			elemCtx = ctxKVItem
		case ctxJSONValue:
			elemCtx = ctxJSONValue
			if orig.Style == 0 && target.Style == yaml.FlowStyle {
				orig.Style = 0
			}
		}
		// Keep orig.Style (e.g., flow style `tags: [auth, smoke]`) unless a new
		// element is a mapping/multiline that cannot sit cleanly in flow style.
		keepFlow := orig.Style == yaml.FlowStyle
		updatedItems := make([]*yaml.Node, len(target.Content))
		for i, tItem := range target.Content {
			if tItem.Kind == yaml.MappingNode && elemCtx != ctxJSONValue {
				keepFlow = false
			}
			if i < len(orig.Content) {
				oItem := orig.Content[i]
				if nodesSemanticallyEqual(oItem, tItem, elemCtx) {
					updatedItems[i] = oItem
					continue
				}
				patchNode(oItem, tItem, elemCtx)
				updatedItems[i] = oItem
			} else {
				updatedItems[i] = cloneYAMLNode(tItem, elemCtx == ctxJSONValue)
			}
		}
		if !keepFlow && orig.Style == yaml.FlowStyle {
			orig.Style = 0
		}
		orig.Content = updatedItems

	case yaml.ScalarNode:
		orig.Tag = target.Tag
		orig.Value = target.Value
		if strings.Contains(target.Value, "\n") {
			orig.Style = target.Style
			if orig.Style == 0 {
				orig.Style = yaml.LiteralStyle
			}
		} else if orig.Style == yaml.LiteralStyle || orig.Style == yaml.FoldedStyle {
			orig.Style = target.Style
		}
		// DoubleQuotedStyle / SingleQuotedStyle / plain style on single-line
		// scalars is preserved as-is!
	}
}

func copyNodeComments(dst, src *yaml.Node) {
	if dst == nil || src == nil {
		return
	}
	if dst.HeadComment == "" {
		dst.HeadComment = src.HeadComment
	}
	if dst.LineComment == "" {
		dst.LineComment = src.LineComment
	}
	if dst.FootComment == "" {
		dst.FootComment = src.FootComment
	}
}

func cloneYAMLNode(n *yaml.Node, stripFlow bool) *yaml.Node {
	if n == nil {
		return nil
	}
	cp := *n
	if stripFlow && (cp.Kind == yaml.MappingNode || cp.Kind == yaml.SequenceNode) && cp.Style == yaml.FlowStyle {
		cp.Style = 0
	}
	if len(n.Content) > 0 {
		cp.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			cp.Content[i] = cloneYAMLNode(c, stripFlow)
		}
	}
	return &cp
}

// --- Project format-preserving save ports ---------------------------------

// SaveRequestPreserving saves r back to entry.RelPath after checking baseline
// against disk, surgically patching the on-disk YAML AST so comments, key
// ordering, untouched formatting, and extensions are preserved. On any conflict
// or validation error neither disk nor Project memory is modified.
func (p *Project) SaveRequestPreserving(entry *RequestEntry, r *Request, baseline string) (string, error) {
	if p == nil || p.Store == nil {
		return "", configErrorf("project or store is nil")
	}
	if entry == nil {
		return "", configErrorf("request entry is nil")
	}
	if r == nil {
		return "", configErrorf("request is nil")
	}

	var data []byte
	var err error
	if baseline == "" {
		if p.Store.Exists(entry.RelPath) {
			_, diskDig, rerr := p.Store.ReadBounded(entry.RelPath, p.Store.EffectiveLimits().MaxYAMLBytes)
			if rerr == nil {
				return "", &ConflictError{Path: entry.RelPath, Baseline: "", Disk: diskDig}
			}
		}
		data, err = MergeRequestYAML(nil, r)
		if err != nil {
			return "", err
		}
	} else {
		diskBytes, diskDigest, rerr := p.Store.ReadBounded(entry.RelPath, p.Store.EffectiveLimits().MaxYAMLBytes)
		if rerr != nil {
			if isNotExistErr(rerr) {
				return "", &ConflictError{Path: entry.RelPath, Baseline: baseline, Disk: ""}
			}
			return "", rerr
		}
		if diskDigest != baseline {
			return "", &ConflictError{Path: entry.RelPath, Baseline: baseline, Disk: diskDigest}
		}
		data, err = MergeRequestYAML(diskBytes, r)
		if err != nil {
			return "", err
		}
	}

	if err := p.Store.SaveChecked(entry.RelPath, data, baseline); err != nil {
		return "", err
	}
	d := Digest(data)
	stored := *r
	if stored.SchemaVersion == 0 {
		stored.SchemaVersion = SupportedSchemaVersion
	}
	entry.BaselineDigest = d
	entry.Request = &stored
	p.Requests[stored.ID] = entry
	p.revalidate()
	return d, nil
}

// SaveEnvironmentPreserving saves an edited environment back to its file while
// checking its baseline digest and preserving comments, variable order, and
// extensions.
func (p *Project) SaveEnvironmentPreserving(id string, env *Environment) error {
	if p == nil || p.Store == nil {
		return configErrorf("project or store is nil")
	}
	entry, ok := p.EnvEntries[id]
	if !ok {
		return configErrorf("environment %q is not loaded for editing", id)
	}
	if env == nil {
		return configErrorf("environment is nil")
	}
	if err := validateEnvironmentVariables(env.Variables); err != nil {
		return err
	}
	stored := *env
	stored.ID = id
	if stored.SchemaVersion == 0 {
		stored.SchemaVersion = SupportedSchemaVersion
	}

	diskBytes, diskDigest, rerr := p.Store.ReadBounded(entry.RelPath, p.Store.EffectiveLimits().MaxYAMLBytes)
	if rerr != nil {
		if isNotExistErr(rerr) {
			return &ConflictError{Path: entry.RelPath, Baseline: entry.BaselineDigest, Disk: ""}
		}
		return rerr
	}
	if diskDigest != entry.BaselineDigest {
		return &ConflictError{Path: entry.RelPath, Baseline: entry.BaselineDigest, Disk: diskDigest}
	}
	data, err := MergeEnvironmentYAML(diskBytes, &stored)
	if err != nil {
		return err
	}
	if err := p.Store.SaveChecked(entry.RelPath, data, entry.BaselineDigest); err != nil {
		return err
	}
	entry.BaselineDigest = Digest(data)
	entry.Environment = &stored
	p.Environments[id] = &stored
	return nil
}

// SaveCollectionPreserving writes col to dir/collection.yaml while preserving
// comments and extensions when updating an existing file.
func (p *Project) SaveCollectionPreserving(dir string, col *Collection, baseline string) (string, error) {
	if p == nil || p.Store == nil {
		return "", configErrorf("project or store is nil")
	}
	if col == nil {
		return "", configErrorf("collection is nil")
	}
	colRel, err := sanitizeCollectionRel(dir)
	if err != nil {
		return "", err
	}
	fullDir := collectionsDirOf(colRel)
	fileRel := fullDir + "/collection.yaml"

	var data []byte
	if baseline == "" {
		data, err = MergeCollectionYAML(nil, col)
		if err != nil {
			return "", err
		}
	} else {
		diskBytes, diskDigest, rerr := p.Store.ReadBounded(fileRel, p.Store.EffectiveLimits().MaxYAMLBytes)
		if rerr != nil {
			if isNotExistErr(rerr) {
				return "", &ConflictError{Path: fileRel, Baseline: baseline, Disk: ""}
			}
			return "", rerr
		}
		if diskDigest != baseline {
			return "", &ConflictError{Path: fileRel, Baseline: baseline, Disk: diskDigest}
		}
		data, err = MergeCollectionYAML(diskBytes, col)
		if err != nil {
			return "", err
		}
	}
	if err := p.Store.SaveChecked(fileRel, data, baseline); err != nil {
		return "", err
	}
	stored := *col
	if stored.SchemaVersion == 0 {
		stored.SchemaVersion = SupportedSchemaVersion
	}
	if p.colByDir == nil {
		p.colByDir = map[string]*Collection{}
	}
	p.colByDir[fullDir] = &stored
	p.rebuildTree()
	return Digest(data), nil
}

// SaveProjectFilePreserving writes pf to project.yaml while checking baseline
// and preserving comments, variable key order, and extensions.
func (p *Project) SaveProjectFilePreserving(pf *ProjectFile, baseline string) (string, error) {
	if p == nil || p.Store == nil {
		return "", configErrorf("project or store is nil")
	}
	if pf == nil {
		return "", configErrorf("project file is nil")
	}
	diskBytes, diskDigest, rerr := p.Store.ReadBounded("project.yaml", p.Store.EffectiveLimits().MaxYAMLBytes)
	if rerr != nil {
		if isNotExistErr(rerr) {
			return "", &ConflictError{Path: "project.yaml", Baseline: baseline, Disk: ""}
		}
		return "", rerr
	}
	if baseline != "" && diskDigest != baseline {
		return "", &ConflictError{Path: "project.yaml", Baseline: baseline, Disk: diskDigest}
	}
	data, err := MergeProjectFileYAML(diskBytes, pf)
	if err != nil {
		return "", err
	}
	if err := p.Store.SaveChecked("project.yaml", data, diskDigest); err != nil {
		return "", err
	}
	stored := *pf
	if stored.SchemaVersion == 0 {
		stored.SchemaVersion = SupportedSchemaVersion
	}
	p.File = &stored
	p.revalidate()
	return Digest(data), nil
}

func isNotExistErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "cannot find the file")
}
