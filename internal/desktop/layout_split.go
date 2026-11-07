package desktop

// Request/response split layout (§7). The splitter itself is MyGo's
// ui.SplitVerticalPercent, whose only guard is a fixed minimum pane of
// t.Space(10) DIPs on each side; it has no way to reserve a taller response
// pane. The helpers here add that guard around the widget without changing the
// user's stored preference.

const (
	// defaultBuildPct is the factory request/response split: 55% request,
	// 45% response.
	defaultBuildPct float32 = 55
	// minBuildPct and maxBuildPct bound a stored preference. They match the
	// range configStore accepts, so a value outside it is never persisted.
	minBuildPct float32 = 20
	maxBuildPct float32 = 86
	// minResponseHeight is the smallest response pane, in DIPs, that still
	// shows readable body text. When the window is too short, the request pane
	// is compressed first.
	minResponseHeight float32 = 180
)

// clampBuildPctForHeight returns the build share to hand to the splitter so
// the response pane keeps at least minResponseHeight DIPs. splitHeight is the
// split's committed height from the previous frame; 0 (first frame) means no
// measurement yet, so the preference is used unchanged.
//
// The clamp is temporary: it is applied to the value passed to the splitter,
// never written back to the stored preference.
func clampBuildPctForHeight(pref, splitHeight float32) float32 {
	pref = clampBuildPct(pref)
	panes := splitHeight - 1 // the 1 DIP divider is not part of either pane
	if panes <= 0 {
		return pref
	}
	maxShare := (panes - minResponseHeight) / panes * 100
	if maxShare < 0 {
		maxShare = 0
	}
	if pref > maxShare {
		return maxShare
	}
	return pref
}

// applyBuildPctMove folds a divider move into the stored preference. moved is
// the value the splitter wrote this frame and passed the value it was handed;
// their difference is the user's move. Applying the difference to the
// preference (instead of replacing it with moved) means a temporary height
// clamp never becomes the saved ratio.
func applyBuildPctMove(pref, passed, moved float32) float32 {
	return clampBuildPct(pref + (moved - passed))
}

// clampBuildPct bounds a stored preference to the accepted range.
func clampBuildPct(v float32) float32 {
	if v < minBuildPct {
		return minBuildPct
	}
	if v > maxBuildPct {
		return maxBuildPct
	}
	return v
}
