package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

func kvEnabled(kv core.KV) bool { return kv.IsEnabled() }

func TestRowsRoundTripPreservesOrderDuplicatesAndValues(t *testing.T) {
	original := []core.KV{
		{Name: "page", Value: "2"},
		{Name: "tag", Value: "a=b&c:d"},          // equals, ampersand and colon kept verbatim
		{Name: "q", Value: "hello world"},        // meaningful internal spaces
		{Name: "id", Value: "{{user.id}}"},       // variable template
		{Name: "token", Value: "{{secret.api}}"}, // secret template
		{Name: "dup", Value: "1"},
		{Name: "dup", Value: "2"},                               // duplicate key preserved
		{Name: "empty", Value: ""},                              // empty value preserved
		{Name: "off", Value: "x", Enabled: core.BoolPtr(false)}, // disabled row
	}

	rows := rowsFromKV(original)
	got, err := kvFromRows(rows)
	if err != nil {
		t.Fatalf("kvFromRows: %v", err)
	}
	if len(got) != len(original) {
		t.Fatalf("row count = %d, want %d", len(got), len(original))
	}
	for i := range original {
		if got[i].Name != original[i].Name {
			t.Errorf("row %d name = %q, want %q", i, got[i].Name, original[i].Name)
		}
		if got[i].Value != original[i].Value {
			t.Errorf("row %d value = %q, want %q", i, got[i].Value, original[i].Value)
		}
		if kvEnabled(got[i]) != kvEnabled(original[i]) {
			t.Errorf("row %d enabled = %v, want %v", i, kvEnabled(got[i]), kvEnabled(original[i]))
		}
	}
}

func TestRowsFromKVKeepsDisabledRowsVisible(t *testing.T) {
	rows := rowsFromKV([]core.KV{{Name: "off", Value: "x", Enabled: core.BoolPtr(false)}})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (data + trailing blank)", len(rows))
	}
	if rows[0].Enabled {
		t.Error("disabled stored row must stay disabled and visible")
	}
	if !rows[1].isBlank() || !rows[1].Enabled {
		t.Error("expected an enabled blank trailing row")
	}
}

func TestKVFromRowsDropsBlankRows(t *testing.T) {
	rows := []*KVRow{
		{ID: "1", Enabled: true, Name: "a", Value: "1"},
		{ID: "2", Enabled: true},               // blank trailing
		{ID: "3", Enabled: true, Value: "   "}, // whitespace-only is blank
	}
	got, err := kvFromRows(rows)
	if err != nil {
		t.Fatalf("kvFromRows: %v", err)
	}
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("got %+v, want only the real row", got)
	}
}

func TestKVFromRowsRejectsValueWithoutKey(t *testing.T) {
	rows := []*KVRow{{ID: "1", Enabled: true, Value: "only-value"}}
	if _, err := kvFromRows(rows); err == nil {
		t.Fatal("expected an error for a value without a key")
	} else if !strings.Contains(err.Error(), "no key") {
		t.Fatalf("error = %q, want mention of missing key", err)
	}
}

func TestKVFromRowsKeepsKeyWithEmptyValue(t *testing.T) {
	got, err := kvFromRows([]*KVRow{{ID: "1", Enabled: true, Name: "flag", Value: ""}})
	if err != nil {
		t.Fatalf("kvFromRows: %v", err)
	}
	if len(got) != 1 || got[0].Name != "flag" || got[0].Value != "" {
		t.Fatalf("got %+v, want one row flag=\"\"", got)
	}
}

func TestSuggestionsAreSeparated(t *testing.T) {
	headers := suggestionsFor(kvHeader)
	query := suggestionsFor(kvQuery)

	for _, want := range []string{"Accept", "Authorization", "Content-Type", "User-Agent", "Cache-Control", "Accept-Language", "Accept-Encoding", "Origin", "Referer", "X-Request-ID", "X-API-Key"} {
		if !contains(headers, want) {
			t.Errorf("header suggestions missing %q", want)
		}
	}
	for _, want := range []string{"page", "page_size", "limit", "offset", "q", "search", "sort", "order", "filter", "fields"} {
		if !contains(query, want) {
			t.Errorf("query suggestions missing %q", want)
		}
	}
	if contains(headers, "page") {
		t.Error("header suggestions must not contain query keys")
	}
	if contains(query, "Authorization") || contains(query, "Content-Type") {
		t.Error("query suggestions must not contain HTTP header names")
	}
}

func TestValueSuggestionsOnlyForContentTypeAndAccept(t *testing.T) {
	if got := valueSuggestions("Content-Type", kvHeader); !contains(got, "application/json") {
		t.Errorf("Content-Type value suggestions = %v", got)
	}
	if got := valueSuggestions("accept", kvHeader); len(got) == 0 {
		t.Error("Accept value suggestions should exist (case-insensitive)")
	}
	if got := valueSuggestions("X-Custom", kvHeader); got != nil {
		t.Errorf("unexpected value suggestions: %v", got)
	}
	if got := valueSuggestions("Content-Type", kvQuery); got != nil {
		t.Errorf("query must not offer header value suggestions: %v", got)
	}
}

func TestFilterSuggestions(t *testing.T) {
	opts := []string{"text/plain", "application/json", "xapplication", "application/xml"}

	if got := filterSuggestions(opts, ""); len(got) != len(opts) {
		t.Fatalf("blank query = %v, want all", got)
	}
	if got := filterSuggestions(opts, "APP"); len(got) == 0 {
		t.Fatal("filtering must ignore case")
	}
	// Prefix matches come before mere substring matches.
	got := filterSuggestions(opts, "app")
	wantOrder := []string{"application/json", "application/xml", "xapplication"}
	if strings.Join(got, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("order = %v, want %v", got, wantOrder)
	}
	if got := filterSuggestions(opts, "zzz"); len(got) != 0 {
		t.Fatalf("no-match = %v, want empty", got)
	}
}

func TestRowIdentityStableOnRemove(t *testing.T) {
	tab := &Tab{QueryRows: []*KVRow{
		{ID: "r1", Enabled: true, Name: "a"},
		{ID: "r2", Enabled: true, Name: "b"},
		{ID: "r3", Enabled: true, Name: "c"},
	}}
	tab.removeRow(kvQuery, "r2")
	got := tab.rows(kvQuery)
	if len(got) != 2 || got[0].ID != "r1" || got[1].ID != "r3" {
		t.Fatalf("after remove: %+v, want ids r1,r3", got)
	}
}

func TestEnsureTrailingBlankIdempotent(t *testing.T) {
	tab := &Tab{QueryRows: []*KVRow{{ID: "x", Enabled: true, Name: "a"}}}
	tab.ensureTrailingBlank(kvQuery)
	tab.ensureTrailingBlank(kvQuery)
	rows := tab.rows(kvQuery)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (data + one blank)", len(rows))
	}
	if !rows[len(rows)-1].isBlank() {
		t.Error("last row should be blank")
	}
}

func TestAddRowAppendsBlank(t *testing.T) {
	tab := &Tab{}
	tab.addRow(kvHeader)
	if len(tab.HeaderRows) != 1 || !tab.HeaderRows[0].isBlank() {
		t.Fatalf("HeaderRows = %+v, want one blank", tab.HeaderRows)
	}
	if len(tab.QueryRows) != 0 {
		t.Error("adding a header row must not touch query rows")
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func TestQueryRawEncodedPasteAndPreviewNoDoubleEncoding(t *testing.T) {
	rawInput := "?q=hello%20world&expr=a%2Bb&dup=1&dup=2&empty=&apiKey={{secret.api}}\n# debug=true"
	rows, err := rawQueryToRows(rawInput)
	if err != nil {
		t.Fatalf("rawQueryToRows: %v", err)
	}
	// 7 data rows (6 enabled + 1 disabled) + 1 trailing blank row
	if len(rows) != 8 {
		t.Fatalf("rows len = %d, want 8", len(rows))
	}
	if rows[0].Name != "q" || rows[0].Value != "hello world" {
		t.Fatalf("row[0] = %+v, want decoded 'hello world'", rows[0])
	}
	if rows[1].Name != "expr" || rows[1].Value != "a+b" {
		t.Fatalf("row[1] = %+v, want decoded 'a+b'", rows[1])
	}
	if rows[4].Name != "empty" || rows[4].Value != "" {
		t.Fatalf("row[4] = %+v, want empty value preserved", rows[4])
	}
	if rows[5].Name != "apiKey" || rows[5].Value != "{{secret.api}}" {
		t.Fatalf("row[5] = %+v, want template preserved", rows[5])
	}
	if rows[6].Name != "debug" || rows[6].Enabled {
		t.Fatalf("row[6] = %+v, want disabled debug row", rows[6])
	}
	if !rows[7].isBlank() {
		t.Fatalf("expected trailing blank row")
	}

	// Re-serializing back to Raw Encoded must NOT double-encode "%20" or "%2B"!
	reformatted := rowsToRawQuery(rows)
	wantRaw := "q=hello+world&expr=a%2Bb&dup=1&dup=2&empty=&apiKey={{secret.api}}\n# debug=true"
	if reformatted != wantRaw {
		t.Fatalf("rowsToRawQuery = %q, want %q", reformatted, wantRaw)
	}
}

func TestKVFormQueryRawEncodedUIAndWireExecution(t *testing.T) {
	var gotQuery url.Values
	var gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		gotQuery = r.URL.Query()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a, ps := openTestProject(t, "Query Raw UI")
	a.mgr.secrets = staticSecrets{"api": "TOP_SECRET_QUERY_KEY"}

	tab := openRequestTabs(t, ps, "Search")[0]
	tab.URL = srv.URL + "/items"
	tab.Section = 0 // Params
	tab.QueryRows = rowsFromKV([]core.KV{
		{Name: "q", Value: "hello world"},
		{Name: "tag", Value: "a+b"},
		{Name: "tag", Value: "2"},
		{Name: "empty", Value: ""},
		{Name: "key", Value: "{{secret.api}}"},
	})

	tt := ui.NewTester(a.View, 1240, 820)
	tt.Frame()

	// Secret must never appear in cleartext in Resolved URL preview
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "TOP_SECRET_QUERY_KEY") {
			t.Fatalf("secret leaked in Params URL preview: %q", txt)
		}
	}
	if !tt.HasText("Resolved URL:") {
		t.Fatalf("expected Resolved URL preview in Params view; texts=%v", tt.Texts())
	}

	// Switch Key:Value -> Raw Encoded -> Key:Value through UI controls
	if err := tt.Click("Raw Encoded"); err != nil {
		t.Fatalf("click Raw Encoded: %v", err)
	}
	if tab.queryRaw != "q=hello+world&tag=a%2Bb&tag=2&empty=&key={{secret.api}}" {
		t.Fatalf("unexpected queryRaw in Raw Encoded mode: %q", tab.queryRaw)
	}
	if err := tt.Click("Key:Value"); err != nil {
		t.Fatalf("click Key:Value: %v", err)
	}

	// Execute against httptest.Server and verify single encoding on the wire
	_, plan, err := a.buildTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTabPlan: %v", err)
	}
	if _, err := core.Execute(context.Background(), plan, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(gotRawQuery, "%25") {
		t.Fatalf("double encoding detected on wire: %q", gotRawQuery)
	}
	if gotQuery.Get("q") != "hello world" || gotQuery["tag"][0] != "a+b" || gotQuery["tag"][1] != "2" {
		t.Fatalf("decoded server query mismatch: %v", gotQuery)
	}
	if vals, ok := gotQuery["empty"]; !ok || len(vals) != 1 || vals[0] != "" {
		t.Fatalf("empty query param lost: %v", gotQuery)
	}
	if gotQuery.Get("key") != "TOP_SECRET_QUERY_KEY" {
		t.Fatalf("secret query param mismatch: %v", gotQuery)
	}
}


