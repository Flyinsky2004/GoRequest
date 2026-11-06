package runner

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gorequest/internal/core"
)

var extractionNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

var sensitiveJSONKeyPattern = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|authorization|cookie|session|credential|private[_-]?key)`)

var sensitiveHeaderNames = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
}

// parseExtractions validates extraction declarations.
func parseExtractions(in []Extraction) ([]Extraction, error) {
	out := make([]Extraction, 0, len(in))
	seen := map[string]bool{}
	for i, e := range in {
		if !extractionNamePattern.MatchString(e.Name) {
			return nil, fmt.Errorf("extraction %d: name %q must match [A-Za-z_][A-Za-z0-9_-]*", i+1, e.Name)
		}
		if seen[e.Name] {
			return nil, fmt.Errorf("extraction %d: duplicate output name %q", i+1, e.Name)
		}
		seen[e.Name] = true
		switch strings.ToLower(strings.TrimSpace(e.From)) {
		case "jsonpath":
			if strings.TrimSpace(e.Path) == "" {
				return nil, fmt.Errorf("extraction %q: jsonpath requires path", e.Name)
			}
			if _, err := parseJSONPath(e.Path); err != nil {
				return nil, fmt.Errorf("extraction %q: %w", e.Name, err)
			}
		case "header":
			if strings.TrimSpace(e.Header) == "" {
				return nil, fmt.Errorf("extraction %q: header requires header name", e.Name)
			}
		case "cookie":
			if strings.TrimSpace(e.Cookie) == "" {
				return nil, fmt.Errorf("extraction %q: cookie requires cookie name", e.Name)
			}
		case "body":
			if strings.TrimSpace(e.Regex) == "" {
				return nil, fmt.Errorf("extraction %q: body extraction requires regex", e.Name)
			}
			if _, err := regexp.Compile(e.Regex); err != nil {
				return nil, fmt.Errorf("extraction %q: invalid regex: %v", e.Name, err)
			}
			if e.Group != nil && *e.Group < 0 {
				return nil, fmt.Errorf("extraction %q: group must be >= 0", e.Name)
			}
		default:
			return nil, fmt.Errorf("extraction %q: from must be jsonpath, header, cookie or body", e.Name)
		}
		e.From = strings.ToLower(strings.TrimSpace(e.From))
		out = append(out, e)
	}
	return out, nil
}

// extractionSensitive applies the documented defaults. Explicit false wins, so
// an operator can deliberately publish a non-sensitive value while the default
// keeps credentials out of reports.
func extractionSensitive(e Extraction) bool {
	if e.Sensitive != nil {
		return *e.Sensitive
	}
	switch e.From {
	case "header":
		return sensitiveHeaderNames[strings.ToLower(strings.TrimSpace(e.Header))]
	case "cookie":
		return true
	case "jsonpath":
		return sensitiveJSONKeyPattern.MatchString(lastJSONPathKey(e.Path))
	}
	return false
}

func lastJSONPathKey(path string) string {
	q, err := parseJSONPath(path)
	if err != nil || len(q.segs) == 0 {
		return ""
	}
	for i := len(q.segs) - 1; i >= 0; i-- {
		if q.segs[i].name != "" {
			return q.segs[i].name
		}
	}
	return ""
}

type extractionOutcome struct {
	results []ExtractionResult
	outputs map[string]core.StepOutput
	failure *Failure
}

// extractAll evaluates every extraction. Outputs are only returned when every
// required extraction succeeded; a partial set is never published.
func extractAll(exts []Extraction, ctx *evalContext) extractionOutcome {
	out := extractionOutcome{outputs: map[string]core.StepOutput{}}
	requiredMissing := false
	for _, e := range exts {
		res := ExtractionResult{
			Name:      e.Name,
			From:      e.From,
			Required:  e.IsRequired(),
			Sensitive: extractionSensitive(e),
		}
		value, target, present, err := extractOne(e, ctx)
		res.Target = target
		switch {
		case err != nil:
			res.Message = ctx.redact(err.Error())
			if e.IsRequired() {
				requiredMissing = true
				if out.failure == nil {
					out.failure = newFailure(FailureAssertion, KindExtraction, res.Message)
				}
			}
		case !present:
			res.Message = "value not found"
			if e.IsRequired() {
				requiredMissing = true
				if out.failure == nil {
					out.failure = newFailure(FailureAssertion, KindExtraction,
						fmt.Sprintf("required output %q was not found", e.Name))
				}
			}
		default:
			res.Published = true
			if res.Sensitive {
				res.Value = "***"
				if ctx.red != nil {
					ctx.red.RegisterSecret(value)
				}
				if ctx.onSensitive != nil {
					ctx.onSensitive(value)
				}
			} else {
				res.Value = ctx.redact(value)
			}
			out.outputs[e.Name] = core.StepOutput{Value: value, Sensitive: res.Sensitive}
		}
		out.results = append(out.results, res)
	}
	if requiredMissing {
		out.outputs = map[string]core.StepOutput{}
		for i := range out.results {
			out.results[i].Published = false
			out.results[i].Message = "not published: another required output failed"
		}
	}
	return out
}

func extractOne(e Extraction, ctx *evalContext) (value, target string, present bool, err error) {
	switch e.From {
	case "jsonpath":
		doc, jerr := ctx.jsonDoc()
		if jerr != nil {
			return "", e.Path, false, jerr
		}
		vals := func() []any {
			q, _ := parseJSONPath(e.Path)
			return q.Eval(doc)
		}()
		if len(vals) == 0 {
			return "", e.Path, false, nil
		}
		if len(vals) > 1 {
			return "", e.Path, false, fmt.Errorf("jsonpath %q matched %d values; extraction requires exactly one", e.Path, len(vals))
		}
		s, ok := extractionString(vals[0])
		if !ok {
			return "", e.Path, false, nil
		}
		return s, e.Path, true, nil
	case "header":
		joined, ok := lookupHeader(ctx.result.Headers, e.Header)
		if !ok {
			return "", e.Header, false, nil
		}
		return joined, e.Header, true, nil
	case "cookie":
		for _, c := range ctx.result.Cookies {
			if strings.EqualFold(c.Name, e.Cookie) {
				return c.Value, e.Cookie, true, nil
			}
		}
		return "", e.Cookie, false, nil
	case "body":
		re, rerr := regexp.Compile(e.Regex)
		if rerr != nil {
			return "", e.Regex, false, rerr
		}
		m := re.FindStringSubmatch(string(ctx.result.Body))
		if m == nil {
			return "", e.Regex, false, nil
		}
		group := 0
		if e.Group != nil {
			group = *e.Group
		}
		if group >= len(m) {
			return "", e.Regex, false, fmt.Errorf("regex group %d does not exist", group)
		}
		return m[group], e.Regex, true, nil
	}
	return "", "", false, fmt.Errorf("unsupported extraction source %q", e.From)
}

func extractionString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	case float64:
		return formatFloat(t), true
	case map[string]any, []any:
		b, err := json.Marshal(t)
		if err != nil {
			return "", false
		}
		return string(b), true
	default:
		return "", false
	}
}

func markNotPublished(results []ExtractionResult) []ExtractionResult {
	out := append([]ExtractionResult(nil), results...)
	for i := range out {
		if out[i].Published {
			out[i].Published = false
			out[i].Value = ""
			out[i].Message = "not published: attempt failed"
		}
	}
	return out
}
