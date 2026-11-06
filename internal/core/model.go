package core

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// SupportedSchemaVersion is the only project/request/suite schema version this
// build reads and writes. Files declaring a higher version are refused rather
// than silently downgraded.
const SupportedSchemaVersion = 1

// KV is one ordered, toggleable name/value pair, used for headers, query
// parameters and cookies. Order and duplicates are preserved because these
// are slices, not maps.
type KV struct {
	Name    string `yaml:"name"`
	Value   string `yaml:"value"`
	Enabled *bool  `yaml:"enabled,omitempty"`
}

// IsEnabled reports whether the pair is active. A nil Enabled means true, so
// files may omit it.
func (kv KV) IsEnabled() bool { return kv.Enabled == nil || *kv.Enabled }

// BoolPtr returns a pointer to b, for optional Enabled fields.
func BoolPtr(b bool) *bool { return &b }

// Body is a request body. Type is one of json, text or xml for this MVP.
// Value keeps the parsed YAML node so nested JSON objects round-trip without
// being reordered or flattened. File is reserved for a future external-body
// reference and is not executed yet.
type Body struct {
	Type  string     `yaml:"type"`
	Value *yaml.Node `yaml:"value,omitempty"`
	File  string     `yaml:"file,omitempty"`
}

// UnmarshalYAML decodes a body explicitly so the free-form `value` subtree is
// captured as a yaml.Node (preserving order and structure) instead of being
// rejected by strict field checking meant only for the body's own keys.
func (b *Body) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("body must be a mapping")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		switch key {
		case "type":
			if err := val.Decode(&b.Type); err != nil {
				return err
			}
		case "file":
			if err := val.Decode(&b.File); err != nil {
				return err
			}
		case "value":
			b.Value = val
		default:
			return fmt.Errorf("unknown body field %q", key)
		}
	}
	return nil
}

// HTTPDef is the HTTP specifics of a request.
type HTTPDef struct {
	Method  string `yaml:"method"`
	URL     string `yaml:"url"`
	Query   []KV   `yaml:"query,omitempty"`
	Headers []KV   `yaml:"headers,omitempty"`
	Body    *Body  `yaml:"body,omitempty"`
}

// AuthRef is the request/folder-level auth reference. It is either the scalar
// "none" (clear inherited auth) or a mapping naming a reusable auth scheme
// (ref) or declaring one inline.
type AuthRef struct {
	Mode   string      // "", "none", "ref", "inline"
	Ref    string      // scheme id when Mode == "ref"
	Inline *AuthScheme // scheme when Mode == "inline"
}

// UnmarshalYAML accepts either `auth: none` or `auth: {ref: bearer}` or an
// inline scheme mapping.
func (a *AuthRef) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" || n.Value == "none" {
			a.Mode = "none"
			return nil
		}
		return fmt.Errorf("auth must be \"none\" or a mapping, got %q", n.Value)
	case yaml.MappingNode:
		var probe struct {
			Ref   string `yaml:"ref"`
			Type  string `yaml:"type"`
			Token string `yaml:"token"`
		}
		if err := n.Decode(&probe); err != nil {
			return err
		}
		if probe.Ref != "" {
			a.Mode = "ref"
			a.Ref = probe.Ref
			return nil
		}
		var scheme AuthScheme
		if err := n.Decode(&scheme); err != nil {
			return err
		}
		a.Mode = "inline"
		a.Inline = &scheme
		return nil
	default:
		return fmt.Errorf("auth must be a scalar or mapping")
	}
}

// MarshalYAML emits "none" for Mode none, a {ref: ...} mapping for Mode ref,
// and the inline scheme for Mode inline.
func (a AuthRef) MarshalYAML() (any, error) {
	switch a.Mode {
	case "", "none":
		return "none", nil
	case "ref":
		return map[string]string{"ref": a.Ref}, nil
	case "inline":
		return a.Inline, nil
	default:
		return nil, fmt.Errorf("unknown auth mode %q", a.Mode)
	}
}

// AuthScheme is a reusable auth definition stored under .gorequest/auth/. Secret
// values (tokens, passwords) should use {{secret.name}} references so the value
// itself never lands in the file.
type AuthScheme struct {
	SchemaVersion int            `yaml:"schemaVersion"`
	ID            string         `yaml:"id"`
	Type          string         `yaml:"type"` // bearer | basic | apikey
	Token         string         `yaml:"token,omitempty"`
	Username      string         `yaml:"username,omitempty"`
	Password      string         `yaml:"password,omitempty"`
	Name          string         `yaml:"name,omitempty"`  // api key name
	In            string         `yaml:"in,omitempty"`    // header | query | cookie
	Value         string         `yaml:"value,omitempty"` // api key value template
	Extensions    map[string]any `yaml:"extensions,omitempty"`
	OAuth         *OAuthConfig   `yaml:"oauth,omitempty"` // type oauth2 | oidc (A04)
}

// Request is one request stored as its own YAML file.
type Request struct {
	SchemaVersion int               `yaml:"schemaVersion"`
	ID            string            `yaml:"id"`
	Name          string            `yaml:"name"`
	Protocol      string            `yaml:"protocol,omitempty"`
	Tags          []string          `yaml:"tags,omitempty"`
	Variables     map[string]string `yaml:"variables,omitempty"`
	HTTP          *HTTPDef          `yaml:"http"`
	Auth          *AuthRef          `yaml:"auth,omitempty"`
	Timeout       string            `yaml:"timeout,omitempty"`
	Assert        []yaml.Node       `yaml:"assert,omitempty"`
	Documentation []string          `yaml:"documentation,omitempty"`
	Extensions    map[string]any    `yaml:"extensions,omitempty"`
}

// ProjectFile is .gorequest/project.yaml.
type ProjectFile struct {
	SchemaVersion int               `yaml:"schemaVersion"`
	ID            string            `yaml:"id"`
	Name          string            `yaml:"name"`
	Variables     map[string]string `yaml:"variables,omitempty"`
	Defaults      *Defaults         `yaml:"defaults,omitempty"`
	Extensions    map[string]any    `yaml:"extensions,omitempty"`
}

// Defaults are project-wide request defaults.
type Defaults struct {
	Timeout string `yaml:"timeout,omitempty"`
	Auth    string `yaml:"auth,omitempty"`
}

// Environment is .gorequest/environments/<id>.yaml. Shared files carry plain
// values and secret *names* only; secret values come from the local provider.
type Environment struct {
	SchemaVersion int               `yaml:"schemaVersion"`
	ID            string            `yaml:"id"`
	Variables     map[string]string `yaml:"variables,omitempty"`
	Secrets       []string          `yaml:"secrets,omitempty"`
	Extensions    map[string]any    `yaml:"extensions,omitempty"`
}

// Collection is an optional collection.yaml marking a folder and carrying
// variables inherited by the requests beneath it.
type Collection struct {
	SchemaVersion int               `yaml:"schemaVersion"`
	ID            string            `yaml:"id"`
	Name          string            `yaml:"name"`
	Variables     map[string]string `yaml:"variables,omitempty"`
	Extensions    map[string]any    `yaml:"extensions,omitempty"`
	Auth          *AuthRef          `yaml:"auth,omitempty"`
}

// decodeStrict decodes exactly one YAML document into out with unknown-field
// rejection and duplicate-key rejection (provided by yaml.v3). A second
// document is an error.
func decodeStrict(data []byte, out any) error {
	tree, err := ParseStrictYAMLTree(data, YAMLLimits{})
	if err != nil {
		return err
	}
	switch out.(type) {
	case *Request, *Collection, *CollectionIdentity:
		if err := auditRequestAST(tree.Content[0]); err != nil {
			return err
		}
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if err == io.EOF {
			return fmt.Errorf("empty document")
		}
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("expected a single YAML document, found more")
		}
		return err
	}
	return nil
}

// encodeYAML marshals v with two-space indentation and a trailing newline.
func encodeYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	// yaml.Encoder already ends with a newline; normalize just in case.
	if b := buf.Bytes(); len(b) == 0 || b[len(b)-1] != '\n' {
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}
