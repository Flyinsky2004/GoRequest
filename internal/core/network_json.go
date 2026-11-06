package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// MaxJSONTreeBytes is the maximum payload size (2 MiB) parsed into an
// interactive JSON tree; larger payloads degrade to plain-text mode.
const MaxJSONTreeBytes = 2 << 20

// MaxJSONTreeNodes bounds the number of tree nodes built for a single JSON
// response so deeply nested or huge arrays cannot exhaust memory or frame time.
const MaxJSONTreeNodes = 5000

// MaxResponseSearchMatches caps bounded search results in response views.
const MaxResponseSearchMatches = 10000

// JSONNodeKind identifies the JSON type of a JSONTreeNode.
type JSONNodeKind string

const (
	JSONNodeObject JSONNodeKind = "object"
	JSONNodeArray  JSONNodeKind = "array"
	JSONNodeString JSONNodeKind = "string"
	JSONNodeNumber JSONNodeKind = "number"
	JSONNodeBool   JSONNodeKind = "bool"
	JSONNodeNull   JSONNodeKind = "null"
)

// JSONTreeNode represents one node in an order-preserving parsed JSON response.
type JSONTreeNode struct {
	Path     string
	Key      string
	Kind     JSONNodeKind
	Value    string
	RawValue string
	Depth    int
	Children []*JSONTreeNode
}

// IsContainer reports whether n is an object or array with child elements.
func (n *JSONTreeNode) IsContainer() bool {
	return n != nil && (n.Kind == JSONNodeObject || n.Kind == JSONNodeArray) && len(n.Children) > 0
}

// FlatJSONRow is one visible row in a flattened JSON tree view.
type FlatJSONRow struct {
	Node        *JSONTreeNode
	Expanded    bool
	Matched     bool
	MatchIndex  int // 0-based index among matched nodes, or -1 if not matched
}

// ParseJSONTree parses raw JSON bytes into an order-preserving JSONTreeNode
// hierarchy, bounded by MaxJSONTreeBytes and maxNodes (defaulting to
// MaxJSONTreeNodes). It returns (root, truncatedNodes, error).
func ParseJSONTree(data []byte, maxNodes int) (*JSONTreeNode, bool, error) {
	if len(data) == 0 {
		return nil, false, fmt.Errorf("empty JSON payload")
	}
	if len(data) > MaxJSONTreeBytes {
		return nil, true, fmt.Errorf("JSON payload exceeds %d bytes tree budget", MaxJSONTreeBytes)
	}
	if maxNodes <= 0 {
		maxNodes = MaxJSONTreeNodes
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	count := 0
	truncated := false
	root, err := parseJSONValue(dec, "$", "", 0, &count, maxNodes, &truncated)
	if err != nil {
		return nil, truncated, err
	}
	return root, truncated, nil
}

func parseJSONValue(
	dec *json.Decoder,
	pathStr, key string,
	depth int,
	count *int,
	maxNodes int,
	truncated *bool,
) (*JSONTreeNode, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return parseJSONToken(dec, tok, pathStr, key, depth, count, maxNodes, truncated)
}

func parseJSONToken(
	dec *json.Decoder,
	tok json.Token,
	pathStr, key string,
	depth int,
	count *int,
	maxNodes int,
	truncated *bool,
) (*JSONTreeNode, error) {
	*count++
	node := &JSONTreeNode{
		Path:  pathStr,
		Key:   key,
		Depth: depth,
	}

	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			node.Kind = JSONNodeObject
			totalKeys := 0
			for dec.More() {
				kTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				kStr, ok := kTok.(string)
				if !ok {
					return nil, fmt.Errorf("expected object key string, got %T", kTok)
				}
				totalKeys++
				if *count >= maxNodes {
					*truncated = true
					if err := skipJSONValue(dec); err != nil {
						return nil, err
					}
					continue
				}
				childPath := appendJSONObjectPath(pathStr, kStr)
				child, err := parseJSONValue(dec, childPath, kStr, depth+1, count, maxNodes, truncated)
				if err != nil {
					return nil, err
				}
				node.Children = append(node.Children, child)
			}
			// Consume closing '}'
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			if totalKeys == 1 {
				node.Value = "{1 key}"
			} else {
				node.Value = fmt.Sprintf("{%d keys}", totalKeys)
			}
			node.RawValue = formatSubtreeJSON(node)
			return node, nil

		case '[':
			node.Kind = JSONNodeArray
			idx := 0
			for dec.More() {
				curIdx := idx
				idx++
				if *count >= maxNodes {
					*truncated = true
					if err := skipJSONValue(dec); err != nil {
						return nil, err
					}
					continue
				}
				idxKey := "[" + strconv.Itoa(curIdx) + "]"
				childPath := pathStr + idxKey
				child, err := parseJSONValue(dec, childPath, idxKey, depth+1, count, maxNodes, truncated)
				if err != nil {
					return nil, err
				}
				node.Children = append(node.Children, child)
			}
			// Consume closing ']'
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			if idx == 1 {
				node.Value = "[1 item]"
			} else {
				node.Value = fmt.Sprintf("[%d items]", idx)
			}
			node.RawValue = formatSubtreeJSON(node)
			return node, nil

		default:
			return nil, fmt.Errorf("unexpected delimiter %q", v)
		}

	case string:
		node.Kind = JSONNodeString
		b, _ := json.Marshal(v)
		node.Value = string(b)
		node.RawValue = v
		return node, nil

	case json.Number:
		node.Kind = JSONNodeNumber
		node.Value = v.String()
		node.RawValue = v.String()
		return node, nil

	case bool:
		node.Kind = JSONNodeBool
		if v {
			node.Value = "true"
		} else {
			node.Value = "false"
		}
		node.RawValue = node.Value
		return node, nil

	case nil:
		node.Kind = JSONNodeNull
		node.Value = "null"
		node.RawValue = "null"
		return node, nil

	default:
		return nil, fmt.Errorf("unexpected JSON token %T", tok)
	}
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	default:
		return io.EOF
	}
}

func appendJSONObjectPath(base, key string) string {
	if isSimpleJSONIdentifier(key) {
		return base + "." + key
	}
	quoted, _ := json.Marshal(key)
	return base + "[" + string(quoted) + "]"
}

func isSimpleJSONIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_') {
				return false
			}
		} else {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
				return false
			}
		}
	}
	return true
}

func formatSubtreeJSON(n *JSONTreeNode) string {
	if n == nil {
		return "null"
	}
	switch n.Kind {
	case JSONNodeObject:
		if len(n.Children) == 0 {
			return "{}"
		}
		var b strings.Builder
		b.WriteByte('{')
		for i, c := range n.Children {
			if i > 0 {
				b.WriteString(", ")
			}
			kb, _ := json.Marshal(c.Key)
			b.Write(kb)
			b.WriteString(": ")
			b.WriteString(formatSubtreeJSON(c))
		}
		b.WriteByte('}')
		return b.String()
	case JSONNodeArray:
		if len(n.Children) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, c := range n.Children {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(formatSubtreeJSON(c))
		}
		b.WriteByte(']')
		return b.String()
	case JSONNodeString:
		return n.Value
	default:
		return n.RawValue
	}
}

// FlattenJSONTree produces the ordered slice of visible rows for root, taking
// into account collapsed node paths and an optional search query. When query is
// non-empty, matching nodes and their ancestor chains are automatically shown,
// and match counting is bounded by maxMatches (default MaxResponseSearchMatches).
func FlattenJSONTree(
	root *JSONTreeNode,
	collapsed map[string]bool,
	query string,
	maxMatches int,
) (rows []FlatJSONRow, matchCount int, capped bool) {
	if root == nil {
		return nil, 0, false
	}
	if maxMatches <= 0 {
		maxMatches = MaxResponseSearchMatches
	}
	q := strings.ToLower(strings.TrimSpace(query))

	var matchedPaths map[string]int
	var ancestorVisible map[string]bool
	if q != "" {
		matchedPaths = make(map[string]int)
		ancestorVisible = make(map[string]bool)
		var scan func(n *JSONTreeNode) bool
		scan = func(n *JSONTreeNode) bool {
			selfMatch := strings.Contains(strings.ToLower(n.Key), q) ||
				strings.Contains(strings.ToLower(n.Value), q) ||
				strings.Contains(strings.ToLower(n.Path), q)
			if selfMatch {
				if matchCount < maxMatches {
					matchedPaths[n.Path] = matchCount
					matchCount++
				} else {
					capped = true
				}
			}
			anyChild := false
			for _, c := range n.Children {
				if scan(c) {
					anyChild = true
				}
			}
			if selfMatch || anyChild {
				ancestorVisible[n.Path] = true
				return true
			}
			return false
		}
		scan(root)
	}

	var walk func(n *JSONTreeNode)
	walk = func(n *JSONTreeNode) {
		if q != "" && !ancestorVisible[n.Path] {
			return
		}
		isCollapsed := collapsed != nil && collapsed[n.Path]
		// When filtering by query, expand ancestor containers that lead to a match.
		if q != "" && len(n.Children) > 0 {
			for _, c := range n.Children {
				if ancestorVisible[c.Path] {
					isCollapsed = false
					break
				}
			}
		}
		mIdx, isMatch := -1, false
		if matchedPaths != nil {
			if idx, ok := matchedPaths[n.Path]; ok {
				mIdx = idx
				isMatch = true
			}
		}
		rows = append(rows, FlatJSONRow{
			Node:       n,
			Expanded:   !isCollapsed,
			Matched:    isMatch,
			MatchIndex: mIdx,
		})
		if !isCollapsed {
			for _, c := range n.Children {
				walk(c)
			}
		}
	}
	walk(root)
	return rows, matchCount, capped
}
