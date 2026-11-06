package runner

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// This file implements the documented JSONPath subset. It is intentionally
// small and deterministic:
//
//	$                    root
//	.name  .*            child key / all object values
//	['name']  ["name"]   quoted key
//	[0]  [-1]            array index (negative counts from the end)
//	[*]                  all array elements / object values
//	..name  ..*          recursive descent
//
// Filters ([?(@...)]), slices, unions and scripts are rejected at parse time
// with a config error instead of being silently ignored.

type jpKind int

const (
	jpChild jpKind = iota
	jpIndex
	jpWildcard
	jpRecursive
)

type jpSegment struct {
	kind jpKind
	name string
	idx  int
}

type jsonPathQuery struct {
	raw  string
	segs []jpSegment
}

func parseJSONPath(raw string) (*jsonPathQuery, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("jsonpath is empty")
	}
	if s[0] != '$' {
		return nil, fmt.Errorf("jsonpath %q must start with $", raw)
	}
	q := &jsonPathQuery{raw: raw}
	i := 1
	for i < len(s) {
		switch s[i] {
		case '.':
			recursive := false
			i++
			if i < len(s) && s[i] == '.' {
				recursive = true
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("jsonpath %q ends after separator", raw)
			}
			if s[i] == '*' {
				kind := jpWildcard
				if recursive {
					kind = jpRecursive
				}
				q.segs = append(q.segs, jpSegment{kind: kind})
				i++
				continue
			}
			name, next, err := readJSONPathName(s, i)
			if err != nil {
				return nil, fmt.Errorf("jsonpath %q: %w", raw, err)
			}
			if recursive {
				q.segs = append(q.segs, jpSegment{kind: jpRecursive, name: name})
			} else {
				q.segs = append(q.segs, jpSegment{kind: jpChild, name: name})
			}
			i = next
		case '[':
			seg, next, err := parseJSONPathBracket(s, i)
			if err != nil {
				return nil, fmt.Errorf("jsonpath %q: %w", raw, err)
			}
			q.segs = append(q.segs, seg)
			i = next
		default:
			return nil, fmt.Errorf("jsonpath %q: unexpected character %q at offset %d", raw, s[i], i)
		}
	}
	return q, nil
}

func readJSONPathName(s string, i int) (string, int, error) {
	start := i
	for i < len(s) {
		c := s[i]
		if c == '.' || c == '[' {
			break
		}
		if c == ']' || c == '\'' || c == '"' || c == ' ' || c == '\t' {
			return "", i, fmt.Errorf("invalid character %q in key at offset %d", c, i)
		}
		i++
	}
	if i == start {
		return "", i, fmt.Errorf("empty key at offset %d", start)
	}
	return s[start:i], i, nil
}

func parseJSONPathBracket(s string, i int) (jpSegment, int, error) {
	// s[i] == '['
	j := i + 1
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j >= len(s) {
		return jpSegment{}, j, fmt.Errorf("unterminated bracket")
	}
	if s[j] == '*' {
		j++
		for j < len(s) && s[j] == ' ' {
			j++
		}
		if j >= len(s) || s[j] != ']' {
			return jpSegment{}, j, fmt.Errorf("expected ] after [*")
		}
		return jpSegment{kind: jpWildcard}, j + 1, nil
	}
	if s[j] == '\'' || s[j] == '"' {
		quote := s[j]
		j++
		start := j
		for j < len(s) && s[j] != quote {
			j++
		}
		if j >= len(s) {
			return jpSegment{}, j, fmt.Errorf("unterminated quoted key")
		}
		name := s[start:j]
		j++
		for j < len(s) && s[j] == ' ' {
			j++
		}
		if j >= len(s) || s[j] != ']' {
			return jpSegment{}, j, fmt.Errorf("expected ] after quoted key")
		}
		return jpSegment{kind: jpChild, name: name}, j + 1, nil
	}
	// Union / slice / filter detection gives a clearer error than a number parse.
	end := j
	for end < len(s) && s[end] != ']' {
		end++
	}
	if end >= len(s) {
		return jpSegment{}, end, fmt.Errorf("unterminated bracket")
	}
	body := strings.TrimSpace(s[j:end])
	if strings.ContainsAny(body, ",:?()@") {
		return jpSegment{}, end, fmt.Errorf("unsupported jsonpath feature %q (supported: .name, ['name'], [index], [*], ..name)", body)
	}
	idx, err := strconv.Atoi(body)
	if err != nil {
		return jpSegment{}, end, fmt.Errorf("invalid array index %q", body)
	}
	return jpSegment{kind: jpIndex, idx: idx}, end + 1, nil
}

func (q *jsonPathQuery) String() string {
	if q == nil {
		return ""
	}
	return q.raw
}

// Eval returns every matching node in deterministic order.
func (q *jsonPathQuery) Eval(root any) []any {
	if q == nil {
		return nil
	}
	cur := []any{root}
	for _, seg := range q.segs {
		if len(cur) == 0 {
			return nil
		}
		var next []any
		for _, v := range cur {
			switch seg.kind {
			case jpChild:
				if m, ok := v.(map[string]any); ok {
					if val, ok := m[seg.name]; ok {
						next = append(next, val)
					}
				}
			case jpIndex:
				if arr, ok := v.([]any); ok {
					idx := seg.idx
					if idx < 0 {
						idx += len(arr)
					}
					if idx >= 0 && idx < len(arr) {
						next = append(next, arr[idx])
					}
				}
			case jpWildcard:
				next = append(next, wildcardValues(v)...)
			case jpRecursive:
				walkRecursive(v, seg.name, func(val any) {
					next = append(next, val)
				})
			}
		}
		cur = next
	}
	return cur
}

func wildcardValues(v any) []any {
	switch t := v.(type) {
	case []any:
		return append([]any(nil), t...)
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, t[k])
		}
		return out
	default:
		return nil
	}
}

func walkRecursive(v any, name string, visit func(any)) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := t[k]
			if name == "" || k == name {
				visit(child)
			}
			walkRecursive(child, name, visit)
		}
	case []any:
		for _, child := range t {
			if name == "" {
				visit(child)
			}
			walkRecursive(child, name, visit)
		}
	}
}
