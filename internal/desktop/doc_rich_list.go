package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	docmd "gorequest/internal/markdown"
)

// Structural list edits always replace canonical source once; content input
// remains in the native rich widget. Delimiters, indentation and EOLs outside
// the edited prefix survive byte for byte.
func (a *App) docRichListInput(tab *Tab, st *docViewState, be *docBlockEdit, ev ui.InputEvent) bool {
	if be.kind != docmd.List || !be.proj.Active() || ev.Kind != ui.InputKeyDown {
		return false
	}
	if ev.Key != ui.KeyTab && ev.Key != ui.KeyBackspace && ev.Key != ui.KeyEnter {
		return false
	}
	sel := be.ed.Selection()
	if sel.Start() != sel.End() {
		return false
	}
	text := be.ed.Buffer.String()
	line := strings.Count(text[:sel.Head], "\n")
	lines := be.proj.ListLines(text)
	if line >= len(lines) {
		return false
	}
	meta := lines[line]
	start := docLineOffset(text, line)
	end := docLineEndOffset(text, line)
	src := be.proj.Commit(text)

	sourceStart := be.proj.SourceOffset(text, start) - len(meta.Prefix)
	replacePrefix := func(prefix string) {
		caret := be.proj.SourceOffset(text, sel.Head) + len(prefix) - len(meta.Prefix)
		updated := src[:sourceStart] + prefix + src[sourceStart+len(meta.Prefix):]
		tab.DocEditor.Buffer.Edit(editor.Edit{Start: be.start, End: be.end, Text: updated + be.terminator})
		a.relocateDocBlock(tab, st, be.start+caret)
		tab.Dirty = true
	}
	if ev.Key == ui.KeyTab && (ev.Mods == 0 || ev.Mods == ui.Shift) {
		cursorLine := line
		for line > 0 && lines[line].Marker == "" {
			line--
		}
		meta = lines[line]
		if line == 0 && ev.Mods == 0 {
			return true
		}
		last := line + 1
		for last < len(lines) {
			if lines[last].Marker != "" && lines[last].Depth <= meta.Depth {
				break
			}
			last++
		}
		edited := src
		caret := be.proj.SourceOffset(text, sel.Head)
		// Work backwards so each original source offset remains valid.
		for i := last - 1; i >= line; i-- {
			prefix := lines[i].Prefix
			updated := prefix
			if ev.Mods == ui.Shift {
				if strings.HasPrefix(prefix, "  ") {
					updated = prefix[2:]
				} else {
					updated = strings.TrimPrefix(prefix, "\t")
				}
			} else {
				updated = "  " + prefix
			}
			off := be.proj.SourceOffset(text, docLineOffset(text, i)) - len(prefix)
			edited = edited[:off] + updated + edited[off+len(prefix):]
			if i <= cursorLine {
				caret += len(updated) - len(prefix)
			}
		}
		tab.DocEditor.Buffer.Edit(editor.Edit{Start: be.start, End: be.end, Text: edited + be.terminator})
		a.relocateDocBlock(tab, st, be.start+caret)
		tab.Dirty = true
		return true
	}
	if ev.Key == ui.KeyBackspace && ev.Mods == 0 && sel.Head == start {
		if meta.Indent > 0 {
			prefix := strings.TrimPrefix(meta.Prefix, "  ")
			if prefix == meta.Prefix {
				prefix = strings.TrimPrefix(prefix, "\t")
			}
			replacePrefix(prefix)
		} else {
			replacePrefix("")
		}
		return true
	}
	if ev.Key == ui.KeyEnter && ev.Mods == 0 && start == end && meta.Marker != "" {
		// Empty item exits one level; remove its own marker, not the list.
		prefix := ""
		if meta.Indent >= 2 {
			prefix = meta.Prefix[2:]
		}
		replacePrefix(prefix)
		return true
	}
	return false
}

func (a *App) docRichToggleTask(tab *Tab, st *docViewState, be *docBlockEdit, line int) {
	text := be.ed.Buffer.String()
	lines := be.proj.ListLines(text)
	if line < 0 || line >= len(lines) || !lines[line].Task {
		return
	}
	meta := lines[line]
	off := be.proj.SourceOffset(text, docLineOffset(text, line)) - len(meta.Prefix)
	marker := strings.Index(meta.Prefix, "[")
	if marker < 0 {
		return
	}
	off += marker + 1
	value := "x"
	if meta.Checked {
		value = " "
	}
	tab.DocEditor.Buffer.Edit(editor.Edit{Start: be.start + off, End: be.start + off + 1, Text: value})
	a.syncDocBlock(tab, st)
	tab.Dirty = true
}
