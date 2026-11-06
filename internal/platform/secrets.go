// Package platform holds OS-facing adapters kept out of the pure Go core.
package platform

import (
	"os"
	"strings"
	"unicode"

	"gorequest/internal/core"
)

// EnvSecrets resolves secrets from environment variables named
// GOREQUEST_SECRET_<NAME>, where NAME is upper-cased with non-alphanumeric
// characters turned into underscores. This keeps secret values out of project
// files and works in headless CI without a GUI.
type EnvSecrets struct {
	Prefix string
}

// NewEnvSecrets returns an environment-backed secret provider.
func NewEnvSecrets() EnvSecrets { return EnvSecrets{Prefix: "GOREQUEST_SECRET_"} }

// Get returns the secret value if the environment variable is set and non-empty.
func (e EnvSecrets) Get(name string) (string, bool) {
	if strings.TrimSpace(name) == "" {
		return "", false
	}
	key := e.Prefix + NormalizeSecretName(name)
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// InspectSecret reports whether name is bound in the process environment
// without exposing its secret value.
func (e EnvSecrets) InspectSecret(name string) core.SecretStatus {
	trimmed := strings.TrimSpace(name)
	if _, ok := e.Get(trimmed); ok {
		return core.SecretStatus{
			Name:    trimmed,
			State:   core.SecretStateBound,
			Backend: "env",
		}
	}
	return core.SecretStatus{
		Name:    trimmed,
		State:   core.SecretStateMissing,
		Backend: "env",
	}
}

// NormalizeSecretName converts a secret name into the environment suffix.
func NormalizeSecretName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
