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

// MapSecrets is an in-memory SecretProvider, handy for tests and explicit
// secret bindings.
type MapSecrets map[string]string

// Get returns the named secret if present.
func (m MapSecrets) Get(name string) (string, bool) {
	v, ok := m[name]
	return v, ok
}

var templatePattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Resolver resolves {{name}} and {{secret.name}} templates against a merged
// variable map and a SecretProvider, recursively, with cycle detection.
type Resolver struct {
	vars    map[string]string
	secrets SecretProvider
	stack   []string
}

// NewResolver builds a Resolver over the merged variable map.
func NewResolver(vars map[string]string, secrets SecretProvider) *Resolver {
	if vars == nil {
		vars = map[string]string{}
	}
	return &Resolver{vars: vars, secrets: secrets}
}

// Vars exposes the merged variables (read-only use).
func (r *Resolver) Vars() map[string]string { return r.vars }

// Resolve replaces every template in s. An undefined variable or missing secret
// is an error: templates never silently become empty.
func (r *Resolver) Resolve(s string) (string, error) {
	var firstErr error
	out := templatePattern.ReplaceAllStringFunc(s, func(m string) string {
		if firstErr != nil {
			return m
		}
		name := strings.TrimSpace(templatePattern.FindStringSubmatch(m)[1])
		val, err := r.resolveName(name)
		if err != nil {
			firstErr = err
			return m
		}
		return val
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

func (r *Resolver) resolveName(name string) (string, error) {
	for _, s := range r.stack {
		if s == name {
			return "", configErrorf("variable cycle: %s -> %s", strings.Join(r.stack, " -> "), name)
		}
	}
	if rest, ok := strings.CutPrefix(name, "secret."); ok {
		if rest == "" {
			return "", configErrorf("empty secret name")
		}
		if r.secrets == nil {
			return "", configErrorf("secret %q required but no secret provider is configured", rest)
		}
		v, ok := r.secrets.Get(rest)
		if !ok {
			return "", configErrorf("missing secret %q; bind it in the local secret store or CI", rest)
		}
		return v, nil
	}
	raw, ok := r.vars[name]
	if !ok {
		return "", configErrorf("undefined variable %q", name)
	}
	r.stack = append(r.stack, name)
	val, err := r.Resolve(raw)
	r.stack = r.stack[:len(r.stack)-1]
	if err != nil {
		return "", err
	}
	return val, nil
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
