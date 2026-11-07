package desktop

import "github.com/egoist/mygo/ui"

// This file owns the single-line name field shared by the new-folder dialog
// (forms.go) and the in-place tree rename (tree_actions.go). Both edit a
// display name that may be Chinese, so the field must keep an input method's
// composition intact and submit only on the Enter that confirms the name.
//
// MyGo routes the keys pressed while an input method composes to the input
// method itself (see ui.Element.Composing), so Submitted reports the Enter
// that confirms the name, not the Enter that picks a Pinyin candidate. The two
// call sites share this helper so the label, Escape handling and submit rule
// cannot drift apart.

// folderNameInput draws a single-line folder-name field bound to value. label
// is its accessible name; placeholder shows while the field is empty.
func folderNameInput(c *ui.Context, value *string, label, placeholder string) ui.Element {
	return ui.TextInput(c, value).SingleLine().Label(label).Placeholder(placeholder)
}

// folderNameSubmit wires Escape to onCancel and reports whether Enter
// confirmed the name in this frame. The caller keeps the field and its text
// when submission fails, so the user can correct the name without retyping it.
func folderNameSubmit(in ui.Element, onCancel func()) bool {
	in.OnTextKey(func(k ui.TextKey) bool {
		if k.Key == ui.KeyEscape {
			onCancel()
			return true
		}
		return false
	})
	return in.Submitted()
}
