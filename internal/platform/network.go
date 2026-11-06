package platform

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gorequest/internal/core"
)

// DeviceNetworkSettings holds machine-local network transport defaults, proxy
// settings, certificate references, and device-level target policy guardrails.
// This structure lives strictly in OS-local user config (or in-memory during
// tests) and is never written to shared .gorequest/ repository files.
type DeviceNetworkSettings struct {
	Timeouts     core.StageTimeouts      `json:"timeouts"`
	Redirect     core.RedirectPolicy     `json:"redirect"`
	Proxy        core.ProxyConfig        `json:"proxy"`
	TLS          core.TLSConfig          `json:"tls"`
	DevicePolicy core.DeviceTargetPolicy `json:"devicePolicy"`
	MaxBodyBytes int64                   `json:"maxBodyBytes,omitempty"`
}

// ProjectNetworkBindings holds machine-local certificate/key file slot
// mappings and optional per-project transport overrides keyed by
// ProjectScopeKey(projectID, projectRoot).
type ProjectNetworkBindings struct {
	CertSlots map[string]string   `json:"certSlots,omitempty"`
	Proxy     *core.ProxyConfig   `json:"proxy,omitempty"`
	TLS       *core.TLSConfig     `json:"tls,omitempty"`
	Redirect  *core.RedirectPolicy `json:"redirect,omitempty"`
}

type persistedNetworkState struct {
	Device   DeviceNetworkSettings             `json:"device"`
	Projects map[string]ProjectNetworkBindings `json:"projects,omitempty"`
}

// NetworkStore manages machine-local persistence for DeviceNetworkSettings and
// per-project certificate/key slot bindings. When path is empty, it operates
// purely in memory (ideal for unit tests without touching the host filesystem).
type NetworkStore struct {
	mu    sync.RWMutex
	path  string
	state persistedNetworkState
	// sessionProxyPasswords keeps ephemeral or keychain-backed proxy passwords
	// in memory without writing them to JSON on disk.
	sessionProxyPasswords map[string]string
}

// NewNetworkStore opens or initializes a machine-local NetworkStore at path.
// Passing an empty path creates an in-memory store.
func NewNetworkStore(path string) *NetworkStore {
	s := &NetworkStore{
		path: strings.TrimSpace(path),
		state: persistedNetworkState{
			Device:   DefaultDeviceNetworkSettings(),
			Projects: make(map[string]ProjectNetworkBindings),
		},
		sessionProxyPasswords: make(map[string]string),
	}
	if s.path != "" {
		_ = s.load()
	}
	return s
}

// DefaultDeviceNetworkSettings returns the safe default transport and target
// policy settings for a device (TLS verification enforced, HTTPS required for
// non-loopback credentials, interactive confirmation on mutating production
// requests, 10s dial/TLS timeouts, 30s TTFB timeout, follow up to 10 redirects).
func DefaultDeviceNetworkSettings() DeviceNetworkSettings {
	tTrue := true
	return DeviceNetworkSettings{
		Timeouts: core.StageTimeouts{
			DialTimeout:           core.DefaultDialTimeout,
			TLSHandshakeTimeout:   core.DefaultTLSHandshakeTimeout,
			ResponseHeaderTimeout: core.DefaultResponseHeaderTimeout,
		},
		Redirect: core.RedirectPolicy{
			Mode:    core.RedirectFollow,
			MaxHops: core.DefaultMaxRedirects,
		},
		Proxy: core.ProxyConfig{
			Mode: core.ProxySystem,
		},
		TLS: core.TLSConfig{
			UseSystemCA: &tTrue,
		},
		DevicePolicy: core.DeviceTargetPolicy{
			ProductionAction:        core.ActionConfirm,
			AllowInsecureTLS:        false,
			AllowPlainHTTPWithCreds: false,
		},
		MaxBodyBytes: core.DefaultMaxBody,
	}
}

// DeviceSettings returns a copy of the current machine-local network settings.
func (s *NetworkStore) DeviceSettings() DeviceNetworkSettings {
	if s == nil {
		return DefaultDeviceNetworkSettings()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.state.Device
	if pw, ok := s.sessionProxyPasswords["device"]; ok && cfg.Proxy.Password == "" {
		cfg.Proxy.Password = pw
	}
	return cfg
}

// SaveDeviceSettings updates the machine-local network settings and persists
// them if the store is backed by a file. Plaintext proxy passwords are kept in
// memory only and stripped before disk serialization.
func (s *NetworkStore) SaveDeviceSettings(cfg DeviceNetworkSettings) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.Proxy.Password != "" {
		s.sessionProxyPasswords["device"] = cfg.Proxy.Password
	} else {
		delete(s.sessionProxyPasswords, "device")
	}
	diskCfg := cfg
	diskCfg.Proxy.Password = ""
	s.state.Device = diskCfg
	return s.saveLocked()
}

// BindCertSlot binds a logical slot name (e.g. "corp_ca", "client_cert",
// "client_key") to an absolute file path on this machine for the given project
// scope.
func (s *NetworkStore) BindCertSlot(projectScope, slot, absPath string) error {
	if s == nil {
		return nil
	}
	slot = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(slot), core.LocalFileRefPrefix))
	if err := core.ValidateLocalSlot(slot); err != nil {
		return err
	}
	trimmedPath := strings.TrimSpace(absPath)
	if trimmedPath != "" && !filepath.IsAbs(trimmedPath) {
		return &core.ConfigError{Err: fmt.Errorf("certificate/key binding for %q must be an absolute local path, got %q", slot, absPath)}
	}
	scope := normalizeScopeKey(projectScope)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Projects == nil {
		s.state.Projects = make(map[string]ProjectNetworkBindings)
	}
	pb := s.state.Projects[scope]
	if pb.CertSlots == nil {
		pb.CertSlots = make(map[string]string)
	}
	if trimmedPath == "" {
		delete(pb.CertSlots, slot)
	} else {
		pb.CertSlots[slot] = filepath.Clean(trimmedPath)
	}
	s.state.Projects[scope] = pb
	return s.saveLocked()
}

// ResolveCertSlot resolves a bound local certificate/key path for projectScope.
func (s *NetworkStore) ResolveCertSlot(projectScope, slot string) (string, bool) {
	if s == nil {
		return "", false
	}
	slot = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(slot), core.LocalFileRefPrefix))
	scope := normalizeScopeKey(projectScope)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if pb, ok := s.state.Projects[scope]; ok && pb.CertSlots != nil {
		if p, exists := pb.CertSlots[slot]; exists && p != "" {
			return p, true
		}
	}
	if pb, ok := s.state.Projects["default"]; ok && pb.CertSlots != nil {
		if p, exists := pb.CertSlots[slot]; exists && p != "" {
			return p, true
		}
	}
	return "", false
}

// CertSlots returns a copy of all bound certificate/key slots for projectScope.
func (s *NetworkStore) CertSlots(projectScope string) map[string]string {
	out := make(map[string]string)
	if s == nil {
		return out
	}
	scope := normalizeScopeKey(projectScope)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if pb, ok := s.state.Projects[scope]; ok {
		for k, v := range pb.CertSlots {
			out[k] = v
		}
	}
	return out
}

// LocalFileProviderFor returns a core.LocalFileProvider that checks fallback
// (such as B03's body local file bindings) and this store's certificate/key
// bindings for projectScope.
func (s *NetworkStore) LocalFileProviderFor(projectScope string, fallback core.LocalFileProvider) core.LocalFileProvider {
	return &chainedLocalFileProvider{
		store:        s,
		projectScope: normalizeScopeKey(projectScope),
		fallback:     fallback,
	}
}

type chainedLocalFileProvider struct {
	store        *NetworkStore
	projectScope string
	fallback     core.LocalFileProvider
}

func (c *chainedLocalFileProvider) ResolveLocalFile(slot string) (string, bool) {
	if c.store != nil {
		if p, ok := c.store.ResolveCertSlot(c.projectScope, slot); ok {
			return p, true
		}
	}
	if c.fallback != nil {
		return c.fallback.ResolveLocalFile(slot)
	}
	return "", false
}

func normalizeScopeKey(scope string) string {
	s := strings.TrimSpace(scope)
	if s == "" {
		return "default"
	}
	return s
}

func (s *NetworkStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var loaded persistedNetworkState
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	if loaded.Projects == nil {
		loaded.Projects = make(map[string]ProjectNetworkBindings)
	}
	if loaded.Device.Timeouts.DialTimeout <= 0 {
		loaded.Device.Timeouts.DialTimeout = core.DefaultDialTimeout
	}
	if loaded.Device.Timeouts.TLSHandshakeTimeout <= 0 {
		loaded.Device.Timeouts.TLSHandshakeTimeout = core.DefaultTLSHandshakeTimeout
	}
	if loaded.Device.Redirect.EffectiveMaxHops() <= 0 {
		loaded.Device.Redirect.MaxHops = core.DefaultMaxRedirects
	}
	if loaded.Device.MaxBodyBytes <= 0 {
		loaded.Device.MaxBodyBytes = core.DefaultMaxBody
	}
	s.state = loaded
	return nil
}

func (s *NetworkStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
