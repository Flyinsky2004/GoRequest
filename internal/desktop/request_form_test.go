package desktop

import (
	"strings"
	"testing"

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
