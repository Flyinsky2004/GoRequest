package diagram

import (
	"strings"
)

var rejectedSequence = map[string]string{
	"loop":       "loop blocks are not supported by the offline sequence renderer",
	"alt":        "alt/else blocks are not supported by the offline sequence renderer",
	"else":       "alt/else blocks are not supported by the offline sequence renderer",
	"opt":        "opt blocks are not supported by the offline sequence renderer",
	"par":        "par blocks are not supported by the offline sequence renderer",
	"and":        "par blocks are not supported by the offline sequence renderer",
	"critical":   "critical blocks are not supported by the offline sequence renderer",
	"option":     "critical/option blocks are not supported by the offline sequence renderer",
	"break":      "break blocks are not supported by the offline sequence renderer",
	"rect":       "rect blocks are not supported by the offline sequence renderer",
	"box":        "box groups are not supported by the offline sequence renderer",
	"end":        "blocks are not supported by the offline sequence renderer",
	"activate":   "activation bars are not supported by the offline sequence renderer",
	"deactivate": "activation bars are not supported by the offline sequence renderer",
	"create":     "create/destroy participant statements are not supported by the offline sequence renderer",
	"destroy":    "create/destroy participant statements are not supported by the offline sequence renderer",
}

func parseSequence(lines []parsedLine, limits Limits) (*seqDiagram, *Diagnostic) {
	sd := &seqDiagram{index: map[string]*sparticipant{}}
	header := false
	rows := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line.text)
		if strings.HasPrefix(trimmed, "%%{") {
			return nil, diagAt(line.num, 1, "Mermaid init directives are not supported by the offline renderer")
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if !header {
			if !strings.EqualFold(trimmed, "sequenceDiagram") {
				return nil, diagAt(line.num, 1, "unsupported diagram type; the offline renderer supports flowchart, graph and sequenceDiagram")
			}
			header = true
			continue
		}
		col := leadingSpaceRunes(line.text) + 1
		word := firstWord(trimmed)
		lower := strings.ToLower(word)

		if lower == "participant" || lower == "actor" {
			if d := parseParticipantDecl(sd, trimmed[len(word):], line.num, col+len([]rune(word)), limits); d != nil {
				return nil, d
			}
			continue
		}
		if lower == "note" && isNoteStatement(trimmed[len(word):]) {
			rows++
			if rows > limits.MaxMessages {
				return nil, diagAt(line.num, col, "the diagram exceeds the %d-row limit", limits.MaxMessages)
			}
			if d := parseNote(sd, trimmed[len(word):], line.num, col+len([]rune(word)), limits); d != nil {
				return nil, d
			}
			continue
		}
		if lower == "autonumber" {
			if strings.TrimSpace(trimmed[len(word):]) != "" {
				return nil, diagAt(line.num, col, "autonumber arguments are not supported; only bare \"autonumber\" is")
			}
			sd.autonumber = true
			continue
		}
		if msg, bad := rejectedSequence[lower]; bad {
			return nil, diagAt(line.num, col, "%s", msg)
		}
		rows++
		if rows > limits.MaxMessages {
			return nil, diagAt(line.num, col, "the diagram exceeds the %d-row limit", limits.MaxMessages)
		}
		if d := parseMessage(sd, trimmed, line.num, col, limits); d != nil {
			return nil, d
		}
	}
	if !header {
		return nil, diagAt(1, 1, "missing sequenceDiagram declaration")
	}
	if len(sd.participants) == 0 {
		return nil, diagAt(1, 1, "the sequence diagram declares no participants")
	}
	orderParticipants(sd)
	return sd, nil
}

func firstWord(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

func isNoteStatement(rest string) bool {
	lower := strings.ToLower(strings.TrimSpace(rest))
	return strings.HasPrefix(lower, "left of") || strings.HasPrefix(lower, "right of") || strings.HasPrefix(lower, "over")
}

func parseParticipantDecl(sd *seqDiagram, rest string, line, col int, limits Limits) *Diagnostic {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return diagAt(line, col, "expected a participant ID")
	}
	if i := strings.Index(rest, "@{"); i >= 0 {
		return diagAt(line, col+countRunes(rest[:i]), "participant stereotypes (@{...}) are not supported by the offline sequence renderer")
	}
	id, tail := splitID(rest)
	if id == "" {
		return diagAt(line, col, "expected a participant ID")
	}
	entry := sd.participant(id, line, col)
	if len(sd.participants) > limits.MaxParticipants {
		return diagAt(line, col, "the diagram exceeds the %d-participant limit", limits.MaxParticipants)
	}
	if !entry.declared {
		entry.declared = true
	}
	entry.line, entry.col = line, col
	tail = strings.TrimSpace(tail)
	if tail == "" {
		if !entry.hasLabel {
			entry.label = id
		}
		return nil
	}
	lower := strings.ToLower(tail)
	if !strings.HasPrefix(lower, "as") || (len(tail) > 2 && tail[2] != ' ' && tail[2] != '\t') {
		return diagAt(line, col+countRunes(id), "unexpected text %q after the participant ID; expected \"as 标签\"", tail)
	}
	alias := strings.TrimSpace(tail[2:])
	alias = strings.TrimSuffix(strings.TrimPrefix(alias, "\""), "\"")
	if alias == "" {
		return diagAt(line, col+countRunes(id), "expected a label after \"as\"")
	}
	label, d := validateLabel(decodeEntities(alias), line, col+countRunes(id), limits)
	if d != nil {
		return d
	}
	entry.label = label
	entry.hasLabel = true
	return nil
}

// parseNote parses "left of A: text", "right of A: text", "over A: text"
// and "over A,B: text".
func parseNote(sd *seqDiagram, rest string, line, col int, limits Limits) *Diagnostic {
	rest = strings.TrimSpace(rest)
	side := noteOver
	switch {
	case strings.HasPrefix(strings.ToLower(rest), "left of"):
		side = noteLeft
		rest = strings.TrimSpace(rest[len("left of"):])
	case strings.HasPrefix(strings.ToLower(rest), "right of"):
		side = noteRight
		rest = strings.TrimSpace(rest[len("right of"):])
	case strings.HasPrefix(strings.ToLower(rest), "over"):
		side = noteOver
		rest = strings.TrimSpace(rest[len("over"):])
	}
	head, text, ok := strings.Cut(rest, ":")
	if !ok {
		return diagAt(line, col, "expected ':' after the note participants")
	}
	head = strings.TrimSpace(head)
	names := strings.Split(head, ",")
	if len(names) > 2 {
		return diagAt(line, col, "note over lists more than two participants")
	}
	var from, to *sparticipant
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || !isWholeID(name) {
			return diagAt(line, col, "expected a participant ID in the note, found %q", name)
		}
		p := sd.participant(name, line, col)
		if i == 0 {
			from = p
		} else {
			to = p
		}
	}
	if side != noteOver && to != nil {
		return diagAt(line, col, "a left/right note attaches to one participant; use \"note over A,B\" for a span")
	}
	if to == nil {
		to = from
	}
	label, d := validateLabel(decodeEntities(strings.TrimSpace(text)), line, col+countRunes(head)+1, limits)
	if d != nil {
		return d
	}
	sd.notes = append(sd.notes, &snote{side: side, from: from, to: to, text: label, line: line, col: col})
	sd.items = append(sd.items, seqItem{note: sd.notes[len(sd.notes)-1]})
	return nil
}

func parseMessage(sd *seqDiagram, stmt string, line, col int, limits Limits) *Diagnostic {
	head, text, ok := strings.Cut(stmt, ":")
	if !ok {
		return diagAt(line, col, "expected a message of the form \"A->>B: text\"")
	}
	head = strings.TrimSpace(head)
	lower := strings.ToLower(head)
	if strings.Contains(head, "<<") {
		return diagAt(line, col, "bidirectional arrows (<<->>) are not supported by the offline sequence renderer")
	}
	if strings.Contains(lower, "()") {
		return diagAt(line, col, "central-lifeline connections (()) are not supported by the offline sequence renderer")
	}
	if strings.Contains(head, "-)") {
		return diagAt(line, col, "asynchronous arrows (-), --)) are not supported by the offline sequence renderer")
	}
	idx, arrow, kind, dotted := findArrow(head)
	if idx < 0 {
		return diagAt(line, col, "expected an arrow (->, -->, ->>, -->>, -x, --x) in the message")
	}
	from := strings.TrimSpace(head[:idx])
	to := strings.TrimSpace(head[idx+len(arrow):])
	if from == "" || to == "" {
		return diagAt(line, col, "expected a participant on each side of the arrow")
	}
	if strings.HasSuffix(to, "+") || strings.HasSuffix(to, "-") || strings.HasPrefix(to, "+") || strings.HasPrefix(to, "-") ||
		strings.HasPrefix(from, "+") || strings.HasPrefix(from, "-") {
		return diagAt(line, col, "activation shortcuts (+/-) are not supported by the offline sequence renderer")
	}
	if !isWholeID(from) || !isWholeID(to) {
		return diagAt(line, col, "participant IDs must be a single word; declare an alias instead")
	}
	pfrom := sd.participant(from, line, col)
	pto := sd.participant(to, line, col)
	if len(sd.participants) > limits.MaxParticipants {
		return diagAt(line, col, "the diagram exceeds the %d-participant limit", limits.MaxParticipants)
	}
	label, d := validateLabel(decodeEntities(strings.TrimSpace(text)), line, col+countRunes(head)+1, limits)
	if d != nil {
		return d
	}
	sd.messages = append(sd.messages, &smessage{
		from: pfrom, to: pto, text: label,
		dotted: dotted, head: kind, line: line, col: col,
	})
	sd.items = append(sd.items, seqItem{msg: sd.messages[len(sd.messages)-1]})
	return nil
}

// findArrow returns the earliest supported arrow in head. Longer arrows win
// at the same position (-->> before ->).
func findArrow(head string) (int, string, ArrowKind, bool) {
	patterns := []struct {
		text   string
		head   ArrowKind
		dotted bool
	}{
		{"-->>", ArrowHead, true},
		{"--x", ArrowCross, true},
		{"-->", ArrowNone, true},
		{"->>", ArrowHead, false},
		{"-x", ArrowCross, false},
		{"->", ArrowNone, false},
	}
	best, bestText := -1, ""
	var bestKind ArrowKind
	bestDotted := false
	for _, p := range patterns {
		if i := strings.Index(head, p.text); i >= 0 && (best < 0 || i < best || (i == best && len(p.text) > len(bestText))) {
			best, bestText, bestKind, bestDotted = i, p.text, p.head, p.dotted
		}
	}
	// Reject asynchronous and other unsupported arrow operators explicitly.
	for _, bad := range []string{"-)", "--)"} {
		if i := strings.Index(head, bad); i >= 0 && (best < 0 || i <= best) {
			return -1, "", ArrowNone, false
		}
	}
	return best, bestText, bestKind, bestDotted
}

// splitID splits a leading participant ID from the rest.
func splitID(s string) (string, string) {
	runes := []rune(s)
	i := 0
	for i < len(runes) && isIDRune(runes[i]) {
		i++
	}
	return string(runes[:i]), string(runes[i:])
}

// isWholeID reports whether s is exactly one ID token.
func isWholeID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isIDRune(r) {
			return false
		}
	}
	return true
}

// orderParticipants puts explicit declarations first in declaration order,
// then implicit participants in first-mention order, matching Mermaid.
func orderParticipants(sd *seqDiagram) {
	declared := make([]*sparticipant, 0, len(sd.participants))
	implicit := make([]*sparticipant, 0, len(sd.participants))
	for _, p := range sd.participants {
		if p.label == "" {
			p.label = p.id
		}
		if p.declared {
			declared = append(declared, p)
		} else {
			implicit = append(implicit, p)
		}
	}
	sd.participants = append(declared, implicit...)
}
