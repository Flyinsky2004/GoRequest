package mockserver

import "gorequest/internal/core"

// rule_model.go adds deep-copy helpers for the C01 phase 2 rule editor.
// Editing must never mutate a file snapshot loaded for serving or display: the
// desktop editor drafts a clone, so unedited rules, unknown-to-the-UI advanced
// matcher combinations, and per-variant preserved response fields all survive
// a save round-trip byte-identically (they are copied, then re-serialized with
// the original structures the engine already validates).

// cloneBool returns a copied pointer (nil stays nil).
func cloneBool(v *bool) *bool {
	if v == nil {
		return nil
	}
	out := new(bool)
	*out = *v
	return out
}

// cloneString returns a copied pointer (nil stays nil).
func cloneString(v *string) *string {
	if v == nil {
		return nil
	}
	out := new(string)
	*out = *v
	return out
}

// cloneStringSlice copies the slice, preserving nil vs empty distinction so
// round-trips do not introduce spurious `[]` entries into shared mock files.
func cloneStringSlice(v []string) []string {
	if v == nil {
		return nil
	}
	out := make([]string, len(v))
	copy(out, v)
	return out
}

// cloneValueMatcher deep-copies a ValueMatcher.
func cloneValueMatcher(vm ValueMatcher) ValueMatcher {
	return ValueMatcher{
		Exists:    cloneBool(vm.Exists),
		Equals:    cloneString(vm.Equals),
		NotEquals: cloneString(vm.NotEquals),
		In:        cloneStringSlice(vm.In),
		Contains:  cloneString(vm.Contains),
		Prefix:    cloneString(vm.Prefix),
		Regex:     cloneString(vm.Regex),
	}
}

// cloneKeyValueMatcher deep-copies a KeyValueMatcher.
func cloneKeyValueMatcher(kv KeyValueMatcher) KeyValueMatcher {
	return KeyValueMatcher{
		Name:       kv.Name,
		Exists:     cloneBool(kv.Exists),
		Equals:     cloneString(kv.Equals),
		NotEquals:  cloneString(kv.NotEquals),
		In:         cloneStringSlice(kv.In),
		Contains:   cloneString(kv.Contains),
		Prefix:     cloneString(kv.Prefix),
		Regex:      cloneString(kv.Regex),
		IgnoreCase: kv.IgnoreCase,
	}
}

// cloneJSONFieldMatcher deep-copies a JSONFieldMatcher.
func cloneJSONFieldMatcher(jf JSONFieldMatcher) JSONFieldMatcher {
	return JSONFieldMatcher{
		Path:      jf.Path,
		Exists:    cloneBool(jf.Exists),
		Equals:    cloneString(jf.Equals),
		NotEquals: cloneString(jf.NotEquals),
		In:        cloneStringSlice(jf.In),
		Contains:  cloneString(jf.Contains),
		Prefix:    cloneString(jf.Prefix),
		Regex:     cloneString(jf.Regex),
		Type:      jf.Type,
	}
}

// cloneBodyMatcher deep-copies a BodyMatcher, including the JSON Equals value
// without attempting to reinterpret it (the engine keeps it opaque).
func cloneBodyMatcher(bm *BodyMatcher) *BodyMatcher {
	if bm == nil {
		return nil
	}
	out := &BodyMatcher{
		Equals:     cloneString(bm.Equals),
		Contains:   cloneString(bm.Contains),
		Prefix:     cloneString(bm.Prefix),
		Regex:      cloneString(bm.Regex),
		JSONEquals: bm.JSONEquals,
	}
	if len(bm.JSONFields) > 0 {
		out.JSONFields = make([]JSONFieldMatcher, len(bm.JSONFields))
		for i, f := range bm.JSONFields {
			out.JSONFields[i] = cloneJSONFieldMatcher(f)
		}
	}
	return out
}

// cloneRuleResponse deep-copies a RuleResponse.
func cloneRuleResponse(r RuleResponse) RuleResponse {
	out := RuleResponse{
		Status:      r.Status,
		ContentType: r.ContentType,
		Body:        r.Body,
		DelayMs:     r.DelayMs,
	}
	if r.DelayRangeMs != nil {
		out.DelayRangeMs = &DelayRange{Min: r.DelayRangeMs.Min, Max: r.DelayRangeMs.Max}
	}
	if len(r.Headers) > 0 {
		out.Headers = make([]core.KV, len(r.Headers))
		for i, kv := range r.Headers {
			out.Headers[i] = core.KV{Name: kv.Name, Value: kv.Value, Enabled: cloneBool(kv.Enabled)}
		}
	}
	return out
}

// CloneMockRule returns a fully independent copy of r.
func CloneMockRule(r MockRule) MockRule {
	out := MockRule{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		Enabled:     cloneBool(r.Enabled),
		Priority:    r.Priority,
		Scenarios:   cloneStringSlice(r.Scenarios),
		Response:    cloneRuleResponse(r.Response),
	}
	out.Match = RuleMatch{
		Method: r.Match.Method,
		Path:   r.Match.Path,
	}
	if len(r.Match.PathParams) > 0 {
		out.Match.PathParams = make(map[string]ValueMatcher, len(r.Match.PathParams))
		for k, vm := range r.Match.PathParams {
			out.Match.PathParams[k] = cloneValueMatcher(vm)
		}
	}
	if len(r.Match.Query) > 0 {
		out.Match.Query = make([]KeyValueMatcher, len(r.Match.Query))
		for i, kv := range r.Match.Query {
			out.Match.Query[i] = cloneKeyValueMatcher(kv)
		}
	}
	if len(r.Match.Headers) > 0 {
		out.Match.Headers = make([]KeyValueMatcher, len(r.Match.Headers))
		for i, kv := range r.Match.Headers {
			out.Match.Headers[i] = cloneKeyValueMatcher(kv)
		}
	}
	out.Match.Body = cloneBodyMatcher(r.Match.Body)
	if len(r.Variants) > 0 {
		out.Variants = make(map[string]RuleResponse, len(r.Variants))
		for k, v := range r.Variants {
			out.Variants[k] = cloneRuleResponse(v)
		}
	}
	return out
}

// CloneMockFile returns a fully independent copy of mf, preserving the source
// path and digest metadata so save-conflict checking keeps working after edits.
func CloneMockFile(mf *MockFile) *MockFile {
	if mf == nil {
		return nil
	}
	out := &MockFile{
		SchemaVersion:   mf.SchemaVersion,
		ID:              mf.ID,
		Name:            mf.Name,
		Description:     mf.Description,
		DefaultScenario: mf.DefaultScenario,
		SourceRelPath:   mf.SourceRelPath,
		SourceDigest:    mf.SourceDigest,
	}
	if len(mf.Variables) > 0 {
		out.Variables = make(map[string]string, len(mf.Variables))
		for k, v := range mf.Variables {
			out.Variables[k] = v
		}
	}
	if mf.CORS != nil {
		out.CORS = &CORSConfig{
			AllowOrigins:     cloneStringSlice(mf.CORS.AllowOrigins),
			AllowMethods:     cloneStringSlice(mf.CORS.AllowMethods),
			AllowHeaders:     cloneStringSlice(mf.CORS.AllowHeaders),
			ExposeHeaders:    cloneStringSlice(mf.CORS.ExposeHeaders),
			MaxAgeSeconds:    mf.CORS.MaxAgeSeconds,
			AllowCredentials: mf.CORS.AllowCredentials,
		}
	}
	if len(mf.Rules) > 0 {
		out.Rules = make([]MockRule, len(mf.Rules))
		for i, r := range mf.Rules {
			out.Rules[i] = CloneMockRule(r)
		}
	}
	return out
}
