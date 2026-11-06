package platform

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"

	"gorequest/internal/core"
)

const (
	// DefaultKeychainService is the OS credential service namespace used by GoRequest.
	DefaultKeychainService = "dev.gorequest.secrets"

	// MaxKeychainKeyBytes matches mygo's OS credential key length ceiling.
	MaxKeychainKeyBytes = 256

	// MaxKeychainSecretBytes matches mygo.MaxSecretSize (Windows Credential Blob ceiling).
	MaxKeychainSecretBytes = mygo.MaxSecretSize
)

// ErrSecretNotFound aliases mygo.ErrSecretNotFound so callers and test doubles
// can match missing credential entries with errors.Is.
var ErrSecretNotFound = mygo.ErrSecretNotFound

// KeychainDriver abstracts native OS credential storage (macOS Keychain,
// Windows Credential Manager, Linux libsecret D-Bus Secret Service).
type KeychainDriver interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// MyGoKeychainDriver implements KeychainDriver on top of mygo.Secrets.
type MyGoKeychainDriver struct {
	Service string
}

// NewMyGoKeychainDriver returns a native OS credential driver under service
// (defaulting to DefaultKeychainService when empty).
func NewMyGoKeychainDriver(service string) MyGoKeychainDriver {
	if strings.TrimSpace(service) == "" {
		service = DefaultKeychainService
	}
	return MyGoKeychainDriver{Service: strings.TrimSpace(service)}
}

func (d MyGoKeychainDriver) store() mygo.SecretsModule {
	svc := strings.TrimSpace(d.Service)
	if svc == "" {
		svc = DefaultKeychainService
	}
	return mygo.Secrets.Service(svc)
}

// Get retrieves key from the OS credential store.
func (d MyGoKeychainDriver) Get(key string) (string, error) {
	b, err := d.store().Get(key)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Set stores value under key in the OS credential store.
func (d MyGoKeychainDriver) Set(key, value string) error {
	return d.store().Set(key, []byte(value))
}

// Delete removes key from the OS credential store.
func (d MyGoKeychainDriver) Delete(key string) error {
	return d.store().Delete(key)
}

// ProjectScopeKey derives a deterministic, clone-isolated credential namespace
// "<projectSlug>:<sha256(canonicalProjectRoot)[:16]>" so two clones of the same
// repository (or two distinct projects declaring the same secret name) never
// collide or leak credentials across directories.
func ProjectScopeKey(projectID, projectRoot string) (string, error) {
	trimmedRoot := strings.TrimSpace(projectRoot)
	if trimmedRoot == "" {
		return "", &core.ConfigError{Err: errors.New("project root is required to scope keychain secrets")}
	}
	abs, err := filepath.Abs(trimmedRoot)
	if err != nil {
		return "", &core.ConfigError{Err: fmt.Errorf("resolving project root: %w", err)}
	}
	clean := filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = filepath.Clean(resolved)
	}
	if filepath.Base(clean) == ".gorequest" {
		clean = filepath.Dir(clean)
	}

	rawID := strings.TrimSpace(projectID)
	if rawID == "" {
		rawID = filepath.Base(clean)
	}
	slug := core.Slug(rawID)
	if len(slug) > 48 {
		slug = slug[:48]
	}

	sum := sha256.Sum256([]byte(filepath.ToSlash(clean)))
	return fmt.Sprintf("%s:%x", slug, sum[:8]), nil
}

// ScopedSecretKey builds and validates the full OS credential key
// "<scope>/<secretName>" within the 256-byte non-NUL platform limit.
func ScopedSecretKey(scope, secretName string) (string, error) {
	sc := strings.TrimSpace(scope)
	name := strings.TrimSpace(secretName)
	if sc == "" {
		return "", &core.ConfigError{Err: errors.New("empty secret scope")}
	}
	if name == "" {
		return "", &core.ConfigError{Err: errors.New("empty secret name")}
	}
	if strings.ContainsRune(sc, 0) || strings.ContainsRune(name, 0) {
		return "", &core.ConfigError{Err: fmt.Errorf("secret key for %q contains NUL byte", name)}
	}
	full := sc + "/" + name
	if len(full) > MaxKeychainKeyBytes {
		return "", &core.ConfigError{
			Err: fmt.Errorf("scoped secret key for %q is %d bytes (exceeds %d-byte OS keychain limit)", name, len(full), MaxKeychainKeyBytes),
		}
	}
	return full, nil
}

// KeychainSecrets binds project-scoped secrets in the OS credential store with
// zero plaintext file fallback.
type KeychainSecrets struct {
	driver KeychainDriver
	scope  string
}

// NewKeychainSecrets creates a project-isolated KeychainSecrets adapter.
func NewKeychainSecrets(driver KeychainDriver, projectID, projectRoot string) (*KeychainSecrets, error) {
	scope, err := ProjectScopeKey(projectID, projectRoot)
	if err != nil {
		return nil, err
	}
	return &KeychainSecrets{
		driver: driver,
		scope:  scope,
	}, nil
}

// Scope returns the "<slug>:<hash>" namespace used for this project clone.
func (k *KeychainSecrets) Scope() string {
	if k == nil {
		return ""
	}
	return k.scope
}

// Get retrieves the named secret from the OS keychain. Missing, locked, or
// unsupported backends return ("", false) and NEVER read any local plaintext file.
func (k *KeychainSecrets) Get(name string) (string, bool) {
	if k == nil || k.driver == nil {
		return "", false
	}
	key, err := ScopedSecretKey(k.scope, name)
	if err != nil {
		return "", false
	}
	val, err := k.driver.Get(key)
	if err != nil || val == "" {
		return "", false
	}
	return val, true
}

// InspectSecret probes non-sensitive binding status for name, distinguishing
// missing entries from locked or unsupported OS credential backends.
func (k *KeychainSecrets) InspectSecret(name string) core.SecretStatus {
	trimmed := strings.TrimSpace(name)
	if k == nil || k.driver == nil {
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateUnavailable,
			Backend: "keychain",
			Detail:  "no OS keychain driver configured",
		}
	}
	key, err := ScopedSecretKey(k.scope, trimmed)
	if err != nil {
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateUnavailable,
			Backend: "keychain",
			Detail:  err.Error(),
		}
	}
	val, err := k.driver.Get(key)
	if err == nil {
		if val == "" {
			return core.SecretStatus{
				Name:    trimmed,
				State:   core.SecretStateMissing,
				Backend: "keychain",
			}
		}
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateBound,
			Backend: "keychain",
		}
	}
	if errors.Is(err, ErrSecretNotFound) || errors.Is(err, os.ErrNotExist) {
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateMissing,
			Backend: "keychain",
		}
	}
	if errors.Is(err, errors.ErrUnsupported) {
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateUnavailable,
			Backend: "keychain",
			Detail:  "system credential store unsupported or D-Bus Secret Service unavailable (plaintext fallback refused)",
		}
	}
	return core.SecretStatus{
		Name:    trimmed,
		State:   core.SecretStateUnavailable,
		Backend: "keychain",
		Detail:  fmt.Sprintf("system credential store error: %v", err),
	}
}

// Set writes a secret value into the OS keychain after enforcing key/value size
// and NUL constraints. If the OS credential store is unavailable, Set fails
// closed with *core.ConfigError and never writes to disk.
func (k *KeychainSecrets) Set(name, value string) error {
	if k == nil || k.driver == nil {
		return &core.ConfigError{Err: errors.New("no OS keychain driver configured; refusing plaintext fallback")}
	}
	key, err := ScopedSecretKey(k.scope, name)
	if err != nil {
		return err
	}
	if value == "" {
		return &core.ConfigError{Err: fmt.Errorf("secret %q value cannot be empty", strings.TrimSpace(name))}
	}
	if strings.ContainsRune(value, 0) {
		return &core.ConfigError{Err: fmt.Errorf("secret %q value contains NUL byte", strings.TrimSpace(name))}
	}
	if len(value) > MaxKeychainSecretBytes {
		return &core.ConfigError{
			Err: fmt.Errorf("secret %q is %d bytes (exceeds %d-byte OS keychain limit)", strings.TrimSpace(name), len(value), MaxKeychainSecretBytes),
		}
	}
	if err := k.driver.Set(key, value); err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			return &core.ConfigError{
				Err: fmt.Errorf("cannot store secret %q: system credential store unsupported and plaintext fallback is forbidden", strings.TrimSpace(name)),
			}
		}
		return &core.ConfigError{
			Err: fmt.Errorf("cannot store secret %q in system credential store: %v", strings.TrimSpace(name), err),
		}
	}
	return nil
}

// Delete removes name from the OS keychain. Missing secrets are treated as
// already deleted.
func (k *KeychainSecrets) Delete(name string) error {
	if k == nil || k.driver == nil {
		return &core.ConfigError{Err: errors.New("no OS keychain driver configured")}
	}
	key, err := ScopedSecretKey(k.scope, name)
	if err != nil {
		return err
	}
	if err := k.driver.Delete(key); err != nil {
		if errors.Is(err, ErrSecretNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, errors.ErrUnsupported) {
			return &core.ConfigError{
				Err: fmt.Errorf("cannot delete secret %q: system credential store unsupported", strings.TrimSpace(name)),
			}
		}
		return &core.ConfigError{
			Err: fmt.Errorf("cannot delete secret %q from system credential store: %v", strings.TrimSpace(name), err),
		}
	}
	return nil
}

// LayeredSecrets resolves secrets across explicit memory overrides, OS
// KeychainSecrets, and process EnvSecrets (GOREQUEST_SECRET_<NAME>) in strict
// order without any plaintext file fallback.
type LayeredSecrets struct {
	Override core.MapSecrets
	Keychain *KeychainSecrets
	Env      EnvSecrets
}

// NewProjectSecrets constructs a LayeredSecrets provider for a specific project
// clone using driver (or NewMyGoKeychainDriver("") when driver is nil).
func NewProjectSecrets(driver KeychainDriver, projectID, projectRoot string, override core.MapSecrets) (*LayeredSecrets, error) {
	if driver == nil {
		driver = NewMyGoKeychainDriver("")
	}
	kc, err := NewKeychainSecrets(driver, projectID, projectRoot)
	if err != nil {
		return nil, err
	}
	return &LayeredSecrets{
		Override: override,
		Keychain: kc,
		Env:      NewEnvSecrets(),
	}, nil
}

// Get returns the secret from Override -> Keychain -> Env.
func (l *LayeredSecrets) Get(name string) (string, bool) {
	if l == nil {
		return "", false
	}
	if l.Override != nil {
		if v, ok := l.Override.Get(name); ok && v != "" {
			return v, true
		}
	}
	if l.Keychain != nil {
		if v, ok := l.Keychain.Get(name); ok {
			return v, true
		}
	}
	if v, ok := l.Env.Get(name); ok {
		return v, true
	}
	return "", false
}

// InspectSecret returns non-sensitive resolution diagnostics. If neither
// Override nor Env supplies the secret and Keychain is unavailable/locked, it
// surfaces SecretStateUnavailable so callers receive an actionable error.
func (l *LayeredSecrets) InspectSecret(name string) core.SecretStatus {
	trimmed := strings.TrimSpace(name)
	if l == nil {
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateUnavailable,
			Backend: "none",
			Detail:  "no secret provider configured",
		}
	}
	if l.Override != nil {
		if _, ok := l.Override.Get(trimmed); ok {
			return core.SecretStatus{Name: trimmed, State: core.SecretStateBound, Backend: "memory"}
		}
	}
	var kcStatus core.SecretStatus
	if l.Keychain != nil {
		kcStatus = l.Keychain.InspectSecret(trimmed)
		if kcStatus.State == core.SecretStateBound {
			return kcStatus
		}
	}
	envStatus := l.Env.InspectSecret(trimmed)
	if envStatus.State == core.SecretStateBound {
		return envStatus
	}
	if kcStatus.State == core.SecretStateUnavailable {
		return kcStatus
	}
	return core.SecretStatus{
		Name:    trimmed,
		State:   core.SecretStateMissing,
		Backend: "keychain+env",
	}
}
