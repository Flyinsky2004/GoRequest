package runner

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file exposes a small, additive view over the existing A01
// assertion/extraction parsing so the desktop assertion editor can describe,
// validate and round-trip saved rules without the UI duplicating any parsing
// logic. No existing signature or behavior changes: the helpers below are thin
// wrappers around parseAssertion, parseAssertions and parseExtractions.

// AssertionSpec.Target values. Structured editing in this phase covers the
// first four; the other valid targets and unparsable nodes keep their raw form.
const (
	TargetStatus   = "status"
	TargetHeader   = "header"
	TargetJSONPath = "jsonpath"
	TargetBody     = "body"
	TargetDuration = "durationMs"
	TargetSize     = "size"
	TargetSchema   = "schema"
	TargetInvalid  = "invalid"
)

// MatcherSpec.Kind values; they mirror the assertion DSL matcher keys exactly.
const (
	MatcherExists     = "exists"
	MatcherEquals     = "equals"
	MatcherNotEquals  = "notEquals"
	MatcherIn         = "in"
	MatcherContains   = "contains"
	MatcherNotContain = "notContains"
	MatcherRegex      = "regex"
	MatcherType       = "type"
)

// LiteralSpec.Kind values. Kind records the YAML scalar type the engine decodes
// (strings never compare equal to numbers), Text is its canonical text form.
const (
	LiteralString  = "string"
	LiteralNumber  = "number"
	LiteralBoolean = "boolean"
	LiteralNull    = "null"
)

// LiteralSpec is one typed scalar value of a matcher.
type LiteralSpec struct {
	Kind string
	Text string
}

// MatcherSpec is one matcher of a header/JSONPath/body assertion. Exactly one
// field group is meaningful per Kind, mirroring the parsed Matcher.
type MatcherSpec struct {
	Kind    string
	Exists  *bool        // exists
	Literal *LiteralSpec // equals | notEquals
	In      []LiteralSpec
	Text    string // contains | notContains | regex | type
}

// AssertionSpec is a descriptive view of one saved assert entry. Structured is
// true only for targets a caller can round-trip structurally; other valid
// targets and unparsable nodes report Target/Err/Raw and must be preserved
// verbatim by callers.
type AssertionSpec struct {
	Index        int
	Target       string
	Structured   bool
	Message      string
	Header       string
	JSONPath     string
	StatusEquals *int
	StatusIn     []int
	Matchers     []MatcherSpec
	Err          string
	Raw          string
}

// DescribeAssertions converts saved assert nodes into editable specs. It never
// fails: a bad entry becomes a spec with Target TargetInvalid and Err set, so
// callers can surface the precise problem and preserve the node.
func DescribeAssertions(nodes []yaml.Node) []AssertionSpec {
	out := make([]AssertionSpec, len(nodes))
	for i := range nodes {
		spec := AssertionSpec{Index: i, Target: TargetInvalid}
		a, err := parseAssertion(&nodes[i])
		if err != nil {
			spec.Err = fmt.Sprintf("assertion %d: %v", i+1, err)
			spec.Raw = rawNodePreview(&nodes[i])
			out[i] = spec
			continue
		}
		spec.Message = a.Message
		switch a.target {
		case targetStatus:
			spec.Target = TargetStatus
			spec.Structured = true
			if a.status != nil {
				spec.StatusEquals = a.status.equals
				spec.StatusIn = append([]int(nil), a.status.in...)
			}
		case targetHeader:
			spec.Target = TargetHeader
			spec.Structured = true
			if a.header != nil {
				spec.Header = a.header.name
				spec.Matchers = matcherSpecs(a.header.matcher)
			}
		case targetJSONPath:
			spec.Target = TargetJSONPath
			spec.Structured = true
			if a.jsonPath != nil {
				spec.JSONPath = a.jsonPath.path
				spec.Matchers = matcherSpecs(a.jsonPath.matcher)
			}
		case targetBody:
			spec.Target = TargetBody
			spec.Structured = true
			if a.body != nil {
				spec.Matchers = matcherSpecs(a.body.matcher)
			}
		case targetDuration:
			spec.Target = TargetDuration
			spec.Raw = rawNodePreview(&nodes[i])
		case targetSize:
			spec.Target = TargetSize
			spec.Raw = rawNodePreview(&nodes[i])
		case targetSchema:
			spec.Target = TargetSchema
			spec.Raw = rawNodePreview(&nodes[i])
		default:
			spec.Raw = rawNodePreview(&nodes[i])
		}
		out[i] = spec
	}
	return out
}

// ValidateAssertions runs the engine parser over the exact node list that would
// be saved, returning the first config error with its assertion number.
func ValidateAssertions(nodes []yaml.Node) error {
	_, err := parseAssertions(nodes)
	return err
}

// ValidateExtractions runs the engine extraction validation (name pattern,
// duplicate names, source-specific fields, JSONPath/regex compile).
func ValidateExtractions(exts []Extraction) error {
	_, err := parseExtractions(exts)
	return err
}

// ExtractionEffectiveSensitive reports the sensitivity the engine would apply
// to one extraction (explicit override first, documented defaults second).
func ExtractionEffectiveSensitive(e Extraction) bool {
	return extractionSensitive(e)
}

func matcherSpecs(m Matcher) []MatcherSpec {
	var out []MatcherSpec
	if m.Exists != nil {
		v := *m.Exists
		out = append(out, MatcherSpec{Kind: MatcherExists, Exists: &v})
	}
	if m.Equals != nil {
		out = append(out, MatcherSpec{Kind: MatcherEquals, Literal: literalSpecOf(m.Equals)})
	}
	if m.NotEquals != nil {
		out = append(out, MatcherSpec{Kind: MatcherNotEquals, Literal: literalSpecOf(m.NotEquals)})
	}
	if len(m.In) > 0 {
		in := make([]LiteralSpec, 0, len(m.In))
		for _, l := range m.In {
			in = append(in, *literalSpecOf(&l))
		}
		out = append(out, MatcherSpec{Kind: MatcherIn, In: in})
	}
	if m.Contains != nil {
		out = append(out, MatcherSpec{Kind: MatcherContains, Text: *m.Contains})
	}
	if m.NotContains != nil {
		out = append(out, MatcherSpec{Kind: MatcherNotContain, Text: *m.NotContains})
	}
	if m.Regex != "" {
		out = append(out, MatcherSpec{Kind: MatcherRegex, Text: m.Regex})
	}
	if m.Type != nil {
		out = append(out, MatcherSpec{Kind: MatcherType, Text: *m.Type})
	}
	return out
}

func literalSpecOf(l *literal) *LiteralSpec {
	switch v := l.v.(type) {
	case nil:
		return &LiteralSpec{Kind: LiteralNull}
	case bool:
		return &LiteralSpec{Kind: LiteralBoolean, Text: strconv.FormatBool(v)}
	case int:
		return &LiteralSpec{Kind: LiteralNumber, Text: strconv.Itoa(v)}
	case int64:
		return &LiteralSpec{Kind: LiteralNumber, Text: strconv.FormatInt(v, 10)}
	case uint64:
		return &LiteralSpec{Kind: LiteralNumber, Text: strconv.FormatUint(v, 10)}
	case float64:
		return &LiteralSpec{Kind: LiteralNumber, Text: formatFloat(v)}
	case string:
		return &LiteralSpec{Kind: LiteralString, Text: v}
	default:
		return &LiteralSpec{Kind: LiteralString, Text: fmt.Sprintf("%v", v)}
	}
}

func rawNodePreview(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return ""
	}
	err := enc.Close()
	if err != nil {
		return ""
	}
	s := sb.String()
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}
