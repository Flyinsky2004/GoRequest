// Package platform holds OS-facing adapters kept out of the pure Go core.
package platform

import (
	"os"
	"strings"
	"unicode"
)

// EnvSecrets resolves secrets from environment variables named
// GOREQUEST_SECRET_<NAME>, where NAME is upper-cased with non-alphanumeric
// characters turned into underscores. This keeps secret values out of project
// files and works headless for CI. It is a minimal backend; an OS keychain
// adapter is future work (see docs/decisions.md Q-03).
type EnvSecrets struct {
	Prefix string
}

// NewEnvSecrets returns an environment-backed secret provider.
func NewEnvSecrets() EnvSecrets { return EnvSecrets{Prefix: "GOREQUEST_SECRET_"} }

// Get returns the secret value if the environment variable is set and non-empty.
func (e EnvSecrets) Get(name string) (string, bool) {
	key := e.Prefix + NormalizeSecretName(name)
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", false
	}
	return v, true
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
