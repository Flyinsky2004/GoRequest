package diagram

import (
	"context"
	"fmt"
)

const (
	seqPadX        = 16
	seqMinLaneW    = 110
	seqLaneGap     = 24
	seqRowGap      = 14
	seqHeaderPadY  = 10
	seqNotePadX    = 12
	seqNotePadY    = 8
	seqSelfLoopOut = 30
	seqSelfLoopH   = 24
)

// layoutSequence lays out participant lanes, message rows and notes in
// source order.
func layoutSequence(ctx context.Context, sd *seqDiagram, opts Options, limits Limits) (*Scene, *Diagnostic) {
	fs := opts.FontSize
	if fs <= 0 {
		fs = 14
	}
	maxLabelW := opts.MaxLabelWidth
	if maxLabelW <= 0 {
		maxLabelW = 320
	}
	lh := LineHeight(fs)

	if d := contextDiag(ctx); d != nil {
		return nil, d
	}

	type lane struct {
		p      *sparticipant
		center float32
		w      float32
		lines  []string
	}
	lanes := make([]lane, 0, len(sd.participants))
	headerH := float32(0)
	cursor := float32(marginDIP)
	for _, p := range sd.participants {
		if d := contextDiag(ctx); d != nil {
			return nil, d
		}
		label := p.label
		if label == "" {
			label = p.id
		}
		lines := wrapLabel(label, 240, fs, maxWrappedLines)
		if len(lines) > maxWrappedLines {
			return nil, diagAt(p.line, p.col, "participant label wraps to more than %d lines; shorten it", maxWrappedLines)
		}
		w := labelWidth(lines, fs) + 2*seqPadX
		if w < seqMinLaneW {
			w = seqMinLaneW
		}
		h := float32(len(lines))*lh + 2*seqHeaderPadY
		if h > headerH {
			headerH = h
		}
		lanes = append(lanes, lane{p: p, center: cursor + w/2, w: w, lines: lines})
		cursor += w + seqLaneGap
	}
	centerOf := map[string]float32{}
	for _, l := range lanes {
		centerOf[l.p.id] = l.center
	}

	scene := &Scene{
		Kind: KindSequence, Title: "sequenceDiagram",
		FontSize: fs,
	}
	headerTop := float32(marginDIP)
	headerBottom := headerTop + headerH
	for _, l := range lanes {
		scene.Participants = append(scene.Participants, Participant{
			ID: l.p.id, Label: l.p.label, Lines: l.lines, CenterX: l.center,
			Box: Rect{X: l.center - l.w/2, Y: headerTop, W: l.w, H: headerH},
		})
	}

	y := headerBottom + 24
	minX := float32(marginDIP)
	maxX := cursor - seqLaneGap + marginDIP/2
	number := 0
	for _, item := range sd.items {
		if d := contextDiag(ctx); d != nil {
			return nil, d
		}
		switch {
		case item.msg != nil:
			m := item.msg
			number++
			text := m.text
			if sd.autonumber {
				text = fmt.Sprintf("%d. %s", number, text)
			}
			fromX, toX := centerOf[m.from.id], centerOf[m.to.id]
			self := m.from.id == m.to.id
			labelMax := maxLabelW
			if !self {
				if d := abs32(toX - fromX); d > 0 {
					avail := d - 16
					if avail < 160 {
						avail = 160
					}
					if avail < labelMax {
						labelMax = avail
					}
				}
			}
			lines := wrapLabel(text, labelMax, fs, maxWrappedLines)
			if len(lines) > maxWrappedLines {
				return nil, diagAt(m.line, m.col, "message text wraps to more than %d lines; shorten it", maxWrappedLines)
			}
			labelH := float32(len(lines)) * lh
			lw := labelWidth(lines, fs) + 8
			sm := Message{
				From: m.from.id, To: m.to.id, FromX: fromX, ToX: toX,
				Dotted: m.dotted, Head: m.head, Self: self,
				LabelLines: lines,
			}
			if self {
				loopH := max32(labelH, 34) + 20
				sm.Y = y + loopH/2
				sm.Loop = []Point{
					{X: fromX, Y: sm.Y},
					{X: fromX + seqSelfLoopOut, Y: sm.Y},
					{X: fromX + seqSelfLoopOut, Y: sm.Y + seqSelfLoopH},
					{X: fromX, Y: sm.Y + seqSelfLoopH},
				}
				lx := fromX + seqSelfLoopOut + 12
				sm.LabelBox = Rect{X: lx, Y: sm.Y - labelH/2, W: lw, H: labelH}
				if lx+lw > maxX {
					maxX = lx + lw
				}
				y = sm.Y + seqSelfLoopH/2 + 16
			} else {
				sm.Y = y + labelH + 8
				mid := (fromX + toX) / 2
				sm.LabelBox = Rect{X: mid - lw/2, Y: y, W: lw, H: labelH}
				if sm.LabelBox.X < minX {
					minX = sm.LabelBox.X
				}
				if sm.LabelBox.X+sm.LabelBox.W > maxX {
					maxX = sm.LabelBox.X + sm.LabelBox.W
				}
				y = sm.Y + seqRowGap
			}
			scene.Messages = append(scene.Messages, sm)
		case item.note != nil:
			nt := item.note
			lines := wrapLabel(nt.text, 300, fs, maxWrappedLines)
			if len(lines) > maxWrappedLines {
				return nil, diagAt(nt.line, nt.col, "note text wraps to more than %d lines; shorten it", maxWrappedLines)
			}
			nw := labelWidth(lines, fs) + 2*seqNotePadX
			if nw < 100 {
				nw = 100
			}
			nh := float32(len(lines))*lh + 2*seqNotePadY
			fromX, toX := centerOf[nt.from.id], centerOf[nt.to.id]
			var x float32
			switch nt.side {
			case noteLeft:
				x = fromX - nw - 16
			case noteRight:
				x = fromX + 16
			default:
				x = (fromX+toX)/2 - nw/2
			}
			box := Rect{X: x, Y: y, W: nw, H: nh}
			if box.X < minX {
				minX = box.X
			}
			if box.X+box.W > maxX {
				maxX = box.X + box.W
			}
			scene.Notes = append(scene.Notes, Note{Side: nt.side, Box: box, Lines: lines})
			y = box.Y + box.H + seqRowGap
		}
	}

	// Shift right so the leftmost note/label keeps the page margin.
	shift := float32(0)
	if minX < marginDIP {
		shift = marginDIP - minX
	}
	if shift > 0 {
		for i := range scene.Participants {
			scene.Participants[i].Box.X += shift
			scene.Participants[i].CenterX += shift
		}
		for i := range scene.Messages {
			m := &scene.Messages[i]
			m.FromX += shift
			m.ToX += shift
			m.LabelBox.X += shift
			for k := range m.Loop {
				m.Loop[k].X += shift
			}
		}
		for i := range scene.Notes {
			scene.Notes[i].Box.X += shift
		}
		maxX += shift
	}

	scene.Width = maxX + marginDIP
	scene.Height = y + marginDIP
	for i := range scene.Participants {
		p := &scene.Participants[i]
		if p.Box.X < marginDIP {
			scene.Width = max32(scene.Width, p.Box.X+p.Box.W+marginDIP)
		}
	}
	if d := fitScene(scene); d != nil {
		return nil, d
	}
	if d := checkSceneSize(scene, limits); d != nil {
		return nil, d
	}
	return scene, nil
}
