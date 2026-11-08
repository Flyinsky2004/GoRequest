package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gorequest/internal/core"
)

// FileSecrets is the default local-file secret backend. It stores plaintext
// values in a per-project-clone JSON document under the user's home directory
// (never inside the project). The backend reports Plaintext=true so the UI can
// say so instead of implying encryption.
//
// Safety properties:
//   - the directory is created/forced to 0700 and the file to 0600;
//   - writes are atomic (0600 temp file + fsync + rename);
//   - an existing symlink at the store path is refused rather than followed;
//   - value and document sizes are bounded;
//   - document JSON is validated before use.
type FileSecrets struct {
	root  string
	scope string
	path  string
	// mu serializes read-modify-write cycles on this store within the process.
	mu sync.Mutex
}

type fileStoreDoc struct {
	Version   int               `json:"version"`
	Scope     string            `json:"scope"`
	Backend   string            `json:"backend"`
	Plaintext bool              `json:"plaintext"`
	Secrets   map[string]string `json:"secrets"`
}

// NewFileSecrets builds a local-file backend for scope under root.
func NewFileSecrets(root, scope string) (*FileSecrets, error) {
	trimmedRoot := strings.TrimSpace(root)
	if trimmedRoot == "" {
		return nil, &core.ConfigError{Err: errors.New("local secret store directory unavailable (user home not resolved)")}
	}
	trimmedScope := strings.TrimSpace(scope)
	if trimmedScope == "" {
		return nil, &core.ConfigError{Err: errors.New("empty secret scope")}
	}
	return &FileSecrets{
		root:  filepath.Clean(trimmedRoot),
		scope: trimmedScope,
		path:  filepath.Join(filepath.Clean(trimmedRoot), "store-"+safeScopeName(trimmedScope)+".json"),
	}, nil
}

// Path returns the exact file this backend reads and writes.
func (f *FileSecrets) Path() string {
	if f == nil {
		return ""
	}
	return f.path
}

// Scope returns the clone-isolated namespace.
func (f *FileSecrets) Scope() string {
	if f == nil {
		return ""
	}
	return f.scope
}

func (f *FileSecrets) load() (fileStoreDoc, error) {
	if f == nil || f.path == "" {
		return fileStoreDoc{}, &core.ConfigError{Err: errors.New("local secret store unavailable")}
	}
	doc := fileStoreDoc{Version: secretStoreVersion, Scope: f.scope, Backend: "file", Plaintext: true, Secrets: map[string]string{}}
	if err := checkPrivateRoot(f.root); err != nil {
		return doc, err
	}
	err := readPrivateJSON(f.path, &doc)
	if errors.Is(err, os.ErrNotExist) {
		return fileStoreDoc{Version: secretStoreVersion, Scope: f.scope, Backend: "file", Plaintext: true, Secrets: map[string]string{}}, nil
	}
	if err != nil {
		return doc, err
	}
	if doc.Version != secretStoreVersion {
		return doc, fmt.Errorf("unsupported local secret store version %d", doc.Version)
	}
	if strings.TrimSpace(doc.Scope) != "" && doc.Scope != f.scope {
		return doc, fmt.Errorf("local secret store belongs to a different project scope")
	}
	if doc.Secrets == nil {
		doc.Secrets = map[string]string{}
	}
	return doc, nil
}

func (f *FileSecrets) save(doc fileStoreDoc) error {
	doc.Version = secretStoreVersion
	doc.Scope = f.scope
	doc.Backend = "file"
	doc.Plaintext = true
	if doc.Secrets == nil {
		doc.Secrets = map[string]string{}
	}
	for name, value := range doc.Secrets {
		if err := core.ValidateSecretName(name); err != nil {
			return err
		}
		if len(value) > MaxFileSecretBytes {
			return &core.ConfigError{Err: fmt.Errorf("secret %q is %d bytes (exceeds %d-byte local store limit)", name, len(value), MaxFileSecretBytes)}
		}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > MaxFileStoreBytes {
		return &core.ConfigError{Err: fmt.Errorf("local secret store would exceed %d bytes", MaxFileStoreBytes)}
	}
	return writePrivateFileAtomic(f.path, data)
}

// Get returns the named plaintext value, or false when it is absent.
func (f *FileSecrets) Get(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	if f == nil || trimmed == "" {
		return "", false
	}
	doc, err := f.load()
	if err != nil {
		return "", false
	}
	value, ok := doc.Secrets[trimmed]
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

// InspectSecret reports binding status without returning the value.
func (f *FileSecrets) InspectSecret(name string) core.SecretStatus {
	trimmed := strings.TrimSpace(name)
	if f == nil || f.path == "" {
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateUnavailable,
			Backend: string(SecretBackendFile),
			Detail:  "local secret store unavailable",
		}
	}
	doc, err := f.load()
	if err != nil {
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateUnavailable,
			Backend: string(SecretBackendFile),
			Detail:  fmt.Sprintf("local secret store error: %v", err),
		}
	}
	if value, ok := doc.Secrets[trimmed]; ok && value != "" {
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateBound,
			Backend: string(SecretBackendFile),
		}
	}
	return core.SecretStatus{
		Name:    trimmed,
		State:   core.SecretStateMissing,
		Backend: string(SecretBackendFile),
	}
}

// Set writes or updates one plaintext value atomically.
func (f *FileSecrets) Set(name, value string) error {
	trimmed := strings.TrimSpace(name)
	if err := core.ValidateSecretName(trimmed); err != nil {
		return err
	}
	if value == "" {
		return &core.ConfigError{Err: fmt.Errorf("secret %q value cannot be empty", trimmed)}
	}
	if strings.ContainsRune(value, 0) {
		return &core.ConfigError{Err: fmt.Errorf("secret %q value contains NUL byte", trimmed)}
	}
	if len(value) > MaxFileSecretBytes {
		return &core.ConfigError{Err: fmt.Errorf("secret %q is %d bytes (exceeds %d-byte local store limit)", trimmed, len(value), MaxFileSecretBytes)}
	}
	if f == nil || f.path == "" {
		return &core.ConfigError{Err: errors.New("local secret store unavailable")}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.load()
	if err != nil {
		return &core.ConfigError{Err: fmt.Errorf("reading local secret store: %w", err)}
	}
	doc.Secrets[trimmed] = value
	if err := f.save(doc); err != nil {
		return &core.ConfigError{Err: fmt.Errorf("writing local secret store: %w", err)}
	}
	return nil
}

// Delete removes one value. Missing values are a no-op.
func (f *FileSecrets) Delete(name string) error {
	trimmed := strings.TrimSpace(name)
	if err := core.ValidateSecretName(trimmed); err != nil {
		return err
	}
	if f == nil || f.path == "" {
		return &core.ConfigError{Err: errors.New("local secret store unavailable")}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, err := f.load()
	if err != nil {
		return &core.ConfigError{Err: fmt.Errorf("reading local secret store: %w", err)}
	}
	if _, ok := doc.Secrets[trimmed]; !ok {
		return nil
	}
	delete(doc.Secrets, trimmed)
	if err := f.save(doc); err != nil {
		return &core.ConfigError{Err: fmt.Errorf("writing local secret store: %w", err)}
	}
	return nil
}

// Info describes the real storage location and its availability.
func (f *FileSecrets) Info() SecretBackendInfo {
	info := SecretBackendInfo{
		Kind:      SecretBackendFile,
		Label:     "Local file (plaintext)",
		Plaintext: true,
	}
	if f == nil || f.path == "" {
		info.Detail = "local secret store unavailable"
		return info
	}
	info.Path = f.path
	if err := checkPrivateRoot(f.root); err != nil {
		info.Detail = err.Error()
		return info
	}
	info.Available = true
	return info
}

// Probe returns the same filesystem check as Info; it never creates or reads a
// secret value.
func (f *FileSecrets) Probe() SecretBackendInfo {
	info := f.Info()
	if info.Available {
		if _, err := f.load(); err != nil {
			info.Available = false
			info.Detail = err.Error()
		}
	}
	return info
}

// Names enumerates the values in this store. The local file can enumerate, so
// ok is true even when the store is empty.
func (f *FileSecrets) Names() ([]string, bool) {
	if f == nil || f.path == "" {
		return nil, false
	}
	doc, err := f.load()
	if err != nil {
		return nil, false
	}
	names := make([]string, 0, len(doc.Secrets))
	for name, value := range doc.Secrets {
		if value != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, true
}

func checkPrivateRoot(root string) error {
	fi, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("local secret store directory %q is a symlink; refusing", root)
	}
	if !fi.IsDir() {
		return fmt.Errorf("local secret store path %q is not a directory", root)
	}
	return nil
}
