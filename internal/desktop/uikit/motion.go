package uikit

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// The global beat sheet (§6.1). Timings live here so no page or component ever
// carries a magic duration. Everything is a plain ui.Animation or a scale
// constant; the global spring bounce ceiling is 0.3 and only the icon morph
// uses it.
var (
	// Hover/press washes: rows and trees 100ms, cross cards 140ms, general
	// controls 120ms.
	AnimHover     = ui.Animation{Duration: 120 * time.Millisecond, Ease: ui.EaseOut}
	AnimHoverRow  = ui.Animation{Duration: 100 * time.Millisecond, Ease: ui.EaseOut}
	AnimHoverCard = ui.Animation{Duration: 140 * time.Millisecond, Ease: ui.EaseOut}

	// Sliding indicators (segmented capsule, underline, tab indicator) all use
	// one spring: 280ms, bounce 0.08.
	SpringSlide = ui.Animation{Spring: ui.SpringOptions{Duration: 280 * time.Millisecond, Bounce: 0.08}}

	// Icon morph (Send/copy/save): 340ms, bounce 0.30 — the one place the
	// ceiling is used.
	SpringIconMorph = ui.Animation{Spring: ui.SpringOptions{Duration: 340 * time.Millisecond, Bounce: 0.3}}

	// Number rollers (status/time/size): 420ms, bounce 0.15.
	SpringNumber = ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond, Bounce: 0.15}}

	// Side drawer slide-in: 450ms, bounce 0.08, 60 DIP X offset + fade.
	SpringPanel = ui.Animation{Spring: ui.SpringOptions{Duration: 450 * time.Millisecond, Bounce: 0.08}}

	// Content cross-fade (response arrival, tab content switch): 200ms + Y 4→0.
	AnimContent = ui.Animation{Duration: 200 * time.Millisecond, Ease: ui.EaseInOut}

	// Tree/folder expand-collapse and content growth: a 300ms spring with a
	// little bounce; per-row stagger 20ms.
	SpringExpand = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.15}}
)

// StaggerStep is the per-row delay when a group of rows enters (§6.3).
const StaggerStep = 20 * time.Millisecond

// Press scales (§5/§6.1). Send and the primary button are the 0.97 tier.
const (
	ScaleIconPress    float32 = 0.88
	ScalePillPress    float32 = 0.96
	ScalePrimaryPress float32 = 0.97
)

// Dialog and overlay enter/exit scales (§6.1): enter 0.94–0.96, exit 0.96–0.98.
const (
	ScaleOverlayEnter float32 = 0.96
	ScaleDialogEnter  float32 = 0.94
	ScaleDialogExit   float32 = 0.96
)

// Skeleton shimmer timing (§6.6): 1600ms period = 1100ms sweep + 500ms rest.
const (
	SkeletonPeriod = 1600 * time.Millisecond
	SkeletonScan   = 1100 * time.Millisecond
	SkeletonHold   = 500 * time.Millisecond
	SkeletonBand   = 260 // DIP width of the light band
)
