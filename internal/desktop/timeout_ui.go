package desktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// requestSettingsSection is the index of the "Settings" tab in the request
// editor's underline tabs (Params/Auth/Headers/Body/Scripts/Tests/Settings;
// see workspace.go). FIX08 keeps the quick-entry controller in its own file so
// the shared shell only needs the one-line call site supplied in
// docs/task-results/FIX08-integration.patch.
const requestSettingsSection = 6

// timeoutOrigin identifies which layer supplies the overall request deadline
// at execution time. The order mirrors core.ExecuteWithOptions: a positive
// device-local StageTimeouts.TotalTimeout wins, otherwise Plan.Timeout (built
// from the request's own timeout or core.DefaultTimeout) is used.
type timeoutOrigin int

const (
	timeoutOriginDefault timeoutOrigin = iota
	timeoutOriginRequest
	timeoutOriginDeviceTotal
)

// timeoutResolution is the UI-facing answer to "what timeout is actually in
// effect, and where does it come from?". It is deliberately a pure value: no
// window, network request, or config write is involved in computing it.
type timeoutResolution struct {
	Effective   time.Duration
	Origin      timeoutOrigin
	ParseErr    error // raw is not a Go duration; the plan builder rejects it before sending
	NonPositive bool  // raw parses to <= 0; core silently falls back to the built-in default
}

// Valid reports whether the request's draft would be accepted as-is.
func (r timeoutResolution) Valid() bool { return r.ParseErr == nil && !r.NonPositive }

// resolveRequestTimeout mirrors the two-layer deadline resolution performed at
// send time, without changing it:
//
//   - core.BuildTrackedPlanWithOptions / BuildPlanWithOptions set Plan.Timeout
//     from req.Timeout when it is a positive Go duration, otherwise to
//     core.DefaultTimeout (10s). A malformed value fails plan construction;
//     a non-positive one is silently replaced by the 10s default.
//   - core.ExecuteWithOptions (plan.go) uses opts.Timeouts.TotalTimeout when it
//     is > 0, otherwise Plan.Timeout. That total comes from the device-local
//     NetworkStore, not from the request or the project file.
//
// Project defaults.timeout is written into new project.yaml files but is never
// read by any plan builder in this build, so it is deliberately not a source.
// The UI must not claim "project inheritance" that the engine does not apply.
func resolveRequestTimeout(raw string, deviceTotal time.Duration) timeoutResolution {
	var res timeoutResolution
	planTimeout := core.DefaultTimeout
	if s := strings.TrimSpace(raw); s != "" {
		d, err := time.ParseDuration(s)
		switch {
		case err != nil:
			res.ParseErr = err
		case d <= 0:
			res.NonPositive = true
		default:
			planTimeout = d
			res.Origin = timeoutOriginRequest
		}
	}
	if deviceTotal > 0 {
		res.Effective = deviceTotal
		res.Origin = timeoutOriginDeviceTotal
	} else {
		res.Effective = planTimeout
	}
	return res
}

func timeoutSourceLabel(origin timeoutOrigin, nm *networkMessages) string {
	switch origin {
	case timeoutOriginRequest:
		return nm.TimeoutSourceRequest
	case timeoutOriginDeviceTotal:
		return nm.TimeoutSourceDeviceTotal
	default:
		return nm.TimeoutSourceDefault
	}
}

// validateStageTimeoutDrafts reports the first invalid stage-timeout draft
// without touching the device settings. applyNetworkDrafts already validates
// these, but every input handler discarded its error: one bad value silently
// blocked ALL network settings from being saved while the field kept showing
// the rejected text.
func validateStageTimeoutDrafts(st *projectNetworkState, nm *networkMessages) error {
	if st == nil || nm == nil {
		return nil
	}
	fields := []struct {
		label string
		raw   string
		def   time.Duration
	}{
		{nm.DialTimeoutLabel, st.dialTimeoutDraft, core.DefaultDialTimeout},
		{nm.TLSTimeoutLabel, st.tlsTimeoutDraft, core.DefaultTLSHandshakeTimeout},
		{nm.TTFBTimeoutLabel, st.ttfbTimeoutDraft, core.DefaultResponseHeaderTimeout},
	}
	for _, f := range fields {
		if _, err := parseOptionalDuration(f.raw, f.def); err != nil {
			return fmt.Errorf("%s: %w", f.label, err)
		}
	}
	return nil
}

// applyNetworkDraftsFromUI validates and applies the network modal drafts,
// keeping any error visible instead of silently discarding it. Stage-timeout
// errors are surfaced next to the fields (st.timeoutErr); any other save error
// is returned for the modal-level message. Drafts are never rewritten, so the
// last valid device settings stay in effect and the user's text is preserved.
func (st *projectNetworkState) applyNetworkDraftsFromUI(nm *networkMessages) error {
	if st == nil || nm == nil {
		return nil
	}
	if err := validateStageTimeoutDrafts(st, nm); err != nil {
		st.timeoutErr = fmt.Sprintf(nm.NetworkDraftsInvalidFmt, err)
		return err
	}
	st.timeoutErr = ""
	if err := st.applyNetworkDrafts(); err != nil {
		return err
	}
	return nil
}

// renderRequestTimeoutCard is the first card in the request editor's Settings
// tab and in the Network & TLS modal. It answers "where do I set the request
// timeout?" with a clearly named entry, its unit/examples, the effective value
// with its source, and inline feedback for a bad value. It renders no network
// request and writes nothing until the user saves the request.
func (a *App) renderRequestTimeoutCard(c *ui.Context, t *ui.Theme, tab *Tab, st *projectNetworkState, nm *networkMessages) {
	p := uikit.PaletteFor(t)
	var devTotal time.Duration
	if st != nil && st.netStore != nil {
		devTotal = st.netStore.DeviceSettings().Timeouts.TotalTimeout
	}

	ui.Column(c).Gap(6).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
		ui.Text(c, nm.RequestTimeoutHeader).FontSize(13).FontWeight(700)
		ui.Text(c, nm.RequestTimeoutScopeHint).FontSize(11).TextColor(p.Muted)

		if tab == nil {
			ui.Text(c, nm.TimeoutNoTabHint).FontSize(12).TextColor(p.Muted)
		} else {
			res := resolveRequestTimeout(tab.Timeout, devTotal)
			ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.TotalTimeoutLabel).FontSize(12).TextColor(p.Muted)
				if ui.TextInput(c, &tab.Timeout).Width(120).Font("monospace").
					Placeholder(nm.RequestTimeoutPlaceholder).Changed() {
					tab.Dirty = true
				}
				ui.Box(c).Grow(1)
			})
			ui.Text(c, nm.RequestTimeoutUnitHint).FontSize(11).TextColor(p.Muted)
			switch {
			case res.ParseErr != nil:
				ui.Text(c, fmt.Sprintf(nm.TimeoutInvalidFmt, res.ParseErr)).
					FontSize(11).TextColor(t.Danger)
			case res.NonPositive:
				ui.Text(c, nm.TimeoutNonPositiveFmt).FontSize(11).TextColor(t.Danger)
			default:
				ui.Text(c, fmt.Sprintf(nm.TimeoutEffectiveFmt,
					res.Effective.String(), timeoutSourceLabel(res.Origin, nm))).
					FontSize(11).FontWeight(600).TextColor(p.Muted)
			}
		}
		ui.Text(c, nm.TimeoutEntryHint).FontSize(11).TextColor(p.Faint)
	})
}

// renderStageTimeoutCard keeps the three device-local phase timeouts separate
// from the request-level overall timeout. These are machine-local limits saved
// by NetworkStore; they are never written into the shared project request.
func (a *App) renderStageTimeoutCard(c *ui.Context, t *ui.Theme, st *projectNetworkState, nm *networkMessages) {
	if st == nil || nm == nil {
		return
	}
	p := uikit.PaletteFor(t)

	ui.Column(c).Gap(6).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
		ui.Text(c, nm.StageTimeoutsHeader).FontSize(12).FontWeight(600)
		ui.Text(c, nm.StageTimeoutsNote).FontSize(11).TextColor(p.Muted)
		ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, nm.DialTimeoutLabel).FontSize(12).TextColor(p.Muted)
			if ui.TextInput(c, &st.dialTimeoutDraft).Width(90).Font("monospace").Placeholder("10s").Changed() {
				_ = st.applyNetworkDraftsFromUI(nm)
			}
			ui.Text(c, nm.TLSTimeoutLabel).FontSize(12).TextColor(p.Muted)
			if ui.TextInput(c, &st.tlsTimeoutDraft).Width(90).Font("monospace").Placeholder("10s").Changed() {
				_ = st.applyNetworkDraftsFromUI(nm)
			}
		})
		ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, nm.TTFBTimeoutLabel).FontSize(12).TextColor(p.Muted)
			if ui.TextInput(c, &st.ttfbTimeoutDraft).Width(90).Font("monospace").Placeholder("30s").Changed() {
				_ = st.applyNetworkDraftsFromUI(nm)
			}
			ui.Box(c).Grow(1)
		})
		if st.timeoutErr != "" {
			ui.Text(c, st.timeoutErr).FontSize(11).TextColor(t.Danger)
		}
	})
}

// renderTimeoutBadge is the compact timeout affordance in the URL row. It
// shows the value that actually takes effect, resolved by the same
// resolveRequestTimeout rules the plan builder mirrors, so the label is never
// a hard-coded default. Clicking it opens the request editor's Settings tab,
// where the effective value, its source and the edit field already live; the
// badge itself stays a single line and does not add a settings surface.
func (a *App) renderTimeoutBadge(c *ui.Context, t *ui.Theme, tab *Tab) {
	if tab == nil || tab.Kind != "request" {
		return
	}
	nm := a.networkMsg()
	p := uikit.PaletteFor(t)

	var devTotal time.Duration
	if st := projectNetStateFor(tab.Owner, a); st != nil && st.netStore != nil {
		devTotal = st.netStore.DeviceSettings().Timeouts.TotalTimeout
	}
	res := resolveRequestTimeout(tab.Timeout, devTotal)

	label := fmt.Sprintf(nm.TimeoutBadgeFmt, res.Effective.String())
	color := p.Muted
	if !res.Valid() {
		label = nm.TimeoutBadgeInvalid
		color = t.Danger
	}
	badge := uikit.ActionCursor(ui.Row(c).Height(24).Radius(6).Padding(0, 8).Gap(4).
		AlignItems(ui.Center).BorderWidth(1).BorderColor(p.Hairline).Background(p.Hover))
	badge.Children(func() {
		ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(color).SingleLine()
	})
	badge.Label(label).Tooltip(nm.TimeoutQuickEntryTip)
	if badge.Clicked() {
		tab.Section = requestSettingsSection
	}
}
