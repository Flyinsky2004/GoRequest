package desktop

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

// FIX08 owns network_ui.go plus these dedicated tests. All fixtures use
// t.TempDir() and the in-memory NetworkStore; nothing writes to a real device
// config, real keychain, or the user's sample project. No public network is
// contacted: slow services are httptest.NewServer on 127.0.0.1.

// --- timeout resolution priority (mirrors core plan.go + BuildTrackedPlan) ---

func TestResolveRequestTimeoutPriority(t *testing.T) {
	// Verified from code: positive device TotalTimeout > request > 10s default;
	// project defaults.timeout is written but never read by any plan builder.
	cases := []struct {
		name        string
		raw         string
		deviceTotal time.Duration
		wantEff     time.Duration
		wantOrigin  timeoutOrigin
		wantValid   bool
	}{
		{"blank uses built-in 10s default", "", 0, core.DefaultTimeout, timeoutOriginDefault, true},
		{"whitespace-only uses built-in 10s default", "   ", 0, core.DefaultTimeout, timeoutOriginDefault, true},
		{"request value", "250ms", 0, 250 * time.Millisecond, timeoutOriginRequest, true},
		{"request combined units", "1m30s", 0, 90 * time.Second, timeoutOriginRequest, true},
		{"request with surrounding spaces", "  2.5s  ", 0, 2500 * time.Millisecond, timeoutOriginRequest, true},
		{"bad unit is invalid", "10", 0, core.DefaultTimeout, timeoutOriginDefault, false},
		{"prose is invalid", "10 seconds", 0, core.DefaultTimeout, timeoutOriginDefault, false},
		{"zero is invalid (silently falls back)", "0s", 0, core.DefaultTimeout, timeoutOriginDefault, false},
		{"negative is invalid", "-3s", 0, core.DefaultTimeout, timeoutOriginDefault, false},
		{"device total overrides request", "5s", 2 * time.Second, 2 * time.Second, timeoutOriginDeviceTotal, true},
		{"device total overrides default", "", 1500 * time.Millisecond, 1500 * time.Millisecond, timeoutOriginDeviceTotal, true},
		{"device total overrides an invalid request string too", "garbage", 800 * time.Millisecond, 800 * time.Millisecond, timeoutOriginDeviceTotal, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveRequestTimeout(tc.raw, tc.deviceTotal)
			if got.Effective != tc.wantEff {
				t.Errorf("Effective = %v, want %v", got.Effective, tc.wantEff)
			}
			if got.Origin != tc.wantOrigin {
				t.Errorf("Origin = %v, want %v", got.Origin, tc.wantOrigin)
			}
			if got.Valid() != tc.wantValid {
				t.Errorf("Valid() = %v, want %v", got.Valid(), tc.wantValid)
			}
		})
	}
}

// The UI copy must not claim "project inheritance": defaults.timeout exists in
// project.yaml but no plan builder reads it in this build. This test pins that
// fact so the effective-value/source display stays truthful.
func TestProjectDefaultsTimeoutIsNotAppliedByPlanBuilder(t *testing.T) {
	_, ps := openTestProject(t, "TimeoutDefaults")

	// NewProject writes defaults.timeout: 10s. Change it to a distinctive value
	// and confirm a blank request still resolves to the built-in 10s, not 42s.
	projectYAML := filepath.Join(ps.project.Dir, "project.yaml")
	data, err := os.ReadFile(projectYAML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "timeout: 10s") {
		t.Fatalf("expected a defaults.timeout in project.yaml, got:\n%s", data)
	}
	updated := strings.Replace(string(data), "timeout: 10s", "timeout: 42s", 1)
	if err := os.WriteFile(projectYAML, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-open the project so p.File.Defaults reflects the edited file.
	reopened, err := core.OpenProject(ps.project.Dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if reopened.File == nil || reopened.File.Defaults == nil ||
		strings.TrimSpace(reopened.File.Defaults.Timeout) != "42s" {
		t.Fatalf("defaults.timeout not loaded: %+v", reopened.File.Defaults)
	}

	// A brand-new request has an empty Timeout; the tracked plan builder must
	// still produce the built-in 10s default, not 42s.
	req := &core.Request{
		SchemaVersion: core.SupportedSchemaVersion,
		ID:            "blank-timeout",
		Name:          "Blank",
		HTTP:          &core.HTTPDef{Method: http.MethodGet, URL: "http://127.0.0.1/"},
	}
	plan, _, err := core.BuildTrackedPlanWithOptions(
		context.Background(), req, nil, nil, nil, nil, core.PlanOptions{})
	if err != nil {
		t.Fatalf("BuildTrackedPlanWithOptions: %v", err)
	}
	if plan.Timeout != core.DefaultTimeout {
		t.Fatalf("plan.Timeout = %v, want built-in %v (defaults.timeout must be inert)",
			plan.Timeout, core.DefaultTimeout)
	}
	if got := resolveRequestTimeout("", 0); got.Effective != core.DefaultTimeout || got.Origin != timeoutOriginDefault {
		t.Fatalf("UI resolution disagrees with the plan builder: %+v", got)
	}
}

// --- bilingual message completeness ---

func TestTimeoutMessagesCoverBothLanguages(t *testing.T) {
	// Reuses the same rule as TestNetworkMessagesCoverBothLanguages: every new
	// FIX08 field must be a real, distinct translation in BOTH catalogs.
	fields := []string{
		"RequestTimeoutHeader", "RequestTimeoutScopeHint", "RequestTimeoutPlaceholder",
		"RequestTimeoutUnitHint", "TimeoutEntryHint", "TimeoutQuickEntry",
		"TimeoutQuickEntryTip", "TimeoutQuickEntryInvalid", "TimeoutBadgeFmt",
		"TimeoutBadgeInvalid", "TimeoutEffectiveFmt",
		"TimeoutSourceRequest", "TimeoutSourceDefault", "TimeoutSourceDeviceTotal",
		"TimeoutNoTabHint", "TimeoutInvalidFmt", "TimeoutNonPositiveFmt",
		"StageTimeoutsNote", "NetworkDraftsInvalidFmt",
	}
	for _, name := range fields {
		zh, okZh := timeoutMessageField(&networkMessagesZh, name)
		en, okEn := timeoutMessageField(&networkMessagesEn, name)
		if !okZh || !okEn {
			t.Errorf("field %s missing from a catalog (zh=%v en=%v)", name, okZh, okEn)
			continue
		}
		if strings.TrimSpace(zh) == "" || strings.TrimSpace(en) == "" {
			t.Errorf("field %s empty (zh=%q en=%q)", name, zh, en)
		}
		if zh == en {
			t.Errorf("field %s has identical zh/en strings %q", name, zh)
		}
	}
	// The three source labels and the format string must actually be usable.
	for _, name := range []string{"TimeoutEffectiveFmt", "TimeoutInvalidFmt", "NetworkDraftsInvalidFmt"} {
		zh, _ := timeoutMessageField(&networkMessagesZh, name)
		en, _ := timeoutMessageField(&networkMessagesEn, name)
		if !strings.Contains(zh, "%s") || !strings.Contains(en, "%s") {
			t.Errorf("field %s must contain %%s: zh=%q en=%q", name, zh, en)
		}
	}
}

// timeoutMessageField reads a struct field by name without reflect overhead.
func timeoutMessageField(m *networkMessages, name string) (string, bool) {
	switch name {
	case "RequestTimeoutHeader":
		return m.RequestTimeoutHeader, true
	case "RequestTimeoutScopeHint":
		return m.RequestTimeoutScopeHint, true
	case "RequestTimeoutPlaceholder":
		return m.RequestTimeoutPlaceholder, true
	case "RequestTimeoutUnitHint":
		return m.RequestTimeoutUnitHint, true
	case "TimeoutEntryHint":
		return m.TimeoutEntryHint, true
	case "TimeoutQuickEntry":
		return m.TimeoutQuickEntry, true
	case "TimeoutQuickEntryTip":
		return m.TimeoutQuickEntryTip, true
	case "TimeoutQuickEntryInvalid":
		return m.TimeoutQuickEntryInvalid, true
	case "TimeoutBadgeFmt":
		return m.TimeoutBadgeFmt, true
	case "TimeoutBadgeInvalid":
		return m.TimeoutBadgeInvalid, true
	case "TimeoutEffectiveFmt":
		return m.TimeoutEffectiveFmt, true
	case "TimeoutSourceRequest":
		return m.TimeoutSourceRequest, true
	case "TimeoutSourceDefault":
		return m.TimeoutSourceDefault, true
	case "TimeoutSourceDeviceTotal":
		return m.TimeoutSourceDeviceTotal, true
	case "TimeoutNoTabHint":
		return m.TimeoutNoTabHint, true
	case "TimeoutInvalidFmt":
		return m.TimeoutInvalidFmt, true
	case "TimeoutNonPositiveFmt":
		return m.TimeoutNonPositiveFmt, true
	case "StageTimeoutsNote":
		return m.StageTimeoutsNote, true
	case "NetworkDraftsInvalidFmt":
		return m.NetworkDraftsInvalidFmt, true
	default:
		return "", false
	}
}

// --- UI rendering: discoverability, unit hint, effective value + source ---

// renderTimeoutCardView is a tiny headless view that renders just the request
// timeout card, so the test is independent of the shared workspace shell.
func renderTimeoutCardView(t *testing.T, a *App, tab *Tab, st *projectNetworkState) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(func(c *ui.Context) {
		a.renderRequestTimeoutCard(c, c.Theme(), tab, st, a.networkMsg())
	}, 1000, 700)
	tt.Frame()
	return tt
}

func TestTimeoutCardIsDiscoverableWithUnitsAndEffectiveSource(t *testing.T) {
	a, ps := openTestProject(t, "TimeoutCard")
	tab := openRequestTabs(t, ps, "Slow")[0]
	st := projectNetStateFor(ps, a)

	// Default (blank request timeout): 10s from the built-in default.
	tab.Timeout = ""
	tt := renderTimeoutCardView(t, a, tab, st)
	for _, want := range []string{
		networkMessagesEn.RequestTimeoutHeader,
		networkMessagesEn.RequestTimeoutUnitHint,
		networkMessagesEn.RequestTimeoutScopeHint,
		networkMessagesEn.TimeoutEntryHint,
		"In effect now: 10s",
		networkMessagesEn.TimeoutSourceDefault,
	} {
		if !tt.HasText(want) {
			t.Errorf("default card missing %q; texts=%v", want, tt.Texts())
		}
	}

	// A valid per-request value: effective value + "this request" source.
	tab.Timeout = "3s"
	tt = renderTimeoutCardView(t, a, tab, st)
	if !tt.HasText("In effect now: 3s") || !tt.HasText(networkMessagesEn.TimeoutSourceRequest) {
		t.Errorf("request card missing effective value/source; texts=%v", tt.Texts())
	}

	// A positive device TotalTimeout must be reported as the winning source,
	// never silently shown as "this request" or the 10s default.
	dev := st.netStore.DeviceSettings()
	dev.Timeouts.TotalTimeout = 7 * time.Second
	if err := st.netStore.SaveDeviceSettings(dev); err != nil {
		t.Fatal(err)
	}
	tt = renderTimeoutCardView(t, a, tab, st)
	if !tt.HasText("In effect now: 7s") || !tt.HasText(networkMessagesEn.TimeoutSourceDeviceTotal) {
		t.Errorf("device-total override not shown; texts=%v", tt.Texts())
	}
}

func TestTimeoutCardShowsChineseCatalog(t *testing.T) {
	a, ps := openTestProject(t, "TimeoutCardZh")
	// Real product default is Simplified Chinese; opt in explicitly here.
	a.mgr.cfg.setLanguage(langSimplifiedChinese)
	tab := openRequestTabs(t, ps, "慢请求")[0]
	st := projectNetStateFor(ps, a)
	tab.Timeout = "500ms"

	tt := renderTimeoutCardView(t, a, tab, st)
	for _, want := range []string{
		networkMessagesZh.RequestTimeoutHeader,
		networkMessagesZh.RequestTimeoutUnitHint,
		"当前生效：500ms",
		networkMessagesZh.TimeoutSourceRequest,
	} {
		if !tt.HasText(want) {
			t.Errorf("zh card missing %q; texts=%v", want, tt.Texts())
		}
	}
}

func TestTimeoutCardInlineFeedbackForBadValues(t *testing.T) {
	a, ps := openTestProject(t, "TimeoutBad")
	tab := openRequestTabs(t, ps, "Bad")[0]
	st := projectNetStateFor(ps, a)

	// Bad unit: an inline danger message, NOT a silent ignore, and the draft is
	// left intact so the user can fix it.
	tab.Timeout = "10"
	tt := renderTimeoutCardView(t, a, tab, st)
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "Invalid value:") {
		t.Errorf("bad unit produced no inline error; texts=%v", tt.Texts())
	}
	if tab.Timeout != "10" {
		t.Fatalf("invalid draft was overwritten: %q", tab.Timeout)
	}

	// Zero / negative: distinct non-positive feedback (engine falls back to 10s).
	for _, raw := range []string{"0s", "-3s"} {
		tab.Timeout = raw
		tt = renderTimeoutCardView(t, a, tab, st)
		if !tt.HasText(networkMessagesEn.TimeoutNonPositiveFmt) {
			t.Errorf("%q produced no non-positive feedback; texts=%v", raw, tt.Texts())
		}
		if tab.Timeout != raw {
			t.Fatalf("%q draft was overwritten: %q", raw, tab.Timeout)
		}
	}
}

func TestTimeoutCardWithoutTabExplainsEntry(t *testing.T) {
	a, ps := openTestProject(t, "TimeoutNoTab")
	st := projectNetStateFor(ps, a)
	tt := renderTimeoutCardView(t, a, nil, st)
	if !tt.HasText(networkMessagesEn.TimeoutNoTabHint) {
		t.Errorf("nil-tab card missing entry hint; texts=%v", tt.Texts())
	}
}

// --- Stage timeouts: separate, machine-local, invalid-input feedback ---

func TestStageTimeoutCardNotesDeviceLocalScope(t *testing.T) {
	a, ps := openTestProject(t, "StageScope")
	st := projectNetStateFor(ps, a)
	tt := ui.NewTester(func(c *ui.Context) {
		a.renderStageTimeoutCard(c, c.Theme(), st, a.networkMsg())
	}, 1000, 700)
	tt.Frame()
	if !tt.HasText(networkMessagesEn.StageTimeoutsNote) {
		t.Errorf("stage card missing device-local scope note; texts=%v", tt.Texts())
	}
	if !tt.HasText("network.json") {
		t.Errorf("stage card should name the device config file; texts=%v", tt.Texts())
	}
}

func TestStageTimeoutInvalidDraftReportsAndKeepsLastValid(t *testing.T) {
	// Use an in-memory NetworkStore so no device config file is written.
	st := initProjectNetworkState("fix08-stage", "")
	nm := &networkMessagesEn

	// A valid change is saved.
	st.dialTimeoutDraft = "6s"
	if err := st.applyNetworkDraftsFromUI(nm); err != nil {
		t.Fatalf("valid draft rejected: %v", err)
	}
	if st.timeoutErr != "" {
		t.Fatalf("valid draft left an error: %q", st.timeoutErr)
	}
	valid := st.netStore.DeviceSettings().Timeouts
	if valid.DialTimeout != 6*time.Second {
		t.Fatalf("valid dial not saved: %v", valid.DialTimeout)
	}

	// An invalid value must be reported inline, keep the invalid draft text,
	// and NOT overwrite the last valid device settings.
	st.tlsTimeoutDraft = "5 seconds"
	if err := st.applyNetworkDraftsFromUI(nm); err == nil {
		t.Fatal("invalid stage draft was silently accepted")
	}
	if st.timeoutErr == "" || !strings.Contains(st.timeoutErr, "not saved") {
		t.Fatalf("no inline error for invalid stage draft: %q", st.timeoutErr)
	}
	if st.tlsTimeoutDraft != "5 seconds" {
		t.Fatalf("invalid draft was overwritten: %q", st.tlsTimeoutDraft)
	}
	after := st.netStore.DeviceSettings().Timeouts
	if after.DialTimeout != valid.DialTimeout || after.TLSHandshakeTimeout != valid.TLSHandshakeTimeout {
		t.Fatalf("invalid draft clobbered valid settings: before=%+v after=%+v", valid, after)
	}

	// Fixing the value clears the feedback and saves again.
	st.tlsTimeoutDraft = "4s"
	if err := st.applyNetworkDraftsFromUI(nm); err != nil {
		t.Fatalf("corrected draft rejected: %v", err)
	}
	if st.timeoutErr != "" {
		t.Fatalf("corrected draft left an error: %q", st.timeoutErr)
	}
	if st.netStore.DeviceSettings().Timeouts.TLSHandshakeTimeout != 4*time.Second {
		t.Fatal("corrected TLS timeout not saved")
	}
}

func TestNetworkModalSaveRejectsInvalidStageDraftAndKeepsOpen(t *testing.T) {
	a, ps := openTestProject(t, "ModalInvalid")
	tab := openRequestTabs(t, ps, "Req")[0]
	tab.Resp = &core.ExecResult{Status: 200, Proto: "HTTP/1.1", Body: []byte(`{}`), BodySize: 2}
	tab.respVersion = 1
	st := projectNetStateFor(ps, a)

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	// The Network/TLS settings entry lives inside the Network view.
	if err := tt.Click(networkMessagesEn.NetworkSection); err != nil {
		t.Fatalf("open network view: %v; texts=%v", err, tt.Texts())
	}
	if err := tt.Click(networkMessagesEn.NetworkBtn); err != nil {
		t.Fatalf("open network modal: %v; texts=%v", err, tt.Texts())
	}
	if !st.netModalOpen {
		t.Fatal("network modal did not open")
	}

	// Seed an invalid stage draft, then press the modal Save button through the
	// real UI. The modal must stay open and show inline feedback.
	st.dialTimeoutDraft = "oops"
	tt.Frame()
	if err := tt.Click(networkMessagesEn.SaveNetworkSettingsBtn); err != nil {
		t.Fatalf("click save: %v; texts=%v", err, tt.Texts())
	}
	if !st.netModalOpen {
		t.Fatal("modal closed despite an invalid stage-timeout draft")
	}
	if st.timeoutErr == "" {
		t.Fatal("invalid stage draft on Save produced no inline feedback")
	}
	if st.dialTimeoutDraft != "oops" {
		t.Fatalf("invalid draft was lost: %q", st.dialTimeoutDraft)
	}
}

func TestNetworkModalCancelPreservesDraftAndSettings(t *testing.T) {
	a, ps := openTestProject(t, "ModalCancel")
	tab := openRequestTabs(t, ps, "Req")[0]
	tab.Resp = &core.ExecResult{Status: 200, Proto: "HTTP/1.1", Body: []byte(`{}`), BodySize: 2}
	tab.respVersion = 1
	st := projectNetStateFor(ps, a)
	before := st.netStore.DeviceSettings().Timeouts

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(networkMessagesEn.NetworkSection); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(networkMessagesEn.NetworkBtn); err != nil {
		t.Fatal(err)
	}
	st.ttfbTimeoutDraft = "9s"
	tt.Frame()
	if err := tt.Click(a.msg().Close); err != nil {
		t.Fatalf("click Close: %v; texts=%v", err, tt.Texts())
	}
	if st.netModalOpen {
		t.Fatal("Close did not dismiss the modal")
	}
	// Cancel preserves the user's draft (they can still fix it) and never
	// silently changes the device settings.
	if st.ttfbTimeoutDraft != "9s" {
		t.Fatalf("cancel discarded the draft: %q", st.ttfbTimeoutDraft)
	}
	if st.netStore.DeviceSettings().Timeouts.ResponseHeaderTimeout != before.ResponseHeaderTimeout {
		t.Fatal("cancel changed device settings")
	}

	// Reopening recomputes feedback from the preserved (valid) draft.
	a.openNetworkSettingsModal(ps)
	if st.timeoutErr != "" {
		t.Fatalf("valid preserved draft flagged as invalid on reopen: %q", st.timeoutErr)
	}
}

// --- Inline badge: shows the effective value and switches to Settings ---

func TestTimeoutBadgeNavigatesToSettingsTab(t *testing.T) {
	a, ps := openTestProject(t, "TimeoutBadge")
	tab := openRequestTabs(t, ps, "Req")[0]
	tab.Section = 0
	tab.Timeout = "2s"

	tt := ui.NewTester(func(c *ui.Context) {
		a.renderTimeoutBadge(c, c.Theme(), tab)
	}, 1000, 700)
	tt.Frame()
	want := fmt.Sprintf(networkMessagesEn.TimeoutBadgeFmt, "2s")
	if !tt.HasText(want) {
		t.Fatalf("timeout badge missing its effective value; texts=%v", tt.Texts())
	}
	if err := tt.Click(want); err != nil {
		t.Fatalf("click timeout badge: %v; texts=%v", err, tt.Texts())
	}
	if tab.Section != requestSettingsSection {
		t.Fatalf("timeout badge navigated to section %d, want %d", tab.Section, requestSettingsSection)
	}
}

func TestTimeoutBadgeInvalidFlag(t *testing.T) {
	a, ps := openTestProject(t, "TimeoutBadgeBad")
	tab := openRequestTabs(t, ps, "Req")[0]
	tab.Timeout = "nope"

	tt := ui.NewTester(func(c *ui.Context) {
		a.renderTimeoutBadge(c, c.Theme(), tab)
	}, 1000, 700)
	tt.Frame()
	if !tt.HasText(networkMessagesEn.TimeoutBadgeInvalid) {
		t.Errorf("invalid timeout badge flag missing; texts=%v", tt.Texts())
	}
}

// --- request save -> reopen (real request file round-trip) ---

func TestRequestTimeoutSaveAndReopen(t *testing.T) {
	a, ps := openTestProject(t, "TimeoutSave")
	tab := openRequestTabs(t, ps, "Persist")[0]

	// Set through the tab's editable field, exactly as the Settings card does.
	tab.Timeout = "1m30s"
	tab.Dirty = true
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatalf("saveTabSynced: %v", err)
	}

	// 1) The request file on disk carries the timeout (project-level, shared).
	rel := tab.Entry.RelPath
	data, err := os.ReadFile(filepath.Join(ps.project.Dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "timeout: 1m30s") {
		t.Fatalf("saved request file missing timeout:\n%s", data)
	}

	// 2) Reopen the project from disk: the tab must be refilled from the
	// persisted request, and the UI must resolve the same effective value.
	reopened, err := core.OpenProject(ps.project.Dir)
	if err != nil {
		t.Fatalf("OpenProject(reopen): %v", err)
	}
	var found *core.RequestEntry
	for _, e := range reopened.Requests {
		if e.Request.Name == "Persist" {
			found = e
			break
		}
	}
	if found == nil || strings.TrimSpace(found.Request.Timeout) != "1m30s" {
		t.Fatalf("reopened request timeout = %+v", found)
	}
	res := resolveRequestTimeout(found.Request.Timeout, 0)
	if res.Effective != 90*time.Second || res.Origin != timeoutOriginRequest {
		t.Fatalf("reopened effective timeout wrong: %+v", res)
	}
}

// --- real local slow service: timeout, inheritance, cancel ---

// slowTestServer blocks until the request context is canceled by the client's
// timeout, so no long sleeps are needed and the server never leaks goroutines.
func slowTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRequestTimeoutDrivesRealSlowServiceAndDefaultInheritance(t *testing.T) {
	srv := slowTestServer(t)
	a, ps := openTestProject(t, "SlowService")
	tab := openRequestTabs(t, ps, "Slow")[0]
	tab.URL = srv.URL

	// A short request-level timeout (~60ms) must actually cut off a local slow
	// service, with the real effective value reported in the timeout error.
	tab.Timeout = "60ms"
	req, plan, red, err := a.buildTrackedTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTrackedTabPlan: %v", err)
	}
	if plan.Timeout != 60*time.Millisecond {
		t.Fatalf("plan.Timeout = %v, want 60ms", plan.Timeout)
	}
	_, err = a.executeTabPlan(context.Background(), tab, req, plan, red)
	if err == nil {
		t.Fatal("expected a timeout error from the slow service")
	}
	if !strings.Contains(err.Error(), "timed out after 60ms") {
		t.Fatalf("unexpected timeout error: %v", err)
	}

	// Blank timeout inherits the built-in 10s default: the plan carries 10s and
	// a fast local endpoint completes normally under it.
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer fast.Close()
	tab.Timeout = ""
	tab.URL = fast.URL
	req, plan, red, err = a.buildTrackedTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTrackedTabPlan(default): %v", err)
	}
	if plan.Timeout != core.DefaultTimeout {
		t.Fatalf("blank timeout did not inherit 10s: %v", plan.Timeout)
	}
	res, err := a.executeTabPlan(context.Background(), tab, req, plan, red)
	if err != nil {
		t.Fatalf("default-inheritance request failed: %v", err)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Status)
	}
	if got := resolveRequestTimeout("", 0); got.Effective != core.DefaultTimeout {
		t.Fatalf("UI must report the same 10s default: %+v", got)
	}
}

func TestDeviceTotalTimeoutOverridesRequestOnRealSlowService(t *testing.T) {
	srv := slowTestServer(t)
	a, ps := openTestProject(t, "DeviceOverride")
	tab := openRequestTabs(t, ps, "Slow")[0]
	tab.URL = srv.URL
	tab.Timeout = "30s" // request asks for 30s

	// A positive device-local TotalTimeout wins at execution time (plan.go).
	st := projectNetStateFor(ps, a)
	dev := st.netStore.DeviceSettings()
	dev.Timeouts.TotalTimeout = 80 * time.Millisecond
	if err := st.netStore.SaveDeviceSettings(dev); err != nil {
		t.Fatal(err)
	}
	res := resolveRequestTimeout(tab.Timeout, dev.Timeouts.TotalTimeout)
	if res.Effective != 80*time.Millisecond || res.Origin != timeoutOriginDeviceTotal {
		t.Fatalf("UI resolution wrong for device override: %+v", res)
	}

	req, plan, red, err := a.buildTrackedTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTrackedTabPlan: %v", err)
	}
	_, err = a.executeTabPlan(context.Background(), tab, req, plan, red)
	if err == nil || !strings.Contains(err.Error(), "timed out after 80ms") {
		t.Fatalf("device TotalTimeout did not win: err=%v", err)
	}
}

func TestRequestTimeoutHonorsCancellationBeforeDeadline(t *testing.T) {
	srv := slowTestServer(t)
	a, ps := openTestProject(t, "CancelTimeout")
	tab := openRequestTabs(t, ps, "Slow")[0]
	tab.URL = srv.URL
	tab.Timeout = "30s" // long enough that explicit cancel wins

	req, plan, red, err := a.buildTrackedTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTrackedTabPlan: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately; no sleeping
	_, err = a.executeTabPlan(ctx, tab, req, plan, red)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected an explicit cancellation error, got: %v", err)
	}
}

// A bad timeout must be surfaced by the plan builder rather than silently
// ignored or bypassed; the UI report must agree.
func TestInvalidRequestTimeoutFailsPlanBuildNotSilentlyIgnored(t *testing.T) {
	a, ps := openTestProject(t, "InvalidTimeout")
	tab := openRequestTabs(t, ps, "Bad")[0]
	tab.URL = "http://127.0.0.1:1/"
	tab.Timeout = "10 seconds"

	res := resolveRequestTimeout(tab.Timeout, 0)
	if res.Valid() || res.ParseErr == nil {
		t.Fatalf("UI did not flag the invalid value: %+v", res)
	}
	_, _, _, err := a.buildTrackedTabPlan(context.Background(), tab)
	if err == nil {
		t.Fatal("invalid timeout was silently accepted by the plan builder")
	}
	if !strings.Contains(err.Error(), "invalid timeout") {
		t.Fatalf("unexpected plan-build error: %v", err)
	}
}

// The device NetworkStore must be constructible with an empty path (in-memory)
// for the FIX08 tests to avoid touching real device config.
func TestFIX08NetworkStoreIsInMemory(t *testing.T) {
	st := initProjectNetworkState("fix08-inmem", "")
	if st.netStore == nil {
		t.Fatal("nil network store")
	}
	if got := st.netStore.DeviceSettings().Timeouts.TotalTimeout; got != 0 {
		t.Fatalf("unexpected default device TotalTimeout: %v", got)
	}
	// platform.NewNetworkStore("") is documented as in-memory.
	if got := platform.DefaultDeviceNetworkSettings().Timeouts.DialTimeout; got != core.DefaultDialTimeout {
		t.Fatalf("unexpected default dial timeout: %v", got)
	}
}
