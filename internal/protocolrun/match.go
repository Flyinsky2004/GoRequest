package protocolrun

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// pathSegment is one bounded JSON path element: an object key or an array
// index.
type pathSegment struct {
	key     string
	index   int
	isIndex bool
}

// parseJSONPath parses the small documented subset: dotted object keys and
// [n] array indexes, for example payload.data.items[0].id.
func parseJSONPath(raw string) ([]pathSegment, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") || strings.Contains(s, ".[") || strings.HasSuffix(s, "]") && strings.Contains(s, "].") {
		return nil, fmt.Errorf("jsonPath %q has an empty segment", raw)
	}
	var out []pathSegment
	for i := 0; i < len(s); {
		switch s[i] {
		case '[':
			j := strings.IndexByte(s[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("jsonPath %q has an unterminated index", raw)
			}
			idxText := strings.TrimSpace(s[i+1 : i+j])
			if idxText == "" || strings.HasPrefix(idxText, "-") {
				return nil, fmt.Errorf("jsonPath %q has an invalid array index %q", raw, idxText)
			}
			idx := 0
			for _, r := range idxText {
				if r < '0' || r > '9' {
					return nil, fmt.Errorf("jsonPath %q has an invalid array index %q", raw, idxText)
				}
				idx = idx*10 + int(r-'0')
			}
			out = append(out, pathSegment{index: idx, isIndex: true})
			i += j + 1
		case '.':
			i++
		default:
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			key := strings.TrimSpace(s[i:j])
			if key == "" {
				return nil, fmt.Errorf("jsonPath %q has an empty key", raw)
			}
			out = append(out, pathSegment{key: key})
			i = j
		}
	}
	return out, nil
}

// lookupJSON walks decoded JSON. A missing key, an out-of-range index or a
// wrong container type reports found=false rather than an error.
func lookupJSON(root any, path []pathSegment) (any, bool) {
	cur := root
	for _, seg := range path {
		if seg.isIndex {
			arr, ok := cur.([]any)
			if !ok || seg.index >= len(arr) {
				return nil, false
			}
			cur = arr[seg.index]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := obj[seg.key]
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// canonicalJSON renders a decoded JSON value deterministically (encoding/json
// sorts map keys) so structural equality does not depend on key order.
func canonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

func decodeJSONValue(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("payload contains more than one JSON value")
	}
	return v, nil
}

// Validate checks criteria that can be checked without a message.
func (m *MatchSpec) Validate() error {
	if m.Empty() {
		return fmt.Errorf("match requires at least one criterion")
	}
	if m.BinaryHex != "" && !isHexString(m.BinaryHex) {
		return fmt.Errorf("match.binaryHex is not valid hexadecimal")
	}
	if m.JSONPath != "" {
		if _, err := parseJSONPath(m.JSONPath); err != nil {
			return err
		}
	}
	if len(m.JSONEquals) > 0 {
		if _, err := decodeJSONValue(m.JSONEquals); err != nil {
			return fmt.Errorf("match.jsonEquals is not valid JSON: %w", err)
		}
	}
	return nil
}

// Match evaluates every configured criterion with AND semantics. The detail
// string explains the decisive fact; an invalid runtime payload is simply a
// non-match, never a silent success.
func (m MatchSpec) Match(msg Message) (bool, string) {
	if m.Empty() {
		return false, "no match criteria"
	}
	if m.Equals != "" {
		if !msg.Text {
			return false, "payload is binary, not the expected text"
		}
		if string(msg.Data) != m.Equals {
			return false, "text payload differs"
		}
	}
	if m.Contains != "" {
		if !msg.Text {
			return false, "payload is binary, not text"
		}
		if !strings.Contains(string(msg.Data), m.Contains) {
			return false, "text payload does not contain the requested substring"
		}
	}
	if m.BinaryHex != "" {
		want, err := hex.DecodeString(m.BinaryHex)
		if err != nil {
			return false, "binaryHex is invalid"
		}
		if msg.Text || !bytes.Equal(msg.Data, want) {
			return false, "binary payload differs"
		}
	}
	if m.JSONPath != "" || len(m.JSONEquals) > 0 {
		if !msg.Text {
			return false, "payload is binary, not JSON"
		}
		root, err := decodeJSONValue(msg.Data)
		if err != nil {
			return false, "payload is not valid JSON"
		}
		value := root
		if m.JSONPath != "" {
			path, err := parseJSONPath(m.JSONPath)
			if err != nil {
				return false, "jsonPath is invalid"
			}
			var ok bool
			value, ok = lookupJSON(root, path)
			if !ok {
				return false, "jsonPath did not resolve in the payload"
			}
		}
		if len(m.JSONEquals) > 0 {
			expected, err := decodeJSONValue(m.JSONEquals)
			if err != nil {
				return false, "jsonEquals is invalid"
			}
			a, errA := canonicalJSON(value)
			b, errB := canonicalJSON(expected)
			if errA != nil || errB != nil || !reflect.DeepEqual(a, b) {
				return false, "JSON value differs"
			}
		}
	}
	return true, "matched"
}
