package core

import (
	"path/filepath"
	"sort"
	"strings"
)

// EnvironmentEntry is a loaded environment together with the file it came from
// and the digest recorded when it was read. Remembering the real relative path
// (which may be .yml and may differ from the environment id) lets edits be
// written back to the same file with conflict detection instead of assuming a
// path from the id.
type EnvironmentEntry struct {
	Environment    *Environment
	RelPath        string
	BaselineDigest string
}

// EnvironmentsDir is the project-relative directory that holds environment
// files.
const EnvironmentsDir = "environments"

// ValidateEnvironmentID checks an id supplied for a new environment. The id is
// used both as the stable reference and as the file name, so it must be a plain
// name: non-empty, not the reserved "(none)" sentinel, and free of path
// separators, "..", absolute paths, control characters and characters that are
// not portable in file names.
func ValidateEnvironmentID(id string) error {
	t := strings.TrimSpace(id)
	if t == "" {
		return configErrorf("environment id is empty")
	}
	if strings.EqualFold(t, "(none)") {
		return configErrorf("environment id %q is reserved", t)
	}
	if filepath.IsAbs(t) {
		return configErrorf("environment id must not be an absolute path")
	}
	if strings.ContainsAny(t, `/\`) {
		return configErrorf("environment id must not contain path separators")
	}
	if t == "." || t == ".." || strings.Contains(t, "..") {
		return configErrorf("environment id must not contain %q", "..")
	}
	for _, r := range t {
		if r < 0x20 || r == 0x7f {
			return configErrorf("environment id contains a control character")
		}
		if strings.ContainsRune(`<>:"|?*`, r) {
			return configErrorf("environment id contains an invalid character %q", string(r))
		}
	}
	return nil
}

// environmentRelPath maps a validated env id to its file under environments/.
func environmentRelPath(id string) string {
	return filepath.ToSlash(filepath.Join(EnvironmentsDir, id+".yaml"))
}

// validateEnvironmentVariables rejects keys that cannot be referenced as
// {{name}} templates. Values are free-form and never validated by content.
func validateEnvironmentVariables(vars map[string]string) error {
	for k := range vars {
		if strings.TrimSpace(k) == "" {
			return configErrorf("environment variable key is empty")
		}
		if strings.ContainsAny(k, "{}") {
			return configErrorf("invalid environment variable key %q (must not contain braces)", k)
		}
	}
	return nil
}

// sortedVarKeys returns the variable keys in a stable order so the editor and
// tests never see map iteration order (which would make rows jump each frame).
func sortedVarKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// NewEnvironment validates and writes a brand-new environment file, then
// registers it in memory. It refuses a duplicate id and never overwrites an
// existing file: SaveChecked with an empty baseline requires the target to be
// absent.
func (p *Project) NewEnvironment(env *Environment) (*EnvironmentEntry, error) {
	if env == nil {
		return nil, configErrorf("environment is nil")
	}
	id := strings.TrimSpace(env.ID)
	if err := ValidateEnvironmentID(id); err != nil {
		return nil, err
	}
	if _, dup := p.Environments[id]; dup {
		return nil, configErrorf("environment id %q already exists", id)
	}
	if err := validateEnvironmentVariables(env.Variables); err != nil {
		return nil, err
	}
	stored := *env
	stored.ID = id
	if stored.SchemaVersion == 0 {
		stored.SchemaVersion = SupportedSchemaVersion
	}
	data, err := encodeYAML(&stored)
	if err != nil {
		return nil, err
	}
	rel := environmentRelPath(id)
	// Empty baseline = "the file must not exist yet"; an existing target (even
	// one holding a different id) is reported as a conflict, never overwritten.
	if err := p.Store.SaveChecked(rel, data, ""); err != nil {
		return nil, err
	}
	entry := &EnvironmentEntry{Environment: &stored, RelPath: rel, BaselineDigest: Digest(data)}
	if p.EnvEntries == nil {
		p.EnvEntries = map[string]*EnvironmentEntry{}
	}
	p.EnvEntries[id] = entry
	p.Environments[id] = &stored
	p.EnvOrder = append(p.EnvOrder, id)
	sort.Strings(p.EnvOrder)
	return entry, nil
}

// SaveEnvironment writes an edited environment back to the file it was loaded
// from, using the recorded baseline for conflict detection. The id is treated
// as immutable here so references never silently migrate. On any error the
// in-memory model is left unchanged.
func (p *Project) SaveEnvironment(id string, env *Environment) error {
	entry, ok := p.EnvEntries[id]
	if !ok {
		return configErrorf("environment %q is not loaded for editing", id)
	}
	if env == nil {
		return configErrorf("environment is nil")
	}
	if err := validateEnvironmentVariables(env.Variables); err != nil {
		return err
	}
	stored := *env
	stored.ID = id
	if stored.SchemaVersion == 0 {
		stored.SchemaVersion = SupportedSchemaVersion
	}
	data, err := encodeYAML(&stored)
	if err != nil {
		return err
	}
	if err := p.Store.SaveChecked(entry.RelPath, data, entry.BaselineDigest); err != nil {
		// Conflict or write failure: do not touch the loaded state.
		return err
	}
	entry.BaselineDigest = Digest(data)
	entry.Environment = &stored
	p.Environments[id] = &stored
	return nil
}

// SortedVariableKeys exposes stable ordering for callers outside core.
func (e *Environment) SortedVariableKeys() []string {
	if e == nil {
		return nil
	}
	return sortedVarKeys(e.Variables)
}
