package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/localstate"
)

func TestHistoryEndpointDisplay(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"path query fragment", "https://api.test/v1/users?page=2#top", "/v1/users?page=2#top"},
		{"ipv6 and port", "http://[::1]:8080/api/items?x=1", "/api/items?x=1"},
		{"root without path", "https://api.test", "/"},
		{"root with query", "https://api.test?x=1", "/?x=1"},
		{"escaped path query fragment", "https://api.test/a%2Fb?x=%20#f%20", "/a%2Fb?x=%20#f%20"},
		{"user info stripped", "https://user:pw@api.test/x", "/x"},
		{"relative kept", "/api/users?x=1#f", "/api/users?x=1#f"},
		{"scheme-relative", "//api.test/x?y=1", "/x?y=1"},
		{"opaque kept", "mailto:user@example.com", "mailto:user@example.com"},
		{"invalid kept", "http://[::1", "http://[::1"},
		{"empty", "", ""},
		{"plain word kept", "not-a-url", "not-a-url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := historyEndpoint(tc.raw); got != tc.want {
				t.Fatalf("historyEndpoint(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
	if got := historyDisplayURL("https://api.test/x?y=1", false); got != "https://api.test/x?y=1" {
		t.Fatalf("full display must keep the stored URL, got %q", got)
	}
}

func TestHistoryEndpointOnlySurvivesRefreshFilterAndRestore(t *testing.T) {
	a, ps, hs := newHistoryTestApp(t)
	tab := openRequestTabs(t, ps, "Login")[0]
	full := "https://api.test/v1/users?page=2#top"
	ares, err := hs.appendRecord(historyKey(ps), historyRecord{
		Ts: time.Now(), ProjectKey: historyKey(ps), ProjectName: "History",
		RequestID: tab.Entry.Request.ID, RequestName: "Login", Method: "GET",
		URL: full, Status: 200, Outcome: localstate.OutcomeOK,
	})
	if err != nil {
		t.Fatal(err)
	}

	a.historyView = true
	a.historyUI = historyUIState{endpointOnly: true}
	a.historyKey = historyKey(ps)
	a.reloadHistory()
	if len(a.historyRecords) != 1 {
		t.Fatalf("records = %d, want 1", len(a.historyRecords))
	}
	if !a.historyUI.endpointOnly {
		t.Fatal("refresh must keep the endpoint-only switch")
	}

	// The filter and panel visibility are view state around the same switch.
	a.historyUI.timeRange = 1
	if got := len(a.visibleHistory()); got != 1 {
		t.Fatalf("filtered records = %d, want 1", got)
	}
	a.historyView = false
	a.historyView = true
	if !a.historyUI.endpointOnly {
		t.Fatal("hiding and reopening the panel must keep the endpoint-only switch")
	}

	// Restoring is not a display path: it must write the full stored URL.
	a.restoreHistory(ares.Record)
	if tab.URL != full {
		t.Fatalf("restore used %q, want the full URL %q", tab.URL, full)
	}
	if !a.historyUI.endpointOnly {
		t.Fatal("restoring must not change the display switch")
	}
}

func historyDisplayTestApp(t *testing.T) (*App, *projectState, *historyStore, string) {
	t.Helper()
	a, ps, hs := newHistoryTestApp(t)
	tab := openRequestTabs(t, ps, "Login")[0]
	full := "https://api.test/v1/users?page=2#top"
	if _, err := hs.appendRecord(historyKey(ps), historyRecord{
		Ts: time.Now(), ProjectKey: historyKey(ps), ProjectName: "History",
		RequestID: tab.Entry.Request.ID, RequestName: "Login", Method: "GET",
		URL: full, Status: 200, Outcome: localstate.OutcomeOK,
	}); err != nil {
		t.Fatal(err)
	}
	a.historyView = true
	a.historyUI = historyUIState{endpointOnly: true}
	a.historyKey = historyKey(ps)
	a.reloadHistory()
	return a, ps, hs, full
}

func TestHistoryRowActionsDoNotRestoreOrCrossFire(t *testing.T) {
	t.Run("details", func(t *testing.T) {
		a, _, _, full := historyDisplayTestApp(t)
		tt := ui.NewTester(a.View, 1200, 860)
		tt.Frame()
		if !tt.HasText("/v1/users?page=2#top") {
			t.Fatalf("endpoint text missing; texts=%v", tt.Texts())
		}
		if tt.HasText(full) {
			t.Fatalf("endpoint mode still shows the full URL; texts=%v", tt.Texts())
		}
		if err := tt.Click("Details"); err != nil {
			t.Fatal(err)
		}
		if !a.historyUI.detailOpen {
			t.Fatal("details action did not open the detail modal")
		}
		if !a.historyView {
			t.Fatal("details action must not restore the request")
		}
		if len(a.historyRecords) != 1 {
			t.Fatalf("details action deleted the record: %d left", len(a.historyRecords))
		}
	})

	t.Run("delete", func(t *testing.T) {
		a, ps, hs, _ := historyDisplayTestApp(t)
		tt := ui.NewTester(a.View, 1200, 860)
		tt.Frame()
		if err := tt.Click("Delete"); err != nil {
			t.Fatal(err)
		}
		if !a.historyView {
			t.Fatal("delete action must not restore the request")
		}
		if a.historyUI.detailOpen {
			t.Fatal("delete action must not open the detail modal")
		}
		if len(a.historyRecords) != 0 {
			t.Fatalf("records after delete = %d, want 0", len(a.historyRecords))
		}
		left, err := hs.loadFiltered(historyKey(ps), localstate.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(left.Records) != 0 {
			t.Fatalf("record was not removed from disk: %+v", left.Records)
		}
	})
}

func TestHistoryRowClickStillRestoresFullURL(t *testing.T) {
	a, _, _, full := historyDisplayTestApp(t)
	tt := ui.NewTester(a.View, 1200, 860)
	tt.Frame()
	if err := tt.Click("/v1/users?page=2#top"); err != nil {
		t.Fatal(err)
	}
	if a.historyView {
		t.Fatal("clicking the row should restore and leave the history view")
	}
	tab := a.ps.activeTab()
	if tab == nil || tab.URL != full {
		t.Fatalf("row restore used %q, want %q", tabURL(tab), full)
	}
}

func tabURL(tab *Tab) string {
	if tab == nil {
		return ""
	}
	return tab.URL
}

func TestHistoryEndpointToggleAvailableInEmptyAndAllProjectViews(t *testing.T) {
	a, ps, _ := newHistoryTestApp(t)
	a.historyView = true
	a.historyKey = historyKey(ps)
	a.historyRecords = nil
	tt := ui.NewTester(a.View, 1200, 860)
	tt.Frame()
	if !tt.HasText("Endpoint only") {
		t.Fatalf("endpoint-only switch missing from the empty list; texts=%v", tt.Texts())
	}

	a.historyUI = historyUIState{allProjects: true}
	a.reloadHistory()
	tt.Frame()
	if !tt.HasText("Endpoint only") {
		t.Fatalf("endpoint-only switch missing from the all-project view; texts=%v", tt.Texts())
	}
}
