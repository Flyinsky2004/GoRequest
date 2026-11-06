package core

import (
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Variable layer names from lowest to highest precedence.
const (
	LayerProject     = "project"
	LayerEnvironment = "environment"
	LayerFolder      = "folder"
	LayerRequest     = "request"
	LayerRun         = "run"
	LayerStep        = "step"
	LayerOverride    = "override"
)

// ResolveOptions carries optional runtime variable layers and reserved runtime
// namespaces on top of static project/environment/folder/request variables.
type ResolveOptions struct {
	RunVars      map[string]string
	StepVars     map[string]string
	OverrideVars map[string]string
	DataVars     map[string]string
	StepOutputs  map[string]StepOutput
}

// VariableSource records one layer where a variable was defined.
type VariableSource struct {
	Layer string // project | environment | folder | request | run | step | override
	ID    string // environment/folder/request identifier when available
	Path  string // project-relative source file path when backed by disk
	Raw   string // raw unexpanded template/value declared at this layer
}

// Label returns a human-readable origin identifier such as "environment:dev"
// or "folder:collections/users".
func (s VariableSource) Label() string {
	switch s.Layer {
	case LayerEnvironment:
		if s.ID != "" {
			return "environment:" + s.ID
		}
	case LayerFolder:
		if s.Path != "" {
			return "folder:" + strings.TrimSuffix(s.Path, "/collection.yaml")
		}
		if s.ID != "" {
			return "folder:" + s.ID
		}
	case LayerRequest:
		if s.ID != "" {
			return "request:" + s.ID
		}
	}
	return s.Layer
}

// VariableEntry describes the winning definition, override history, and safe
// preview value of a single variable. When Sensitive is true, ResolvedValue is
// cleared and DisplayValue is "***" so secret plaintext never leaves Core.
type VariableEntry struct {
	Name          string
	RawValue      string
	ResolvedValue string
	DisplayValue  string
	Sensitive     bool
	Secrets       []string
	Effective     VariableSource
	Overridden    []VariableSource // lower-precedence definitions ordered lowest -> highest
	Error         string
}

// VariableInspection summarizes all visible variables and referenced/declared
// secrets for a request under a chosen environment and optional runtime context.
type VariableInspection struct {
	Entries []VariableEntry
	ByName  map[string]VariableEntry
	Secrets []SecretStatus
}

// IsReservedNamespace reports whether name uses one of the reserved prefixes
// (secret., data., steps.) that ordinary variables may never shadow.
func IsReservedNamespace(name string) bool {
	trimmed := strings.TrimSpace(name)
	return strings.HasPrefix(trimmed, "secret.") ||
		strings.HasPrefix(trimmed, "data.") ||
		strings.HasPrefix(trimmed, "steps.")
}

// ResolveVarsWith merges all seven ordinary variable layers in strict order:
// project -> environment -> ancestor folders (outermost to innermost) ->
// request -> run -> step -> override. Reserved prefixes in ordinary maps are
// ignored so they can never spoof secret., data., or steps.
func (p *Project) ResolveVarsWith(entry *RequestEntry, envID string, opts *ResolveOptions) (map[string]string, *Environment, error) {
	layers, env, err := p.collectVariableLayers(entry, envID, opts)
	if err != nil {
		return nil, nil, err
	}
	merged := map[string]string{}
	for _, l := range layers {
		for k, v := range l.vars {
			if IsReservedNamespace(k) {
				continue
			}
			merged[k] = v
		}
	}
	return merged, env, nil
}

type namedVarLayer struct {
	source VariableSource
	vars   map[string]string
}

func (p *Project) collectVariableLayers(entry *RequestEntry, envID string, opts *ResolveOptions) ([]namedVarLayer, *Environment, error) {
	var layers []namedVarLayer
	var env *Environment

	if p != nil && p.File != nil && len(p.File.Variables) > 0 {
		layers = append(layers, namedVarLayer{
			source: VariableSource{Layer: LayerProject, ID: p.File.ID, Path: "project.yaml"},
			vars:   p.File.Variables,
		})
	}

	if envID != "" {
		if p == nil {
			return nil, nil, configErrorf("environment %q not found", envID)
		}
		e, ok := p.Environments[envID]
		if !ok {
			return nil, nil, configErrorf("environment %q not found", envID)
		}
		env = e
		var envPath string
		if p.EnvEntries != nil && p.EnvEntries[envID] != nil {
			envPath = p.EnvEntries[envID].RelPath
		}
		if len(env.Variables) > 0 {
			layers = append(layers, namedVarLayer{
				source: VariableSource{Layer: LayerEnvironment, ID: env.ID, Path: envPath},
				vars:   env.Variables,
			})
		}
	}

	if entry != nil {
		seenFolderKeys := map[string]bool{}
		for _, dir := range ancestorCollectionDirs(entry.RelPath) {
			if p != nil && p.colByDir != nil {
				if col, ok := p.colByDir[dir]; ok && len(col.Variables) > 0 {
					for k := range col.Variables {
						seenFolderKeys[k] = true
					}
					layers = append(layers, namedVarLayer{
						source: VariableSource{
							Layer: LayerFolder,
							ID:    col.ID,
							Path:  dir + "/collection.yaml",
						},
						vars: col.Variables,
					})
				}
			}
		}
		// Fallback for synthetic in-memory RequestEntry fixtures that populate
		// CollectionVars directly without colByDir.
		if len(entry.CollectionVars) > 0 {
			extra := map[string]string{}
			for k, v := range entry.CollectionVars {
				if !seenFolderKeys[k] {
					extra[k] = v
				}
			}
			if len(extra) > 0 {
				dir := ""
				if entry.RelPath != "" {
					dir = filepath.ToSlash(filepath.Dir(entry.RelPath))
				}
				layers = append(layers, namedVarLayer{
					source: VariableSource{Layer: LayerFolder, Path: dir},
					vars:   extra,
				})
			}
		}

		if entry.Request != nil && len(entry.Request.Variables) > 0 {
			layers = append(layers, namedVarLayer{
				source: VariableSource{
					Layer: LayerRequest,
					ID:    entry.Request.ID,
					Path:  entry.RelPath,
				},
				vars: entry.Request.Variables,
			})
		}
	}

	if opts != nil {
		if len(opts.RunVars) > 0 {
			layers = append(layers, namedVarLayer{
				source: VariableSource{Layer: LayerRun},
				vars:   opts.RunVars,
			})
		}
		if len(opts.StepVars) > 0 {
			layers = append(layers, namedVarLayer{
				source: VariableSource{Layer: LayerStep},
				vars:   opts.StepVars,
			})
		}
		if len(opts.OverrideVars) > 0 {
			layers = append(layers, namedVarLayer{
				source: VariableSource{Layer: LayerOverride},
				vars:   opts.OverrideVars,
			})
		}
	}

	return layers, env, nil
}

// ancestorCollectionDirs returns the collection directories enclosing rel from
// outermost (e.g. "collections/api") to innermost ("collections/api/users").
func ancestorCollectionDirs(rel string) []string {
	if strings.TrimSpace(rel) == "" {
		return nil
	}
	cur := filepath.ToSlash(filepath.Dir(rel))
	var rev []string
	for cur != "" && cur != "." && cur != "collections" {
		if !strings.HasPrefix(cur, "collections/") {
			break
		}
		rev = append(rev, cur)
		parent := filepath.ToSlash(filepath.Dir(cur))
		if parent == cur {
			break
		}
		cur = parent
	}
	out := make([]string, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

// InspectVariables computes the effective variable table, per-variable winning
// origin, overridden lower-layer history, sensitivity masking, and secret
// binding statuses without ever exposing secret values.
func (p *Project) InspectVariables(entry *RequestEntry, envID string, secrets SecretProvider, opts *ResolveOptions) (*VariableInspection, error) {
	layers, env, err := p.collectVariableLayers(entry, envID, opts)
	if err != nil {
		return nil, err
	}

	history := map[string][]VariableSource{}
	merged := map[string]string{}
	secretSet := map[string]bool{}

	for _, l := range layers {
		for k, v := range l.vars {
			if IsReservedNamespace(k) {
				continue
			}
			src := l.source
			src.Raw = v
			history[k] = append(history[k], src)
			merged[k] = v
			collectSecretRefs(v, secretSet)
		}
	}

	red := NewRedactor()
	ts := &trackingSecrets{inner: secrets, red: red}
	r := NewResolver(merged, ts)
	if opts != nil {
		r.WithDataVars(opts.DataVars)
		r.WithStepOutputs(opts.StepOutputs)
		for _, so := range opts.StepOutputs {
			if so.Sensitive && so.Value != "" {
				red.RegisterSecret(so.Value)
			}
		}
	}

	names := make([]string, 0, len(history))
	for k := range history {
		names = append(names, k)
	}
	sortStrings(names)

	entries := make([]VariableEntry, 0, len(names))
	byName := make(map[string]VariableEntry, len(names))

	for _, name := range names {
		chain := history[name]
		eff := chain[len(chain)-1]
		var overridden []VariableSource
		if len(chain) > 1 {
			overridden = append([]VariableSource(nil), chain[:len(chain)-1]...)
		}
		ve := VariableEntry{
			Name:       name,
			RawValue:   eff.Raw,
			Effective:  eff,
			Overridden: overridden,
		}
		tv, resolveErr := r.ResolveTracked(eff.Raw)
		if resolveErr != nil {
			ve.Error = red.RedactString(resolveErr.Error())
			ve.DisplayValue = eff.Raw
		} else if tv.Sensitive {
			ve.Sensitive = true
			ve.Secrets = tv.Secrets
			ve.ResolvedValue = ""
			ve.DisplayValue = "***"
			for _, sec := range tv.Secrets {
				secretSet[sec] = true
			}
		} else {
			ve.ResolvedValue = tv.Value
			ve.DisplayValue = tv.Value
		}
		entries = append(entries, ve)
		byName[name] = ve
	}

	if env != nil {
		for _, s := range env.Secrets {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				secretSet[trimmed] = true
			}
		}
	}
	if entry != nil && entry.Request != nil {
		collectRequestSecretRefs(p, entry, secretSet)
	}

	secNames := make([]string, 0, len(secretSet))
	for s := range secretSet {
		secNames = append(secNames, s)
	}
	sortStrings(secNames)

	statuses := make([]SecretStatus, 0, len(secNames))
	for _, s := range secNames {
		statuses = append(statuses, InspectSecretFrom(secrets, s))
	}

	return &VariableInspection{
		Entries: entries,
		ByName:  byName,
		Secrets: statuses,
	}, nil
}

func collectSecretRefs(s string, into map[string]bool) {
	for _, name := range TemplateNames(s) {
		if rest, ok := strings.CutPrefix(name, "secret."); ok {
			if rest = strings.TrimSpace(rest); rest != "" {
				into[rest] = true
			}
		}
	}
}

func collectNodeSecretRefs(n *yaml.Node, into map[string]bool) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode && (n.Tag == "" || n.Tag == "!!str") {
		collectSecretRefs(n.Value, into)
	}
	for _, child := range n.Content {
		collectNodeSecretRefs(child, into)
	}
}

func collectRequestSecretRefs(p *Project, entry *RequestEntry, into map[string]bool) {
	req := entry.Request
	if req == nil {
		return
	}
	if req.HTTP != nil {
		collectSecretRefs(req.HTTP.URL, into)
		for _, kv := range req.HTTP.Query {
			if kv.IsEnabled() {
				collectSecretRefs(kv.Name, into)
				collectSecretRefs(kv.Value, into)
			}
		}
		for _, kv := range req.HTTP.Headers {
			if kv.IsEnabled() {
				collectSecretRefs(kv.Name, into)
				collectSecretRefs(kv.Value, into)
			}
		}
		if req.HTTP.Body != nil {
			collectNodeSecretRefs(req.HTTP.Body.Value, into)
		}
	}
	if p != nil {
		if eff, err := p.ResolveEffectiveAuth(entry, req); err == nil && eff != nil && eff.Scheme != nil {
			collectSchemeSecretRefs(eff.Scheme, into)
		}
	} else if req.Auth != nil && req.Auth.Mode == "inline" && req.Auth.Inline != nil {
		collectSchemeSecretRefs(req.Auth.Inline, into)
	}
}

func collectSchemeSecretRefs(s *AuthScheme, into map[string]bool) {
	if s == nil {
		return
	}
	collectSecretRefs(s.Token, into)
	collectSecretRefs(s.Username, into)
	collectSecretRefs(s.Password, into)
	collectSecretRefs(s.Name, into)
	collectSecretRefs(s.Value, into)
}
