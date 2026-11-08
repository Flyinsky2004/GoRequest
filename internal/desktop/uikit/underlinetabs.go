package uikit

import "github.com/egoist/mygo/ui"

// UnderlineTabs renders a row of underline tabs (§5): 13px labels, the selected
// one in the text color at 600 with a 2px accent underline that slides between
// tabs with the shared spring, widths following each label. badges, when
// non-empty at an index, draw a small faint count after the label (for
// "Headers (3)"). A hairline runs along the whole row's bottom edge. It reports
// whether the selection changed this frame.
//
// It is the original-signature entry point of Tabs; new callers that need
// optional icons or disabled tabs use Tabs with TabEntry values.
func UnderlineTabs(c *ui.Context, p Palette, selected *int, labels []string, badges []string) (ui.Element, bool) {
	entries := entriesFromLabels(labels)
	for i := range entries {
		if i < len(badges) {
			entries[i].Badge = badges[i]
		}
	}
	return Tabs(c, p, selected, entries)
}
