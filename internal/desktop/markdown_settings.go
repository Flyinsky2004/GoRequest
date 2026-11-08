package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"gorequest/internal/desktop/uikit"
)

// Kept beside the owner, like docMessages, to avoid a shared i18n-table edit.
type markdownSettingsMessages struct {
	Section, Font, Padding, Line, Reset, Note, Saved, Failed string
}

func markdownSettingsMessagesFor(l language) markdownSettingsMessages {
	if l == langEnglish {
		return markdownSettingsMessages{"Markdown preview", "Rendered font scale", "Preview padding (DIP)", "Line spacing", "Reset Markdown defaults", "Local preferences for preview and split view. Source editor font is independent.", "Markdown preview preferences saved", "Could not save Markdown preferences: %s"}
	}
	return markdownSettingsMessages{"Markdown 预览", "渲染字体比例", "预览四边留白（DIP）", "行距", "恢复 Markdown 默认值", "本机偏好，应用于预览与分栏；源码编辑器字号独立。", "Markdown 预览偏好已保存", "Markdown 偏好保存失败：%s"}
}

type markdownSettingsErrorKey struct{}

func (s *workspacePrefsStore) markdownSettingsGroup(c *ui.Context, p uikit.Palette, l language, setStatus func(string)) {
	m := markdownSettingsMessagesFor(l)
	settingsGroup(c, p, m.Section, func() {
		root := ui.Column(c).Gap(6).FillWidth()
		failure := ui.Local(root, markdownSettingsErrorKey{}, func() string { return "" })
		save := func(next markdownPreferences) {
			status := m.Saved
			*failure = ""
			if err := s.setMarkdownPolicy(next); err != nil {
				*failure = fmt.Sprintf(m.Failed, err)
				status = *failure
			} else if s.markdownChanged != nil {
				s.markdownChanged()
			}
			if setStatus != nil {
				setStatus(status)
			}
		}
		root.Children(func() {
			current := s.markdownPolicy()
			choice := func(label string, value int, values []int, suffix string, update func(int)) {
				ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
					ui.Text(c, label).FontSize(12).TextColor(p.Muted).Grow(1)
					choices := make([]string, 0, len(values)+1)
					found := false
					for _, v := range values {
						choices = append(choices, strconv.Itoa(v)+suffix)
						found = found || v == value
					}
					text := strconv.Itoa(value) + suffix
					if !found {
						choices = append(choices, text)
					}
					if uikit.Select(c, &text, choices, uikit.SameIcon(uikit.IconType)).Width(110).Label(label).Cursor(ui.CursorPointer).Changed() {
						if v, err := strconv.Atoi(strings.TrimSuffix(text, suffix)); err == nil {
							update(v)
						}
					}
				})
			}
			choice(m.Font, current.FontPercent, []int{75, 90, 100, 110, 125, 150, 175, 200}, "%", func(v int) { current.FontPercent = v; save(current) })
			choice(m.Padding, current.Padding, []int{8, 12, 16, 24, 32, 48, 64}, "", func(v int) { current.Padding = v; save(current) })
			choice(m.Line, current.LinePercent, []int{120, 140, 150, 160, 180, 200, 220}, "%", func(v int) { current.LinePercent = v; save(current) })
			reset := ui.Button(c, "").Cursor(ui.CursorPointer)
			reset.Label(m.Reset)
			reset.Children(func() {
				if settingsIconReset != nil {
					ui.Icon(c, settingsIconReset).FontSize(13).TextColor(p.Text).Shrink(0)
				}
				ui.Text(c, m.Reset).TextColor(p.Text).SingleLine()
			})
			if reset.Clicked() {
				save(defaultMarkdownPreferences())
			}
			ui.Text(c, m.Note).FontSize(11).TextColor(p.Faint)
			if *failure != "" {
				ui.Text(c, *failure).FontSize(12).TextColor(p.Text)
			}
		})
	})
}
