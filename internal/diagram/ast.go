// Package diagram parses and lays out an offline subset of Mermaid
// flowcharts and sequence diagrams. It is deliberately independent of any
// GUI toolkit: parsing, layout and the scene model are pure Go, so the
// same scene feeds both native MyGo painting and static SVG export.
//
// The supported syntax is documented in docs/contracts/F03-mermaid-subset.md.
// Anything outside it is rejected with a line/column diagnostic; a partial
// picture is never produced for input the parser does not fully understand.
package diagram

import (
	"fmt"
	"time"
)

// Kind identifies the parsed diagram type.
type Kind string

const (
	KindFlowchart Kind = "flowchart"
	KindSequence  Kind = "sequence"
)

// Direction is a flowchart layout direction.
type Direction string

const (
	TopDown   Direction = "TD"
	BottomUp  Direction = "BT"
	LeftRight Direction = "LR"
	RightLeft Direction = "RL"
)

// Shape is a flowchart node outline.
type Shape int

const (
	ShapeRect Shape = iota
	ShapeRounded
	ShapeStadium
	ShapeCircle
	ShapeDiamond
)

// String names a shape for diagnostics and tests.
func (s Shape) String() string {
	switch s {
	case ShapeRounded:
		return "rounded"
	case ShapeStadium:
		return "stadium"
	case ShapeCircle:
		return "circle"
	case ShapeDiamond:
		return "diamond"
	}
	return "rect"
}

// Diagnostic is the first unsupported or malformed construct, with a 1-based
// line and column in Unicode code points. The full source is always kept by
// the caller; a diagnostic never replaces it.
type Diagnostic struct {
	Line    int
	Column  int
	Message string
}

func (d *Diagnostic) Error() string {
	if d == nil {
		return ""
	}
	if d.Line > 0 {
		return fmt.Sprintf("line %d:%d: %s", d.Line, d.Column, d.Message)
	}
	return d.Message
}

func diagAt(line, col int, format string, args ...any) *Diagnostic {
	return &Diagnostic{Line: line, Column: col, Message: fmt.Sprintf(format, args...)}
}

// Limits bound one render. Zero fields take the defaults.
type Limits struct {
	MaxSourceBytes  int
	MaxNodes        int
	MaxEdges        int
	MaxParticipants int
	MaxMessages     int
	MaxLabelRunes   int
	MaxLabelLines   int
	MaxWidth        float32
	MaxHeight       float32
	MaxSVGBytes     int
	Timeout         time.Duration
}

// DefaultLimits are the documented budgets of the offline renderer.
var DefaultLimits = Limits{
	MaxSourceBytes:  64 << 10,
	MaxNodes:        200,
	MaxEdges:        400,
	MaxParticipants: 20,
	MaxMessages:     300,
	MaxLabelRunes:   200,
	MaxLabelLines:   8,
	MaxWidth:        2400,
	MaxHeight:       6000,
	MaxSVGBytes:     2 << 20,
	Timeout:         250 * time.Millisecond,
}

func (l Limits) effective() Limits {
	d := DefaultLimits
	if l.MaxSourceBytes > 0 {
		d.MaxSourceBytes = l.MaxSourceBytes
	}
	if l.MaxNodes > 0 {
		d.MaxNodes = l.MaxNodes
	}
	if l.MaxEdges > 0 {
		d.MaxEdges = l.MaxEdges
	}
	if l.MaxParticipants > 0 {
		d.MaxParticipants = l.MaxParticipants
	}
	if l.MaxMessages > 0 {
		d.MaxMessages = l.MaxMessages
	}
	if l.MaxLabelRunes > 0 {
		d.MaxLabelRunes = l.MaxLabelRunes
	}
	if l.MaxLabelLines > 0 {
		d.MaxLabelLines = l.MaxLabelLines
	}
	if l.MaxWidth > 0 {
		d.MaxWidth = l.MaxWidth
	}
	if l.MaxHeight > 0 {
		d.MaxHeight = l.MaxHeight
	}
	if l.MaxSVGBytes > 0 {
		d.MaxSVGBytes = l.MaxSVGBytes
	}
	if l.Timeout > 0 {
		d.Timeout = l.Timeout
	}
	return d
}

// Options configure Render.
type Options struct {
	Limits Limits
	// MaxLabelWidth wraps long labels for sequence messages and notes.
	// Zero uses 320 DIP.
	MaxLabelWidth float32
	// FontSize is the base text size in DIP. Zero uses 14.
	FontSize float32
}
