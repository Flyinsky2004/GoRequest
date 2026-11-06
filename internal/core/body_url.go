package core

import (
	"fmt"
	"net/url"
	"strings"
)

// VarPreviewStatus describes the resolution state of one {{...}} reference.
type VarPreviewStatus string

const (
	VarStatusDefined       VarPreviewStatus = "defined"
	VarStatusUndefined     VarPreviewStatus = "undefined"
	VarStatusCycle         VarPreviewStatus = "cycle"
	VarStatusSecretBound   VarPreviewStatus = "secret-bound"
	VarStatusSecretMissing VarPreviewStatus = "secret-missing"
)

// VarPreviewItem is the safe, secret-redacted resolution summary of one
// template variable for UI preview. Secret values are never placed in Display.
type VarPreviewItem struct {
	Name    string
	Status  VarPreviewStatus
	Display string
}

// URLPreview summarizes variable resolution and final context-encoded URL
// construction for a request before sending. Secrets are always masked.
type URLPreview struct {
	Variables []VarPreviewItem
	// ResolvedURL is the safe display preview of the final URL including encoded
	// query parameters, with any secret values masked as "(secret)".
	ResolvedURL string
	// EncodedQuery is the encoded query string portion (without leading "?"),
	// preserving duplicate keys and empty values without double-encoding.
	EncodedQuery string
	Issues       []string
}

type maskingSecretProvider struct {
	inner SecretProvider
}

func (m maskingSecretProvider) Get(name string) (string, bool) {
	if m.inner == nil {
		return "", false
	}
	if _, ok := m.inner.Get(name); !ok {
		return "", false
	}
	return "(secret)", true
}

// PreviewTemplateVars inspects all {{name}} / {{secret.name}} templates across
// inputs in first-seen order, resolving recursive plain variables, detecting
// cycles, and masking secret values as "(secret)".
func PreviewTemplateVars(vars map[string]string, secrets SecretProvider, inputs ...string) []VarPreviewItem {
	seen := map[string]bool{}
	var names []string
	for _, in := range inputs {
		for _, n := range TemplateNames(in) {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	masked := maskingSecretProvider{inner: secrets}
	out := make([]VarPreviewItem, 0, len(names))
	for _, name := range names {
		if rest, ok := strings.CutPrefix(name, "secret."); ok {
			if rest != "" && secrets != nil {
				if _, ok := secrets.Get(rest); ok {
					out = append(out, VarPreviewItem{
						Name:    name,
						Status:  VarStatusSecretBound,
						Display: "(secret)",
					})
					continue
				}
			}
			out = append(out, VarPreviewItem{
				Name:    name,
				Status:  VarStatusSecretMissing,
				Display: "?",
			})
			continue
		}

		r := NewResolver(vars, masked)
		val, err := r.Resolve("{{" + name + "}}")
		if err != nil {
			status := VarStatusUndefined
			if strings.Contains(err.Error(), "variable cycle") {
				status = VarStatusCycle
			}
			out = append(out, VarPreviewItem{
				Name:    name,
				Status:  status,
				Display: "?",
			})
			continue
		}
		out = append(out, VarPreviewItem{
			Name:    name,
			Status:  VarStatusDefined,
			Display: val,
		})
	}
	return out
}

// PreviewRequestURL builds a secret-safe preview of URL + Query resolution and
// context encoding without mutating any state or touching the network.
func PreviewRequestURL(rawURL string, query []KV, vars map[string]string, secrets SecretProvider) URLPreview {
	inputs := make([]string, 0, 1+2*len(query))
	inputs = append(inputs, rawURL)
	for _, kv := range query {
		if kv.IsEnabled() {
			inputs = append(inputs, kv.Name, kv.Value)
		}
	}

	preview := URLPreview{
		Variables: PreviewTemplateVars(vars, secrets, inputs...),
	}

	masked := maskingSecretProvider{inner: secrets}
	safeResolve := func(s string) string {
		return templatePattern.ReplaceAllStringFunc(s, func(m string) string {
			name := strings.TrimSpace(templatePattern.FindStringSubmatch(m)[1])
			r := NewResolver(vars, masked)
			val, err := r.Resolve("{{" + name + "}}")
			if err != nil {
				return m
			}
			return val
		})
	}

	for _, v := range preview.Variables {
		switch v.Status {
		case VarStatusUndefined:
			preview.Issues = append(preview.Issues, fmt.Sprintf("undefined variable {{%s}}", v.Name))
		case VarStatusCycle:
			preview.Issues = append(preview.Issues, fmt.Sprintf("variable cycle involving {{%s}}", v.Name))
		case VarStatusSecretMissing:
			preview.Issues = append(preview.Issues, fmt.Sprintf("unbound secret {{%s}}", v.Name))
		}
	}

	resolvedBase := strings.TrimSpace(safeResolve(rawURL))
	if resolvedBase == "" {
		return preview
	}

	// Build ordered encoded query segments without double-encoding pre-existing
	// query parameters in rawURL.
	basePart := resolvedBase
	existingRawQuery := ""
	fragPart := ""
	if hashIdx := strings.IndexByte(basePart, '#'); hashIdx >= 0 {
		fragPart = basePart[hashIdx:]
		basePart = basePart[:hashIdx]
	}
	if qIdx := strings.IndexByte(basePart, '?'); qIdx >= 0 {
		existingRawQuery = basePart[qIdx+1:]
		basePart = basePart[:qIdx]
	}

	var encodedParts []string
	if existingRawQuery != "" {
		encodedParts = append(encodedParts, existingRawQuery)
	}
	for _, kv := range query {
		if !kv.IsEnabled() {
			continue
		}
		if strings.TrimSpace(kv.Name) == "" && strings.TrimSpace(kv.Value) == "" {
			continue
		}
		k := safeResolve(kv.Name)
		v := safeResolve(kv.Value)
		encodedParts = append(encodedParts, queryEscapePreservingTemplates(k)+"="+queryEscapePreservingTemplates(v))
	}

	preview.EncodedQuery = strings.Join(encodedParts, "&")
	finalURL := basePart
	if preview.EncodedQuery != "" {
		finalURL += "?" + preview.EncodedQuery
	}
	finalURL += fragPart
	preview.ResolvedURL = finalURL

	if !strings.Contains(basePart, "{{") {
		if u, err := url.Parse(basePart); err != nil || u.Scheme == "" || u.Host == "" {
			preview.Issues = append(preview.Issues, "URL must include scheme and host")
		}
	}
	return preview
}

// PreviewURLEncodedBody produces a secret-masked preview of an
// application/x-www-form-urlencoded body in exact field order.
func PreviewURLEncodedBody(fields []FormField, vars map[string]string, secrets SecretProvider) string {
	masked := maskingSecretProvider{inner: secrets}
	var parts []string
	for _, f := range fields {
		if !f.IsEnabled() {
			continue
		}
		if strings.TrimSpace(f.Name) == "" && strings.TrimSpace(f.Value) == "" {
			continue
		}
		r := NewResolver(vars, masked)
		if f.Encoded {
			k, err1 := resolveTemplatesIntoPercentEncodedBestEffort(f.Name, r)
			v, err2 := resolveTemplatesIntoPercentEncodedBestEffort(f.Value, r)
			if err1 != nil {
				k = f.Name
			}
			if err2 != nil {
				v = f.Value
			}
			parts = append(parts, k+"="+v)
			continue
		}
		k := resolveBestEffort(f.Name, r)
		v := resolveBestEffort(f.Value, r)
		parts = append(parts, queryEscapePreservingTemplates(k)+"="+queryEscapePreservingTemplates(v))
	}
	return strings.Join(parts, "&")
}

func resolveBestEffort(s string, r *Resolver) string {
	return templatePattern.ReplaceAllStringFunc(s, func(m string) string {
		val, err := r.Resolve(m)
		if err != nil {
			return m
		}
		return val
	})
}

func resolveTemplatesIntoPercentEncodedBestEffort(s string, r *Resolver) (string, error) {
	if err := validatePercentEncodedWithTemplates(s); err != nil {
		return "", err
	}
	out := templatePattern.ReplaceAllStringFunc(s, func(m string) string {
		val, err := r.Resolve(m)
		if err != nil {
			return m
		}
		return url.QueryEscape(val)
	})
	return out, nil
}

func validatePercentEncodedWithTemplates(s string) error {
	idxs := templatePattern.FindAllStringIndex(s, -1)
	pos := 0
	for _, loc := range idxs {
		if err := validatePercentSegment(s[pos:loc[0]]); err != nil {
			return err
		}
		pos = loc[1]
	}
	return validatePercentSegment(s[pos:])
}

func validatePercentSegment(seg string) error {
	if strings.ContainsAny(seg, "\r\n ") {
		return fmt.Errorf("pre-encoded value %q contains unencoded whitespace", seg)
	}
	if strings.ContainsAny(seg, "&=") {
		return fmt.Errorf("pre-encoded value %q contains unescaped '&' or '='", seg)
	}
	for i := 0; i < len(seg); i++ {
		if seg[i] == '%' {
			if i+2 >= len(seg) || !isHexByte(seg[i+1]) || !isHexByte(seg[i+2]) {
				return fmt.Errorf("invalid percent-encoding in %q", seg)
			}
			i += 2
		}
	}
	return nil
}

func isHexByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func resolveTemplatesIntoPercentEncoded(s string, r *Resolver) (string, error) {
	if err := validatePercentEncodedWithTemplates(s); err != nil {
		return "", err
	}
	idxs := templatePattern.FindAllStringSubmatchIndex(s, -1)
	if len(idxs) == 0 {
		return s, nil
	}
	var b strings.Builder
	pos := 0
	for _, loc := range idxs {
		b.WriteString(s[pos:loc[0]])
		rawExpr := s[loc[0]:loc[1]]
		val, err := r.Resolve(rawExpr)
		if err != nil {
			return "", err
		}
		b.WriteString(url.QueryEscape(val))
		pos = loc[1]
	}
	b.WriteString(s[pos:])
	return b.String(), nil
}

// queryEscapePreservingTemplates percent-encodes literal spans once while
// leaving {{template}} placeholders intact and readable.
func queryEscapePreservingTemplates(s string) string {
	idxs := templatePattern.FindAllStringIndex(s, -1)
	if len(idxs) == 0 {
		return url.QueryEscape(s)
	}
	var b strings.Builder
	pos := 0
	for _, loc := range idxs {
		if loc[0] > pos {
			b.WriteString(url.QueryEscape(s[pos:loc[0]]))
		}
		b.WriteString(s[loc[0]:loc[1]])
		pos = loc[1]
	}
	if pos < len(s) {
		b.WriteString(url.QueryEscape(s[pos:]))
	}
	return b.String()
}

// queryUnescapePreservingTemplates decodes percent-encoding (and '+') once on
// literal spans while preserving {{template}} expressions verbatim.
func queryUnescapePreservingTemplates(s string) (string, error) {
	idxs := templatePattern.FindAllStringIndex(s, -1)
	if len(idxs) == 0 {
		return url.QueryUnescape(s)
	}
	var b strings.Builder
	pos := 0
	for _, loc := range idxs {
		if loc[0] > pos {
			dec, err := url.QueryUnescape(s[pos:loc[0]])
			if err != nil {
				return "", err
			}
			b.WriteString(dec)
		}
		b.WriteString(s[loc[0]:loc[1]])
		pos = loc[1]
	}
	if pos < len(s) {
		dec, err := url.QueryUnescape(s[pos:])
		if err != nil {
			return "", err
		}
		b.WriteString(dec)
	}
	return b.String(), nil
}

// FormatRawQueryKV formats ordered KVs into raw URL-encoded query syntax
// ("a=1&b=hello+world&a=2&empty="), keeping {{templates}} readable and placing
// disabled items on "# key=value" lines so nothing is lost on round-trip.
func FormatRawQueryKV(kvs []KV) string {
	var active []string
	var lines []string
	flushActive := func() {
		if len(active) > 0 {
			lines = append(lines, strings.Join(active, "&"))
			active = nil
		}
	}
	for _, kv := range kvs {
		enc := queryEscapePreservingTemplates(kv.Name) + "=" + queryEscapePreservingTemplates(kv.Value)
		if kv.IsEnabled() {
			active = append(active, enc)
		} else {
			flushActive()
			lines = append(lines, "# "+enc)
		}
	}
	flushActive()
	return strings.Join(lines, "\n")
}

// ParseRawQueryKV parses a raw URL query string (with optional leading "?" and
// optional "# " disabled lines) into ordered KVs, decoding percent-escapes once
// so subsequent request execution never double-encodes values.
func ParseRawQueryKV(raw string) ([]KV, error) {
	var out []KV
	lines := strings.Split(raw, "\n")
	for lineIdx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		enabled := true
		if strings.HasPrefix(trimmed, "#") {
			enabled = false
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			if trimmed == "" {
				continue
			}
		}
		if lineIdx == 0 && enabled && strings.HasPrefix(trimmed, "?") {
			trimmed = trimmed[1:]
		}
		if trimmed == "" {
			continue
		}
		segments := strings.Split(trimmed, "&")
		for _, seg := range segments {
			if seg == "" {
				continue
			}
			eq := strings.IndexByte(seg, '=')
			if eq < 0 {
				return nil, fmt.Errorf("line %d: query token %q missing '='", lineIdx+1, seg)
			}
			rawKey := seg[:eq]
			rawVal := seg[eq+1:]
			key, err := queryUnescapePreservingTemplates(rawKey)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid percent-encoding in key %q: %w", lineIdx+1, rawKey, err)
			}
			if strings.TrimSpace(key) == "" {
				return nil, fmt.Errorf("line %d: query parameter has an empty key", lineIdx+1)
			}
			val, err := queryUnescapePreservingTemplates(rawVal)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid percent-encoding in value %q: %w", lineIdx+1, rawVal, err)
			}
			e := enabled
			out = append(out, KV{Name: key, Value: val, Enabled: &e})
		}
	}
	return out, nil
}

// FormatRawURLEncodedFields formats FormFields into raw x-www-form-urlencoded
// text. Pre-encoded fields (Encoded == true) are emitted verbatim without
// escaping "%" a second time.
func FormatRawURLEncodedFields(fields []FormField) string {
	var active []string
	var lines []string
	flushActive := func() {
		if len(active) > 0 {
			lines = append(lines, strings.Join(active, "&"))
			active = nil
		}
	}
	for _, f := range fields {
		var enc string
		if f.Encoded {
			enc = f.Name + "=" + f.Value
		} else {
			enc = queryEscapePreservingTemplates(f.Name) + "=" + queryEscapePreservingTemplates(f.Value)
		}
		if f.IsEnabled() {
			active = append(active, enc)
		} else {
			flushActive()
			lines = append(lines, "# "+enc)
		}
	}
	flushActive()
	return strings.Join(lines, "\n")
}

// ParseRawURLEncodedFields parses raw x-www-form-urlencoded text ("a=1&b=2",
// with optional "# " disabled lines) into FormFields, decoding percent-escapes
// once while preserving {{templates}}.
func ParseRawURLEncodedFields(raw string) ([]FormField, error) {
	kvs, err := ParseRawQueryKV(raw)
	if err != nil {
		return nil, err
	}
	out := make([]FormField, len(kvs))
	for i, kv := range kvs {
		enabled := kv.IsEnabled()
		out[i] = FormField{
			Name:    kv.Name,
			Value:   kv.Value,
			Enabled: &enabled,
		}
	}
	return out, nil
}

// FormatFormBatchLines serializes FormFields as one "key = value" per line
// (disabled prefixed with "# "), matching the KV batch-text convention.
func FormatFormBatchLines(fields []FormField) string {
	var lines []string
	for _, f := range fields {
		if strings.TrimSpace(f.Name) == "" && strings.TrimSpace(f.Value) == "" {
			continue
		}
		line := f.Name + " = " + f.Value
		if !f.IsEnabled() {
			line = "# " + line
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// ParseFormBatchLines parses "key = value" batch lines (with "# " disabled
// support) into ordered FormFields.
func ParseFormBatchLines(text string) ([]FormField, error) {
	var out []FormField
	for i, line := range strings.Split(text, "\n") {
		raw := strings.TrimSpace(line)
		if raw == "" {
			continue
		}
		enabled := true
		if strings.HasPrefix(raw, "#") {
			enabled = false
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "#"))
			if raw == "" {
				continue
			}
		}
		idx := strings.IndexByte(raw, '=')
		if idx < 0 {
			return nil, fmt.Errorf("line %d: missing separator \"=\"", i+1)
		}
		name := strings.TrimSpace(raw[:idx])
		val := strings.TrimSpace(raw[idx+1:])
		if name == "" {
			return nil, fmt.Errorf("line %d: missing key", i+1)
		}
		e := enabled
		out = append(out, FormField{Name: name, Value: val, Enabled: &e})
	}
	return out, nil
}
