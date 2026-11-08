package desktop

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

func TestDisplayDurationUnits(t *testing.T) {
	for _, tc := range []struct {
		ms   float64
		want string
	}{
		{-1, "0ms"}, {0, "0ms"}, {0.001, "<0.01ms"}, {0.01, "0.01ms"},
		{4.2, "4.2ms"}, {999.99, "999.99ms"}, {999.999, "1s"},
		{1000, "1s"}, {1429, "1.43s"}, {30000, "30s"}, {59994, "59.99s"},
		{59999, "1m"}, {60000, "1m"}, {90000, "1m 30s"},
		{3599999, "1h"}, {3600000, "1h"}, {3661000, "1h 1m 1s"},
		{86400000, "24h"}, {math.NaN(), "—"}, {math.Inf(1), "—"},
	} {
		t.Run(fmt.Sprint(tc.ms), func(t *testing.T) {
			if got := formatDurationMs(tc.ms); got != tc.want {
				t.Fatalf("formatDurationMs(%v) = %q, want %q", tc.ms, got, tc.want)
			}
		})
	}
	if got := formatDuration(30 * time.Second); got != "30s" {
		t.Fatalf("duration input = %q", got)
	}
	if got := formatObservedDuration(30*time.Second, false, "reused / n/a"); got != "reused / n/a" {
		t.Fatalf("unobserved duration became a measurement: %q", got)
	}
}

func TestDisplayByteUnits(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{-1, "—"}, {0, "0 B"}, {1, "1 B"}, {1023, "1023 B"},
		{1024, "1 KiB"}, {1429, "1.4 KiB"}, {4096, "4 KiB"},
		{1<<20 - 1, "1 MiB"}, {1 << 20, "1 MiB"}, {1536 << 10, "1.5 MiB"},
		{1 << 30, "1 GiB"}, {1 << 40, "1 TiB"}, {1 << 50, "1 PiB"},
		{1 << 60, "1 EiB"}, {math.MaxInt64, "8 EiB"},
	} {
		t.Run(fmt.Sprint(tc.bytes), func(t *testing.T) {
			if got := formatByteSize(tc.bytes); got != tc.want {
				t.Fatalf("formatByteSize(%d) = %q, want %q", tc.bytes, got, tc.want)
			}
		})
	}
}

func TestDisplayDurationDeltaPreservesSign(t *testing.T) {
	for ms, want := range map[float64]string{-30000: "-30s", 30000: "+30s", 0: "0ms"} {
		if got := formatDurationDeltaMs(ms); got != want {
			t.Errorf("duration delta %v = %q, want %q", ms, got, want)
		}
	}
}

func TestDisplaySendStatusUnitsInBothLanguages(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		m := layoutStatusMessagesFor(lang)
		for _, status := range []string{
			translate(m.SendDoneFmt, 200, formatDuration(30*time.Second)),
			translate(m.SendFailedFmt, formatDuration(30*time.Second), "offline"),
			translate(m.SendCanceledFmt, formatDuration(30*time.Second)),
		} {
			if !strings.Contains(status, "30s") || strings.Contains(status, "%!") {
				t.Errorf("%s: unexpected terminal status %q", lang, status)
			}
		}
	}
}

func TestDisplayResponseUsesReadableUnits(t *testing.T) {
	a, ps := openTestProject(t, "Readable response units")
	tab := openRequestTabs(t, ps, "Readable")[0]
	tab.Resp = &core.ExecResult{
		Status: 200, Proto: "HTTP/2.0", Duration: 30 * time.Second,
		Body: []byte("ok"), BodySize: 1429,
	}
	tab.respVersion++
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("30s · 1.4 KiB") {
		t.Fatalf("response did not format duration and size: %v", tt.Texts())
	}
	if tab.Resp.Duration != 30*time.Second || tab.Resp.BodySize != 1429 {
		t.Fatal("display formatting changed response measurements")
	}
}

func TestDisplayHistoryUsesReadableUnits(t *testing.T) {
	a, ps, _ := newHistoryTestApp(t)
	a.historyView = true
	a.historyKey = historyKey(ps)
	a.historyRecords = []historyRecord{{
		Ts: time.Now(), ID: "readable-units", RequestName: "Readable",
		Method: "GET", URL: "https://example.test/", Status: 200,
		DurationMs: 30000, SizeBytes: 1429,
	}}
	tt := ui.NewTester(a.View, 1100, 860)
	tt.Frame()
	if !tt.HasText("30s") {
		t.Fatalf("history row did not format elapsed time: %v", tt.Texts())
	}
	a.historyUI = historyUIState{detailOpen: true, detail: a.historyRecords[0]}
	tt.Frame()
	if !tt.HasText("200 · 30s · 1.4 KiB") {
		t.Fatalf("history detail did not format elapsed time and size: %v", tt.Texts())
	}
}
