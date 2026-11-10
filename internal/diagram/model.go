package diagram

// The internal AST is intentionally small: only constructs the layout can
// draw are represented. Anything else is rejected during parsing, so the
// layout never guesses at a diagram's intent.

type flowchart struct {
	dir   Direction
	nodes []*fnode
	edges []fedge
	index map[string]*fnode
}

type fnode struct {
	id       string
	label    string
	hasLabel bool
	shape    Shape
	line     int
	col      int
}

type fedge struct {
	from  *fnode
	to    *fnode
	label string
	// hasLabel keeps an empty |label| distinct from no label.
	hasLabel bool
	dotted   bool
	thick    bool
	arrow    bool
	line     int
	col      int
}

func (f *flowchart) node(id string, line, col int) *fnode {
	if n, ok := f.index[id]; ok {
		return n
	}
	n := &fnode{id: id, line: line, col: col}
	f.index[id] = n
	f.nodes = append(f.nodes, n)
	return n
}

type seqDiagram struct {
	participants []*sparticipant
	index        map[string]*sparticipant
	messages     []*smessage
	notes        []*snote
	// items keeps messages and notes in source order: layout assigns rows in
	// the order the author wrote them.
	items      []seqItem
	autonumber bool
}

type seqItem struct {
	msg  *smessage
	note *snote
}

type sparticipant struct {
	id       string
	label    string
	hasLabel bool
	declared bool
	line     int
	col      int
}

// ArrowKind is a sequence message's end decoration.
type ArrowKind int

const (
	ArrowNone  ArrowKind = iota // A->B
	ArrowHead                   // A->>B
	ArrowCross                  // A-xB
)

type smessage struct {
	from, to *sparticipant
	text     string
	dotted   bool
	head     ArrowKind
	line     int
	col      int
}

type noteSide int

const (
	noteOver noteSide = iota
	noteLeft
	noteRight
)

type snote struct {
	side     noteSide
	from, to *sparticipant
	text     string
	line     int
	col      int
}

func (s *seqDiagram) participant(id string, line, col int) *sparticipant {
	if p, ok := s.index[id]; ok {
		return p
	}
	p := &sparticipant{id: id, line: line, col: col}
	s.index[id] = p
	s.participants = append(s.participants, p)
	return p
}
