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
		"关于",
	}
}

// aboutTexts holds the About pane's own labels and the fixed project facts.
type aboutTexts struct {
	Developer, Contact, Project, Updates string
}

const (
	aboutDeveloperName = "Flyinsky2004"
	aboutContactEmail  = "w2084151024@gmail.com"
	aboutProjectURL    = "https://github.com/Flyinsky2004/GoRequest"
)

func aboutTextsFor(l language) aboutTexts {
	if l == langEnglish {
		return aboutTexts{Developer: "Developer", Contact: "Contact email", Project: "Project", Updates: "UPDATES"}
	}
	return aboutTexts{Developer: "开发者", Contact: "联系邮箱", Project: "项目地址", Updates: "检查更新"}
}
