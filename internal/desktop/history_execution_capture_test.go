package desktop

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorequest/internal/core"
)

func TestFullHistoryPreservesRealStreamThroughExecutionAndReload(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		full, enableAfterRun bool
	}{{"redacted", false, false}, {"full", true, false}, {"enable-after-send", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			a, ps, store := newHistoryTestApp(t)
			a.mgr.cfg.setHistoryFullCapture(tc.full)
			a.mgr.cfg.setHistorySaveBodies(true)
			tab := openRequestTabs(t, ps, "Stream capture")[0]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: desktop-stream-secret\n\n")
			}))
			defer server.Close()
			red := core.NewRedactor()
			red.RegisterSecret("desktop-stream-secret")
			if opts := a.buildExecuteOptionsForTab(ps, tab, tab.Entry.Request, nil, red); opts.UnredactedResult {
				t.Fatal("shared Runner/diagnostics options must retain redacted outputs")
			}
			plan := &core.Plan{Method: "GET", URL: server.URL}
			res, err := a.executeTabPlan(context.Background(), tab, tab.Entry.Request, plan, red)
			if err != nil {
				t.Fatal(err)
			}
			if tc.enableAfterRun {
				a.mgr.cfg.setHistoryFullCapture(true)
			}
			a.recordHistory(tab, plan, red, res)
			records := store.load(historyKey(ps))
			if len(records) != 1 || records[0].Response == nil {
				t.Fatal("stream response was not persisted")
			}
			raw, err := records[0].Response.BodyBytes()
			if err != nil {
				t.Fatal(err)
			}
			if records[0].IsUnredacted() != tc.full || strings.Contains(string(raw), "desktop-stream-secret") != tc.full {
				t.Fatalf("full=%v: persisted stream=%q", tc.full, raw)
			}
		})
	}
}
