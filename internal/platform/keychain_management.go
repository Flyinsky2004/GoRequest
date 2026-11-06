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

// ManagedCredentials binds a project-scoped OS credential store with a small
// inventory surface for the native UI. It deliberately rejects plaintext file
// fallback: a nil/unavailable driver is an error, never a downgrade.
type ManagedCredentials struct {
	writer CredentialWriter
	insp   CredentialInspector
	mu     sync.Mutex
	// known remembers names the UI has successfully written or observed so a
	// backend that cannot enumerate keys can still show and delete entries.
	known map[string]bool
}

// NewManagedCredentials builds a project-isolated managed credential store.
// driver may be a fake for tests; passing nil uses the real MyGo OS backend.
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

// List returns non-sensitive status records for observed/declared names.
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
// via KeychainSecrets; errors from the OS backend are returned verbatim (wrapped
// for context) and never cause a local plaintext file to be written.
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
	if err := m.writer.Set(trimmed, value); err != nil {
		return CredentialChange{}, &core.ConfigError{Err: fmt.Errorf("writing credential %q to system store: %w", trimmed, err)}
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
		return CredentialChange{}, &core.ConfigError{Err: fmt.Errorf("deleting credential %q from system store: %w", trimmed, err)}
	}
	m.mu.Lock()
	delete(m.known, trimmed)
	m.mu.Unlock()
	return CredentialChange{Name: trimmed, Action: "deleted"}, nil
}
