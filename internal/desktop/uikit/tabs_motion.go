package uikit

// Tab motion tokens. The shared beat sheet lives in motion.go; the tab family
// names the timings it uses here, so its components carry no magic durations
// and this task's files never edit the shared motion table.
var (
	// AnimTabIndicator slides the selected indicator between tabs and the
	// segmented capsule between segments (§6.1).
	AnimTabIndicator = SpringSlide
	// AnimTabContent fades and lifts newly selected content (§6.1).
	AnimTabContent = AnimContent
	// AnimTabHover is the hover/press wash of tab and segment faces.
	AnimTabHover = AnimHover
)

// TabContentRise is how far newly selected content rises into place, in DIPs.
const TabContentRise float32 = 4
