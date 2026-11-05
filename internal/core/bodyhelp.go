package core

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseBodyText builds a Body from editor text and a type ("json", "text" or
// "xml"). For json the text is parsed as YAML/JSON into a structured node so
// nested objects keep their shape in the stored file.
func ParseBodyText(typ, text string) (*Body, error) {
	switch typ {
	case "json":
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(text), &n); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		if len(n.Content) == 0 {
			return nil, fmt.Errorf("JSON body is empty")
		}
		return &Body{Type: "json", Value: n.Content[0]}, nil
	case "text", "xml":
		return &Body{Type: typ, Value: &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: text}}, nil
	default:
		return nil, fmt.Errorf("unsupported body type %q", typ)
	}
}

// BodyEditText returns editor text for a body: JSON pretty-printed for json
// bodies, the raw string for text/xml, empty for nil.
func BodyEditText(b *Body) string {
	if b == nil || b.Value == nil {
		return ""
	}
	switch b.Type {
	case "json":
		var v any
		if err := b.Value.Decode(&v); err != nil {
			return b.Value.Value
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return b.Value.Value
		}
		return string(out)
	default:
		return b.Value.Value
	}
}
