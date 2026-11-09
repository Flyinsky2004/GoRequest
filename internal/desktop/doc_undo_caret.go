package desktop

import "github.com/egoist/mygo/ui/editor"

// Selection metadata only: the canonical Buffer owns all text and undo/redo
// decisions. A version mismatch (source mode, table, external edit or reset)
// discards the association instead of guessing how its history moved.
// The bounded graph follows only transactions observed by the canvas.
type docCaretHistory struct {
	buffer  *editor.Buffer
	version uint64
	current *docCaretNode
	count   int
	next    int
	steps   [512]*docCaretStep
}
type docCaretNode struct{ undo, redo *docCaretStep }
type docCaretStep struct {
	before, after editor.Selection
	from, to      *docCaretNode
}

func (h *docCaretHistory) sync(buf *editor.Buffer) {
	if h.buffer != buf || h.version != buf.Version() || h.current == nil {
		*h = docCaretHistory{buffer: buf, version: buf.Version(), current: &docCaretNode{}}
	}
}
func (h *docCaretHistory) record(buf *editor.Buffer, before, after editor.Selection) {
	// sync was called immediately before the canonical edit.
	next := &docCaretNode{}
	step := &docCaretStep{before: before, after: after, from: h.current, to: next}
	// Evict only the oldest association, retaining the most recent directed
	// selections. Detached branches cannot keep an unbounded linked graph.
	if old := h.steps[h.next]; old != nil {
		if old.from.redo == old {
			old.from.redo = nil
		}
		if old.to.undo == old {
			old.to.undo = nil
		}
	}
	h.steps[h.next] = step
	h.next = (h.next + 1) % len(h.steps)
	h.count = min(h.count+1, len(h.steps))
	h.current.redo = step
	next.undo = step
	h.current = next
	h.version = buf.Version()
}

func (be *docBlockEdit) sourceSelection() editor.Selection {
	s := be.ed.Selection()
	text := be.ed.Buffer.String()
	return editor.Selection{Anchor: be.start + be.proj.SourceOffset(text, s.Anchor), Head: be.start + be.proj.SourceOffset(text, s.Head)}
}
func (be *docBlockEdit) rememberCaret() {
	if be.ed.Buffer.String() == be.base {
		s := be.sourceSelection()
		be.caretBefore = &s
	}
}
