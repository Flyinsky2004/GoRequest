package desktop

// This file holds the wording introduced by the settings interface's
// left-category / right-content layout (FIX05). The shared translation catalog
// lives in i18n.go; the category names are kept here so this card does not edit
// that shared file. Group titles shown inside the content pane still come from
// the main catalog (Appearance/Language/Startup/…) or from their own owners
// (workspace preferences, Markdown preview), so no existing text moves.

// settingsCategory identifies one entry in the settings window's left rail.
// The order below is the display order.
type settingsCategory int

const (
	settingsCategoryAppearance settingsCategory = iota
	settingsCategoryLanguage
	settingsCategoryStartup
	settingsCategoryWorkspace
	settingsCategoryMarkdown
	settingsCategoryHistory
	settingsCategoryUpdates
	settingsCategoryAbout
	settingsCategoryCount
)

// settingsCategoryTitles returns the rail's category labels in display order.
// Every category maps to real, working settings; there are no placeholders.
func settingsCategoryTitles(l language) []string {
	if l == langEnglish {
		return []string{
			"Appearance",
			"Language",
			"Startup",
			"Workspace",
			"Markdown preview",
			"History",
			"Updates",
			"About",
		}
	}
	return []string{
		"外观",
		"语言",
		"启动",
		"工作区偏好",
		"Markdown 预览",
		"历史",
		"更新",
		"关于",
	}
}
