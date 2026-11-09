package core

import (
	"strings"
	"testing"
)

func TestGraphQLCompoundVariableKeepsUnderlyingSecretRedaction(t *testing.T) {
	const secret = "test-secret-original-47382"
	req := graphQLRequest("POST", "query Q { q }", "", `{"value":"prefix-{{secret.token}}-suffix"}`)
	_, red, err := BuildTrackedPlan(req, nil, MapSecrets{"token": secret}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, echo := range []string{secret, "prefix-" + secret + "-suffix"} {
		if strings.Contains(red.RedactString(echo), secret) {
			t.Fatal("GraphQL response can expose a component of a compound secret")
		}
	}
}
