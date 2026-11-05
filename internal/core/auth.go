package core

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// applyAuth resolves the auth scheme against the resolver and mutates the
// header/query collections of a plan. It never writes secret values anywhere
// except the in-memory plan.
func applyAuth(scheme *AuthScheme, r *Resolver, headers *[][2]string, query *[][2]string) error {
	if scheme == nil {
		return nil
	}
	switch strings.ToLower(scheme.Type) {
	case "", "none":
		return nil
	case "bearer":
		tok, err := r.Resolve(scheme.Token)
		if err != nil {
			return err
		}
		if tok == "" {
			return configErrorf("bearer auth requires a token")
		}
		*headers = append(*headers, [2]string{"Authorization", "Bearer " + tok})
		return nil
	case "basic":
		user, err := r.Resolve(scheme.Username)
		if err != nil {
			return err
		}
		pass, err := r.Resolve(scheme.Password)
		if err != nil {
			return err
		}
		enc := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		*headers = append(*headers, [2]string{"Authorization", "Basic " + enc})
		return nil
	case "apikey":
		val, err := r.Resolve(scheme.Value)
		if err != nil {
			return err
		}
		if scheme.Name == "" {
			return configErrorf("api key auth requires a name")
		}
		switch strings.ToLower(scheme.In) {
		case "header", "":
			*headers = append(*headers, [2]string{scheme.Name, val})
		case "query":
			*query = append(*query, [2]string{scheme.Name, val})
		case "cookie":
			*headers = append(*headers, [2]string{"Cookie", scheme.Name + "=" + val})
		default:
			return configErrorf("api key auth 'in' must be header, query or cookie, got %q", scheme.In)
		}
		return nil
	default:
		return configErrorf("unsupported auth type %q", scheme.Type)
	}
}

// validateAuthScheme checks an auth scheme's required fields without resolving
// any secret values.
func validateAuthScheme(s *AuthScheme) error {
	if s == nil {
		return nil
	}
	if s.SchemaVersion != 0 && s.SchemaVersion != SupportedSchemaVersion {
		return fmt.Errorf("unsupported auth schemaVersion %d", s.SchemaVersion)
	}
	switch strings.ToLower(s.Type) {
	case "", "none", "bearer":
		return nil
	case "basic":
		return nil
	case "apikey":
		if s.Name == "" {
			return fmt.Errorf("api key auth requires a name")
		}
		switch strings.ToLower(s.In) {
		case "", "header", "query", "cookie":
			return nil
		default:
			return fmt.Errorf("api key auth 'in' must be header, query or cookie")
		}
	default:
		return fmt.Errorf("unsupported auth type %q", s.Type)
	}
}
