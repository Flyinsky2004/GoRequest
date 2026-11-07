package desktop

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// R2: the timeout entry is a compact badge inside the URL row, showing the
// value that actually takes effect. It replaces the old standalone row, so at
// most the variable chips and the resolved preview sit between the URL and the
// section tabs. Assertions use ui.NewTester geometry/text, not screenshots.
func TestTimeoutBadgeLivesInURLRowAndTracksResolution(t *testing.T) {
	a, ps := openTestProject(t, "TimeoutBadgeLayout")
	tab := openRequestTabs(t, ps, "Req")[0]
	tab.Method = "GET"
	tab.URL = "https://example.com/api"
	tab.Timeout = ""

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	urlRect, ok := tt.Find("request URL")
	if !ok {
		t.Fatalf("URL input not found; texts=%v", tt.Texts())
	}
	badgeText := fmt.Sprintf(networkMessagesEn.TimeoutBadgeFmt, "10s")
	badge, ok := tt.Find(badgeText)
	if !ok {
		t.Fatalf("default timeout badge %q missing; texts=%v", badgeText, tt.Texts())
	}

	// Same row: the badge's vertical center lies inside the URL input's band.
	centerY := badge.Y + badge.H/2
	if centerY < urlRect.Y || centerY > urlRect.Y+urlRect.H {
		t.Fatalf("timeout badge is not in the URL row: badge=%+v url=%+v", badge, urlRect)
	}
	// To the right of the URL input, not below it.
	if badge.X < urlRect.X+urlRect.W-1 {
		t.Fatalf("timeout badge is not to the right of the URL: badge=%+v url=%+v", badge, urlRect)
	}
	// The old standalone row must be gone.
	if tt.HasText(networkMessagesEn.TimeoutQuickEntry) {
		t.Fatalf("old standalone timeout row still rendered; texts=%v", tt.Texts())
	}

	// A request value updates the badge immediately.
	tab.Timeout = "3s"
	tt.Frame()
	if !tt.HasText(fmt.Sprintf(networkMessagesEn.TimeoutBadgeFmt, "3s")) {
		t.Fatalf("request timeout did not update the badge; texts=%v", tt.Texts())
	}

	// A positive device TotalTimeout wins, exactly as at send time.
	st := projectNetStateFor(ps, a)
	dev := st.netStore.DeviceSettings()
	dev.Timeouts.TotalTimeout = 7 * time.Second
	if err := st.netStore.SaveDeviceSettings(dev); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(fmt.Sprintf(networkMessagesEn.TimeoutBadgeFmt, "7s")) {
		t.Fatalf("device total timeout did not update the badge; texts=%v", tt.Texts())
	}

	// An invalid draft is flagged instead of showing a fake effective value.
	dev.Timeouts.TotalTimeout = 0
	if err := st.netStore.SaveDeviceSettings(dev); err != nil {
		t.Fatal(err)
	}
	tab.Timeout = "nope"
	tt.Frame()
	if !tt.HasText(networkMessagesEn.TimeoutBadgeInvalid) {
		t.Fatalf("invalid timeout was not flagged in the badge; texts=%v", tt.Texts())
	}

	// Clicking the badge reaches the existing Settings tab.
	tab.Timeout = "3s"
	tt.Frame()
	if err := tt.Click(fmt.Sprintf(networkMessagesEn.TimeoutBadgeFmt, "3s")); err != nil {
		t.Fatalf("click timeout badge: %v; texts=%v", err, tt.Texts())
	}
	if tab.Section != requestSettingsSection {
		t.Fatalf("timeout badge navigated to section %d, want %d", tab.Section, requestSettingsSection)
	}
}
