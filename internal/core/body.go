package core

import (
	"encoding/json"
	"fmt"
	"mime"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Body type tokens (v1 candidate draft; compatible extension over MVP
// json/text/xml, not unilaterally frozen as a public schema).
const (
	BodyNone       = "none"
	BodyJSON       = "json"
	BodyText       = "text"
	BodyXML        = "xml"
	BodyURLEncoded = "urlencoded"
	BodyFormData   = "form-data"
	BodyBinary     = "binary"
	BodyUnsupported = "unsupported"
)

// LocalFileRefPrefix marks an explicit local-machine file binding slot in
// Body.File or MultipartPart.File (for example "local:avatar"). Only the slot
// identifier is stored in shared .gorequest YAML; actual machine paths live in
// OS-local state and never enter Git-tracked files.
const LocalFileRefPrefix = "local:"

// FileRefKind distinguishes a .gorequest-relative file reference from an
// explicit local-machine file mapping slot.
type FileRefKind int

const (
	FileRefNone FileRefKind = iota
	FileRefProject
	FileRefLocal
)

// FileRef is a parsed file reference from Body.File or MultipartPart.
type FileRef struct {
	Kind FileRefKind
	// Path is the normalized forward-slash relative path inside .gorequest/
	// when Kind == FileRefProject.
	Path string
	// Slot is the logical binding key when Kind == FileRefLocal.
	Slot string
}

var localSlotPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidateLocalSlot checks that a local-file mapping key is a portable slot
// name and never a filesystem path or template expression.
func ValidateLocalSlot(slot string) error {
	if slot == "" {
		return configErrorf("local file mapping key is empty")
	}
	if strings.ContainsAny(slot, `/\:`) || strings.Contains(slot, "..") {
		return configErrorf("local file mapping key %q must be a logical name, not a file path", slot)
	}
	if !localSlotPattern.MatchString(slot) {
		return configErrorf("invalid local file mapping key %q (use letters, digits, '.', '_' or '-')", slot)
	}
	return nil
}

// ValidateProjectRelPath validates a .gorequest-relative file path lexically
// without touching the filesystem. Symlink containment is checked separately
// when opening the file.
func ValidateProjectRelPath(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", configErrorf("project file path is empty")
	}
	if strings.ContainsAny(rel, "\x00\r\n") {
		return "", configErrorf("invalid characters in file path %q", rel)
	}
	// Reject Windows drive letters, UNC paths, backslashes and leading slashes
	// on every OS so project files remain portable and cannot smuggle host paths.
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || strings.Contains(rel, `\`) {
		return "", configErrorf("file path %q must use relative '/' paths under .gorequest/", rel)
	}
	if len(rel) >= 2 && ((rel[0] >= 'A' && rel[0] <= 'Z') || (rel[0] >= 'a' && rel[0] <= 'z')) && rel[1] == ':' {
		return "", configErrorf("absolute drive path %q is not allowed in project files; use %s<slot>", rel, LocalFileRefPrefix)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", configErrorf("file path %q escapes .gorequest/", rel)
		}
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", configErrorf("file path %q escapes .gorequest/", rel)
	}
	return clean, nil
}

// ParseFileRef classifies raw (from Body.File or MultipartPart.File) into a
// project-relative path or an explicit local-machine slot ("local:<slot>").
func ParseFileRef(raw string) (FileRef, error) {
	if raw == "" {
		return FileRef{Kind: FileRefNone}, nil
	}
	if slot, ok := strings.CutPrefix(raw, LocalFileRefPrefix); ok {
		if err := ValidateLocalSlot(slot); err != nil {
			return FileRef{}, err
		}
		return FileRef{Kind: FileRefLocal, Slot: slot}, nil
	}
	clean, err := ValidateProjectRelPath(raw)
	if err != nil {
		return FileRef{}, err
	}
	return FileRef{Kind: FileRefProject, Path: clean}, nil
}

// FormatLocalFileRef formats a logical slot name as "local:<slot>".
func FormatLocalFileRef(slot string) string {
	return LocalFileRefPrefix + strings.TrimSpace(slot)
}

// CanonicalBodyType normalizes known body type names and aliases. Unrecognized
// non-empty types return BodyUnsupported so callers preserve them rather than
// coercing them to "json" or "none".
func CanonicalBodyType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none":
		return BodyNone
	case "json":
		return BodyJSON
	case "text":
		return BodyText
	case "xml":
		return BodyXML
	case "urlencoded", "x-www-form-urlencoded", "application/x-www-form-urlencoded":
		return BodyURLEncoded
	case "form-data", "multipart", "multipart/form-data":
		return BodyFormData
	case "binary", "octet-stream", "application/octet-stream":
		return BodyBinary
	case "":
		return ""
	default:
		return BodyUnsupported
	}
}

// StandardHTTPMethods returns the standard HTTP methods offered first in UI
// pickers. Custom RFC 9110 token methods are also valid.
func StandardHTTPMethods() []string {
	return []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
}

// ValidateHTTPMethod checks that method is a non-empty RFC 9110 HTTP token
// (tchar = "!" / "#" / "$" / "%" / "&" / "'" / "*" / "+" / "-" / "." / "^" /
// "_" / "`" / "|" / "~" / DIGIT / ALPHA).
func ValidateHTTPMethod(method string) error {
	if method == "" {
		return configErrorf("HTTP method is empty")
	}
	for i := 0; i < len(method); i++ {
		if !isHTTPTokenByte(method[i]) {
			return configErrorf("invalid HTTP method %q", method)
		}
	}
	return nil
}

func isHTTPTokenByte(b byte) bool {
	if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') {
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

// FormField is one ordered item of an application/x-www-form-urlencoded body.
// Extra retains any unrecognized YAML key/value node pairs so round-tripping
// never silently drops fields, while strict validation and execution reject
// unknown keys.
type FormField struct {
	Name    string
	Value   string
	Enabled *bool
	// Encoded indicates Value (and Name) are already percent-encoded and must
	// not be escaped a second time on the wire.
	Encoded bool
	Extra   []*yaml.Node
}

// IsEnabled reports whether f is active (nil Enabled defaults to true).
func (f FormField) IsEnabled() bool { return f.Enabled == nil || *f.Enabled }

// MultipartPart is one ordered part of a multipart/form-data body.
type MultipartPart struct {
	Name        string
	Kind        string // "" (inferred), "text", or "file"
	Value       string
	hasValue    bool
	File        string // .gorequest-relative path, or "local:<slot>"
	LocalFile   string // explicit local slot ("localFile" key in YAML)
	FileName    string // optional Content-Disposition filename override
	ContentType string // optional part Content-Type
	Enabled     *bool
	Extra       []*yaml.Node
}

// IsEnabled reports whether p is active (nil Enabled defaults to true).
func (p MultipartPart) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

// EffectiveKind returns "file" or "text" for the part.
func (p MultipartPart) EffectiveKind() string {
	k := strings.ToLower(strings.TrimSpace(p.Kind))
	if k != "" {
		return k
	}
	if p.File != "" || p.LocalFile != "" {
		return "file"
	}
	return "text"
}

// ResolveFileRef returns the effective FileRef for a file part, checking
// mutual exclusivity between File and LocalFile.
func (p MultipartPart) ResolveFileRef() (FileRef, error) {
	if p.File != "" && p.LocalFile != "" {
		return FileRef{}, configErrorf("multipart part %q specifies both file and localFile", p.Name)
	}
	if p.LocalFile != "" {
		if err := ValidateLocalSlot(p.LocalFile); err != nil {
			return FileRef{}, err
		}
		return FileRef{Kind: FileRefLocal, Slot: p.LocalFile}, nil
	}
	return ParseFileRef(p.File)
}

// TypedBody is a structured view over *Body that adapters and the Desktop UI
// edit without modifying core/model.go. Preserved keeps a deep clone of
// unrecognized or untouched YAML bodies so saving never swallows unknown modes
// or fields.
type TypedBody struct {
	Kind           string // BodyNone..BodyBinary or BodyUnsupported
	RawType        string // original Body.Type when loaded
	JSONNode       *yaml.Node
	Text           string
	HasInlineValue bool
	File           string // project-relative path when backed by a project file
	LocalFile      string // logical slot when backed by an explicit local mapping
	ContentType    string // optional explicit contentType in binary mapping value
	FormFields     []FormField
	MultipartParts []MultipartPart
	BodyExtra      []*yaml.Node
	Preserved      *Body
}

// CloneYAMLNode deep-copies a yaml.Node tree so in-memory edits cannot mutate
// a loaded Request's AST.
func CloneYAMLNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	cp := *n
	if len(n.Content) > 0 {
		cp.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			cp.Content[i] = CloneYAMLNode(c)
		}
	}
	return &cp
}

// CloneBody returns a deep copy of b.
func CloneBody(b *Body) *Body {
	if b == nil {
		return nil
	}
	return &Body{
		Type:  b.Type,
		File:  b.File,
		Value: CloneYAMLNode(b.Value),
	}
}

func unwrapDocNode(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return n.Content[0]
	}
	return n
}

func checkNoDuplicateMappingKeys(n *yaml.Node) error {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if err := checkNoDuplicateMappingKeys(c); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			if _, dup := seen[k]; dup {
				return configErrorf("duplicate YAML key %q in body", k)
			}
			seen[k] = struct{}{}
			if err := checkNoDuplicateMappingKeys(n.Content[i+1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// InspectBody decodes a *Body into a TypedBody. Unknown body types succeed with
// Kind == BodyUnsupported and Preserved populated so callers can display and
// round-trip them without data loss. Structural errors in a known body type
// return a ConfigError alongside a non-nil TypedBody holding Preserved.
func InspectBody(b *Body) (*TypedBody, error) {
	if b == nil {
		return &TypedBody{Kind: BodyNone}, nil
	}
	preserved := CloneBody(b)
	if err := checkNoDuplicateMappingKeys(b.Value); err != nil {
		return &TypedBody{Kind: BodyUnsupported, RawType: b.Type, Preserved: preserved}, err
	}

	kind := CanonicalBodyType(b.Type)
	if kind == "" {
		return &TypedBody{Kind: BodyUnsupported, RawType: b.Type, Preserved: preserved}, configErrorf("body has no type")
	}
	if kind == BodyUnsupported {
		return &TypedBody{Kind: BodyUnsupported, RawType: b.Type, Preserved: preserved}, nil
	}

	tb := &TypedBody{
		Kind:      kind,
		RawType:   b.Type,
		Preserved: preserved,
	}

	val := unwrapDocNode(b.Value)

	if b.File != "" {
		ref, err := ParseFileRef(b.File)
		if err != nil {
			return tb, err
		}
		switch ref.Kind {
		case FileRefProject:
			tb.File = ref.Path
		case FileRefLocal:
			tb.LocalFile = ref.Slot
		}
	}

	switch kind {
	case BodyNone:
		if b.File != "" || val != nil {
			return tb, configErrorf("body of type \"none\" must not set value or file")
		}
		return tb, nil

	case BodyJSON:
		if b.File != "" && val != nil {
			return tb, configErrorf("json body cannot specify both value and file")
		}
		if b.File == "" && val == nil {
			return tb, configErrorf("json body requires either value or file")
		}
		if val != nil {
			tb.JSONNode = CloneYAMLNode(val)
			tb.HasInlineValue = true
		}
		return tb, nil

	case BodyText, BodyXML:
		if b.File != "" && val != nil {
			return tb, configErrorf("%s body cannot specify both value and file", kind)
		}
		if b.File == "" && val == nil {
			return tb, configErrorf("%s body requires either value or file", kind)
		}
		if val != nil {
			if val.Kind != yaml.ScalarNode {
				return tb, configErrorf("%s body value must be a string scalar", kind)
			}
			tb.Text = val.Value
			tb.HasInlineValue = true
		}
		return tb, nil

	case BodyURLEncoded:
		if b.File != "" && val != nil {
			return tb, configErrorf("urlencoded body cannot specify both value and file")
		}
		if b.File != "" {
			return tb, nil
		}
		if val == nil {
			return tb, nil
		}
		fields, err := decodeFormFieldsNode(val)
		if err != nil {
			return tb, err
		}
		tb.FormFields = fields
		tb.HasInlineValue = true
		return tb, nil

	case BodyFormData:
		if b.File != "" {
			return tb, configErrorf("form-data body must put file references on individual parts in value, not body.file")
		}
		if val == nil {
			return tb, nil
		}
		parts, err := decodeMultipartPartsNode(val)
		if err != nil {
			return tb, err
		}
		tb.MultipartParts = parts
		tb.HasInlineValue = true
		return tb, nil

	case BodyBinary:
		if b.File != "" && val != nil {
			return tb, configErrorf("binary body cannot specify both file and value")
		}
		if val != nil {
			if err := decodeBinaryValueNode(val, tb); err != nil {
				return tb, err
			}
		}
		if tb.File == "" && tb.LocalFile == "" && len(tb.BodyExtra) == 0 {
			return tb, configErrorf("binary body requires file or local file mapping")
		}
		return tb, nil
	}

	return tb, nil
}

func decodeFormFieldsNode(val *yaml.Node) ([]FormField, error) {
	if val.Kind != yaml.SequenceNode {
		return nil, configErrorf("urlencoded body value must be a sequence of {name, value} items")
	}
	out := make([]FormField, 0, len(val.Content))
	for idx, item := range val.Content {
		if item.Kind != yaml.MappingNode {
			return nil, configErrorf("urlencoded item %d must be a mapping", idx+1)
		}
		var f FormField
		for i := 0; i+1 < len(item.Content); i += 2 {
			k := item.Content[i]
			v := item.Content[i+1]
			switch k.Value {
			case "name":
				if v.Kind != yaml.ScalarNode {
					return nil, configErrorf("urlencoded item %d: name must be a string", idx+1)
				}
				f.Name = v.Value
			case "value":
				if v.Kind != yaml.ScalarNode {
					return nil, configErrorf("urlencoded item %d: value must be a string", idx+1)
				}
				f.Value = v.Value
			case "enabled":
				var b bool
				if err := v.Decode(&b); err != nil {
					return nil, configErrorf("urlencoded item %d: invalid enabled boolean: %v", idx+1, err)
				}
				f.Enabled = &b
			case "encoded":
				var b bool
				if err := v.Decode(&b); err != nil {
					return nil, configErrorf("urlencoded item %d: invalid encoded boolean: %v", idx+1, err)
				}
				f.Encoded = b
			default:
				f.Extra = append(f.Extra, CloneYAMLNode(k), CloneYAMLNode(v))
			}
		}
		out = append(out, f)
	}
	return out, nil
}

func decodeMultipartPartsNode(val *yaml.Node) ([]MultipartPart, error) {
	if val.Kind != yaml.SequenceNode {
		return nil, configErrorf("form-data body value must be a sequence of part mappings")
	}
	out := make([]MultipartPart, 0, len(val.Content))
	for idx, item := range val.Content {
		if item.Kind != yaml.MappingNode {
			return nil, configErrorf("form-data part %d must be a mapping", idx+1)
		}
		var p MultipartPart
		for i := 0; i+1 < len(item.Content); i += 2 {
			k := item.Content[i]
			v := item.Content[i+1]
			switch k.Value {
			case "name":
				if v.Kind != yaml.ScalarNode {
					return nil, configErrorf("form-data part %d: name must be a string", idx+1)
				}
				p.Name = v.Value
			case "kind":
				if v.Kind != yaml.ScalarNode {
					return nil, configErrorf("form-data part %d: kind must be a string", idx+1)
				}
				p.Kind = v.Value
			case "value":
				if v.Kind != yaml.ScalarNode {
					return nil, configErrorf("form-data part %d: value must be a string", idx+1)
				}
				p.Value = v.Value
				p.hasValue = true
			case "file":
				if v.Kind != yaml.ScalarNode {
					return nil, configErrorf("form-data part %d: file must be a string", idx+1)
				}
				p.File = v.Value
			case "localFile":
				if v.Kind != yaml.ScalarNode {
					return nil, configErrorf("form-data part %d: localFile must be a string", idx+1)
				}
				p.LocalFile = v.Value
			case "fileName":
				if v.Kind != yaml.ScalarNode {
					return nil, configErrorf("form-data part %d: fileName must be a string", idx+1)
				}
				p.FileName = v.Value
			case "contentType":
				if v.Kind != yaml.ScalarNode {
					return nil, configErrorf("form-data part %d: contentType must be a string", idx+1)
				}
				p.ContentType = v.Value
			case "enabled":
				var b bool
				if err := v.Decode(&b); err != nil {
					return nil, configErrorf("form-data part %d: invalid enabled boolean: %v", idx+1, err)
				}
				p.Enabled = &b
			default:
				p.Extra = append(p.Extra, CloneYAMLNode(k), CloneYAMLNode(v))
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func decodeBinaryValueNode(val *yaml.Node, tb *TypedBody) error {
	if val.Kind != yaml.MappingNode {
		return configErrorf("binary body value must be a mapping when present")
	}
	var gotFile, gotLocal string
	for i := 0; i+1 < len(val.Content); i += 2 {
		k := val.Content[i]
		v := val.Content[i+1]
		switch k.Value {
		case "file":
			if v.Kind != yaml.ScalarNode {
				return configErrorf("binary body file must be a string")
			}
			gotFile = v.Value
		case "localFile":
			if v.Kind != yaml.ScalarNode {
				return configErrorf("binary body localFile must be a string")
			}
			gotLocal = v.Value
		case "contentType":
			if v.Kind != yaml.ScalarNode {
				return configErrorf("binary body contentType must be a string")
			}
			tb.ContentType = v.Value
		default:
			tb.BodyExtra = append(tb.BodyExtra, CloneYAMLNode(k), CloneYAMLNode(v))
		}
	}
	if gotFile != "" && gotLocal != "" {
		return configErrorf("binary body cannot specify both file and localFile")
	}
	if gotLocal != "" {
		if err := ValidateLocalSlot(gotLocal); err != nil {
			return err
		}
		tb.LocalFile = gotLocal
	} else if gotFile != "" {
		ref, err := ParseFileRef(gotFile)
		if err != nil {
			return err
		}
		if ref.Kind == FileRefLocal {
			tb.LocalFile = ref.Slot
		} else {
			tb.File = ref.Path
		}
	}
	return nil
}

// ToBody converts tb back into a *Body ready for YAML encoding. Unsupported
// modes return their deep-cloned Preserved body verbatim so saving never drops
// unrecognized modes or fields.
func (tb *TypedBody) ToBody() (*Body, error) {
	if tb == nil {
		return nil, nil
	}
	switch tb.Kind {
	case BodyUnsupported:
		if tb.Preserved != nil {
			return CloneBody(tb.Preserved), nil
		}
		if tb.RawType != "" {
			return &Body{Type: tb.RawType}, nil
		}
		return nil, configErrorf("unsupported body has no preserved payload")

	case BodyNone:
		if strings.EqualFold(strings.TrimSpace(tb.RawType), BodyNone) {
			return &Body{Type: tb.RawType}, nil
		}
		return nil, nil

	case BodyJSON:
		typ := preserveAliasOr(tb.RawType, BodyJSON, BodyJSON)
		fileStr, err := tb.encodeFileField()
		if err != nil {
			return nil, err
		}
		if fileStr != "" {
			if tb.JSONNode != nil {
				return nil, configErrorf("json body cannot specify both value and file")
			}
			return &Body{Type: typ, File: fileStr}, nil
		}
		if tb.JSONNode == nil {
			return nil, configErrorf("json body has no value")
		}
		return &Body{Type: typ, Value: CloneYAMLNode(tb.JSONNode)}, nil

	case BodyText, BodyXML:
		typ := preserveAliasOr(tb.RawType, tb.Kind, tb.Kind)
		fileStr, err := tb.encodeFileField()
		if err != nil {
			return nil, err
		}
		if fileStr != "" {
			if tb.HasInlineValue {
				return nil, configErrorf("%s body cannot specify both value and file", tb.Kind)
			}
			return &Body{Type: typ, File: fileStr}, nil
		}
		return &Body{
			Type:  typ,
			Value: scalarStringNode(tb.Text),
		}, nil

	case BodyURLEncoded:
		typ := preserveAliasOr(tb.RawType, BodyURLEncoded, BodyURLEncoded)
		fileStr, err := tb.encodeFileField()
		if err != nil {
			return nil, err
		}
		if fileStr != "" {
			if len(tb.FormFields) > 0 {
				return nil, configErrorf("urlencoded body cannot specify both value and file")
			}
			return &Body{Type: typ, File: fileStr}, nil
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, f := range tb.FormFields {
			m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			appendMapKV(m, "name", scalarStringNode(f.Name))
			appendMapKV(m, "value", scalarStringNode(f.Value))
			if f.Encoded {
				appendMapKV(m, "encoded", scalarBoolNode(true))
			}
			if f.Enabled != nil {
				appendMapKV(m, "enabled", scalarBoolNode(*f.Enabled))
			}
			m.Content = append(m.Content, cloneNodeSlice(f.Extra)...)
			seq.Content = append(seq.Content, m)
		}
		return &Body{Type: typ, Value: seq}, nil

	case BodyFormData:
		typ := preserveAliasOr(tb.RawType, BodyFormData, BodyFormData)
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, p := range tb.MultipartParts {
			m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			appendMapKV(m, "name", scalarStringNode(p.Name))
			effKind := p.EffectiveKind()
			if p.Kind != "" || effKind == "file" {
				kVal := p.Kind
				if kVal == "" {
					kVal = effKind
				}
				appendMapKV(m, "kind", scalarStringNode(kVal))
			}
			if effKind == "file" {
				if p.LocalFile != "" {
					if err := ValidateLocalSlot(p.LocalFile); err != nil {
						return nil, err
					}
					appendMapKV(m, "localFile", scalarStringNode(p.LocalFile))
				} else if p.File != "" {
					if _, err := ParseFileRef(p.File); err != nil {
						return nil, err
					}
					appendMapKV(m, "file", scalarStringNode(p.File))
				}
				if p.FileName != "" {
					appendMapKV(m, "fileName", scalarStringNode(p.FileName))
				}
			} else {
				appendMapKV(m, "value", scalarStringNode(p.Value))
			}
			if p.ContentType != "" {
				appendMapKV(m, "contentType", scalarStringNode(p.ContentType))
			}
			if p.Enabled != nil {
				appendMapKV(m, "enabled", scalarBoolNode(*p.Enabled))
			}
			m.Content = append(m.Content, cloneNodeSlice(p.Extra)...)
			seq.Content = append(seq.Content, m)
		}
		return &Body{Type: typ, Value: seq}, nil

	case BodyBinary:
		typ := preserveAliasOr(tb.RawType, BodyBinary, BodyBinary)
		fileStr, err := tb.encodeFileField()
		if err != nil {
			return nil, err
		}
		if tb.ContentType != "" || len(tb.BodyExtra) > 0 {
			m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			if tb.LocalFile != "" {
				appendMapKV(m, "localFile", scalarStringNode(tb.LocalFile))
			} else if tb.File != "" {
				appendMapKV(m, "file", scalarStringNode(tb.File))
			}
			if tb.ContentType != "" {
				appendMapKV(m, "contentType", scalarStringNode(tb.ContentType))
			}
			m.Content = append(m.Content, cloneNodeSlice(tb.BodyExtra)...)
			return &Body{Type: typ, Value: m}, nil
		}
		if fileStr == "" {
			return nil, configErrorf("binary body requires a project file or local file mapping")
		}
		return &Body{Type: typ, File: fileStr}, nil

	default:
		return nil, configErrorf("unknown body kind %q", tb.Kind)
	}
}

func (tb *TypedBody) encodeFileField() (string, error) {
	if tb.File != "" && tb.LocalFile != "" {
		return "", configErrorf("body cannot specify both project file and local file mapping")
	}
	if tb.LocalFile != "" {
		if err := ValidateLocalSlot(tb.LocalFile); err != nil {
			return "", err
		}
		return FormatLocalFileRef(tb.LocalFile), nil
	}
	if tb.File != "" {
		clean, err := ValidateProjectRelPath(tb.File)
		if err != nil {
			return "", err
		}
		return clean, nil
	}
	return "", nil
}

func preserveAliasOr(rawType, wantCanonical, fallback string) string {
	if rawType != "" && CanonicalBodyType(rawType) == wantCanonical {
		return rawType
	}
	return fallback
}

func scalarStringNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func scalarBoolNode(b bool) *yaml.Node {
	v := "false"
	if b {
		v = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
}

func appendMapKV(m *yaml.Node, key string, val *yaml.Node) {
	m.Content = append(m.Content, scalarStringNode(key), val)
}

func cloneNodeSlice(in []*yaml.Node) []*yaml.Node {
	if len(in) == 0 {
		return nil
	}
	out := make([]*yaml.Node, len(in))
	for i, n := range in {
		out[i] = CloneYAMLNode(n)
	}
	return out
}

// ValidateBody performs strict static validation of a Body without accessing
// the network or requiring local secret/file bindings. Unknown body types,
// unknown item fields, mutually exclusive fields, header injection in MIME
// metadata, and path traversal are all reported as ConfigErrors.
func ValidateBody(b *Body) error {
	if b == nil {
		return nil
	}
	tb, err := InspectBody(b)
	if err != nil {
		return err
	}
	switch tb.Kind {
	case BodyUnsupported:
		return configErrorf("unsupported body type %q", b.Type)
	case BodyNone, BodyJSON, BodyText, BodyXML:
		return nil
	case BodyBinary:
		if len(tb.BodyExtra) >= 2 {
			return configErrorf("unknown binary body field %q", tb.BodyExtra[0].Value)
		}
		if tb.ContentType != "" {
			if err := validateMediaType(tb.ContentType); err != nil {
				return configErrorf("binary body contentType: %v", err)
			}
		}
		return nil
	case BodyURLEncoded:
		for i, f := range tb.FormFields {
			if len(f.Extra) >= 2 {
				return configErrorf("urlencoded item %d: unknown field %q", i+1, f.Extra[0].Value)
			}
			if f.IsEnabled() && strings.TrimSpace(f.Name) == "" {
				return configErrorf("urlencoded item %d: enabled field has an empty name", i+1)
			}
			if f.IsEnabled() && f.Encoded {
				if err := validatePercentEncodedWithTemplates(f.Name); err != nil {
					return configErrorf("urlencoded item %d name: %v", i+1, err)
				}
				if err := validatePercentEncodedWithTemplates(f.Value); err != nil {
					return configErrorf("urlencoded item %d value: %v", i+1, err)
				}
			}
		}
		return nil
	case BodyFormData:
		for i, p := range tb.MultipartParts {
			if len(p.Extra) >= 2 {
				return configErrorf("form-data part %d: unknown field %q", i+1, p.Extra[0].Value)
			}
			if err := validateMultipartPartStatic(i, p); err != nil {
				return err
			}
		}
		return nil
	default:
		return configErrorf("unsupported body type %q", b.Type)
	}
}

func validateMultipartPartStatic(idx int, p MultipartPart) error {
	k := p.EffectiveKind()
	if k != "text" && k != "file" {
		return configErrorf("form-data part %d (%q): kind must be \"text\" or \"file\", got %q", idx+1, p.Name, p.Kind)
	}
	if !p.IsEnabled() {
		// Still check lexical path safety on disabled file parts so bad paths
		// cannot hide in disabled rows.
		if p.File != "" || p.LocalFile != "" {
			if _, err := p.ResolveFileRef(); err != nil {
				return configErrorf("form-data part %d: %v", idx+1, err)
			}
		}
		return nil
	}
	if strings.TrimSpace(p.Name) == "" {
		return configErrorf("form-data part %d: enabled part has an empty name", idx+1)
	}
	if strings.ContainsAny(p.Name, "\r\n") {
		return configErrorf("form-data part %q contains a newline in name", p.Name)
	}
	if strings.ContainsAny(p.FileName, "\r\n") {
		return configErrorf("form-data part %q contains a newline in fileName", p.Name)
	}
	if p.ContentType != "" {
		if err := validateMediaType(p.ContentType); err != nil {
			return configErrorf("form-data part %q contentType: %v", p.Name, err)
		}
	}
	switch k {
	case "text":
		if p.File != "" || p.LocalFile != "" {
			return configErrorf("form-data text part %q must not specify file or localFile", p.Name)
		}
	case "file":
		if p.hasValue && p.Value != "" {
			return configErrorf("form-data file part %q cannot specify inline value", p.Name)
		}
		ref, err := p.ResolveFileRef()
		if err != nil {
			return configErrorf("form-data part %q: %v", p.Name, err)
		}
		if ref.Kind == FileRefNone {
			return configErrorf("form-data file part %q has no file or localFile", p.Name)
		}
	}
	return nil
}

func validateMediaType(ct string) error {
	if strings.ContainsAny(ct, "\r\n") {
		return fmt.Errorf("contains newline; refusing header injection")
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil || strings.TrimSpace(mt) == "" {
		return fmt.Errorf("invalid media type %q", ct)
	}
	return nil
}

// formatYAMLNodeAsJSON renders a YAML AST node as pretty-printed JSON while
// preserving mapping key order and {{template}} string scalars.
func formatYAMLNodeAsJSON(n *yaml.Node) (string, error) {
	n = unwrapDocNode(n)
	if n == nil {
		return "", nil
	}
	var b strings.Builder
	if err := writeYAMLNodeJSON(&b, n, 0); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeYAMLNodeJSON(b *strings.Builder, n *yaml.Node, depth int) error {
	if n == nil {
		b.WriteString("null")
		return nil
	}
	indent := strings.Repeat("  ", depth)
	childIndent := strings.Repeat("  ", depth+1)
	switch n.Kind {
	case yaml.MappingNode:
		if len(n.Content) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{\n")
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				b.WriteString(",\n")
			}
			kb, err := json.Marshal(n.Content[i].Value)
			if err != nil {
				return err
			}
			b.WriteString(childIndent)
			b.Write(kb)
			b.WriteString(": ")
			if err := writeYAMLNodeJSON(b, n.Content[i+1], depth+1); err != nil {
				return err
			}
		}
		b.WriteByte('\n')
		b.WriteString(indent)
		b.WriteByte('}')
		return nil

	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[\n")
		for i, c := range n.Content {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(childIndent)
			if err := writeYAMLNodeJSON(b, c, depth+1); err != nil {
				return err
			}
		}
		b.WriteByte('\n')
		b.WriteString(indent)
		b.WriteByte(']')
		return nil

	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "":
			raw, err := json.Marshal(n.Value)
			if err != nil {
				return err
			}
			b.Write(raw)
			return nil
		case "!!null":
			b.WriteString("null")
			return nil
		default:
			var v any
			if err := n.Decode(&v); err != nil {
				return err
			}
			raw, err := json.Marshal(v)
			if err != nil {
				return err
			}
			b.Write(raw)
			return nil
		}
	default:
		return fmt.Errorf("unsupported YAML node kind %d", n.Kind)
	}
}

// normalizeJSONYAMLStyle converts flow-style JSON AST nodes into clean
// block-style YAML nodes so saved .request.yaml files remain diff-friendly.
func normalizeJSONYAMLStyle(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			normalizeJSONYAMLStyle(c)
		}
	case yaml.MappingNode:
		n.Style = 0
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind == yaml.ScalarNode && isPlainSafeYAMLKey(k.Value) {
				k.Style = 0
			}
			normalizeJSONYAMLStyle(n.Content[i+1])
		}
	case yaml.SequenceNode:
		n.Style = 0
		for _, c := range n.Content {
			normalizeJSONYAMLStyle(c)
		}
	case yaml.ScalarNode:
		if n.Tag == "!!str" && !strings.Contains(n.Value, "{{") && isPlainSafeYAMLScalar(n.Value) {
			n.Style = 0
		}
	}
}

func isPlainSafeYAMLKey(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off":
		return false
	}
	return !(s[0] >= '0' && s[0] <= '9')
}

func isPlainSafeYAMLScalar(s string) bool {
	return isPlainSafeYAMLKey(s)
}
