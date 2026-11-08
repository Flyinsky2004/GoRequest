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

// SecretBackendKind names a user-selectable secret storage backend. The
// default is the OS credential store. The local-file backend is an explicit
// opt-in: it stores plaintext under the user's home directory and never
// pretends to be encrypted.
type SecretBackendKind string

const (
	SecretBackendKeychain SecretBackendKind = "keychain"
	SecretBackendFile     SecretBackendKind = "file"
)

// Valid reports whether k is a backend this build understands.
func (k SecretBackendKind) Valid() bool {
	return k == SecretBackendKeychain || k == SecretBackendFile
}

// String returns the stable identifier, defaulting to keychain.
func (k SecretBackendKind) String() string {
	if k.Valid() {
		return string(k)
	}
	return string(SecretBackendKeychain)
}

// SecretBackendInfo is the non-sensitive description shown in the UI: which
// backend is active, where it really lives, whether it is a plaintext file, and
// why it is unavailable when it is.
type SecretBackendInfo struct {
	Kind      SecretBackendKind
	Label     string
	Path      string
	Plaintext bool
	Available bool
	Detail    string
}

// SecretBackend is a writable, inspectable secret store. Get/InspectSecret
// come from core; Set/Delete are the management surface. Names reports every
// entry the backend can enumerate; ok is false when the backend cannot
// enumerate (the OS keychain does not expose its item list).
type SecretBackend interface {
	core.SecretProvider
	core.SecretInspector
	Set(name, value string) error
	Delete(name string) error
	Info() SecretBackendInfo
	Names() (names []string, ok bool)
}

// SecretBackendProber is implemented by backends that can cheaply confirm
// availability. It is called on explicit user actions (opening the storage
// section or the value dialog, starting a switch), never every frame, and it
// never writes a value.
type SecretBackendProber interface {
	Probe() SecretBackendInfo
}

const (
	secretStoreVersion = 1

	// MaxFileSecretBytes bounds one value in the local-file backend.
	MaxFileSecretBytes = 64 << 10
	// MaxFileStoreBytes bounds one local store/manifest/preference document.
	MaxFileStoreBytes = 4 << 20

	secretStoreDirPerm  = 0o700
	secretStoreFilePerm = 0o600
)

// DefaultSecretStoreRoot returns the per-user directory for the local-file
// backend, the management manifest and the selected-backend preference. It is
// deliberately outside any project so secret values never enter project Git.
func DefaultSecretStoreRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", &core.ConfigError{Err: errors.New("user home directory unavailable for the local secret store")}
	}
	return filepath.Join(home, ".gorequest", "secrets"), nil
}

// SecretStore resolves per-project-clone scopes, builds the selected backend,
// and owns the small home-directory documents that survive a restart:
// preferences.json (selected backend per scope) and manifest.json (names the
// user has managed, so entries behind a non-enumerable keychain can still be
// found and copied).
type SecretStore struct {
	// Root is the directory for local files and the two JSON documents. An
	// empty Root resolves to DefaultSecretStoreRoot on first use.
	Root string
	// Keychain is the OS credential driver. nil means the real MyGo backend.
	Keychain KeychainDriver
}

// NewSecretStore builds a store. root "" means the default home directory and
// driver nil means the real OS credential store.
func NewSecretStore(root string, driver KeychainDriver) *SecretStore {
	return &SecretStore{Root: strings.TrimSpace(root), Keychain: driver}
}

// Scope returns the clone-isolated scope key for a project.
func (s *SecretStore) Scope(projectID, projectRoot string) (string, error) {
	return ProjectScopeKey(projectID, projectRoot)
}

func (s *SecretStore) root() (string, error) {
	if s != nil && strings.TrimSpace(s.Root) != "" {
		return filepath.Clean(strings.TrimSpace(s.Root)), nil
	}
	return DefaultSecretStoreRoot()
}

// Backend builds the backend named by kind for a project clone.
func (s *SecretStore) Backend(scope, projectID, projectRoot string, kind SecretBackendKind) (SecretBackend, error) {
	if !kind.Valid() {
		kind = SecretBackendKeychain
	}
	if kind == SecretBackendFile {
		root, err := s.root()
		if err != nil {
			return nil, err
		}
		return NewFileSecrets(root, scope)
	}
	driver := KeychainDriver(nil)
	if s != nil {
		driver = s.Keychain
	}
	if driver == nil {
		driver = NewMyGoKeychainDriver("")
	}
	return NewKeychainSecrets(driver, projectID, projectRoot)
}

// SelectedKind returns the persisted backend for scope, defaulting to the OS
// keychain when nothing valid is recorded.
func (s *SecretStore) SelectedKind(scope string) SecretBackendKind {
	root, err := s.root()
	if err != nil {
		return SecretBackendKeychain
	}
	doc, err := loadSecretPrefs(root)
	if err != nil {
		return SecretBackendKeychain
	}
	kind := SecretBackendKind(doc.Scopes[scope])
	if !kind.Valid() {
		return SecretBackendKeychain
	}
	return kind
}

// SetSelectedKind persists the backend choice for scope. It is called only
// after a switch has copied and verified every value.
func (s *SecretStore) SetSelectedKind(scope string, kind SecretBackendKind) error {
	if !kind.Valid() {
		return &core.ConfigError{Err: fmt.Errorf("unsupported secret backend %q", kind)}
	}
	if strings.TrimSpace(scope) == "" {
		return &core.ConfigError{Err: errors.New("empty secret scope")}
	}
	root, err := s.root()
	if err != nil {
		return err
	}
	secretDocsMu.Lock()
	defer secretDocsMu.Unlock()
	doc, err := loadSecretPrefs(root)
	if err != nil {
		return err
	}
	if doc.Scopes == nil {
		doc.Scopes = map[string]string{}
	}
	doc.Scopes[scope] = string(kind)
	return saveSecretPrefs(root, doc)
}

// Manifest returns the managed-name manifest for scope. The manifest is shared
// by every backend for that scope: it records names the user has written
// through GoRequest, not values.
func (s *SecretStore) Manifest(scope string) *ManifestStore {
	root, err := s.root()
	if err != nil {
		return &ManifestStore{scope: scope, err: err}
	}
	return &ManifestStore{root: root, scope: scope}
}

// FilePath returns the exact local-file store path for scope.
func (s *SecretStore) FilePath(scope string) (string, error) {
	root, err := s.root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "store-"+safeScopeName(scope)+".json"), nil
}

// --- preferences ----------------------------------------------------------

type secretPrefsDoc struct {
	Version int               `json:"version"`
	Scopes  map[string]string `json:"scopes"`
}

func loadSecretPrefs(root string) (secretPrefsDoc, error) {
	doc := secretPrefsDoc{Version: secretStoreVersion, Scopes: map[string]string{}}
	err := readPrivateJSON(filepath.Join(root, "preferences.json"), &doc)
	if errors.Is(err, os.ErrNotExist) {
		return secretPrefsDoc{Version: secretStoreVersion, Scopes: map[string]string{}}, nil
	}
	if err != nil {
		return doc, err
	}
	if doc.Scopes == nil {
		doc.Scopes = map[string]string{}
	}
	return doc, nil
}

func saveSecretPrefs(root string, doc secretPrefsDoc) error {
	doc.Version = secretStoreVersion
	if doc.Scopes == nil {
		doc.Scopes = map[string]string{}
	}
	return writePrivateJSON(filepath.Join(root, "preferences.json"), doc)
}

// --- managed-name manifest ------------------------------------------------

// ManifestStore persists the names the user has managed for one project clone.
// It exists because the OS keychain cannot enumerate its items: without this
// list, an undeclared-but-managed secret could not be found again when the
// user switches backends. It never stores values.
type ManifestStore struct {
	root  string
	scope string
	err   error
}

type secretManifestDoc struct {
	Version int                 `json:"version"`
	Scopes  map[string][]string `json:"scopes"`
}

func (m *ManifestStore) path() string {
	return filepath.Join(m.root, "manifest.json")
}

func (m *ManifestStore) load() (secretManifestDoc, error) {
	doc := secretManifestDoc{Version: secretStoreVersion, Scopes: map[string][]string{}}
	if m == nil || m.err != nil {
		if m != nil {
			return doc, m.err
		}
		return doc, &core.ConfigError{Err: errors.New("no secret manifest store")}
	}
	err := readPrivateJSON(m.path(), &doc)
	if errors.Is(err, os.ErrNotExist) {
		return secretManifestDoc{Version: secretStoreVersion, Scopes: map[string][]string{}}, nil
	}
	if err != nil {
		return doc, err
	}
	if doc.Scopes == nil {
		doc.Scopes = map[string][]string{}
	}
	return doc, nil
}

func (m *ManifestStore) save(doc secretManifestDoc) error {
	doc.Version = secretStoreVersion
	if doc.Scopes == nil {
		doc.Scopes = map[string][]string{}
	}
	return writePrivateJSON(m.path(), doc)
}

// Names returns the managed names recorded for this scope, sorted.
func (m *ManifestStore) Names() ([]string, error) {
	if m == nil {
		return nil, nil
	}
	secretDocsMu.Lock()
	defer secretDocsMu.Unlock()
	doc, err := m.load()
	if err != nil {
		return nil, err
	}
	names := append([]string(nil), doc.Scopes[m.scope]...)
	sort.Strings(names)
	return names, nil
}

// Add records name as managed for this scope. It is idempotent.
func (m *ManifestStore) Add(name string) error {
	trimmed := strings.TrimSpace(name)
	if err := core.ValidateSecretName(trimmed); err != nil {
		return err
	}
	if m == nil {
		return &core.ConfigError{Err: errors.New("no secret manifest store")}
	}
	secretDocsMu.Lock()
	defer secretDocsMu.Unlock()
	doc, err := m.load()
	if err != nil {
		return err
	}
	for _, existing := range doc.Scopes[m.scope] {
		if existing == trimmed {
			return nil
		}
	}
	doc.Scopes[m.scope] = append(doc.Scopes[m.scope], trimmed)
	sort.Strings(doc.Scopes[m.scope])
	return m.save(doc)
}

// Remove forgets name for this scope. Missing names are a no-op.
func (m *ManifestStore) Remove(name string) error {
	trimmed := strings.TrimSpace(name)
	if m == nil {
		return nil
	}
	secretDocsMu.Lock()
	defer secretDocsMu.Unlock()
	doc, err := m.load()
	if err != nil {
		return err
	}
	kept := doc.Scopes[m.scope][:0]
	for _, existing := range doc.Scopes[m.scope] {
		if existing != trimmed {
			kept = append(kept, existing)
		}
	}
	if len(kept) == 0 {
		delete(doc.Scopes, m.scope)
	} else {
		doc.Scopes[m.scope] = kept
	}
	return m.save(doc)
}

// --- private JSON documents ----------------------------------------------

// secretDocsMu serializes read-modify-write cycles on the small home-directory
// documents. It is process-local; cross-process writes still rely on the
// atomic rename below, which never leaves a half-written document.
var secretDocsMu sync.Mutex

func readPrivateJSON(path string, v any) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("secret store document %q is a symlink; refusing to read it", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("secret store document %q is not a regular file", path)
	}
	if fi.Size() > MaxFileStoreBytes {
		return fmt.Errorf("secret store document %q exceeds %d bytes", path, MaxFileStoreBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("secret store document %q is corrupt: %w", path, err)
	}
	return nil
}

func writePrivateJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > MaxFileStoreBytes {
		return fmt.Errorf("secret store document %q would exceed %d bytes", path, MaxFileStoreBytes)
	}
	return writePrivateFileAtomic(path, data)
}

// writePrivateFileAtomic writes data through a 0600 temporary file in the same
// directory, fsyncs it, then renames it over the target. The rename replaces a
// symlink itself rather than following it, and rejectSymlink refuses an
// existing link first so a planted link cannot redirect the write.
func writePrivateFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	if err := rejectSymlink(path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gorequest-secret-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(secretStoreFilePerm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := rejectSymlink(path); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, secretStoreFilePerm)
}

func ensurePrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("secret store directory %q is a symlink; refusing", dir)
		}
		if !fi.IsDir() {
			return fmt.Errorf("secret store path %q is not a directory", dir)
		}
		if fi.Mode().Perm() != secretStoreDirPerm {
			return os.Chmod(dir, secretStoreDirPerm)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, secretStoreDirPerm); err != nil {
		return err
	}
	return os.Chmod(dir, secretStoreDirPerm)
}

func rejectSymlink(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("secret store path %q is a symlink; refusing", path)
	}
	return nil
}

func safeScopeName(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range scope {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "default"
	}
	if len(out) > 160 {
		out = out[:160]
	}
	return out
}
