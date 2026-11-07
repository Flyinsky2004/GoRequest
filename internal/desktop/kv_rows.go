package desktop

// This file holds the row helpers shared by every KV table: the Query and
// Headers tables in request_form.go and the URL-encoded Body form in
// body_editor.go. Keeping them here means the three tables treat their
// trailing ghost row and their cell placeholders the same way.

// keepSingleTrailingBlank returns rows up to the last non-blank row plus
// exactly one blank ghost row. Clearing a row can leave several blank rows at
// the end; every KV table shows one ghost row, so the extras are dropped.
// Blank rows before the last real row are kept, so a row the user is clearing
// in the middle of the table never disappears under the caret.
func keepSingleTrailingBlank[T any](rows []T, blank func(T) bool, newBlank func() T) []T {
	last := -1
	for i, r := range rows {
		if !blank(r) {
			last = i
		}
	}
	// Common case: one real tail followed by one blank ghost row. Return the
	// slice unchanged so a steady frame allocates nothing.
	if last == len(rows)-2 && blank(rows[len(rows)-1]) {
		return rows
	}
	out := make([]T, 0, last+2)
	for i := 0; i <= last; i++ {
		out = append(out, rows[i])
	}
	// Keep the last blank row, the canonical ghost the user is most likely
	// typing into, and drop the stale blanks before it.
	var ghost T
	hasGhost := false
	for i := last + 1; i < len(rows); i++ {
		if blank(rows[i]) {
			ghost, hasGhost = rows[i], true
		}
	}
	if hasGhost {
		return append(out, ghost)
	}
	return append(out, newBlank())
}

// isBlankKVRow reports whether a Query/Headers row is the blank ghost row.
func isBlankKVRow(r *KVRow) bool { return r == nil || r.isBlank() }

// isBlankFormRow reports whether a URL-encoded Body form row is the blank ghost
// row.
func isBlankFormRow(r *formBodyRow) bool { return r == nil || r.isBlank() }

// kvCellPlaceholders returns the Key and Value placeholders for one KV row.
// Only the trailing ghost row has a hint: its Key says what typing there does,
// and its Value stays empty. The column headers already say "Key" and "Value",
// so repeating them as placeholders would print each heading twice. Rows that
// hold data keep no placeholder.
func kvCellPlaceholders(m *messages, blank bool) (key, val string) {
	if !blank {
		return "", ""
	}
	return m.AddParamHint, ""
}
