package desktop

import "gorequest/internal/docedit"

// docBlockAtCaret returns the block a caret at off belongs to. A caret at the
// very end of the last line (the block has no trailing newline) stays in that
// block; a caret after a block's newline is on the next, blank line and has no
// block, so the canvas opens a synthetic empty block there.
func docBlockAtCaret(blocks []docedit.Block, off int) (docedit.Block, bool) {
	if b, ok := docedit.BlockAt(blocks, off); ok {
		return b, true
	}
	for _, b := range blocks {
		if b.End == off && !b.HasTerminator() {
			return b, true
		}
	}
	return docedit.Block{}, false
}
