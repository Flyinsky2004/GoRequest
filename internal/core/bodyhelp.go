package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseBodyText builds a Body from editor text and a type ("none", "json",
// "text", "xml" or "urlencoded"). For json the text is parsed as YAML/JSON into
// a structured node (with duplicate keys rejected and block style normalized)
// so nested objects keep their key order and shape in the stored file.
func ParseBodyText(typ, text string) (*Body, error) {
	switch CanonicalBodyType(typ) {
	case BodyNone:
		if strings.TrimSpace(text) != "" {
			return nil, fmt.Errorf("body type \"none\" cannot carry text")
		}
		return nil, nil

	case BodyJSON:
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(text), &n); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		if len(n.Content) == 0 {
			return nil, fmt.Errorf("JSON body is empty")
		}
		if err := checkNoDuplicateMappingKeys(&n); err != nil {
			return nil, err
		}
		root := n.Content[0]
		normalizeJSONYAMLStyle(root)
		return &Body{Type: preserveAliasOr(typ, BodyJSON, BodyJSON), Value: root}, nil

	case BodyText:
		return &Body{
			Type:  preserveAliasOr(typ, BodyText, BodyText),
			Value: &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: text},
		}, nil

	case BodyXML:
		return &Body{
			Type:  preserveAliasOr(typ, BodyXML, BodyXML),
			Value: &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: text},
		}, nil

	case BodyURLEncoded:
		var (
			fields []FormField
			err    error
		)
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			if !strings.Contains(trimmed, "\n") && strings.Contains(trimmed, "&") {
				fields, err = ParseRawURLEncodedFields(trimmed)
			} else {
				fields, err = ParseFormBatchLines(text)
			}
			if err != nil {
				return nil, fmt.Errorf("invalid urlencoded body: %w", err)
			}
		}
		tb := &TypedBody{
			Kind:       BodyURLEncoded,
			RawType:    preserveAliasOr(typ, BodyURLEncoded, BodyURLEncoded),
			FormFields: fields,
		}
		return tb.ToBody()

	default:
		return nil, fmt.Errorf("unsupported body type %q", typ)
	}
}

// BodyEditText returns editor text for a body: key-order-preserving pretty JSON
// for json bodies, the raw string for text/xml, batch lines for urlencoded, and
// empty for nil or non-text modes.
func BodyEditText(b *Body) string {
	if b == nil || b.Value == nil {
		return ""
	}
	val := unwrapDocNode(b.Value)
	if val == nil {
		return ""
	}
	switch CanonicalBodyType(b.Type) {
	case BodyJSON:
		if s, err := formatYAMLNodeAsJSON(val); err == nil {
			return s
		}
		var v any
		if err := val.Decode(&v); err != nil {
			return val.Value
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return val.Value
		}
		return string(out)

	case BodyURLEncoded:
		if tb, err := InspectBody(b); err == nil {
			return FormatFormBatchLines(tb.FormFields)
		}
		return ""

	case BodyText, BodyXML:
		if val.Kind == yaml.ScalarNode {
			return val.Value
		}
		return ""

	default:
		if val.Kind == yaml.ScalarNode {
			return val.Value
		}
		return ""
	}
}

// BodyPreviewYAML renders b as two-space-indented YAML for read-only display of
// preserved unsupported body modes.
func BodyPreviewYAML(b *Body) string {
	if b == nil {
		return ""
	}
	raw, err := encodeYAML(b)
	if err != nil {
		return fmt.Sprintf("type: %s\n", b.Type)
	}
	return strings.TrimRight(string(raw), "\n")
}
