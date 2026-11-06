package core

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// APIKeyLocations lists the valid transport positions for API Key auth.
var APIKeyLocations = []string{"header", "query", "cookie"}

// NormalizeAPIKeyLocation returns the canonical lowercase location ("header",
// "query", or "cookie"), defaulting an empty location to "header".
func NormalizeAPIKeyLocation(in string) string {
	v := strings.ToLower(strings.TrimSpace(in))
	if v == "" {
		return "header"
	}
	return v
}

// ValidateAPIKeyLocation validates and normalizes an API Key location.
func ValidateAPIKeyLocation(in string) (string, error) {
	v := NormalizeAPIKeyLocation(in)
	switch v {
	case "header", "query", "cookie":
		return v, nil
	default:
		return "", configErrorf("api key auth 'in' must be header, query or cookie, got %q", in)
	}
}

// CloneAuthScheme returns a deep copy of s, or nil when s is nil.
func CloneAuthScheme(s *AuthScheme) *AuthScheme {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// CloneAuthRef returns a deep copy of a, or nil when a is nil.
func CloneAuthRef(a *AuthRef) *AuthRef {
	if a == nil {
		return nil
	}
	cp := *a
	cp.Inline = CloneAuthScheme(a.Inline)
	return &cp
}

// applyAuth resolves the auth scheme against the resolver and mutates the
// header/query collections of a plan. It never writes secret values anywhere
// except the in-memory plan.
func applyAuth(scheme *AuthScheme, r *Resolver, headers *[][2]string, query *[][2]string) error {
	if scheme == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(scheme.Type)) {
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
		if strings.ContainsAny(tok, "\r\n") {
			return configErrorf("bearer token contains a newline; refusing header injection")
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
		if strings.TrimSpace(scheme.Name) == "" {
			return configErrorf("api key auth requires a name")
		}
		keyName, err := r.Resolve(scheme.Name)
		if err != nil {
			return err
		}
		keyName = strings.TrimSpace(keyName)
		if keyName == "" {
			return configErrorf("api key auth requires a name")
		}
		val, err := r.Resolve(scheme.Value)
		if err != nil {
			return err
		}
		loc, err := ValidateAPIKeyLocation(scheme.In)
		if err != nil {
			return err
		}
		switch loc {
		case "header":
			if strings.ContainsAny(keyName, "\r\n") || strings.ContainsAny(val, "\r\n") {
				return configErrorf("api key header %q contains a newline; refusing header injection", keyName)
			}
			*headers = append(*headers, [2]string{keyName, val})
		case "query":
			*query = append(*query, [2]string{keyName, val})
		case "cookie":
			if strings.ContainsAny(keyName, "\r\n;=") || strings.ContainsAny(val, "\r\n;") {
				return configErrorf("api key cookie %q contains invalid cookie characters; refusing injection", keyName)
			}
			appendCookiePair(headers, keyName+"="+val)
		}
		return nil
	default:
		return configErrorf("unsupported auth type %q", scheme.Type)
	}
}

func appendCookiePair(headers *[][2]string, pair string) {
	for i := range *headers {
		if strings.EqualFold((*headers)[i][0], "Cookie") {
			if strings.TrimSpace((*headers)[i][1]) == "" {
				(*headers)[i][1] = pair
			} else {
				(*headers)[i][1] = (*headers)[i][1] + "; " + pair
			}
			return
		}
	}
	*headers = append(*headers, [2]string{"Cookie", pair})
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
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "", "none", "bearer":
		return nil
	case "basic":
		return nil
	case "apikey":
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("api key auth requires a name")
		}
		switch strings.ToLower(strings.TrimSpace(s.In)) {
		case "", "header", "query", "cookie":
			return nil
		default:
			return fmt.Errorf("api key auth 'in' must be header, query or cookie")
		}
	default:
		return fmt.Errorf("unsupported auth type %q", s.Type)
	}
}
