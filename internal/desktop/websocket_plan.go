package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"weak"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/protocolrun"
	"gorequest/internal/stream"
	gows "gorequest/internal/websocket"
)

// websocket_plan.go owns the bounded script-action workspace: open a saved
// protocolrun plan, validate it, run it once, cancel it and inspect the
// per-step facts. It is deliberately a separate section from the manual
// debugging pane (Messages): the manual session is driven by hand, while a
// plan is a bounded, replayable sequence that always re-resolves the current
// environment and secrets and never reuses values captured from a past run.
//
// Nothing here runs on open, on file selection or on a frame: only the explicit
// Run action starts network I/O, and one run owns at most one connection.

// wsSectionScript is the configuration tab index that selects the bounded
// script-action workspace. It is deliberately a sibling of Headers/Auth/
// Settings: selecting it replaces the manual message pane, so hand-driven
// debugging and a bounded, replayable plan are never mixed on one surface.
const wsSectionScript = 3

// planStateRunning is the UI-only state shown while a run is in flight.
const planStateRunning protocolrun.RunState = "running"

// wsPlanState is the bounded-plan state of one WebSocket tab.
type wsPlanState struct {
	path    string            // absolute path of the opened plan file
	digest  string            // digest of the plan bytes last loaded
	refPath string            // project-relative path persisted into the request, if any
	plan    *protocolrun.Plan // last successfully decoded plan
	err     string            // load/compile/run error shown to the user
	note    string            // short status line

	result *protocolrun.Result
	runErr string

	running  bool
	busy     bool
	cancel   context.CancelFunc
	gen      uint64
	started  time.Time
	finished time.Time

	// tried records that a persisted plan reference was already restored for
	// this tab, so a missing/broken file is not re-read on every frame.
	tried bool

	confirmChecked bool
	confirmAsked   bool
	promptFacts    string
}

// ensureWSPlan returns this tab's plan state, creating it on first use.
func (st *wsTabState) ensureWSPlan() *wsPlanState {
	if st == nil {
		return nil
	}
	if st.plan == nil {
		st.plan = &wsPlanState{}
	}
	return st.plan
}

// wsPlanOpenFile lets the user choose a plan file. The file is decoded and
// validated immediately, but nothing is executed.
func (a *App) wsPlanOpenFile(tab *Tab) {
	st := tab.ensureWSState()
	if st == nil {
		return
	}
	pl := st.ensureWSPlan()
	_ = pl
	a.pickLocalFileDialog(func(abs string) {
		a.wsPlanLoad(tab, abs)
	})
}

// wsPlanLoad decodes and validates a plan file from disk and, when the file
// lives inside the project store, binds it to the request as a project-relative
// reference. It never runs anything.
func (a *App) wsPlanLoad(tab *Tab, path string) {
	st := tab.ensureWSState()
	if st == nil {
		return
	}
	pl := st.ensureWSPlan()
	m := a.wsWorkspaceMsg()
	p, digest, err := protocolrun.LoadPlanFile(path)
	if err != nil {
		pl.err = fmt.Sprintf(m.PlanLoadFailedFmt, err.Error())
		pl.note = ""
		return
	}
	pl.path = path
	pl.digest = digest
	pl.plan = p
	pl.err = ""
	pl.result = nil
	pl.runErr = ""
	pl.note = fmt.Sprintf(m.PlanLoadedFmt, path, len(p.Steps))

	// Persist a project-relative reference so a bounded WS request can be
	// executed later (for example from a suite) without copying the plan body
	// or any resolved secret into the request YAML. Files outside the project
	// stay local to this tab.
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	if ps != nil && ps.project != nil && tab.Entry != nil && tab.Entry.Request != nil {
		if rel := projectRelSDL(ps.project.Dir, path); rel != "" {
			protocolrun.SetPlanRef(tab.Entry.Request, protocolrun.PlanRef{Path: rel, Digest: digest})
			pl.refPath = rel
			if !tab.Dirty {
				tab.Dirty = true
			}
			return
		}
	}
	pl.refPath = ""
}

// wsPlanEnsure restores a persisted plan reference once per tab, on first
// render. It is a local disk read only; no network I/O happens on open.
func (a *App) wsPlanEnsure(tab *Tab) {
	st := tab.ensureWSState()
	if st == nil {
		return
	}
	pl := st.ensureWSPlan()
	if pl.tried || pl.path != "" || pl.running {
		return
	}
	pl.tried = true
	if tab.Entry == nil || tab.Entry.Request == nil {
		return
	}
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	if ps == nil || ps.project == nil {
		return
	}
	ref, ok := protocolrun.PlanRefFromRequest(tab.Entry.Request)
	if !ok {
		return
	}
	abs := filepath.Join(ps.project.Dir, filepath.FromSlash(ref.Path))
	a.wsPlanLoad(tab, abs)
	if pl.path == "" {
		// The stored file is gone or invalid; wsPlanLoad already set the error.
		return
	}
	if ref.Digest != "" && pl.digest != "" && ref.Digest != pl.digest {
		pl.note = fmt.Sprintf(a.wsWorkspaceMsg().PlanDigestChangedFmt, ref.Digest, pl.digest)
	}
}

// wsPlanRun compiles the plan against the CURRENT environment/secrets and
// starts one bounded run off the UI thread. A replay re-reads the file from
// disk, so an edited plan and changed secrets both take effect.
func (a *App) wsPlanRun(tab *Tab) {
	st := tab.ensureWSState()
	if st == nil {
		return
	}
	pl := st.ensureWSPlan()
	wm := a.wsWorkspaceMsg()
	if pl.running {
		pl.note = wm.PlanBusy
		return
	}
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	if ps == nil || ps.project == nil {
		pl.err = wm.PlanNoProject
		return
	}
	path := strings.TrimSpace(pl.path)
	if path == "" {
		pl.err = wm.PlanNoPlan
		return
	}
	if tab.Entry == nil {
		// ResolveVars reads the entry's collection/request variables; an
		// unattached tab has no such scope, so refuse instead of dereferencing.
		pl.err = wm.PlanNoProject
		return
	}

	// Replay contract: read the file again and resolve templates against the
	// current environment, current secrets and a fresh redactor. No header,
	// query or message value from an earlier run is ever reused.
	plan, err := protocolrun.OpenPlan(path)
	if err != nil {
		pl.err = fmt.Sprintf(wm.PlanLoadFailedFmt, err.Error())
		return
	}
	vars, _, err := ps.project.ResolveVars(tab.Entry, ps.currentEnv())
	if err != nil {
		pl.err = fmt.Sprintf(wm.PlanResolveFailedFmt, err.Error())
		return
	}
	red := core.NewRedactor()
	resolver := core.NewResolver(vars, ps.secrets)
	compiled, err := protocolrun.Compile(plan, protocolrun.CompileOptions{
		Resolver:  resolver,
		Redactor:  red,
		MaxBinary: protocolrun.MaxBinaryFileBytes,
	})
	if err != nil {
		pl.err = fmt.Sprintf(wm.PlanCompileFailedFmt, err.Error())
		return
	}

	net := a.buildExecuteOptionsForTab(ps, tab, nil, nil, red)
	jar := net.CookieJar
	if net.DisableCookies {
		jar = nil
	}
	if jar != nil {
		jar.RegisterSecrets(red)
	}
	opts := gows.Options{
		Headers:           nil,
		EnableCompression: st.compress,
		Deadlines: stream.DeadlinePolicy{
			Connect:   net.Timeouts.DialTimeout,
			Handshake: net.Timeouts.TLSHandshakeTimeout,
			Message:   gows.DefaultMessageTimeout,
			// The plan's own total deadline is enforced by protocolrun.Run's
			// context; duplicating it here would produce two competing clocks.
			Total: 0,
		},
		MaxMessageBytes: plan.Limits.EffectiveMaxMessageBytes(),
		Proxy:           net.Proxy,
		TLS:             net.TLS,
		TargetPolicy:    net.TargetPolicy,
		HasTargetPolicy: net.HasTargetPolicy,
		EnvID:           net.EnvID,
		Interactive:     true,
		Confirmed:       pl.confirmChecked,
		HasCredentials:  wsPlanHasCredentials(compiled),
		Secrets:         net.Secrets,
		LocalFiles:      net.LocalFiles,
		ProjectRoot:     net.ProjectRoot,
		Redactor:        red,
		CookieJar:       jar,
	}
	// Preflight the exact connect step so the confirmation is visible BEFORE
	// any attempt. Connect re-evaluates and enforces the same policy.
	if spec := wsPlanConnectSpec(compiled); spec != nil {
		opts.URL = spec.URL
		opts.Subprotocols = append([]string(nil), spec.Subprotocols...)
		opts.Headers = make([][2]string, 0, len(spec.Headers))
		for _, h := range spec.Headers {
			opts.Headers = append(opts.Headers, [2]string{h.Name, h.Value})
		}
	}
	if decision, perr := gows.PromptRequiredBy(&opts); perr == nil && decision != nil &&
		decision.Outcome == core.DecisionConfirmRequired && !pl.confirmChecked {
		pl.confirmAsked = true
		pl.promptFacts = strings.Join(decision.Reasons, "; ")
		pl.note = wm.PlanConfirmNeeded
		return
	}
	// Confirmation approves THIS target for THIS run only.
	pl.confirmChecked = false
	pl.confirmAsked = false
	pl.promptFacts = ""

	ctx, cancel := context.WithCancel(context.Background())
	pl.cancel = cancel
	pl.running = true
	pl.busy = true
	pl.err = ""
	pl.runErr = ""
	pl.result = nil
	pl.plan = plan
	pl.note = wm.PlanRunning
	pl.started = time.Now().UTC()
	pl.finished = time.Time{}
	pl.gen++
	gen := pl.gen

	key := weak.Make(a)
	win := a.win
	limits := plan.Limits
	go func() {
		tr := protocolrun.NewWebSocketTransport(opts, limits)
		res := protocolrun.Run(ctx, compiled, tr)
		cancel()
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			owner := key.Value()
			if owner == nil || owner.closed {
				return
			}
			d := tab.ws
			if d == nil || d.plan == nil || d.plan.gen != gen || ps.indexOfTab(tab) < 0 {
				return
			}
			wm := owner.wsWorkspaceMsg()
			pl := d.plan
			pl.running = false
			pl.busy = false
			pl.cancel = nil
			pl.result = res
			pl.finished = time.Now().UTC()
			if res != nil && res.Error != "" && res.State != protocolrun.StatePassed {
				pl.runErr = fmt.Sprintf(wm.PlanRunFailedFmt, res.Error)
			}
			pl.note = fmt.Sprintf(wm.PlanRunDoneFmt, res.State)
		})
	}()
}

// wsPlanCancel cancels the in-flight run. The transport closes with the
// runner's cancel code; the result keeps the cancel fact.
func (a *App) wsPlanCancel(tab *Tab) {
	st := tab.ensureWSState()
	if st == nil || st.plan == nil {
		return
	}
	pl := st.plan
	if !pl.running || pl.cancel == nil {
		return
	}
	pl.cancel()
	pl.note = a.wsWorkspaceMsg().PlanCanceling
}

// wsPlanConnectSpec returns the plan's single connect step, if any.
func wsPlanConnectSpec(plan *protocolrun.CompiledPlan) *protocolrun.ConnectSpec {
	if plan == nil {
		return nil
	}
	for i := range plan.Steps {
		if plan.Steps[i].Connect != nil {
			return plan.Steps[i].Connect
		}
	}
	return nil
}

// wsPlanHasCredentials reports whether any resolved handshake header carries
// credentials, so the target policy evaluates the same "credentials over plain
// transport" rules as a manual session.
func wsPlanHasCredentials(plan *protocolrun.CompiledPlan) bool {
	spec := wsPlanConnectSpec(plan)
	if spec == nil {
		return false
	}
	for _, h := range spec.Headers {
		if gowsHeaderCarriesCredentials(h.Name) {
			return true
		}
	}
	return false
}

// wsPlanSection is the upper pane of the Script section: file selection,
// explicit run/cancel and the plan-level facts.
func (a *App) wsPlanSection(c *ui.Context, t *ui.Theme, tab *Tab, st *wsTabState, wm *wsWorkspaceMessages, p uikit.Palette) {
	a.wsPlanEnsure(tab)
	pl := st.ensureWSPlan()
	ui.Column(c).Grow(1).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			pathText := pl.path
			if pathText == "" {
				pathText = wm.PlanPathPlaceholder
			}
			ui.Text(c, pathText).FontSize(12).Font("monospace").TextColor(p.Muted).SingleLine().Grow(1)
			if uikit.ActionCursor(ui.Button(c, wm.PlanOpen)).Clicked() {
				a.wsPlanOpenFile(tab)
			}
			if pl.running {
				if uikit.ActionCursor(ui.Button(c, wm.PlanCancel)).Clicked() {
					a.wsPlanCancel(tab)
				}
			} else if uikit.ActionCursor(uikit.PrimaryButton(c, wm.PlanRun).Disabled(pl.path == "")).Clicked() {
				a.wsPlanRun(tab)
			}
		})
		if pl.note != "" {
			ui.Text(c, pl.note).FontSize(11).TextColor(p.Muted).SingleLine()
		}
		if pl.err != "" {
			ui.Text(c, pl.err).FontSize(11).TextColor(p.Danger)
		}
		if pl.runErr != "" {
			ui.Text(c, pl.runErr).FontSize(11).TextColor(p.Danger)
		}
		if pl.confirmAsked {
			ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(8).
				BorderWidth(1).BorderColor(t.Warning).Children(func() {
				ui.Text(c, wm.PlanConfirmNeeded).FontSize(12).FontWeight(600).TextColor(t.Warning)
				if pl.promptFacts != "" {
					ui.Text(c, pl.promptFacts).FontSize(11).TextColor(p.Muted)
				}
				uikit.ActionCursor(ui.Checkbox(c, &pl.confirmChecked, wm.PlanConfirmCheck))
			})
		}
		ui.Text(c, wm.PlanReplayHint).FontSize(11).TextColor(p.Faint)
		if pl.plan == nil {
			ui.Column(c).Grow(1).FillWidth().Center().Children(func() {
				ui.Text(c, wm.PlanNoPlan).FontSize(12).TextColor(p.Faint)
			})
			return
		}
		a.wsPlanSteps(c, tab, pl, wm, p)
	})
}

// wsPlanSteps lists the declared actions so the user can see the bounded
// sequence before running it.
func (a *App) wsPlanSteps(c *ui.Context, tab *Tab, pl *wsPlanState, wm *wsWorkspaceMessages, p uikit.Palette) {
	plan := pl.plan
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Text(c, wm.PlanStepsLabel).FontSize(11).TextColor(p.Faint)
		ui.Text(c, fmt.Sprintf(wm.PlanBoundsFmt,
			formatByteSize(plan.Limits.EffectiveMaxMessageBytes()),
			formatDuration(plan.Limits.EffectiveTotalTimeout()),
			plan.Limits.EffectiveMaxRetainMessages())).FontSize(10).TextColor(p.Faint)
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				for i := range plan.Steps {
					step := plan.Steps[i]
					label := step.Label
					if label == "" {
						label = string(step.Action)
					}
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, fmt.Sprintf("#%d", i+1)).FontSize(10).Font("monospace").
							TextColor(p.Faint).Width(28)
						ui.Text(c, string(step.Action)).FontSize(10).Font("monospace").
							TextColor(p.Muted).Width(60)
						ui.Text(c, label).FontSize(11).TextColor(p.Text).SingleLine().Grow(1)
					})
				}
			})
		})
	})
}

// wsPlanResultPane is the lower pane while the Script section is selected. It
// renders the per-step facts exactly as the runner recorded them: direction,
// sequence, cursor movement, skipped/retained messages, the failure and the
// transport close code — never folded into an HTTP status.
func (a *App) wsPlanResultPane(c *ui.Context, t *ui.Theme, tab *Tab, st *wsTabState, wm *wsWorkspaceMessages, p uikit.Palette) {
	pl := st.ensureWSPlan()
	ui.Column(c).Grow(1).FillHeight().Background(p.BG).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(8).Padding(6, 10).AlignItems(ui.Center).
			BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
			ui.Text(c, wm.PlanResultTitle).FontSize(12).FontWeight(600).TextColor(p.Text)
			if pl.running {
				wsPlanStateChip(c, planStateRunning, wm, p)
			} else if pl.result != nil {
				wsPlanStateChip(c, pl.result.State, wm, p)
			}
			ui.Box(c).Grow(1)
			if pl.running && pl.cancel != nil {
				if uikit.ActionCursor(ui.Button(c, wm.PlanCancel)).Clicked() {
					a.wsPlanCancel(tab)
				}
			}
		})
		if pl.result == nil {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, wm.PlanNoResult).FontSize(12).TextColor(p.Faint)
			})
			return
		}
		res := pl.result
		ui.Row(c).FillWidth().Wrap().Gap(12).Padding(2, 10).Children(func() {
			ui.Text(c, fmt.Sprintf(wm.PlanTotalsFmt, res.SentCount, res.ReceivedCount,
				formatByteSize(res.BytesReceived))).FontSize(11).Font("monospace").TextColor(p.Muted)
			if res.FailedStep >= 0 {
				ui.Text(c, fmt.Sprintf(wm.PlanFailedStepFmt, res.FailedStep+1)).FontSize(11).TextColor(t.Danger)
			}
			if res.CloseCode != 0 || res.CloseReason != "" {
				ui.Text(c, fmt.Sprintf(wm.PlanCloseFmt, res.CloseCode, res.CloseReason)).FontSize(11).
					Font("monospace").TextColor(p.Faint)
			}
			if res.EndedAt.After(res.StartedAt) {
				ui.Text(c, formatDuration(res.EndedAt.Sub(res.StartedAt))).FontSize(11).
					Font("monospace").TextColor(p.Faint)
			}
		})
		if res.Error != "" {
			ui.Text(c, res.Error).FontSize(11).TextColor(t.Danger).Padding(2, 10)
		}
		ui.Scroll(c).Grow(1).FillWidth().Padding(6, 10).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				for i := range res.Steps {
					a.wsPlanStepResult(c, t, res.Steps[i], wm, p)
				}
			})
		})
	})
}

// wsPlanStepResult renders one step's recorded facts.
func (a *App) wsPlanStepResult(c *ui.Context, t *ui.Theme, sr protocolrun.StepResult, wm *wsWorkspaceMessages, p uikit.Palette) {
	label := sr.Label
	if label == "" {
		label = string(sr.Action)
	}
	ui.Column(c).FillWidth().Gap(2).Padding(6, 8).Background(p.Sidebar).Radius(6).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("#%d", sr.Index+1)).FontSize(10).Font("monospace").
				TextColor(p.Faint).Width(28)
			ui.Text(c, wsDirectionGlyph(sr.Direction)).FontSize(12).
				TextColor(wsDirectionColor(sr.Direction, p))
			ui.Text(c, string(sr.Action)).FontSize(10).Font("monospace").TextColor(p.Muted).Width(56)
			ui.Text(c, label).FontSize(11).TextColor(p.Text).SingleLine().Grow(1)
			wsPlanStateChip(c, sr.State, wm, p)
		})
		ui.Row(c).FillWidth().Wrap().Gap(12).Children(func() {
			ui.Text(c, fmt.Sprintf(wm.PlanCursorFmt, sr.CursorBefore, sr.CursorAfter)).
				FontSize(10).Font("monospace").TextColor(p.Faint)
			if sr.SentSeq != 0 {
				ui.Text(c, fmt.Sprintf(wm.PlanSentSeqFmt, sr.SentSeq)).FontSize(10).
					Font("monospace").TextColor(p.Faint)
			}
			if sr.ReceivedSeq != 0 {
				ui.Text(c, fmt.Sprintf(wm.PlanRecvSeqFmt, sr.ReceivedSeq, formatByteSize(int64(sr.ReceivedBytes)))).
					FontSize(10).Font("monospace").TextColor(p.Faint)
			}
			if len(sr.Skipped) > 0 {
				retained := 0
				for _, sk := range sr.Skipped {
					if sk.Retained {
						retained++
					}
				}
				ui.Text(c, fmt.Sprintf(wm.PlanSkippedFmt, len(sr.Skipped), retained)).FontSize(10).
					TextColor(t.Warning)
			}
			if sr.CloseCode != 0 || sr.CloseReason != "" {
				ui.Text(c, fmt.Sprintf(wm.PlanCloseFmt, sr.CloseCode, sr.CloseReason)).FontSize(10).
					Font("monospace").TextColor(t.Danger)
			}
		})
		if sr.Detail != "" {
			ui.Text(c, sr.Detail).FontSize(10).TextColor(p.Muted).SingleLine()
		}
		if len(sr.Payload) > 0 {
			preview := string(sr.Payload)
			if sr.Truncated {
				preview += " …"
			}
			ui.Text(c, preview).FontSize(10).Font("monospace").TextColor(p.Text).
				Selectable().SingleLine()
		}
		if sr.Error != "" {
			ui.Text(c, fmt.Sprintf(wm.PlanStepErrorFmt, sr.Error)).FontSize(10).TextColor(t.Danger)
		}
	})
}

// wsPlanStateChip shows a run/step state as text + color (never color alone).
func wsPlanStateChip(c *ui.Context, state protocolrun.RunState, wm *wsWorkspaceMessages, p uikit.Palette) {
	label, col := wm.PlanStatePassed, p.Success
	switch state {
	case protocolrun.StateFailed:
		label, col = wm.PlanStateFailed, p.Danger
	case protocolrun.StateCanceled:
		label, col = wm.PlanStateCanceled, p.Warning
	case protocolrun.StateTimedOut:
		label, col = wm.PlanStateTimedOut, p.Warning
	case planStateRunning:
		label, col = wm.PlanStateRunning, p.Muted
	}
	ui.Text(c, label).FontSize(10).Font("monospace").TextColor(col).
		Padding(1, 5).Background(p.BG).Radius(4).SingleLine()
}
