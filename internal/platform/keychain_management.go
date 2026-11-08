package platform

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gorequest/internal/core"
)

// CredentialWriter is the minimal OS credential port used by the identity
// management UI. KeychainSecrets implements it; tests can inject an in-memory
// fake backend using NewKeychainSecrets(fakeDriver, ...).
type CredentialWriter interface {
	Set(name, value string) error
	Delete(name string) error
}

// CredentialInspector reports non-sensitive binding status for one credential.
type CredentialInspector interface {
	InspectSecret(name string) core.SecretStatus
}

// CredentialRecord is a single project-scoped credential name with its status.
// It never contains a secret value.
type CredentialRecord struct {
	Name    string
	State   core.SecretState
	Backend string
	Detail  string
}

// CredentialChange describes the result of a create/update/delete operation.
type CredentialChange struct {
	Name   string
	Action string // "created" | "updated" | "deleted" | "unchanged"
}

// ManagedCredentials binds a project-scoped secret backend with a small
// inventory surface for the native UI. It never silently falls back to a
// different backend: the selected backend is whatever the user chose, and an
// unavailable backend is an error.
type ManagedCredentials struct {
	writer   CredentialWriter
	insp     CredentialInspector
	names    SecretNameLister
	manifest *ManifestStore
	mu       sync.Mutex
	// known remembers names the UI has successfully written or observed so a
	// backend that cannot enumerate keys can still show and delete entries.
	known map[string]bool
}

// SecretNameLister is implemented by backends that can enumerate entries. The
// OS keychain deliberately cannot; the local-file backend can.
type SecretNameLister interface {
	Names() ([]string, bool)
}

// NewManagedCredentials builds a project-isolated managed credential store on
// the OS keychain. driver may be a fake for tests; passing nil uses the real
// MyGo OS backend.
func NewManagedCredentials(driver KeychainDriver, projectID, projectRoot string) (*ManagedCredentials, error) {
	if driver == nil {
		driver = NewMyGoKeychainDriver("")
	}
	kc, err := NewKeychainSecrets(driver, projectID, projectRoot)
	if err != nil {
		return nil, err
	}
	return NewManagedCredentialsFrom(kc, kc)
}

// NewManagedCredentialsWithBackend adapts the user-selected backend. manifest
// may be nil; when present, names the user manages are persisted so they can
// be found again even behind a non-enumerable keychain.
func NewManagedCredentialsWithBackend(backend SecretBackend, manifest *ManifestStore) (*ManagedCredentials, error) {
	if backend == nil {
		return nil, &core.ConfigError{Err: errors.New("credential backend unavailable")}
	}
	mc := &ManagedCredentials{
		writer:   backend,
		insp:     backend,
		manifest: manifest,
		known:    map[string]bool{},
	}
	if lister, ok := backend.(SecretNameLister); ok {
		mc.names = lister
	}
	if manifest != nil {
		if names, err := manifest.Names(); err == nil {
			for _, name := range names {
				mc.known[name] = true
			}
		}
	}
	return mc, nil
}

// NewManagedCredentialsFrom adapts any writer/inspector pair. Both must be
// non-nil; this constructor is what tests use with a memory backend.
func NewManagedCredentialsFrom(writer CredentialWriter, insp CredentialInspector) (*ManagedCredentials, error) {
	if writer == nil || insp == nil {
		return nil, &core.ConfigError{Err: errors.New("credential backend unavailable; refusing plaintext fallback")}
	}
	return &ManagedCredentials{
		writer: writer,
		insp:   insp,
		known:  map[string]bool{},
	}, nil
}

// ValidateName is a boundary check before a value reaches the OS store.
func (m *ManagedCredentials) ValidateName(name string) error {
	return core.ValidateSecretName(name)
}

// Observe records a declared secret name (from a project file) without reading
// or storing its value.
func (m *ManagedCredentials) Observe(name string) error {
	trimmed := strings.TrimSpace(name)
	if err := m.ValidateName(trimmed); err != nil {
		return err
	}
	m.mu.Lock()
	m.known[trimmed] = true
	m.mu.Unlock()
	return nil
}

// List returns non-sensitive status records for observed/declared names plus
// every name the backend or the persistent manifest can enumerate.
func (m *ManagedCredentials) List(names []string) ([]CredentialRecord, error) {
	if m == nil {
		return nil, &core.ConfigError{Err: errors.New("credential backend unavailable")}
	}
	m.mu.Lock()
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			m.known[trimmed] = true
		}
	}
	lister := m.names
	manifest := m.manifest
	m.mu.Unlock()

	if lister != nil {
		if listed, ok := lister.Names(); ok {
			m.mu.Lock()
			for _, name := range listed {
				m.known[name] = true
			}
			m.mu.Unlock()
		}
	}
	if manifest != nil {
		if listed, err := manifest.Names(); err == nil {
			m.mu.Lock()
			for _, name := range listed {
				m.known[name] = true
			}
			m.mu.Unlock()
		}
	}

	m.mu.Lock()
	seen := make([]string, 0, len(m.known))
	for name := range m.known {
		seen = append(seen, name)
	}
	m.mu.Unlock()
	sort.Strings(seen)

	out := make([]CredentialRecord, 0, len(seen))
	for _, name := range seen {
		st := m.insp.InspectSecret(name)
		out = append(out, CredentialRecord{
			Name:    name,
			State:   st.State,
			Backend: st.Backend,
			Detail:  st.Detail,
		})
	}
	return out, nil
}

// Set writes or updates one credential. It refuses empty/NUL/too-long values
// via the selected backend. When a manifest is configured the name is recorded
// before the value is written, so a value can always be found again; a manifest
// failure aborts before the backend is touched.
func (m *ManagedCredentials) Set(name, value string) (CredentialChange, error) {
	if m == nil {
		return CredentialChange{}, &core.ConfigError{Err: errors.New("credential backend unavailable")}
	}
	trimmed := strings.TrimSpace(name)
	if err := m.ValidateName(trimmed); err != nil {
		return CredentialChange{}, err
	}
	if value == "" {
		return CredentialChange{}, &core.ConfigError{Err: fmt.Errorf("secret %q value cannot be empty", trimmed)}
	}
	before := m.insp.InspectSecret(trimmed)
	if m.manifest != nil {
		if err := m.manifest.Add(trimmed); err != nil {
			return CredentialChange{}, &core.ConfigError{Err: fmt.Errorf("recording managed secret %q: %w", trimmed, err)}
		}
	}
	if err := m.writer.Set(trimmed, value); err != nil {
		return CredentialChange{}, &core.ConfigError{Err: fmt.Errorf("writing credential %q to the selected store: %w", trimmed, err)}
	}
	m.mu.Lock()
	m.known[trimmed] = true
	m.mu.Unlock()
	action := "created"
	if before.State == core.SecretStateBound {
		action = "updated"
	}
	return CredentialChange{Name: trimmed, Action: action}, nil
}

// Delete removes one credential. Missing secrets are idempotent. Backend
// failures are reported without any local cleanup that would imply a fallback.
func (m *ManagedCredentials) Delete(name string) (CredentialChange, error) {
	if m == nil {
		return CredentialChange{}, &core.ConfigError{Err: errors.New("credential backend unavailable")}
	}
	trimmed := strings.TrimSpace(name)
	if err := m.ValidateName(trimmed); err != nil {
		return CredentialChange{}, err
	}
	if err := m.writer.Delete(trimmed); err != nil {
		return CredentialChange{}, &core.ConfigError{Err: fmt.Errorf("deleting credential %q from the selected store: %w", trimmed, err)}
	}
	m.mu.Lock()
	delete(m.known, trimmed)
	m.mu.Unlock()
	if m.manifest != nil {
		// The value is already gone; a manifest cleanup failure only leaves a
		// name listed as missing, never a recoverable secret, so it is not
		// reported as a failed delete.
		_ = m.manifest.Remove(trimmed)
	}
	return CredentialChange{Name: trimmed, Action: "deleted"}, nil
}
