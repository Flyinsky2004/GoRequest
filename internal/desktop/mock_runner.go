package desktop

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/mockserver"
)

// runnerMockSeed is the deterministic seed used for a Runner-scoped controlled
// Mock snapshot. The Runner panel has no per-run seed control; a fixed seed keeps
// generated mock values reproducible across runs.
const runnerMockSeed int64 = 1

// runnerMockResult is the local, in-memory outcome of the controlled Mock
// lifecycle for one Runner run. It is never written into shared project YAML.
type runnerMockResult struct {
	BaseURL       string
	BoundAddr     string
	Port          int
	Scenario      string
	Snapshot      string
	AccessRecords []mockserver.AccessRecord
	ServerStopped bool
	StopErr       string
}

// startRunnerMockLease validates the frozen panel configuration, compiles the
// project's mock rules for the selected scenario, and binds a loopback listener.
// The returned variables carry the actual bound endpoint for Suite injection.
func (a *App) startRunnerMockLease(ctx context.Context, p *core.Project, s *runnerState) (*mockserver.SuiteMockLease, map[string]string, error) {
	if p == nil {
		return nil, nil, fmt.Errorf("controlled Mock requires a loaded project")
	}
	port, err := s.mockPortValue()
	if err != nil {
		return nil, nil, err
	}
	scenario := strings.TrimSpace(s.mockScenario)
	if scenario == "" {
		scenario = mockserver.DefaultScenario
	}
	endpointVar := strings.TrimSpace(s.mockEndpointVar)
	if endpointVar != "" && core.IsReservedNamespace(endpointVar) {
		return nil, nil, fmt.Errorf("Mock endpoint variable %q uses a reserved namespace (secret./data./steps.)", endpointVar)
	}
	snap, err := mockserver.CompileProjectMocks(p, scenario, runnerMockSeed, mockserver.ServerLimits{})
	if err != nil {
		return nil, nil, err
	}
	if snap == nil || len(snap.Rules) == 0 {
		return nil, nil, &core.ConfigError{Err: fmt.Errorf("controlled Mock requires at least one enabled rule for scenario %q", scenario)}
	}
	lease, err := mockserver.StartSuiteMockLease(ctx, p, mockserver.SuiteMockConfig{
		Port:        port,
		Scenario:    scenario,
		Seed:        runnerMockSeed,
		EndpointVar: endpointVar,
		Snapshot:    snap,
		// Register secrets before the listener accepts requests so access records
		// are scrubbed from the first request.
		Redactor: a.buildMockRedactor(p),
	})
	if err != nil {
		return nil, nil, err
	}
	return lease, lease.Variables(), nil
}

// runnerMockMetadata captures the endpoint and frozen rule identity before the
// lease is stopped (BaseURL/Port report "" once stopped).
func runnerMockMetadata(lease *mockserver.SuiteMockLease) *runnerMockResult {
	if lease == nil {
		return nil
	}
	return &runnerMockResult{
		BaseURL:   lease.BaseURL(),
		BoundAddr: lease.BoundAddr(),
		Port:      lease.Port(),
		Scenario:  lease.Scenario(),
		Snapshot:  lease.SnapshotDigest(),
	}
}

// runnerMockFinalize records the bounded access records and the real shutdown
// outcome. It never reports ServerStopped for a failed shutdown.
func runnerMockFinalize(lease *mockserver.SuiteMockLease, meta *runnerMockResult, stopErr error) *runnerMockResult {
	if lease == nil {
		return nil
	}
	out := &runnerMockResult{}
	if meta != nil {
		*out = *meta
	}
	out.AccessRecords = lease.AccessRecords()
	out.ServerStopped = lease.ServerStopped()
	if stopErr != nil {
		out.StopErr = stopErr.Error()
	}
	return out
}

// stopRunnerMockLease stops a lease with a bounded timeout. The run context may
// already be canceled, so shutdown always uses a fresh background context.
func stopRunnerMockLease(lease *mockserver.SuiteMockLease) error {
	if lease == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return lease.Stop(ctx)
}

// mockPortValue parses the panel's port text. Empty means the ephemeral port 0.
func (s *runnerState) mockPortValue() (int, error) {
	if s == nil {
		return 0, nil
	}
	raw := strings.TrimSpace(s.mockPort)
	if raw == "" {
		return 0, nil
	}
	p, err := strconv.Atoi(raw)
	if err != nil || p < 0 || p > 65535 {
		return 0, fmt.Errorf("invalid Mock port %q (must be 0..65535; 0 allocates an ephemeral loopback port)", raw)
	}
	return p, nil
}

// mockConfigError returns a localized-free validation message for the current
// Mock configuration, or "" when it is valid. The panel renders it next to the
// port field and StartRunner re-validates before binding.
func (s *runnerState) mockConfigError() string {
	if s == nil || !s.mockEnabled {
		return ""
	}
	if _, err := s.mockPortValue(); err != nil {
		return err.Error()
	}
	if v := strings.TrimSpace(s.mockEndpointVar); v != "" && core.IsReservedNamespace(v) {
		return fmt.Sprintf("Mock endpoint variable %q uses a reserved namespace", v)
	}
	return ""
}

// runnerMockConfigUI renders the Runner panel's controlled Mock configuration.
// It is called from the integrator-owned RunnerModals via a one-line patch; the
// controls are disabled while a run is active so this run's frozen values cannot
// be changed underneath it.
func (a *App) runnerMockConfigUI(c *ui.Context, t *ui.Theme, p uikit.Palette, s *runnerState) {
	if s == nil {
		return
	}
	m := a.runnerMockMsg()
	ui.Column(c).FillWidth().Gap(6).Padding(8, 10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			toggle := ui.Checkbox(c, &s.mockEnabled, m.Enable).Disabled(s.running)
			uikit.ActionCursor(toggle)
			ui.Box(c).Grow(1)
			if s.running && s.mockEnabled {
				ui.Text(c, m.RunningLocked).FontSize(11).TextColor(p.Muted)
			}
		})
		if !s.mockEnabled {
			return
		}
		ui.Text(c, m.Loopback).FontSize(11).TextColor(p.Muted)
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.Scenario).FontSize(12).TextColor(p.Muted)
			scenario := uikit.MenuButton(c, runnerValue(s.mockScenario), func(mm *ui.Menu) {
				choices := s.mockScenarios
				if len(choices) == 0 {
					choices = []string{mockserver.DefaultScenario}
				}
				for _, sc := range choices {
					choice := sc
					if mm.Item(choice).Chosen() {
						s.mockScenario = choice
					}
				}
			})
			scenario.Disabled(s.running)
			ui.Text(c, m.Port).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &s.mockPort).Width(80).Disabled(s.running)
			ui.Text(c, m.EndpointVar).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &s.mockEndpointVar).Width(140).Disabled(s.running)
		})
		ui.Text(c, m.EndpointHint).FontSize(11).TextColor(p.Faint)
		if errText := s.mockConfigError(); errText != "" {
			ui.Text(c, errText).FontSize(11).TextColor(t.Danger)
		}
	})
}

// runnerMockResultUI renders the last controlled Mock outcome (actual endpoint,
// scenario, rule digest, release status and bounded request count).
func (a *App) runnerMockResultUI(c *ui.Context, t *ui.Theme, p uikit.Palette, s *runnerState) {
	lines := a.runnerMockSummary(s)
	if len(lines) == 0 {
		return
	}
	ui.Column(c).FillWidth().Gap(2).Padding(8, 10).Background(p.Sidebar).Radius(6).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		for i, line := range lines {
			color := p.Text
			if i == 0 && s.mockResult != nil && !s.mockResult.ServerStopped {
				color = t.Warning
			}
			ui.Text(c, line).FontSize(11).TextColor(color)
		}
	})
}

func shortRunnerMockDigest(digest string) string {
	if len(digest) > 16 {
		return digest[:16]
	}
	return digest
}

// runnerMockSummary returns display lines for the last controlled Mock run. It
// is presentation-only; lifecycle state stays in runnerState/mock_runner.go.
func (a *App) runnerMockSummary(s *runnerState) []string {
	if s == nil || s.mockResult == nil {
		return nil
	}
	r := s.mockResult
	m := a.runnerMockMsg()
	released := m.Released
	if !r.ServerStopped {
		released = m.NotReleased
	}
	lines := []string{
		fmt.Sprintf("%s: %s · %s=%s · %s=%s · %s", m.Result, r.BaseURL, m.ScenarioLabel, r.Scenario, m.DigestLabel, shortRunnerMockDigest(r.Snapshot), released),
		fmt.Sprintf("%s: %d", m.AccessLabel, len(r.AccessRecords)),
	}
	if r.StopErr != "" {
		lines = append(lines, m.StopError+": "+r.StopErr)
	}
	return lines
}
