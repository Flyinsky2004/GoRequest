package desktop

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"gorequest/internal/update"
)

// buildVersion can be injected at build time with
// -ldflags "-X gorequest/internal/desktop.buildVersion=1.2.3". The release
// workflow instead writes mygo.json's version, which mygo.App.Version reports
// for a packaged app. An unpackaged development run reports no comparable
// version and is never offered an update.
var buildVersion string

const (
	updateCheckTimeout    = 20 * time.Second
	updateDownloadTimeout = 15 * time.Minute
)

// installedVersion returns the version of the running application. It prefers
// an injected build version, then the packaged MyGo version. A development
// run returns "" so the checker reports StatusDevelopment instead of comparing
// a placeholder such as the old "0.1.0 (MVP)" display string.
func installedVersion() string {
	if v := strings.TrimSpace(buildVersion); v != "" {
		return v
	}
	if mygo.IsDev() {
		return ""
	}
	return strings.TrimSpace(mygo.App.Version())
}

// installedVersionLabel is the localized version shown in Settings/About.
func installedVersionLabel(l language) string {
	v := installedVersion()
	if update.IsDevelopmentVersion(v) {
		return updateMessagesFor(l).VersionDev
	}
	return v
}

// updateStatePathFor keeps update state beside the app config, never in a
// project or Git. An empty config path keeps the state in memory.
func updateStatePathFor(cfgPath string) string {
	if cfgPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfgPath), "update-state.json")
}

// releaseChecker and updateDownloader are the two network-facing pieces the
// controller needs. Tests substitute in-memory implementations; the real
// implementations live in internal/update.
type releaseChecker interface {
	Check(ctx context.Context, current string) (*update.CheckResult, error)
}

type updateDownloader interface {
	Download(ctx context.Context, cand *update.Candidate, progress func(done, total int64)) (*update.Downloaded, error)
}

// updateResultCode is a language-independent result the UI localizes.
type updateResultCode int

const (
	updateResultNone updateResultCode = iota
	updateResultUpToDate
	updateResultDevelopment
	updateResultNoRelease
	updateResultNoAsset
	updateResultRateLimited
	updateResultOffline
	updateResultError
	updateResultCanceled
	updateResultDownloaded
)

// updateDialogKind selects which update modal is shown.
type updateDialogKind int

const (
	updateDialogNone updateDialogKind = iota
	updateDialogChecking
	updateDialogAvailable
	updateDialogDownloading
	updateDialogResult
)

// updateController owns the manager-level update state: the daily cache,
// skip/defer/never preferences, the in-flight check or download, and the modal
// state shared by the welcome and project windows. UI fields are only mutated
// on the UI thread (through post); the state store is goroutine-safe.
type updateController struct {
	mgr        *Manager
	checker    releaseChecker
	downloader updateDownloader
	state      *update.StateStore
	now        func() time.Time
	installed  func() string
	open       func(string) error
	goos       string
	// Production callbacks use the application dispatcher, which remains
	// available while individual windows are closing or changing hosts.
	runOnUI func(func())

	running       atomic.Bool
	manualPending atomic.Bool
	// aboutRequested makes the next Settings sheet open on About, where the
	// update controls live.
	aboutRequested atomic.Bool

	// host is the window that owns the current dialog. It is set on the UI
	// thread before a check starts and protected by hostMu because background
	// check/download goroutines read it to post their results.
	hostMu sync.RWMutex
	host   window

	// UI-thread fields.
	dialogOpen bool
	dialogKind updateDialogKind
	candidate  *update.Candidate
	current    string
	progress   int64
	total      int64
	result     updateResultCode
	detail     string
	cancel     context.CancelFunc
}

func newUpdateController(m *Manager, state *update.StateStore, checker releaseChecker, downloader updateDownloader,
	now func() time.Time, installed func() string, open func(string) error, goos string) *updateController {
	return &updateController{
		mgr:        m,
		checker:    checker,
		downloader: downloader,
		state:      state,
		now:        now,
		installed:  installed,
		open:       open,
		goos:       goos,
	}
}

// newRealUpdateController wires the production HTTP clients. The API client
// has a short timeout; the download client allows a large installer while the
// per-download context still bounds it.
func newRealUpdateController(m *Manager) *updateController {
	api := &http.Client{Timeout: updateCheckTimeout}
	download := &http.Client{Timeout: updateDownloadTimeout}
	checker := &update.Checker{Client: api, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	dl := &update.Updater{Client: download}
	u := newUpdateController(m, update.NewStateStore(updateStatePathFor(m.cfg.path)), checker, dl,
		time.Now, installedVersion, update.OpenInstaller, runtime.GOOS)
	u.runOnUI = mygo.RunOnMain
	return u
}

func (u *updateController) today() string { return update.Today(u.now()) }

// versionLabel is the localized version the controller is comparing, so the
// Settings pane and the checker always show the same value.
func (u *updateController) versionLabel(l language) string {
	v := u.installed()
	if update.IsDevelopmentVersion(v) {
		return updateMessagesFor(l).VersionDev
	}
	return v
}

// post dispatches production callbacks before inspecting any Manager window
// state. The application dispatcher does not depend on the old host surviving.
// Headless controllers use their injected window's Update instead.
func (u *updateController) post(fn func()) {
	if u == nil {
		return
	}
	if u.runOnUI != nil {
		u.runOnUI(func() { u.deliverUpdate(fn, nil) })
		return
	}
	h := u.getHost()
	if h == nil || h.IsDestroyed() {
		// Only headless controllers lack an application dispatcher.
		u.deliverUpdate(fn, nil)
		return
	}
	h.Update(func() { u.deliverUpdate(fn, h) })
}

// deliverUpdate runs on the UI thread in production. Selecting a replacement
// host and touching dialog fields must share the Manager's lifecycle thread.
func (u *updateController) deliverUpdate(fn func(), scheduledHost window) {
	if u.mgr != nil {
		u.setHost(u.mgr.updateHostWindow())
	}
	fn()
	if h := u.getHost(); h != nil && h != scheduledHost && !h.IsDestroyed() {
		h.Update(func() {})
	}
}

func (u *updateController) getHost() window {
	u.hostMu.RLock()
	defer u.hostMu.RUnlock()
	return u.host
}

func (u *updateController) setHost(w window) {
	u.hostMu.Lock()
	u.host = w
	u.hostMu.Unlock()
}

// autoCheck performs the startup check at most once per local day per
// installed version. It is called from a goroutine by Manager.Start, so a
// slow or offline network never blocks startup.
func (u *updateController) autoCheck() {
	if u == nil {
		return
	}
	st := u.state.Load()
	current := u.installed()
	if st.NeverNotify || !st.AutoCheckEnabled() {
		return
	}
	if st.CheckedToday(current, u.today()) {
		return
	}
	u.runCheck(false)
}

// manualCheckFrom starts a manual check owned by host. Manual checks ignore
// the daily cache, the skipped version, "later today" and "never notify".
// When a check is already in flight, the request is queued so the user still
// receives a manual result instead of silence.
func (u *updateController) manualCheckFrom(host window) {
	if u == nil {
		return
	}
	u.setHost(host)
	go u.runCheck(true)
}

func (u *updateController) runCheck(manual bool) {
	if u == nil {
		return
	}
	if !u.running.CompareAndSwap(false, true) {
		if manual {
			u.manualPending.Store(true)
		}
		return
	}
	defer u.running.Store(false)
	u.runCheckBody(manual)
	// A manual request that arrived while this check ran gets its own result.
	if u.manualPending.Swap(false) {
		u.runCheckBody(true)
	}
}

func (u *updateController) runCheckBody(manual bool) {

	// Automatic checks stay invisible while in flight; only a real update
	// opens a dialog. Manual checks show their progress immediately.
	u.post(func() {
		u.dialogKind = updateDialogChecking
		u.dialogOpen = manual
		u.candidate = nil
		u.current = ""
		u.progress, u.total = 0, 0
		u.result = updateResultNone
		u.detail = ""
	})

	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	u.post(func() { u.cancel = cancel })

	res, err := u.checker.Check(ctx, u.installed())
	u.post(func() { u.cancel = nil })

	current := u.installed()
	today := u.today()
	now := u.now().UTC().Format(time.RFC3339)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			// A canceled check is not a completed daily check: it must not
			// suppress the automatic check later today. The outcome is still
			// recorded for Settings.
			u.state.Mutate(func(st *update.State) {
				st.LastCheckedAt = now
				st.LastResult = update.ResultCanceled
				st.LastError = ""
			})
			u.post(func() { u.closeDialog() })
			return
		}
		code, token := classifyUpdateError(err)
		u.state.Mutate(func(st *update.State) {
			st.LastCheckDate = today
			st.LastCheckVersion = current
			st.LastCheckedAt = now
			st.LastResult = token
			st.LastError = err.Error()
		})
		u.post(func() {
			u.dialogKind = updateDialogResult
			u.result = code
			u.detail = err.Error()
			u.dialogOpen = manual
		})
		return
	}

	switch res.Status {
	case update.StatusDevelopment:
		u.state.Mutate(func(st *update.State) {
			st.LastCheckDate = today
			st.LastCheckVersion = current
			st.LastCheckedAt = now
			st.LastResult = update.ResultDevelopment
			st.LastError = ""
			st.LatestVersion = ""
		})
		u.post(func() {
			u.dialogKind = updateDialogResult
			u.result = updateResultDevelopment
			u.dialogOpen = manual
		})
	case update.StatusNoRelease:
		u.state.Mutate(func(st *update.State) {
			st.LastCheckDate = today
			st.LastCheckVersion = current
			st.LastCheckedAt = now
			st.LastResult = update.ResultNoRelease
			st.LastError = ""
			st.LatestVersion = ""
		})
		u.post(func() {
			u.dialogKind = updateDialogResult
			u.result = updateResultNoRelease
			u.dialogOpen = manual
		})
	case update.StatusUpToDate:
		u.state.Mutate(func(st *update.State) {
			st.LastCheckDate = today
			st.LastCheckVersion = current
			st.LastCheckedAt = now
			st.LastResult = update.ResultUpToDate
			st.LastError = ""
			st.LatestVersion = ""
		})
		u.post(func() {
			u.dialogKind = updateDialogResult
			u.result = updateResultUpToDate
			u.current = current
			u.dialogOpen = manual
		})
	case update.StatusUpdateAvailable:
		if res.Candidate == nil {
			u.post(func() {
				u.dialogKind = updateDialogResult
				u.result = updateResultError
				u.detail = "check returned no candidate"
				u.dialogOpen = manual
			})
			return
		}
		version := res.Candidate.Release.Version.String()
		st, _ := u.state.Mutate(func(st *update.State) {
			st.LastCheckDate = today
			st.LastCheckVersion = current
			st.LastCheckedAt = now
			st.LastResult = update.ResultUpdateAvailable
			st.LastError = ""
			st.LatestVersion = version
		})
		show := manual || !st.Suppresses(version, today)
		u.post(func() {
			u.candidate = res.Candidate
			u.current = current
			u.dialogKind = updateDialogAvailable
			u.dialogOpen = show
		})
	}
}

func classifyUpdateError(err error) (updateResultCode, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return updateResultOffline, update.ResultOffline
	}
	kind, ok := update.KindOf(err)
	if !ok {
		return updateResultError, update.ResultError
	}
	switch kind {
	case update.ErrorRateLimit:
		return updateResultRateLimited, update.ResultRateLimited
	case update.ErrorNetwork:
		return updateResultOffline, update.ResultOffline
	case update.ErrorNoRelease:
		return updateResultNoRelease, update.ResultNoRelease
	case update.ErrorNoAsset:
		return updateResultNoAsset, update.ResultNoAsset
	default:
		return updateResultError, update.ResultError
	}
}

// --- dialog actions ------------------------------------------------------

func (u *updateController) closeDialog() {
	u.dialogOpen = false
	u.dialogKind = updateDialogNone
	u.cancel = nil
}

// later defers this version for the rest of the local day.
func (u *updateController) later() {
	if u.candidate != nil {
		version := u.candidate.Release.Version.String()
		today := u.today()
		u.state.Mutate(func(st *update.State) {
			st.DeferredVersion = version
			st.DeferredDate = today
		})
	}
	u.closeDialog()
}

// skipVersion stops automatic prompts for exactly this version.
func (u *updateController) skipVersion() {
	if u.candidate != nil {
		version := u.candidate.Release.Version.String()
		u.state.Mutate(func(st *update.State) { st.SkippedVersion = version })
	}
	u.closeDialog()
}

// neverNotify persistently disables automatic checks and reminders. Manual
// checks and the Settings toggle still work.
func (u *updateController) neverNotify() {
	u.state.Mutate(func(st *update.State) { st.NeverNotify = true })
	u.closeDialog()
}

func (u *updateController) cancelCheck() {
	if u.cancel != nil {
		u.cancel()
	}
	u.closeDialog()
}

// startDownload downloads and verifies the selected asset, then opens the
// platform installer or disk image. It never replaces the app, never quits it,
// and never reports the update as installed.
func (u *updateController) startDownload() {
	if u == nil || u.candidate == nil {
		return
	}
	cand := u.candidate
	u.dialogKind = updateDialogDownloading
	u.dialogOpen = true
	u.progress, u.total = 0, cand.Asset.Size
	ctx, cancel := context.WithTimeout(context.Background(), updateDownloadTimeout)
	u.cancel = cancel

	go u.downloadAndOpen(ctx, cand)
}

// downloadAndOpen is the download/open half of startDownload, split out so a
// test can run it synchronously against fake downloader/installer interfaces.
func (u *updateController) downloadAndOpen(ctx context.Context, cand *update.Candidate) {
	dl, err := u.downloader.Download(ctx, cand, func(done, total int64) {
		u.post(func() {
			u.progress = done
			u.total = total
		})
	})
	if err != nil {
		u.post(func() {
			if errors.Is(err, context.Canceled) {
				u.closeDialog()
				return
			}
			u.dialogKind = updateDialogResult
			u.result = updateResultError
			u.detail = err.Error()
			u.dialogOpen = true
			u.cancel = nil
		})
		return
	}
	if err := u.open(dl.Path); err != nil {
		u.post(func() {
			u.dialogKind = updateDialogResult
			u.result = updateResultError
			u.detail = err.Error()
			u.dialogOpen = true
			u.cancel = nil
		})
		return
	}
	u.post(func() {
		u.state.Mutate(func(st *update.State) {
			st.LastCheckedAt = u.now().UTC().Format(time.RFC3339)
			st.LastResult = update.ResultDownloaded
			st.LastError = ""
		})
		u.dialogKind = updateDialogResult
		u.result = updateResultDownloaded
		u.detail = dl.Path
		u.dialogOpen = true
		u.cancel = nil
	})
}

// openSettings closes the update dialog and opens the Settings sheet on About in the
// host window, where the user can restore automatic checks or check manually.
func (u *updateController) openSettings() {
	u.closeDialog()
	u.aboutRequested.Store(true)
	if u.mgr != nil {
		u.mgr.openSettingsWindow()
	}
}

// --- settings integration ------------------------------------------------

func (u *updateController) setAutoCheck(v bool) {
	if u == nil {
		return
	}
	u.state.Mutate(func(st *update.State) {
		b := v
		st.AutoCheck = &b
	})
}

func (u *updateController) setNeverNotify(v bool) {
	if u == nil {
		return
	}
	u.state.Mutate(func(st *update.State) { st.NeverNotify = v })
}

// lastStatusText summarizes the persisted last check for Settings. The raw
// error is kept for traceability, not hidden behind a generic label.
func (u *updateController) lastStatusText(l language) string {
	um := updateMessagesFor(l)
	st := u.state.Load()
	if strings.TrimSpace(st.LastCheckedAt) == "" {
		return um.NeverChecked
	}
	label := um.StatusUnknown
	switch st.LastResult {
	case update.ResultUpdateAvailable:
		if st.LatestVersion == "" {
			label = um.StatusUnknown
		} else {
			label = translate(um.StatusUpdateAvailable, st.LatestVersion)
		}
	case update.ResultUpToDate:
		label = um.StatusUpToDate
	case update.ResultDevelopment:
		label = um.StatusDevelopment
	case update.ResultNoRelease:
		label = um.StatusNoRelease
	case update.ResultNoAsset:
		label = um.StatusNoAsset
	case update.ResultRateLimited:
		label = um.StatusRateLimited
	case update.ResultOffline:
		label = um.StatusOffline
	case update.ResultCanceled:
		label = um.StatusCanceled
	case update.ResultDownloaded:
		label = um.StatusDownloaded
	case update.ResultError:
		label = um.StatusError
	}
	when := st.LastCheckedAt
	if t, err := time.Parse(time.RFC3339, st.LastCheckedAt); err == nil {
		when = t.Local().Format("2006-01-02 15:04")
	}
	out := translate(um.LastCheckFormat, when, label)
	if st.LastError != "" && (st.LastResult == update.ResultError || st.LastResult == update.ResultRateLimited || st.LastResult == update.ResultOffline) {
		out += " · " + st.LastError
	}
	return out
}
