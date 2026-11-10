package diagram

// Point is a position in DIP.
type Point struct {
	X, Y float32
}

// Rect is an axis-aligned rectangle in DIP.
type Rect struct {
	X, Y, W, H float32
}

// Center returns the rectangle's center.
func (r Rect) Center() Point { return Point{r.X + r.W/2, r.Y + r.H/2} }

// Contains reports whether p is inside the rectangle.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X <= r.X+r.W && p.Y >= r.Y && p.Y <= r.Y+r.H
}

// Scene is the renderer-independent geometry of one diagram. Native MyGo
// painting and static SVG export both read exactly these fields.
type Scene struct {
	Kind     Kind
	Title    string // the source declaration, for accessibility text
	Width    float32
	Height   float32
	FontSize float32

	Nodes        []Node
	Edges        []Edge
	Participants []Participant
	Messages     []Message
	Notes        []Note
}

// Node is one flowchart node.
type Node struct {
	ID    string
	Shape Shape
	Box   Rect
	Lines []string
}

// Edge is one flowchart connection. Points is the routed polyline; the last
// point sits on the target's border when Arrow is set.
type Edge struct {
	From, To   string
	Points     []Point
	Dotted     bool
	Thick      bool
	Arrow      bool
	Label      string
	LabelLines []string
	LabelBox   Rect
}

// Participant is one sequence lane header and its lifeline.
type Participant struct {
	ID      string
	Label   string
	Box     Rect
	Lines   []string
	CenterX float32
}

// Message is one sequence arrow.
type Message struct {
	From, To   string
	FromX, ToX float32
	Y          float32
	Dotted     bool
	Head       ArrowKind
	Self       bool
	// Loop is the routed self-message polyline; empty for ordinary messages.
	Loop       []Point
	LabelLines []string
	LabelBox   Rect
}

// Note is one sequence note.
type Note struct {
	Side  noteSide
	Box   Rect
	Lines []string
}
