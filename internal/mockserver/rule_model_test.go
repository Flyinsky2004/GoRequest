package mockserver

import (
	"reflect"
	"testing"

	"gorequest/internal/core"
)

// C01 phase 2: the rule editor drafts against deep clones, so these tests
// pin the independence guarantees the desktop editor relies on: mutating the
// original (or the clone) must never leak into the other copy, including
// pointer predicates, slices, maps, variants, and CORS data.
//
// NOTE: per the current task constraints these tests were authored but NOT
// executed; add them to the post-merge verification run:
//
//	go test -race ./internal/mockserver/...

func sampleRuleForClone() MockRule {
	enabled := false
	status := "beta"
	return MockRule{
		ID:          "orders-get",
		Name:        "Get orders",
		Description: "sample",
		Enabled:     &enabled,
		Priority:    40,
		Scenarios:   []string{"default", "success"},
		Match: RuleMatch{
			Method: "GET",
			Path:   "/v1/orders/{orderId}",
			PathParams: map[string]ValueMatcher{
				"orderId": {Prefix: &status},
			},
			Query: []KeyValueMatcher{
				{Name: "expand", Equals: &status},
				{Name: "x-kind", IgnoreCase: true, In: []string{"a", "b"}},
			},
			Headers: []KeyValueMatcher{
				{Name: "x-client", Equals: &status, NotEquals: &status},
			},
			Body: &BodyMatcher{
				JSONFields: []JSONFieldMatcher{
					{Path: "user.id", Equals: &status, Type: "string"},
				},
			},
		},
		Response: RuleResponse{
			Status:      200,
			ContentType: "application/json",
			Headers:     []core.KV{{Name: "X-A", Value: "1"}},
			Body:        "{}",
			DelayMs:     12,
		},
		Variants: map[string]RuleResponse{
			"error_400": {
				Status:       400,
				Headers:      []core.KV{{Name: "X-B", Value: "2"}},
				DelayRangeMs: &DelayRange{Min: 5, Max: 9},
			},
		},
	}
}

func TestCloneMockRuleIsDeepAndIndependent(t *testing.T) {
	orig := sampleRuleForClone()
	clone := CloneMockRule(orig)

	// Mutate every pointer/slice/map surface of the original.
	*orig.Enabled = true
	orig.Scenarios[0] = "mutated"
	orig.Match.Query[1].In[0] = "mutated"
	orig.Match.Headers[0].Equals = nil
	orig.Match.Body.JSONFields[0].Path = "mutated"
	orig.Response.Headers[0].Value = "mutated"
	orig.Variants["error_400"].Status = 500
	orig.Variants["error_400"].DelayRangeMs.Max = 999
	orig.Match.PathParams["orderId"].Prefix = nil

	if clone.Enabled == nil || *clone.Enabled != false {
		t.Fatalf("clone enabled pointer aliased: %+v", clone.Enabled)
	}
	if clone.Scenarios[0] != "default" {
		t.Fatalf("clone scenarios aliased: %v", clone.Scenarios)
	}
	if clone.Match.Query[1].In[0] != "a" {
		t.Fatalf("clone query In aliased: %v", clone.Match.Query[1].In)
	}
	if clone.Match.Headers[0].Equals == nil || *clone.Match.Headers[0].Equals != "beta" {
		t.Fatalf("clone header predicates aliased: %+v", clone.Match.Headers[0])
	}
	if clone.Match.Body.JSONFields[0].Path != "user.id" {
		t.Fatalf("clone jsonFields aliased: %+v", clone.Match.Body.JSONFields)
	}
	if clone.Response.Headers[0].Value != "1" {
		t.Fatalf("clone response headers aliased: %+v", clone.Response.Headers)
	}
	if clone.Variants["error_400"].Status != 400 || clone.Variants["error_400"].DelayRangeMs.Max != 9 {
		t.Fatalf("clone variants aliased: %+v", clone.Variants["error_400"])
	}
	if clone.Match.PathParams["orderId"].Prefix == nil || *clone.Match.PathParams["orderId"].Prefix != "beta" {
		t.Fatalf("clone path params aliased: %+v", clone.Match.PathParams)
	}
}

func TestCloneMockFilePreservesEverythingIndependently(t *testing.T) {
	orig := &MockFile{
		SchemaVersion:   1,
		ID:              "orders-service",
		Name:            "Orders",
		DefaultScenario: "default",
		Variables:       map[string]string{"region": "ap-east"},
		CORS:            &CORSConfig{AllowOrigins: []string{"https://app.local"}, MaxAgeSeconds: 120},
		Rules:           []MockRule{sampleRuleForClone()},
		SourceRelPath:   "mocks/orders-service.mock.yaml",
		SourceDigest:    "digest-1",
	}
	clone := CloneMockFile(orig)

	orig.Variables["region"] = "mutated"
	orig.CORS.AllowOrigins[0] = "mutated"
	delete(orig.Rules[0].Variants, "error_400")
	orig.Rules = orig.Rules[:0]
	orig.SourceDigest = "mutated"

	if clone.Variables["region"] != "ap-east" {
		t.Fatalf("clone variables aliased: %v", clone.Variables)
	}
	if clone.CORS == nil || clone.CORS.AllowOrigins[0] != "https://app.local" || clone.CORS.MaxAgeSeconds != 120 {
		t.Fatalf("clone CORS aliased: %+v", clone.CORS)
	}
	if len(clone.Rules) != 1 {
		t.Fatalf("clone rules aliased: %d rules", len(clone.Rules))
	}
	if _, ok := clone.Rules[0].Variants["error_400"]; !ok {
		t.Fatalf("clone rule variants were dropped: %+v", clone.Rules[0].Variants)
	}
	if !reflect.DeepEqual(clone.Rules[0].Scenarios, []string{"default", "success"}) {
		t.Fatalf("clone rule scenarios unexpected: %v", clone.Rules[0].Scenarios)
	}
	if clone.SourceDigest != "digest-1" || clone.SourceRelPath != "mocks/orders-service.mock.yaml" {
		t.Fatalf("clone source metadata lost: %q %q", clone.SourceDigest, clone.SourceRelPath)
	}
}

func TestCloneMockFileNilAndEmptyMatchersRoundTrip(t *testing.T) {
	if CloneMockFile(nil) != nil {
		t.Fatal("CloneMockFile(nil) must be nil")
	}
	clone := CloneMockFile(&MockFile{
		ID:    "e",
		Name:  "E",
		Rules: []MockRule{{ID: "r", Priority: 1, Match: RuleMatch{Method: "GET", Path: "/x"}, Response: RuleResponse{Body: "b"}}},
	})
	got := clone.Rules[0].Match
	if got.Method != "GET" || got.Path != "/x" || got.Body != nil || got.Query != nil || got.Headers != nil || got.PathParams != nil {
		t.Fatalf("empty matchers must round-trip nil: %+v", got)
	}
	if clone.Rules[0].Variants != nil || clone.Rules[0].Response.Headers != nil || clone.Rules[0].Response.DelayRangeMs != nil {
		t.Fatalf("empty response data must round-trip nil: %+v", clone.Rules[0].Response)
	}
}
