package desktop

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

var uiBodyModes = []string{
	core.BodyNone,
	core.BodyJSON,
	core.BodyXML,
	core.BodyText,
	core.BodyURLEncoded,
	core.BodyFormData,
	core.BodyBinary,
}

func uiBodyModeIndex(kind string) int {
	for i, k := range uiBodyModes {
		if k == kind {
			return i
		}
	}
	return 0
}

const (
	bodySourceInline      = 0
	bodySourceProjectFile = 1
	bodySourceLocalFile   = 2
)

const (
	binSourceProjectFile = 0
	binSourceLocalFile   = 1
)

const (
	partSourceText        = 0
	partSourceProjectFile = 1
	partSourceLocalFile   = 2
)

type formBodyRow struct {
	ID      string
	Enabled bool
	Name    string
	Value   string
	Encoded bool
	Extra   []*yaml.Node
}

func newBlankFormRow() *formBodyRow {
	return &formBodyRow{ID: nextRowID(), Enabled: true}
}

func (r *formBodyRow) isBlank() bool {
	return strings.TrimSpace(r.Name) == "" && strings.TrimSpace(r.Value) == "" && len(r.Extra) == 0
}

type multipartBodyRow struct {
	ID             string
	Enabled        bool
	Name           string
	PartSource     int // partSourceText | partSourceProjectFile | partSourceLocalFile
	Value          string
	ProjectFile    string
	LocalSlot      string
	LocalPathDraft string
	FileName       string
	ContentType    string
	Extra          []*yaml.Node
}

func newBlankMultipartRow(source int) *multipartBodyRow {
	return &multipartBodyRow{
		ID:         nextRowID(),
		Enabled:    true,
		PartSource: source,
	}
}

func (r *multipartBodyRow) isBlank() bool {
	return strings.TrimSpace(r.Name) == "" &&
		strings.TrimSpace(r.Value) == "" &&
		strings.TrimSpace(r.ProjectFile) == "" &&
		strings.TrimSpace(r.LocalSlot) == "" &&
		strings.TrimSpace(r.FileName) == "" &&
		strings.TrimSpace(r.ContentType) == "" &&
		len(r.Extra) == 0
}

// tabBodyState holds rich B03 body editing state for a request Tab.
type tabBodyState struct {
	loadedReq   *core.Request
	origBody    *core.Body
	bodyMutated bool
	wasTabDirty bool

	unsupported bool
	rawType     string
	loadErr     string
	unknownKeys []string

	modeIdx int // index into uiBodyModes

	// Inline / file source for json, xml, text
	textSource     int // bodySourceInline | bodySourceProjectFile | bodySourceLocalFile
	inlineJSON     string
	inlineXML      string
	inlineText     string
	projectFile    string
	localSlot      string
	localPathDraft string
	builtEditorKey string
	// builtEditorGen is the theme generation tab.bodyEditor was themed with.
	builtEditorGen int

	// Binary mode state
	binSource      int // binSourceProjectFile | binSourceLocalFile
	binProjectFile string
	binLocalSlot   string
	binLocalPath   string
	binContentType string
	binExtra       []*yaml.Node

	// Form URL-encoded state
	formRows     []*formBodyRow
	formViewMode int // 0 Key:Value, 1 Batch Lines, 2 Raw Encoded
	formRawText  string
	formErr      string

	// Multipart form-data state
	multipartRows []*multipartBodyRow
	multipartErr  string

	// Tracking legacy Tab.BodyIdx / Tab.BodyText for test compatibility
	lastSyncedLegacyIdx  int
	lastSyncedLegacyText string
}

func expectedLegacyIdx(b *core.Body) int {
	if b == nil {
		return 0
	}
	return indexOf(bodyTypes, b.Type)
}

func ensureBodyState(tab *Tab, app *App) *tabBodyState {
	if tab == nil {
		return nil
	}
	var req *core.Request
	if tab.Entry != nil {
		req = tab.Entry.Request
	}
	bs := tab.bodyState
	if bs == nil {
		bs = newBodyStateFromRequest(req, tab.Owner, app)
		tab.bodyState = bs
		// Detect if a caller/test mutated legacy tab.BodyIdx or tab.BodyText
		// directly after opening the tab.
		var orig *core.Body
		if req != nil && req.HTTP != nil {
			orig = req.HTTP.Body
		}
		initIdx := expectedLegacyIdx(orig)
		initTxt := core.BodyEditText(orig)
		if tab.BodyIdx != initIdx || tab.BodyText != initTxt {
			bs.applyLegacyOverride(tab.BodyIdx, tab.BodyText)
		}
		bs.syncLegacyFields(tab)
		return bs
	}

	// Reload after SaveRequest replaced entry.Request, or after reloadRequestTab
	// discarded edits (tab.Dirty went from true back to false on same req).
	if req != bs.loadedReq || (!tab.Dirty && bs.wasTabDirty) {
		bs = newBodyStateFromRequest(req, tab.Owner, app)
		tab.bodyState = bs
		tab.bodyEditor = nil
		bs.syncLegacyFields(tab)
		return bs
	}

	if tab.BodyIdx != bs.lastSyncedLegacyIdx || tab.BodyText != bs.lastSyncedLegacyText {
		bs.applyLegacyOverride(tab.BodyIdx, tab.BodyText)
		tab.bodyEditor = nil
		bs.syncLegacyFields(tab)
	}
	bs.wasTabDirty = tab.Dirty
	return bs
}

// resetBodyState clears cached body UI state so the next frame or save reloads
// cleanly from tab.Entry.Request.
func (t *Tab) resetBodyState() {
	if t == nil {
		return
	}
	t.bodyState = nil
	t.bodyEditor = nil
}

func newBodyStateFromRequest(req *core.Request, ps *projectState, app *App) *tabBodyState {
	var orig *core.Body
	if req != nil && req.HTTP != nil {
		orig = req.HTTP.Body
	}
	bs := &tabBodyState{
		loadedReq:     req,
		origBody:      core.CloneBody(orig),
		formRows:      []*formBodyRow{newBlankFormRow()},
		multipartRows: []*multipartBodyRow{newBlankMultipartRow(partSourceText)},
	}
	if orig == nil {
		bs.modeIdx = uiBodyModeIndex(core.BodyNone)
		return bs
	}

	tb, err := core.InspectBody(orig)
	bs.rawType = orig.Type
	if err != nil {
		// Malformed or duplicate-key body on disk: preserve verbatim until
		// explicitly replaced so saving another field never swallows it.
		bs.unsupported = true
		bs.loadErr = err.Error()
		return bs
	}
	if tb.Kind == core.BodyUnsupported {
		bs.unsupported = true
		return bs
	}

	bs.modeIdx = uiBodyModeIndex(tb.Kind)
	bs.unknownKeys = collectUnknownKeys(tb)

	lookupBound := func(slot string) string {
		if app == nil || ps == nil || slot == "" {
			return ""
		}
		p, _ := app.BoundLocalFile(ps, slot)
		return p
	}

	switch tb.Kind {
	case core.BodyNone:
		// nothing more to populate

	case core.BodyJSON:
		switch {
		case tb.LocalFile != "":
			bs.textSource = bodySourceLocalFile
			bs.localSlot = tb.LocalFile
			bs.localPathDraft = lookupBound(tb.LocalFile)
		case tb.File != "":
			bs.textSource = bodySourceProjectFile
			bs.projectFile = tb.File
		default:
			bs.textSource = bodySourceInline
			bs.inlineJSON = core.BodyEditText(orig)
		}

	case core.BodyXML:
		switch {
		case tb.LocalFile != "":
			bs.textSource = bodySourceLocalFile
			bs.localSlot = tb.LocalFile
			bs.localPathDraft = lookupBound(tb.LocalFile)
		case tb.File != "":
			bs.textSource = bodySourceProjectFile
			bs.projectFile = tb.File
		default:
			bs.textSource = bodySourceInline
			bs.inlineXML = tb.Text
		}

	case core.BodyText:
		switch {
		case tb.LocalFile != "":
			bs.textSource = bodySourceLocalFile
			bs.localSlot = tb.LocalFile
			bs.localPathDraft = lookupBound(tb.LocalFile)
		case tb.File != "":
			bs.textSource = bodySourceProjectFile
			bs.projectFile = tb.File
		default:
			bs.textSource = bodySourceInline
			bs.inlineText = tb.Text
		}

	case core.BodyURLEncoded:
		if tb.LocalFile != "" {
			bs.textSource = bodySourceLocalFile
			bs.localSlot = tb.LocalFile
			bs.localPathDraft = lookupBound(tb.LocalFile)
		} else if tb.File != "" {
			bs.textSource = bodySourceProjectFile
			bs.projectFile = tb.File
		} else {
			rows := make([]*formBodyRow, 0, len(tb.FormFields)+1)
			for _, f := range tb.FormFields {
				rows = append(rows, &formBodyRow{
					ID:      nextRowID(),
					Enabled: f.IsEnabled(),
					Name:    f.Name,
					Value:   f.Value,
					Encoded: f.Encoded,
					Extra:   f.Extra,
				})
			}
			rows = append(rows, newBlankFormRow())
			bs.formRows = rows
		}

	case core.BodyFormData:
		rows := make([]*multipartBodyRow, 0, len(tb.MultipartParts)+1)
		for _, p := range tb.MultipartParts {
			r := &multipartBodyRow{
				ID:          nextRowID(),
				Enabled:     p.IsEnabled(),
				Name:        p.Name,
				FileName:    p.FileName,
				ContentType: p.ContentType,
				Extra:       p.Extra,
			}
			if p.EffectiveKind() == "file" {
				ref, refErr := p.ResolveFileRef()
				if refErr == nil && ref.Kind == core.FileRefLocal {
					r.PartSource = partSourceLocalFile
					r.LocalSlot = ref.Slot
					r.LocalPathDraft = lookupBound(ref.Slot)
				} else if p.LocalFile != "" {
					r.PartSource = partSourceLocalFile
					r.LocalSlot = p.LocalFile
					r.LocalPathDraft = lookupBound(p.LocalFile)
				} else {
					r.PartSource = partSourceProjectFile
					r.ProjectFile = p.File
				}
			} else {
				r.PartSource = partSourceText
				r.Value = p.Value
			}
			rows = append(rows, r)
		}
		if len(rows) == 0 {
			rows = append(rows, newBlankMultipartRow(partSourceText))
		}
		bs.multipartRows = rows

	case core.BodyBinary:
		bs.binContentType = tb.ContentType
		bs.binExtra = tb.BodyExtra
		if tb.LocalFile != "" {
			bs.binSource = binSourceLocalFile
			bs.binLocalSlot = tb.LocalFile
			bs.binLocalPath = lookupBound(tb.LocalFile)
		} else {
			bs.binSource = binSourceProjectFile
			bs.binProjectFile = tb.File
		}
	}
	return bs
}

func collectUnknownKeys(tb *core.TypedBody) []string {
	if tb == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(extra []*yaml.Node) {
		for i := 0; i+1 < len(extra); i += 2 {
			k := extra[i].Value
			if k != "" && !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	add(tb.BodyExtra)
	for _, f := range tb.FormFields {
		add(f.Extra)
	}
	for _, p := range tb.MultipartParts {
		add(p.Extra)
	}
	return out
}

func (bs *tabBodyState) applyLegacyOverride(legacyIdx int, legacyText string) {
	if legacyIdx < 0 || legacyIdx >= len(bodyTypes) {
		legacyIdx = 0
	}
	bs.unsupported = false
	bs.bodyMutated = true
	if strings.TrimSpace(legacyText) == "" {
		bs.modeIdx = uiBodyModeIndex(core.BodyNone)
		bs.inlineJSON = ""
		bs.inlineXML = ""
		bs.inlineText = ""
		return
	}
	typ := bodyTypes[legacyIdx]
	bs.rawType = typ
	bs.textSource = bodySourceInline
	switch typ {
	case "json":
		bs.modeIdx = uiBodyModeIndex(core.BodyJSON)
		bs.inlineJSON = legacyText
	case "xml":
		bs.modeIdx = uiBodyModeIndex(core.BodyXML)
		bs.inlineXML = legacyText
	default:
		bs.modeIdx = uiBodyModeIndex(core.BodyText)
		bs.inlineText = legacyText
	}
}

func (bs *tabBodyState) currentKind() string {
	if bs.unsupported {
		return core.BodyUnsupported
	}
	if bs.modeIdx < 0 || bs.modeIdx >= len(uiBodyModes) {
		return core.BodyNone
	}
	return uiBodyModes[bs.modeIdx]
}

func (bs *tabBodyState) activeInlineBufferPtr() *string {
	switch bs.currentKind() {
	case core.BodyJSON:
		return &bs.inlineJSON
	case core.BodyXML:
		return &bs.inlineXML
	default:
		return &bs.inlineText
	}
}

func (bs *tabBodyState) markMutated(tab *Tab) {
	bs.bodyMutated = true
	tab.Dirty = true
	bs.wasTabDirty = true
	bs.syncLegacyFields(tab)
}

// syncLegacyFields updates Tab.BodyIdx and Tab.BodyText so send_guard.go's
// undefined-variable check sees all template expressions used inside any body
// mode (including urlencoded and form-data).
func (bs *tabBodyState) syncLegacyFields(tab *Tab) {
	if tab == nil || bs == nil {
		return
	}
	switch bs.currentKind() {
	case core.BodyJSON:
		tab.BodyIdx = 0
		if bs.textSource == bodySourceInline {
			tab.BodyText = bs.inlineJSON
		} else {
			tab.BodyText = ""
		}
	case core.BodyText:
		tab.BodyIdx = 1
		if bs.textSource == bodySourceInline {
			tab.BodyText = bs.inlineText
		} else {
			tab.BodyText = ""
		}
	case core.BodyXML:
		tab.BodyIdx = 2
		if bs.textSource == bodySourceInline {
			tab.BodyText = bs.inlineXML
		} else {
			tab.BodyText = ""
		}
	case core.BodyURLEncoded:
		var sb strings.Builder
		for _, r := range bs.formRows {
			if r != nil && r.Enabled && !r.isBlank() {
				sb.WriteString(r.Name)
				sb.WriteByte('=')
				sb.WriteString(r.Value)
				sb.WriteByte('\n')
			}
		}
		tab.BodyText = sb.String()
	case core.BodyFormData:
		var sb strings.Builder
		for _, r := range bs.multipartRows {
			if r != nil && r.Enabled && !r.isBlank() {
				sb.WriteString(r.Name)
				sb.WriteByte('=')
				if r.PartSource == partSourceText {
					sb.WriteString(r.Value)
				}
				sb.WriteByte(';')
				sb.WriteString(r.FileName)
				sb.WriteByte(';')
				sb.WriteString(r.ContentType)
				sb.WriteByte('\n')
			}
		}
		tab.BodyText = sb.String()
	default:
		tab.BodyText = ""
	}
	bs.lastSyncedLegacyIdx = tab.BodyIdx
	bs.lastSyncedLegacyText = tab.BodyText
	bs.wasTabDirty = tab.Dirty
}

func (bs *tabBodyState) ensureTrailingFormBlank() {
	if len(bs.formRows) == 0 || !bs.formRows[len(bs.formRows)-1].isBlank() {
		bs.formRows = append(bs.formRows, newBlankFormRow())
	}
}

func (bs *tabBodyState) removeFormRow(id string) {
	kept := make([]*formBodyRow, 0, len(bs.formRows))
	for _, r := range bs.formRows {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	bs.formRows = kept
	bs.ensureTrailingFormBlank()
}

func (bs *tabBodyState) removeMultipartRow(id string) {
	kept := make([]*multipartBodyRow, 0, len(bs.multipartRows))
	for _, r := range bs.multipartRows {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		kept = append(kept, newBlankMultipartRow(partSourceText))
	}
	bs.multipartRows = kept
}

// bodyFromTab serializes the tab's Body state into a *core.Body. Untouched or
// unsupported bodies return a deep clone of the original AST so unknown modes
// and fields are never silently swallowed.
func bodyFromTab(tab *Tab) (*core.Body, error) {
	bs := ensureBodyState(tab, nil)
	if bs == nil {
		return nil, nil
	}
	if bs.unsupported || !bs.bodyMutated {
		return core.CloneBody(bs.origBody), nil
	}

	kind := bs.currentKind()
	switch kind {
	case core.BodyNone:
		return nil, nil

	case core.BodyJSON, core.BodyXML, core.BodyText:
		switch bs.textSource {
		case bodySourceProjectFile:
			clean, err := core.ValidateProjectRelPath(strings.TrimSpace(bs.projectFile))
			if err != nil {
				return nil, err
			}
			return &core.Body{Type: preserveBodyAlias(bs.rawType, kind), File: clean}, nil

		case bodySourceLocalFile:
			slot := strings.TrimSpace(bs.localSlot)
			if err := core.ValidateLocalSlot(slot); err != nil {
				return nil, err
			}
			return &core.Body{Type: preserveBodyAlias(bs.rawType, kind), File: core.FormatLocalFileRef(slot)}, nil

		default:
			txt := *bs.activeInlineBufferPtr()
			if kind == core.BodyJSON && strings.TrimSpace(txt) == "" {
				return nil, fmt.Errorf("JSON body is empty")
			}
			return core.ParseBodyText(preserveBodyAlias(bs.rawType, kind), txt)
		}

	case core.BodyURLEncoded:
		if bs.formViewMode != 0 && bs.formErr != "" {
			return nil, fmt.Errorf("urlencoded body: %s", bs.formErr)
		}
		var fields []core.FormField
		for i, r := range bs.formRows {
			if r == nil || r.isBlank() {
				continue
			}
			if strings.TrimSpace(r.Name) == "" && strings.TrimSpace(r.Value) != "" {
				return nil, fmt.Errorf("urlencoded row %d: value %q has no key", i+1, r.Value)
			}
			enabled := r.Enabled
			fields = append(fields, core.FormField{
				Name:    r.Name,
				Value:   r.Value,
				Enabled: &enabled,
				Encoded: r.Encoded,
				Extra:   r.Extra,
			})
		}
		tb := &core.TypedBody{
			Kind:       core.BodyURLEncoded,
			RawType:    preserveBodyAlias(bs.rawType, core.BodyURLEncoded),
			FormFields: fields,
		}
		return tb.ToBody()

	case core.BodyFormData:
		var parts []core.MultipartPart
		for i, r := range bs.multipartRows {
			if r == nil || r.isBlank() {
				continue
			}
			if strings.TrimSpace(r.Name) == "" {
				return nil, fmt.Errorf("multipart row %d: part name is empty", i+1)
			}
			enabled := r.Enabled
			p := core.MultipartPart{
				Name:        r.Name,
				FileName:    strings.TrimSpace(r.FileName),
				ContentType: strings.TrimSpace(r.ContentType),
				Enabled:     &enabled,
				Extra:       r.Extra,
			}
			switch r.PartSource {
			case partSourceProjectFile:
				p.Kind = "file"
				clean, err := core.ValidateProjectRelPath(strings.TrimSpace(r.ProjectFile))
				if err != nil {
					return nil, fmt.Errorf("multipart part %q: %w", r.Name, err)
				}
				p.File = clean
			case partSourceLocalFile:
				p.Kind = "file"
				slot := strings.TrimSpace(r.LocalSlot)
				if err := core.ValidateLocalSlot(slot); err != nil {
					return nil, fmt.Errorf("multipart part %q: %w", r.Name, err)
				}
				p.LocalFile = slot
			default:
				p.Kind = "text"
				p.Value = r.Value
			}
			parts = append(parts, p)
		}
		tb := &core.TypedBody{
			Kind:           core.BodyFormData,
			RawType:        preserveBodyAlias(bs.rawType, core.BodyFormData),
			MultipartParts: parts,
		}
		return tb.ToBody()

	case core.BodyBinary:
		tb := &core.TypedBody{
			Kind:        core.BodyBinary,
			RawType:     preserveBodyAlias(bs.rawType, core.BodyBinary),
			ContentType: strings.TrimSpace(bs.binContentType),
			BodyExtra:   bs.binExtra,
		}
		if bs.binSource == binSourceLocalFile {
			slot := strings.TrimSpace(bs.binLocalSlot)
			if err := core.ValidateLocalSlot(slot); err != nil {
				return nil, err
			}
			tb.LocalFile = slot
		} else {
			clean, err := core.ValidateProjectRelPath(strings.TrimSpace(bs.binProjectFile))
			if err != nil {
				return nil, err
			}
			tb.File = clean
		}
		return tb.ToBody()
	}

	return nil, nil
}

func preserveBodyAlias(rawType, canonical string) string {
	if rawType != "" && core.CanonicalBodyType(rawType) == canonical {
		return rawType
	}
	return canonical
}
