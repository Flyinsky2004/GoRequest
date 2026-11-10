package diagram

import (
	"context"
	"fmt"
	"math"
)

const (
	marginDIP        = 20
	layerGapDIP      = 64
	nodeGapDIP       = 28
	nodePadX         = 14
	nodePadY         = 10
	minNodeWidth     = 64
	maxNodeTextWidth = 220
	maxEdgeLabelW    = 160
	maxWrappedLines  = 12
)

func contextDiag(ctx context.Context) *Diagnostic {
	if err := ctx.Err(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &Diagnostic{Message: "diagram layout exceeded its time budget; the source is shown unchanged"}
		}
		return &Diagnostic{Message: "diagram layout was canceled; the source is shown unchanged"}
	}
	return nil
}

// layoutFlowchart computes node boxes and routed edges for the parsed
// flowchart. The algorithm is iterative and bounded by the node/edge limits.
func layoutFlowchart(ctx context.Context, fc *flowchart, opts Options, limits Limits) (*Scene, *Diagnostic) {
	fs := opts.FontSize
	if fs <= 0 {
		fs = 14
	}
	lh := LineHeight(fs)
	n := len(fc.nodes)
	idx := make(map[string]int, n)
	for i, node := range fc.nodes {
		idx[node.id] = i
	}

	nodeW := make([]float32, n)
	nodeH := make([]float32, n)
	nodeLines := make([][]string, n)
	for i, node := range fc.nodes {
		if d := contextDiag(ctx); d != nil {
			return nil, d
		}
		label := node.label
		if !node.hasLabel {
			label = node.id
		}
		lines := wrapLabel(label, maxNodeTextWidth, fs, maxWrappedLines)
		if len(lines) > maxWrappedLines {
			return nil, diagAt(node.line, node.col, "label wraps to more than %d lines; shorten it", maxWrappedLines)
		}
		nodeLines[i] = lines
		w := labelWidth(lines, fs) + 2*nodePadX
		h := float32(len(lines))*lh + 2*nodePadY
		if w < minNodeWidth {
			w = minNodeWidth
		}
		switch node.shape {
		case ShapeDiamond:
			w = labelWidth(lines, fs)*1.3 + 2*nodePadX + 16
			h = float32(len(lines))*lh*1.6 + 2*nodePadY + 8
			if w < 90 {
				w = 90
			}
			if h < 64 {
				h = 64
			}
		case ShapeCircle:
			side := max32(w, h)
			side *= 1.15
			w, h = side, side
		}
		nodeW[i], nodeH[i] = w, h
	}

	// Adjacency and back-edge detection (iterative DFS) so cycles still lay
	// out in bounded time. Back edges are routed around the drawing.
	adj := make([][]int, n) // edge indices leaving each node
	in := make([][]int, n)  // edge indices entering each node
	back := make([]bool, len(fc.edges))
	for ei, e := range fc.edges {
		u, v := idx[e.from.id], idx[e.to.id]
		adj[u] = append(adj[u], ei)
		in[v] = append(in[v], ei)
	}
	state := make([]int8, n)
	for start := 0; start < n; start++ {
		if state[start] != 0 {
			continue
		}
		type frame struct{ v, i int }
		stack := []frame{{start, 0}}
		state[start] = 1
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.i >= len(adj[f.v]) {
				state[f.v] = 2
				stack = stack[:len(stack)-1]
				continue
			}
			ei := adj[f.v][f.i]
			f.i++
			e := fc.edges[ei]
			v := idx[e.to.id]
			switch state[v] {
			case 0:
				state[v] = 1
				stack = append(stack, frame{v, 0})
			case 1:
				back[ei] = true
			}
			if d := contextDiag(ctx); d != nil {
				return nil, d
			}
		}
	}

	// Longest-path layering over the acyclic (non-back) edges.
	indeg := make([]int, n)
	for ei, e := range fc.edges {
		if back[ei] {
			continue
		}
		indeg[idx[e.to.id]]++
	}
	layer := make([]int, n)
	queue := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, ei := range adj[u] {
			if back[ei] {
				continue
			}
			v := idx[fc.edges[ei].to.id]
			if layer[v] < layer[u]+1 {
				layer[v] = layer[u] + 1
			}
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
		if d := contextDiag(ctx); d != nil {
			return nil, d
		}
	}
	maxLayer := 0
	for _, l := range layer {
		if l > maxLayer {
			maxLayer = l
		}
	}

	layers := make([][]int, maxLayer+1)
	for i := 0; i < n; i++ {
		layers[layer[i]] = append(layers[layer[i]], i)
	}
	// One deterministic barycenter pass reduces crossings without turning
	// into a general graph-drawing optimizer.
	position := make([]int, n)
	for i := 0; i < n; i++ {
		position[i] = i
	}
	for li := 1; li <= maxLayer; li++ {
		if d := contextDiag(ctx); d != nil {
			return nil, d
		}
		nodes := layers[li]
		pred := make([][]int, len(nodes))
		for k, v := range nodes {
			for _, ei := range in[v] {
				if back[ei] {
					continue
				}
				u := idx[fc.edges[ei].from.id]
				if layer[u] < li {
					pred[k] = append(pred[k], position[u])
				}
			}
		}
		bary := make([]float32, len(nodes))
		for k := range nodes {
			if len(pred[k]) == 0 {
				bary[k] = float32(position[nodes[k]])
				continue
			}
			var sum float32
			for _, p := range pred[k] {
				sum += float32(p)
			}
			bary[k] = sum / float32(len(pred[k]))
		}
		// Stable insertion sort by barycenter, then declaration order.
		for a := 1; a < len(nodes); a++ {
			for b := a; b > 0; b-- {
				if bary[b] < bary[b-1] || (bary[b] == bary[b-1] && nodes[b] < nodes[b-1]) {
					nodes[b], nodes[b-1] = nodes[b-1], nodes[b]
					bary[b], bary[b-1] = bary[b-1], bary[b]
					pred[b], pred[b-1] = pred[b-1], pred[b]
					continue
				}
				break
			}
		}
		for k, v := range nodes {
			position[v] = k*1000 + li // keep layer separation in the key
		}
	}

	backCount := 0
	for _, b := range back {
		if b {
			backCount++
		}
	}
	crossExtra := float32(0)
	if backCount > 0 {
		crossExtra = 24 + float32(backCount)*16
	}

	// Band sizes along the main axis and cross extent.
	layerMain := make([]float32, maxLayer+1)
	layerCross := make([]float32, maxLayer+1)
	for li, nodes := range layers {
		var cross float32
		for k, v := range nodes {
			mainSize, crossSize := mainCross(fc.dir, nodeW[v], nodeH[v])
			if mainSize > layerMain[li] {
				layerMain[li] = mainSize
			}
			if k > 0 {
				cross += nodeGapDIP
			}
			cross += crossSize
		}
		layerCross[li] = cross
	}
	var crossTotal float32
	for li := range layers {
		if layerCross[li] > crossTotal {
			crossTotal = layerCross[li]
		}
	}
	layerMainOff := make([]float32, maxLayer+1)
	var totalMain float32
	for li := 0; li <= maxLayer; li++ {
		layerMainOff[li] = totalMain
		totalMain += layerMain[li]
		if li < maxLayer {
			totalMain += layerGapDIP
		}
	}
	width := 2*marginDIP + totalMain
	height := 2*marginDIP + crossTotal
	if isVertical(fc.dir) {
		width, height = 2*marginDIP+crossTotal+crossExtra, 2*marginDIP+totalMain
	} else {
		width, height = 2*marginDIP+totalMain, 2*marginDIP+crossTotal+crossExtra
	}

	boxes := make([]Rect, n)
	for li, nodes := range layers {
		crossPos := marginDIP + (crossTotal-layerCross[li])/2
		for _, v := range nodes {
			_, crossSize := mainCross(fc.dir, nodeW[v], nodeH[v])
			mainOff := layerMainOff[li] + (layerMain[li]-mainSize(fc.dir, nodeW[v], nodeH[v]))/2
			boxes[v] = placeRect(fc.dir, marginDIP+mainOff, crossPos, nodeW[v], nodeH[v], totalMain, marginDIP)
			crossPos += crossSize + nodeGapDIP
		}
		if d := contextDiag(ctx); d != nil {
			return nil, d
		}
	}

	scene := &Scene{
		Kind: KindFlowchart, Title: "flowchart " + string(fc.dir),
		Width: width, Height: height, FontSize: fs,
	}
	for i, node := range fc.nodes {
		scene.Nodes = append(scene.Nodes, Node{
			ID: node.id, Shape: node.shape, Box: boxes[i], Lines: nodeLines[i],
		})
	}

	// Edges: clip forward edges to node borders; route back edges (and
	// self-loops) around the cross-positive side of the drawing.
	routeIndex := 0
	for ei, e := range fc.edges {
		if d := contextDiag(ctx); d != nil {
			return nil, d
		}
		a, b := idx[e.from.id], idx[e.to.id]
		var pts []Point
		if back[ei] {
			routeIndex++
			pts = backRoute(fc.dir, boxes[a], boxes[b], marginDIP, crossTotal, float32(routeIndex), a == b)
		} else {
			p0 := borderPoint(boxes[a], boxes[b].Center())
			p1 := borderPoint(boxes[b], boxes[a].Center())
			pts = []Point{p0, p1}
		}
		se := Edge{
			From: e.from.id, To: e.to.id, Points: pts,
			Dotted: e.dotted, Thick: e.thick, Arrow: e.arrow,
		}
		if e.hasLabel && len(pts) >= 2 {
			mid, dir := polylineMidpoint(pts)
			lines := wrapLabel(e.label, maxEdgeLabelW, fs, maxWrappedLines)
			lh := LineHeight(fs)
			lw := labelWidth(lines, fs) + 8
			lhh := float32(len(lines))*lh + 4
			// Offset the label to the upper side of the segment.
			nx, ny := -dir.Y, dir.X
			if ny > 0 {
				nx, ny = -nx, -ny
			}
			pos := Point{mid.X + nx*(lhh/2+6), mid.Y + ny*(lhh/2+6)}
			se.Label = e.label
			se.LabelLines = lines
			se.LabelBox = Rect{X: pos.X - lw/2, Y: pos.Y - lhh/2, W: lw, H: lhh}
		}
		scene.Edges = append(scene.Edges, se)
	}

	if d := fitScene(scene); d != nil {
		return nil, d
	}
	if d := checkSceneSize(scene, limits); d != nil {
		return nil, d
	}
	return scene, nil
}

func mainCross(dir Direction, w, h float32) (main, cross float32) {
	if isVertical(dir) {
		return h, w
	}
	return w, h
}

func mainSize(dir Direction, w, h float32) float32 {
	m, _ := mainCross(dir, w, h)
	return m
}

func isVertical(dir Direction) bool { return dir == TopDown || dir == BottomUp }

func isReverse(dir Direction) bool { return dir == BottomUp || dir == RightLeft }

func placeRect(dir Direction, mainAbs, crossPos, w, h, totalMain, margin float32) Rect {
	if isVertical(dir) {
		y := mainAbs
		if isReverse(dir) {
			y = 2*margin + totalMain - mainAbs - h
		}
		return Rect{X: crossPos, Y: y, W: w, H: h}
	}
	x := mainAbs
	if isReverse(dir) {
		x = 2*margin + totalMain - mainAbs - w
	}
	return Rect{X: x, Y: crossPos, W: w, H: h}
}

// borderPoint returns the point where the ray from r's center towards
// toward leaves r. Diamonds and circles use their bounding rectangle, which
// keeps every arrow attached to a visible edge.
func borderPoint(r Rect, toward Point) Point {
	c := r.Center()
	dx, dy := toward.X-c.X, toward.Y-c.Y
	if dx == 0 && dy == 0 {
		return c
	}
	hw, hh := r.W/2, r.H/2
	var tx, ty float32 = math.MaxFloat32, math.MaxFloat32
	if dx != 0 {
		tx = hw / abs32(dx)
	}
	if dy != 0 {
		ty = hh / abs32(dy)
	}
	t := min32(tx, ty)
	return Point{c.X + dx*t, c.Y + dy*t}
}

// backRoute routes a back edge (or self-loop) around the cross-positive side.
func backRoute(dir Direction, from, to Rect, margin, crossTotal float32, order float32, self bool) []Point {
	offset := 12 + order*16
	if isVertical(dir) {
		routeX := 2*margin + crossTotal + offset
		fc := from.Center()
		tc := to.Center()
		if self {
			return []Point{
				{X: from.X + from.W, Y: fc.Y},
				{X: routeX, Y: fc.Y},
				{X: routeX, Y: fc.Y + 26},
				{X: from.X + from.W, Y: fc.Y + 26},
			}
		}
		return []Point{
			{X: from.X + from.W, Y: fc.Y},
			{X: routeX, Y: fc.Y},
			{X: routeX, Y: tc.Y},
			{X: to.X + to.W, Y: tc.Y},
		}
	}
	routeY := 2*margin + crossTotal + offset
	fc := from.Center()
	tc := to.Center()
	if self {
		return []Point{
			{X: fc.X, Y: from.Y + from.H},
			{X: fc.X, Y: routeY},
			{X: fc.X + 26, Y: routeY},
			{X: fc.X + 26, Y: to.Y + to.H},
		}
	}
	return []Point{
		{X: fc.X, Y: from.Y + from.H},
		{X: fc.X, Y: routeY},
		{X: tc.X, Y: routeY},
		{X: tc.X, Y: to.Y + to.H},
	}
}

func polylineMidpoint(pts []Point) (Point, Point) {
	var total float64
	for i := 1; i < len(pts); i++ {
		total += math.Hypot(float64(pts[i].X-pts[i-1].X), float64(pts[i].Y-pts[i-1].Y))
	}
	half := total / 2
	for i := 1; i < len(pts); i++ {
		seg := math.Hypot(float64(pts[i].X-pts[i-1].X), float64(pts[i].Y-pts[i-1].Y))
		if half <= seg && seg > 0 {
			t := float32(half / seg)
			dx := (pts[i].X - pts[i-1].X) / float32(seg)
			dy := (pts[i].Y - pts[i-1].Y) / float32(seg)
			return Point{X: pts[i-1].X + t*(pts[i].X-pts[i-1].X), Y: pts[i-1].Y + t*(pts[i].Y-pts[i-1].Y)}, Point{X: dx, Y: dy}
		}
		half -= seg
	}
	last := pts[len(pts)-1]
	return last, Point{X: 1, Y: 0}
}

// fitScene shifts a scene so nothing sits outside the positive canvas, and
// grows the canvas to include edge labels that overhang the node bounds.
func fitScene(s *Scene) *Diagnostic {
	if s == nil {
		return &Diagnostic{Message: "no diagram scene"}
	}
	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := float32(-math.MaxFloat32), float32(-math.MaxFloat32)
	consider := func(x0, y0, x1, y1 float32) {
		minX = min32(minX, x0)
		minY = min32(minY, y0)
		maxX = max32(maxX, x1)
		maxY = max32(maxY, y1)
	}
	for _, n := range s.Nodes {
		consider(n.Box.X, n.Box.Y, n.Box.X+n.Box.W, n.Box.Y+n.Box.H)
	}
	for _, e := range s.Edges {
		for _, p := range e.Points {
			consider(p.X, p.Y, p.X, p.Y)
		}
		if e.LabelBox.W > 0 {
			consider(e.LabelBox.X, e.LabelBox.Y, e.LabelBox.X+e.LabelBox.W, e.LabelBox.Y+e.LabelBox.H)
		}
	}
	for _, p := range s.Participants {
		consider(p.Box.X, p.Box.Y, p.Box.X+p.Box.W, p.Box.Y+p.Box.H)
	}
	for _, m := range s.Messages {
		if m.LabelBox.W > 0 {
			consider(m.LabelBox.X, m.LabelBox.Y, m.LabelBox.X+m.LabelBox.W, m.LabelBox.Y+m.LabelBox.H)
		}
		for _, p := range m.Loop {
			consider(p.X, p.Y, p.X, p.Y)
		}
	}
	for _, n := range s.Notes {
		consider(n.Box.X, n.Box.Y, n.Box.X+n.Box.W, n.Box.Y+n.Box.H)
	}
	if maxX < minX {
		return &Diagnostic{Message: "diagram layout produced an empty scene"}
	}
	dx, dy := float32(0), float32(0)
	if minX < 0 {
		dx = -minX
	}
	if minY < 0 {
		dy = -minY
	}
	if dx != 0 || dy != 0 {
		for i := range s.Nodes {
			s.Nodes[i].Box.X += dx
			s.Nodes[i].Box.Y += dy
		}
		for i := range s.Edges {
			for k := range s.Edges[i].Points {
				s.Edges[i].Points[k].X += dx
				s.Edges[i].Points[k].Y += dy
			}
			s.Edges[i].LabelBox.X += dx
			s.Edges[i].LabelBox.Y += dy
		}
		for i := range s.Participants {
			s.Participants[i].Box.X += dx
			s.Participants[i].CenterX += dx
			s.Participants[i].Box.Y += dy
		}
		for i := range s.Messages {
			s.Messages[i].FromX += dx
			s.Messages[i].ToX += dx
			s.Messages[i].Y += dy
			s.Messages[i].LabelBox.X += dx
			s.Messages[i].LabelBox.Y += dy
			for k := range s.Messages[i].Loop {
				s.Messages[i].Loop[k].X += dx
				s.Messages[i].Loop[k].Y += dy
			}
		}
		for i := range s.Notes {
			s.Notes[i].Box.X += dx
			s.Notes[i].Box.Y += dy
		}
		maxX += dx
		maxY += dy
	}
	if need := maxX + marginDIP; need > s.Width {
		s.Width = need
	}
	if need := maxY + marginDIP; need > s.Height {
		s.Height = need
	}
	return nil
}

func checkSceneSize(s *Scene, limits Limits) *Diagnostic {
	if s.Width > limits.MaxWidth || s.Height > limits.MaxHeight {
		return &Diagnostic{Message: fmt.Sprintf("diagram is %.0f×%.0f DIP; the limit is %.0f×%.0f (reduce nodes or shorten labels)", s.Width, s.Height, limits.MaxWidth, limits.MaxHeight)}
	}
	if s.Width <= 0 || s.Height <= 0 {
		return &Diagnostic{Message: "diagram layout produced an empty scene"}
	}
	return nil
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
