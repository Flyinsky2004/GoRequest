package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/update"
)

// --- fakes ---------------------------------------------------------------

type fakeUpdateChecker struct {
	result   *update.CheckResult
	err      error
	calls    int
	currents []string
	onCall   func(call int)
}

func (f *fakeUpdateChecker) Check(ctx context.Context, current string) (*update.CheckResult, error) {
	f.calls++
	if f.onCall != nil {
		f.onCall(f.calls)
	}
	f.currents = append(f.currents, current)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type fakeUpdateDownloader struct {
	downloaded *update.Downloaded
	err        error
	calls      int
}

func (f *fakeUpdateDownloader) Download(ctx context.Context, cand *update.Candidate, progress func(done, total int64)) (*update.Downloaded, error) {
	f.calls++
	if progress != nil {
		progress(50, 100)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.downloaded, nil
}

type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time { return f.now }

func testUpdateCandidate(t *testing.T, version string) *update.Candidate {
	t.Helper()
	v, err := update.ParseVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	return &update.Candidate{
		Release: update.Release{Tag: "v" + version, Version: v, Notes: "Release notes"},
		Asset:   update.Asset{Name: "gorequest-" + version + "-arm64.dmg", URL: "https://example.test/app.dmg", Size: 100},
	}
}

func availableUpdate(c *update.Candidate) *update.CheckResult {
	return &update.CheckResult{Status: update.StatusUpdateAvailable, Candidate: c}
}

func newUpdateTestManager(t *testing.T, checker releaseChecker, downloader updateDownloader,
	now func() time.Time, installed *string, open func(string) error) (*Manager, *updateController, *fakeFactory) {
	t.Helper()
	m, ff := newTestManager(t)
	statePath := filepath.Join(filepath.Dir(m.cfg.path), "update-state.json")
	u := newUpdateController(m, update.NewStateStore(statePath), checker, downloader, now,
		func() string { return *installed }, open, "darwin")
	m.update = u
	return m, u, ff
}

func fixedClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 8, 10, 0, 0, 0, time.Local)}
}

// --- automatic check -----------------------------------------------------

func TestUpdateAutoCheckOncePerDayAndVersionChange(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	checker := &fakeUpdateChecker{result: &update.CheckResult{Status: update.StatusUpToDate}}
	m, u, _ := newUpdateTestManager(t, checker, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.showWelcome("")
	u.setHost(m.welcome.win)

	u.autoCheck()
	if checker.calls != 1 {
		t.Fatalf("first auto check calls = %d, want 1", checker.calls)
	}
	st := u.state.Load()
	if st.LastCheckDate != "2026-11-08" || st.LastCheckVersion != "0.0.1-dev" || st.LastResult != update.ResultUpToDate {
		t.Fatalf("state after check = %+v", st)
	}

	// Same day, same installed version: no second request.
	u.autoCheck()
	if checker.calls != 1 {
		t.Fatalf("same-day auto check calls = %d, want 1", checker.calls)
	}

	// Installing V2 invalidates the cache immediately, so V3 can be found
	// the same day.
	installed = "0.0.2-dev"
	u.autoCheck()
	if checker.calls != 2 {
		t.Fatalf("version-change auto check calls = %d, want 2", checker.calls)
	}

	// A new local day checks again.
	clock.now = clock.now.Add(24 * time.Hour)
	u.autoCheck()
	if checker.calls != 3 {
		t.Fatalf("next-day auto check calls = %d, want 3", checker.calls)
	}
}

func TestUpdateAutoCheckRespectsPreferences(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	checker := &fakeUpdateChecker{result: &update.CheckResult{Status: update.StatusUpToDate}}
	_, u, _ := newUpdateTestManager(t, checker, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })

	u.setNeverNotify(true)
	u.autoCheck()
	if checker.calls != 0 {
		t.Fatalf("never-notify auto check calls = %d, want 0", checker.calls)
	}

	u.setNeverNotify(false)
	u.setAutoCheck(false)
	u.autoCheck()
	if checker.calls != 0 {
		t.Fatalf("auto-check-off calls = %d, want 0", checker.calls)
	}

	u.setAutoCheck(true)
	u.autoCheck()
	if checker.calls != 1 {
		t.Fatalf("restored auto check calls = %d, want 1", checker.calls)
	}
}

func TestUpdateAutoPromptSuppression(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	cand := testUpdateCandidate(t, "0.0.2-dev")
	checker := &fakeUpdateChecker{result: availableUpdate(cand)}
	m, u, _ := newUpdateTestManager(t, checker, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.showWelcome("")
	u.setHost(m.welcome.win)

	u.autoCheck()
	if !u.dialogOpen || u.dialogKind != updateDialogAvailable {
		t.Fatalf("first automatic prompt not shown: open=%v kind=%v", u.dialogOpen, u.dialogKind)
	}

	// "Later" suppresses this exact version for the rest of the day, even
	// when another check runs (for example after a version change).
	u.later()
	if u.dialogOpen {
		t.Fatal("Later should close the dialog")
	}
	u.state.Mutate(func(st *update.State) { st.LastCheckDate = ""; st.LastCheckVersion = "" })
	u.autoCheck()
	if u.dialogOpen {
		t.Fatal("deferred version must not prompt again the same day")
	}

	// Skipping suppresses only that version.
	u.candidate = cand
	u.skipVersion()
	u.state.Mutate(func(st *update.State) { st.LastCheckDate = ""; st.LastCheckVersion = "" })
	u.autoCheck()
	if u.dialogOpen {
		t.Fatal("skipped version must not prompt again")
	}

	// A newer version prompts normally.
	cand3 := testUpdateCandidate(t, "0.0.3-dev")
	checker.result = availableUpdate(cand3)
	u.state.Mutate(func(st *update.State) { st.LastCheckDate = ""; st.LastCheckVersion = "" })
	u.autoCheck()
	if !u.dialogOpen || u.candidate == nil || u.candidate.Release.Version.String() != "0.0.3-dev" {
		t.Fatalf("newer version did not prompt: open=%v candidate=%+v", u.dialogOpen, u.candidate)
	}

	// Never notify stops automatic checks entirely.
	u.neverNotify()
	u.state.Mutate(func(st *update.State) { st.LastCheckDate = ""; st.LastCheckVersion = "" })
	before := checker.calls
	u.autoCheck()
	if checker.calls != before {
		t.Fatal("never-notify must stop automatic checks")
	}
}

// --- manual check --------------------------------------------------------

func TestUpdateManualCheckBypassesSuppression(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	cand := testUpdateCandidate(t, "0.0.2-dev")
	checker := &fakeUpdateChecker{result: availableUpdate(cand)}
	m, u, _ := newUpdateTestManager(t, checker, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.showWelcome("")
	u.setHost(m.welcome.win)
	u.state.Mutate(func(st *update.State) {
		st.NeverNotify = true
		st.SkippedVersion = "0.0.2-dev"
		st.DeferredVersion = "0.0.2-dev"
		st.DeferredDate = "2026-11-08"
	})

	u.runCheck(true)
	if !u.dialogOpen || u.dialogKind != updateDialogAvailable {
		t.Fatalf("manual check must ignore suppression: open=%v kind=%v", u.dialogOpen, u.dialogKind)
	}
	if checker.currents[0] != "0.0.1-dev" {
		t.Fatalf("checker current = %q", checker.currents[0])
	}
}

func TestUpdateManualResults(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	cases := []struct {
		name   string
		result *update.CheckResult
		err    error
		want   updateResultCode
	}{
		{"up to date", &update.CheckResult{Status: update.StatusUpToDate}, nil, updateResultUpToDate},
		{"development", &update.CheckResult{Status: update.StatusDevelopment}, nil, updateResultDevelopment},
		{"no release", &update.CheckResult{Status: update.StatusNoRelease}, nil, updateResultNoRelease},
		{"rate limit", nil, &update.Error{Kind: update.ErrorRateLimit, Op: "check", Err: errors.New("limited")}, updateResultRateLimited},
		{"offline", nil, &update.Error{Kind: update.ErrorNetwork, Op: "check", Err: errors.New("offline")}, updateResultOffline},
		{"no asset", nil, &update.Error{Kind: update.ErrorNoAsset, Op: "check", Err: errors.New("no asset")}, updateResultNoAsset},
		{"generic", nil, errors.New("boom"), updateResultError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &fakeUpdateChecker{result: tc.result, err: tc.err}
			m, u, _ := newUpdateTestManager(t, checker, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
			m.showWelcome("")
			u.setHost(m.welcome.win)
			u.runCheck(true)
			if !u.dialogOpen || u.dialogKind != updateDialogResult || u.result != tc.want {
				t.Fatalf("manual result: open=%v kind=%v result=%v want=%v", u.dialogOpen, u.dialogKind, u.result, tc.want)
			}
		})
	}
}

func TestUpdateAutomaticErrorsStaySilent(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	checker := &fakeUpdateChecker{err: &update.Error{Kind: update.ErrorNetwork, Op: "check", Err: errors.New("offline")}}
	m, u, _ := newUpdateTestManager(t, checker, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.showWelcome("")
	u.setHost(m.welcome.win)

	u.autoCheck()
	if u.dialogOpen {
		t.Fatal("an automatic network failure must not open a dialog")
	}
	st := u.state.Load()
	if st.LastResult != update.ResultOffline || st.LastError == "" {
		t.Fatalf("failure not traceable in state: %+v", st)
	}
}

func TestUpdateManualCheckQueuedBehindAuto(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	autoInFlight := make(chan struct{})
	releaseAuto := make(chan struct{})
	autoDone := make(chan struct{})
	checker := &fakeUpdateChecker{
		result: &update.CheckResult{Status: update.StatusUpToDate},
		onCall: func(call int) {
			if call == 1 {
				close(autoInFlight)
				<-releaseAuto
			}
		},
	}
	m, u, _ := newUpdateTestManager(t, checker, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.showWelcome("")
	u.setHost(m.welcome.win)

	// Start an automatic check in flight, then queue a manual request while it is blocked.
	go func() {
		u.runCheck(false)
		close(autoDone)
	}()
	select {
	case <-autoInFlight:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic check never entered Checker.Check")
	}

	u.setHost(m.welcome.win)
	u.runCheck(true)
	if !u.manualPending.Load() {
		t.Fatal("manual request was not queued behind the running check")
	}
	close(releaseAuto)
	select {
	case <-autoDone:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic check and queued manual check did not finish")
	}

	// The queued manual body ran after the automatic check, so the user still
	// gets a visible manual result.
	if !u.dialogOpen || u.dialogKind != updateDialogResult || u.result != updateResultUpToDate {
		t.Fatalf("queued manual result not shown: open=%v kind=%v result=%v", u.dialogOpen, u.dialogKind, u.result)
	}
	if checker.calls != 2 {
		t.Fatalf("checker calls = %d, want 2", checker.calls)
	}
}

func TestUpdateCanceledCheckCloses(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	checker := &fakeUpdateChecker{err: context.Canceled}
	m, u, _ := newUpdateTestManager(t, checker, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.showWelcome("")
	u.setHost(m.welcome.win)

	u.runCheck(true)
	if u.dialogOpen {
		t.Fatal("a canceled check must not leave a dialog open")
	}
	if got := u.state.Load().LastResult; got != update.ResultCanceled {
		t.Fatalf("last result = %q, want canceled", got)
	}
}

// --- download / install --------------------------------------------------

func TestUpdateDownloadVerifiedAndOpened(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	cand := testUpdateCandidate(t, "0.0.2-dev")
	var opened string
	dl := &fakeUpdateDownloader{downloaded: &update.Downloaded{Path: "/tmp/gorequest-0.0.2-arm64.dmg", Name: "app.dmg"}}
	m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, dl, clock.Now, &installed, func(path string) error {
		opened = path
		return nil
	})
	m.showWelcome("")
	u.setHost(m.welcome.win)
	u.candidate = cand
	u.dialogKind = updateDialogAvailable
	u.dialogOpen = true

	u.downloadAndOpen(context.Background(), cand)
	if dl.calls != 1 {
		t.Fatalf("download calls = %d", dl.calls)
	}
	if opened != "/tmp/gorequest-0.0.2-arm64.dmg" {
		t.Fatalf("opened = %q", opened)
	}
	if !u.dialogOpen || u.dialogKind != updateDialogResult || u.result != updateResultDownloaded {
		t.Fatalf("download result: open=%v kind=%v result=%v", u.dialogOpen, u.dialogKind, u.result)
	}
	if u.detail != "/tmp/gorequest-0.0.2-arm64.dmg" {
		t.Fatalf("detail = %q", u.detail)
	}
	if got := u.state.Load().LastResult; got != update.ResultDownloaded {
		t.Fatalf("persisted result = %q, want downloaded", got)
	}
}

func TestUpdateDownloadFailures(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	cand := testUpdateCandidate(t, "0.0.2-dev")

	t.Run("download error", func(t *testing.T) {
		dl := &fakeUpdateDownloader{err: &update.Error{Kind: update.ErrorChecksum, Op: "verify", Err: errors.New("mismatch")}}
		m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, dl, clock.Now, &installed, func(string) error { return nil })
		m.showWelcome("")
		u.setHost(m.welcome.win)
		u.downloadAndOpen(context.Background(), cand)
		if u.result != updateResultError || !u.dialogOpen || !strings.Contains(u.detail, "mismatch") {
			t.Fatalf("download error result: %+v", u)
		}
	})

	t.Run("installer error", func(t *testing.T) {
		dl := &fakeUpdateDownloader{downloaded: &update.Downloaded{Path: "/tmp/app.dmg"}}
		m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, dl, clock.Now, &installed, func(string) error {
			return errors.New("no opener")
		})
		m.showWelcome("")
		u.setHost(m.welcome.win)
		u.downloadAndOpen(context.Background(), cand)
		if u.result != updateResultError || !strings.Contains(u.detail, "no opener") {
			t.Fatalf("installer error result: %+v", u)
		}
	})
}

// --- persistence and version display ------------------------------------

func TestUpdateDialogActionsPersist(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	cand := testUpdateCandidate(t, "0.0.2-dev")
	_, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })

	u.candidate = cand
	u.later()
	st := u.state.Load()
	if st.DeferredVersion != "0.0.2-dev" || st.DeferredDate != "2026-11-08" {
		t.Fatalf("later state = %+v", st)
	}

	u.candidate = cand
	u.skipVersion()
	if got := u.state.Load().SkippedVersion; got != "0.0.2-dev" {
		t.Fatalf("skipped = %q", got)
	}

	u.neverNotify()
	if !u.state.Load().NeverNotify {
		t.Fatal("never notify not persisted")
	}
	u.setNeverNotify(false)
	if u.state.Load().NeverNotify {
		t.Fatal("never notify could not be restored in settings")
	}
	u.setAutoCheck(false)
	if u.state.Load().AutoCheckEnabled() {
		t.Fatal("auto check could not be disabled in settings")
	}
}

func TestInstalledVersionLabel(t *testing.T) {
	old := buildVersion
	defer func() { buildVersion = old }()

	buildVersion = "v1.2.3"
	if got := installedVersionLabel(langEnglish); got != "v1.2.3" {
		t.Fatalf("label = %q", got)
	}
	buildVersion = ""
	if got := installedVersionLabel(langEnglish); got != updateMessagesEn.VersionDev {
		t.Fatalf("development label = %q", got)
	}
}

func TestUpdateMessagesComplete(t *testing.T) {
	check := func(name string, v updateMessages) {
		rv := reflect.ValueOf(v)
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			if strings.TrimSpace(rv.Field(i).String()) == "" {
				t.Errorf("%s.%s is empty", name, rt.Field(i).Name)
			}
		}
	}
	check("zh", updateMessagesZh)
	check("en", updateMessagesEn)
}

// --- headless UI ---------------------------------------------------------

func TestUpdateAvailableDialogHeadless(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	cand := testUpdateCandidate(t, "0.0.2-dev")
	m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.showWelcome("")
	u.setHost(m.welcome.win)
	u.candidate = cand
	u.current = "0.0.1-dev"
	u.dialogKind = updateDialogAvailable
	u.dialogOpen = true

	tt := ui.NewTester(m.welcome.view, 720, 640)
	tt.Frame()
	um := updateMessagesEn
	for _, label := range []string{
		translate(um.AvailableTitle, "0.0.2-dev"),
		um.CurrentLabel, "0.0.1-dev", um.TargetLabel, "0.0.2-dev",
		um.Later, um.UpdateNow, um.SkipVersion, um.NeverNotify, um.Settings,
	} {
		if !tt.HasText(label) {
			t.Fatalf("update dialog missing %q; texts=%v", label, tt.Texts())
		}
	}

	if err := tt.Click(um.Later); err != nil {
		t.Fatalf("click Later: %v", err)
	}
	tt.Frame()
	if u.dialogOpen {
		t.Fatal("Later should close the dialog")
	}
	if got := u.state.Load().DeferredVersion; got != "0.0.2-dev" {
		t.Fatalf("deferred = %q", got)
	}
}

func TestUpdateAvailableDialogChinese(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	cand := testUpdateCandidate(t, "0.0.2-dev")
	m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.cfg.cfg.Language = string(langSimplifiedChinese)
	m.showWelcome("")
	u.setHost(m.welcome.win)
	u.candidate = cand
	u.current = "0.0.1-dev"
	u.dialogKind = updateDialogAvailable
	u.dialogOpen = true

	tt := ui.NewTester(m.welcome.view, 720, 640)
	tt.Frame()
	um := updateMessagesZh
	for _, label := range []string{
		translate(um.AvailableTitle, "0.0.2-dev"),
		um.CurrentLabel, um.TargetLabel, um.Later, um.UpdateNow, um.SkipVersion, um.NeverNotify,
	} {
		if !tt.HasText(label) {
			t.Fatalf("Chinese update dialog missing %q; texts=%v", label, tt.Texts())
		}
	}
}

func TestUpdateSettingsCategoryHeadless(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.showWelcome("")

	tt := ui.NewTester(m.welcome.view, 720, 640)
	tt.Frame()
	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("open settings: %v", err)
	}
	tt.Frame()
	if err := tt.Click("About"); err != nil {
		t.Fatalf("open About category: %v", err)
	}
	tt.Frame()
	// The update controls sit below the About facts; scroll the pane to them.
	tt.Scroll(500, 300, 0, 800)
	tt.Frame()

	um := updateMessagesEn
	for _, label := range []string{um.AutoCheckLabel, um.NeverNotifyLabel, um.CheckNow, um.LastCheckLabel, "0.0.1-dev"} {
		if !tt.HasText(label) {
			t.Fatalf("Updates pane missing %q; texts=%v", label, tt.Texts())
		}
	}

	// "Never notify" can be turned on and off again from Settings.
	if err := tt.Click(um.NeverNotifyLabel); err != nil {
		t.Fatalf("toggle never notify: %v", err)
	}
	tt.Frame()
	if !u.state.Load().NeverNotify {
		t.Fatal("never notify was not persisted")
	}
	if err := tt.Click(um.NeverNotifyLabel); err != nil {
		t.Fatalf("untoggle never notify: %v", err)
	}
	tt.Frame()
	if u.state.Load().NeverNotify {
		t.Fatal("never notify was not restored")
	}
}

func TestUpdateSettingsContentNarrow(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		lang := lang
		t.Run(string(lang), func(t *testing.T) {
			installed := "0.0.1-dev"
			clock := fixedClock()
			m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
			m.cfg.cfg.Language = string(lang)
			const width = 340
			tt := ui.NewTester(func(c *ui.Context) {
				p := uikit.PaletteFor(c.Theme())
				ui.Column(c).Width(width).Children(func() {
					settingsCategoryContentWithUpdates(c, p, messagesFor(lang), settingsCategoryAbout,
						m.cfg, m.workspacePrefs(), u, nil, func(string) {})
				})
			}, width, 900)
			assertNoHorizontalOverflow(t, tt, width)
		})
	}
}

func TestUpdateSettingsWithoutController(t *testing.T) {
	m, _ := newTestManager(t)
	m.showWelcome("")
	tt := ui.NewTester(m.welcome.view, 720, 640)
	tt.Frame()
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("About"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(updateMessagesEn.UnavailableNote) {
		t.Fatalf("nil controller should state unavailability; texts=%v", tt.Texts())
	}
}

func TestUpdateHostWindowSelection(t *testing.T) {
	m, _ := newTestManager(t)
	m.showWelcome("")
	if got := m.updateHostWindow(); got != m.welcome.win {
		t.Fatal("visible welcome window should be the update host")
	}
	if err := m.openProject(writeProject(t, "Host")); err != nil {
		t.Fatal(err)
	}
	if got := m.updateHostWindow(); got != m.workspaces[0].win {
		t.Fatal("project window should be the update host when the chooser is hidden")
	}
}
