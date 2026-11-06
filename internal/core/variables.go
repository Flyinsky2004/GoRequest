package core

import (
	"fmt"
	"regexp"
	"strings"
)

// SecretProvider supplies secret values by name. It is the boundary that keeps
// secret material out of project files: the project only ever holds secret
// *names* and {{secret.name}} references.
type SecretProvider interface {
	Get(name string) (string, bool)
}

// SecretState describes whether a named secret is bound, missing, or backed by
// an unavailable credential store, without ever exposing secret values.
type SecretState string

const (
	SecretStateBound       SecretState = "bound"
	SecretStateMissing     SecretState = "missing"
	SecretStateUnavailable SecretState = "unavailable"
)

// SecretStatus reports non-sensitive binding diagnostics for a named secret.
type SecretStatus struct {
	Name    string
	State   SecretState
	Backend string
	Detail  string
}

// SecretInspector is an optional interface implemented by SecretProviders that
// can distinguish a missing secret from an unavailable or locked OS credential
// backend without exposing plaintext values.
type SecretInspector interface {
	InspectSecret(name string) SecretStatus
}

// InspectSecretFrom queries sp for non-sensitive secret status, falling back to
// a boolean Get probe when sp does not implement SecretInspector.
func InspectSecretFrom(sp SecretProvider, name string) SecretStatus {
	trimmed := strings.TrimSpace(name)
	if sp == nil {
		return SecretStatus{
			Name:    trimmed,
			State:   SecretStateUnavailable,
			Backend: "none",
			Detail:  "no secret provider configured",
		}
	}
	if insp, ok := sp.(SecretInspector); ok {
		st := insp.InspectSecret(trimmed)
		if st.Name == "" {
			st.Name = trimmed
		}
		return st
	}
	if _, ok := sp.Get(trimmed); ok {
		return SecretStatus{Name: trimmed, State: SecretStateBound, Backend: "provider"}
	}
	return SecretStatus{Name: trimmed, State: SecretStateMissing, Backend: "provider"}
}

// MapSecrets is an in-memory SecretProvider, handy for tests and explicit
// secret bindings.
type MapSecrets map[string]string

// Get returns the named secret if present.
func (m MapSecrets) Get(name string) (string, bool) {
	v, ok := m[name]
	return v, ok
}

// InspectSecret reports whether name is bound in memory without returning its value.
func (m MapSecrets) InspectSecret(name string) SecretStatus {
	if _, ok := m[name]; ok {
		return SecretStatus{Name: name, State: SecretStateBound, Backend: "memory"}
	}
	return SecretStatus{Name: name, State: SecretStateMissing, Backend: "memory"}
}

// StepOutput holds a runtime value extracted from a prior suite step.
// Sensitive marks values derived from credentials or sensitive fields so taint
// propagates through {{steps.<stepId>.<output>}}.
type StepOutput struct {
	Value     string
	Sensitive bool
}

// TrackedValue pairs a resolved string with sensitivity taint metadata.
type TrackedValue struct {
	Value     string
	Sensitive bool
	Secrets   []string
}

var templatePattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Resolver resolves {{name}}, {{secret.name}}, {{data.field}} and
// {{steps.stepId.output}} templates against merged variables, runtime contexts
// and a SecretProvider, recursively, with cycle detection and taint tracking.
type Resolver struct {
	vars    map[string]string
	secrets SecretProvider
	data    map[string]string
	steps   map[string]StepOutput
	stack   []string
}

// NewResolver builds a Resolver over the merged variable map.
func NewResolver(vars map[string]string, secrets SecretProvider) *Resolver {
	if vars == nil {
		vars = map[string]string{}
	}
	return &Resolver{vars: vars, secrets: secrets}
}

// WithDataVars attaches dataset row variables for {{data.<field>}} resolution.
func (r *Resolver) WithDataVars(data map[string]string) *Resolver {
	r.data = data
	return r
}

// WithStepOutputs attaches extracted step outputs for {{steps.<stepId>.<out>}}.
func (r *Resolver) WithStepOutputs(steps map[string]StepOutput) *Resolver {
	r.steps = steps
	return r
}

// Vars exposes the merged variables (read-only use).
func (r *Resolver) Vars() map[string]string { return r.vars }

// Resolve replaces every template in s. An undefined variable or missing secret
// is an error: templates never silently become empty.
func (r *Resolver) Resolve(s string) (string, error) {
	tv, err := r.ResolveTracked(s)
	if err != nil {
		return "", err
	}
	return tv.Value, nil
}

// ResolveTracked resolves every template in s while tracking whether any
// {{secret.*}} or sensitive runtime step output tainted the resulting string.
func (r *Resolver) ResolveTracked(s string) (TrackedValue, error) {
	var firstErr error
	var sensitive bool
	var secretNames []string
	seenSecrets := map[string]bool{}

	out := templatePattern.ReplaceAllStringFunc(s, func(m string) string {
		if firstErr != nil {
			return m
		}
		name := strings.TrimSpace(templatePattern.FindStringSubmatch(m)[1])
		tv, err := r.resolveNameTracked(name)
		if err != nil {
			firstErr = err
			return m
		}
		if tv.Sensitive {
			sensitive = true
		}
		for _, sec := range tv.Secrets {
			if !seenSecrets[sec] {
				seenSecrets[sec] = true
				secretNames = append(secretNames, sec)
			}
		}
		return tv.Value
	})
	if firstErr != nil {
		return TrackedValue{}, firstErr
	}
	return TrackedValue{
		Value:     out,
		Sensitive: sensitive,
		Secrets:   secretNames,
	}, nil
}

func (r *Resolver) resolveNameTracked(name string) (TrackedValue, error) {
	for _, s := range r.stack {
		if s == name {
			return TrackedValue{}, configErrorf("variable cycle: %s -> %s", strings.Join(r.stack, " -> "), name)
		}
	}

	// Reserved namespace 1: {{secret.<name>}} — never falls back to plain vars.
	if rest, ok := strings.CutPrefix(name, "secret."); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return TrackedValue{}, configErrorf("empty secret name")
		}
		if r.secrets == nil {
			return TrackedValue{}, configErrorf("secret %q required but no secret provider is configured", rest)
		}
		v, ok := r.secrets.Get(rest)
		if !ok {
			if insp, hasInsp := r.secrets.(SecretInspector); hasInsp {
				st := insp.InspectSecret(rest)
				if st.State == SecretStateUnavailable {
					if st.Detail != "" {
						return TrackedValue{}, configErrorf("secret %q unavailable (%s): %s", rest, st.Backend, st.Detail)
					}
					return TrackedValue{}, configErrorf("secret %q unavailable in %s secret backend", rest, st.Backend)
				}
			}
			return TrackedValue{}, configErrorf("missing secret %q; bind it in the local secret store or CI", rest)
		}
		return TrackedValue{Value: v, Sensitive: true, Secrets: []string{rest}}, nil
	}

	// Reserved namespace 2: {{data.<field>}} — dataset iteration row only.
	if rest, ok := strings.CutPrefix(name, "data."); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return TrackedValue{}, configErrorf("empty data variable name")
		}
		if r.data == nil {
			return TrackedValue{}, configErrorf("data variable %q requires a dataset run context", rest)
		}
		v, ok := r.data[rest]
		if !ok {
			return TrackedValue{}, configErrorf("undefined data variable %q", rest)
		}
		return TrackedValue{Value: v}, nil
	}

	// Reserved namespace 3: {{steps.<stepId>.<output>}} — prior step extraction only.
	// Extracted network values are treated as literal strings so untrusted HTTP
	// responses cannot inject {{secret.*}} templates.
	if rest, ok := strings.CutPrefix(name, "steps."); ok {
		rest = strings.TrimSpace(rest)
		stepID, outKey, hasDot := strings.Cut(rest, ".")
		if !hasDot || strings.TrimSpace(stepID) == "" || strings.TrimSpace(outKey) == "" {
			return TrackedValue{}, configErrorf("invalid step output reference %q; expected steps.<stepId>.<output>", name)
		}
		if r.steps == nil {
			return TrackedValue{}, configErrorf("step output %q requires a multi-step suite run context", name)
		}
		out, ok := r.steps[rest]
		if !ok {
			return TrackedValue{}, configErrorf("undefined step output %q", name)
		}
		return TrackedValue{Value: out.Value, Sensitive: out.Sensitive}, nil
	}

	// Ordinary variables (including non-reserved dotted names such as user.id).
	raw, ok := r.vars[name]
	if !ok {
		return TrackedValue{}, configErrorf("undefined variable %q", name)
	}
	r.stack = append(r.stack, name)
	tv, err := r.ResolveTracked(raw)
	r.stack = r.stack[:len(r.stack)-1]
	if err != nil {
		return TrackedValue{}, err
	}
	return tv, nil
}

// TemplateNames returns the distinct {{name}} / {{secret.name}} names used in
// s, in first-seen order. Names are trimmed of surrounding spaces.
func TemplateNames(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range templatePattern.FindAllStringSubmatch(s, -1) {
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// UndefinedTemplates returns the distinct template names in s that cannot be
// resolved from vars or secrets, in first-seen order. It is the cheap, non-
// recursive check the UI uses to warn before sending and to color unresolved
// {{variables}}; the real build resolves recursively and errors on the first
// miss.
func UndefinedTemplates(s string, vars map[string]string, secrets SecretProvider) []string {
	return UndefinedTemplatesWithContext(s, vars, secrets, nil)
}

// UndefinedTemplatesWithContext extends UndefinedTemplates with optional
// runtime dataset and step-output namespaces.
func UndefinedTemplatesWithContext(s string, vars map[string]string, secrets SecretProvider, opts *ResolveOptions) []string {
	var out []string
	for _, name := range TemplateNames(s) {
		if rest, ok := strings.CutPrefix(name, "secret."); ok {
			rest = strings.TrimSpace(rest)
			if rest == "" || secrets == nil {
				out = append(out, name)
			} else if _, ok := secrets.Get(rest); !ok {
				out = append(out, name)
			}
			continue
		}
		if rest, ok := strings.CutPrefix(name, "data."); ok {
			rest = strings.TrimSpace(rest)
			if rest == "" || opts == nil || opts.DataVars == nil {
				out = append(out, name)
			} else if _, ok := opts.DataVars[rest]; !ok {
				out = append(out, name)
			}
			continue
		}
		if rest, ok := strings.CutPrefix(name, "steps."); ok {
			rest = strings.TrimSpace(rest)
			stepID, outKey, hasDot := strings.Cut(rest, ".")
			if !hasDot || strings.TrimSpace(stepID) == "" || strings.TrimSpace(outKey) == "" || opts == nil || opts.StepOutputs == nil {
				out = append(out, name)
			} else if _, ok := opts.StepOutputs[rest]; !ok {
				out = append(out, name)
			}
			continue
		}
		if _, ok := vars[name]; !ok {
			out = append(out, name)
		}
	}
	return out
}

// MergeVars layers maps from lowest to highest precedence and returns a new
// map. Later maps override earlier ones.
func MergeVars(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, layer := range layers {
		for k, v := range layer {
			out[k] = v
		}
	}
	return out
}

// StringVariables turns a map into sorted "k=v" lines for display.
func StringVariables(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%s", k, m[k]))
	}
	return out
}
