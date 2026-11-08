package desktop

import (
	"strconv"
	"strings"
	"sync"

	"gorequest/internal/core"
)

// maxPendingSSEBytes bounds events buffered between two UI frames. When a
// server floods faster than the UI drains, the reader goroutine waits instead
// of growing memory without limit; that backpressure reaches the socket.
const maxPendingSSEBytes = 8 << 20

// tabSSEStream bridges core's server-sent-event callbacks (reader goroutine) to
// one tab's UI state (UI thread).
//
// The reader goroutine only appends to a mutex-guarded pending list and asks
// for at most one window Update at a time. Events that arrive while an update
// is already queued are coalesced into it, so a fast server cannot force one
// frame per event or per byte. Every flush re-checks the owning tab, its send
// generation and the window, so a superseded or closed stream can never write
// into a newer response.
type tabSSEStream struct {
	app *App
	tab *Tab
	win window
	seq int

	mu           sync.Mutex
	cond         *sync.Cond
	pending      []core.SSEEvent
	pendingBytes int
	open         *core.StreamOpen
	end          *core.StreamEnd
	scheduled    bool
	closed       bool

	// UI-thread-only display state, published to tab.Resp on each flush.
	opened     bool
	phase      core.StreamPhase
	eventCount int
	bytes      int64
	truncated  bool
	display    strings.Builder
}

func newTabSSEStream(a *App, tab *Tab, win window, seq int) *tabSSEStream {
	s := &tabSSEStream{app: a, tab: tab, win: win, seq: seq}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// handler returns the core callbacks for this stream.
func (s *tabSSEStream) handler() *core.StreamHandler {
	return &core.StreamHandler{
		OnOpen:  s.onOpen,
		OnEvent: s.onEvent,
		OnEnd:   s.onEnd,
	}
}

func (s *tabSSEStream) onOpen(info core.StreamOpen) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.open = &info
	s.mu.Unlock()
	s.schedule()
}

func (s *tabSSEStream) onEvent(ev core.SSEEvent) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	// Backpressure: a flood between two frames waits here instead of growing
	// the pending buffer without bound.
	for s.pendingBytes >= maxPendingSSEBytes && !s.closed {
		s.cond.Wait()
	}
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.pending = append(s.pending, ev)
	s.pendingBytes += len(ev.Data) + len(ev.ID) + len(ev.Type) + 64
	s.mu.Unlock()
	s.schedule()
}

func (s *tabSSEStream) onEnd(end core.StreamEnd) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.end = &end
	s.mu.Unlock()
	s.schedule()
}

// schedule asks for one UI update unless one is already queued. The check and
// the flag share the mutex with the pending append, so an event that arrives
// while a flush is draining either lands in that drain or schedules the next.
func (s *tabSSEStream) schedule() {
	s.mu.Lock()
	if s.closed || s.scheduled {
		s.mu.Unlock()
		return
	}
	s.scheduled = true
	s.mu.Unlock()

	win := s.win
	if win == nil || win.IsDestroyed() {
		s.close()
		return
	}
	win.Update(func() { s.flush() })
}

// close detaches the stream. Late callbacks become no-ops.
func (s *tabSSEStream) close() {
	s.mu.Lock()
	s.closed = true
	s.pending = nil
	s.pendingBytes = 0
	s.open = nil
	s.end = nil
	s.cond.Broadcast()
	s.mu.Unlock()
}

// flush applies everything buffered since the last frame. It is safe to call
// directly from the send-completion update and from a scheduled update; a
// second call with nothing pending is a no-op.
func (s *tabSSEStream) flush() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	events := s.pending
	s.pending = nil
	s.pendingBytes = 0
	open := s.open
	s.open = nil
	end := s.end
	s.end = nil
	s.scheduled = false
	// Wake a reader that filled the pending buffer while this frame was queued.
	s.cond.Broadcast()
	s.mu.Unlock()

	if s.app == nil || s.app.closed {
		s.close()
		return
	}
	if s.win != nil && s.win.IsDestroyed() {
		s.close()
		return
	}
	ps := s.app.ps
	if s.tab != nil && s.tab.Owner != nil {
		ps = s.tab.Owner
	}
	if s.tab == nil || s.tab.sendSeq != s.seq || ps == nil || ps.indexOfTab(s.tab) < 0 {
		s.close()
		return
	}

	changed := false
	if open != nil {
		s.applyOpen(*open)
		changed = true
	}
	if len(events) > 0 {
		for _, ev := range events {
			s.appendEvent(ev)
		}
		changed = true
	}
	if end != nil {
		s.applyEnd(*end)
		changed = true
	}
	if changed {
		s.publish()
	}
}

func (s *tabSSEStream) applyOpen(info core.StreamOpen) {
	s.opened = true
	s.phase = core.StreamPhaseOpen
	tab := s.tab
	tab.Resp = &core.ExecResult{
		Status:      info.Status,
		Proto:       info.Proto,
		Headers:     info.Headers,
		ContentType: info.ContentType,
		FinalURL:    info.FinalURL,
		Stream:      &core.StreamInfo{Kind: "sse", Phase: core.StreamPhaseOpen},
	}
	tab.RespErr = ""
	tab.RespRawIdx = 0
	// A new response starts from a fresh read-only buffer.
	tab.respEditor = nil
	tab.respBuiltVer = -1
	tab.respVersion++
}

func (s *tabSSEStream) appendEvent(ev core.SSEEvent) {
	s.eventCount++
	if ev.Truncated {
		s.truncated = true
	}
	s.display.WriteString(formatSSEEvent(ev))
}

func (s *tabSSEStream) applyEnd(end core.StreamEnd) {
	s.phase = end.Phase
	if end.EventCount > s.eventCount {
		s.eventCount = end.EventCount
	}
	if end.BytesReceived > 0 {
		s.bytes = end.BytesReceived
	}
	if end.Truncated {
		s.truncated = true
	}
}

// publish copies the accumulated redacted event log into the tab's response so
// the existing response panel, copy, save and history paths all see exactly
// what the UI shows.
func (s *tabSSEStream) publish() {
	tab := s.tab
	if tab.Resp == nil {
		tab.Resp = &core.ExecResult{}
	}
	text := s.display.String()
	size := s.bytes
	if size <= 0 {
		size = int64(len(text))
	}
	tab.Resp.Body = []byte(text)
	tab.Resp.BodySize = size
	tab.Resp.Pretty = ""
	tab.Resp.IsBinary = false
	if tab.Resp.Stream == nil {
		tab.Resp.Stream = &core.StreamInfo{Kind: "sse"}
	}
	tab.Resp.Stream.Kind = "sse"
	tab.Resp.Stream.Phase = s.phase
	tab.Resp.Stream.EventCount = s.eventCount
	tab.Resp.Stream.BytesReceived = size
	tab.Resp.Stream.Truncated = s.truncated
	tab.respVersion++
	if tab.respEditor != nil {
		// Keep the same editor (and its scroll position) instead of rebuilding
		// it every frame; Reset drops the read-only undo history.
		tab.respEditor.Buffer.Reset(text)
		tab.respBuiltVer = tab.respVersion
		tab.respBuiltIdx = tab.RespRawIdx
	}
}

// formatSSEEvent renders one parsed event back into the familiar wire form so
// the response panel reads like the stream the server sent. The event fields
// are already redacted by core.
func formatSSEEvent(ev core.SSEEvent) string {
	var b strings.Builder
	for _, c := range ev.Comments {
		b.WriteString(": ")
		b.WriteString(c)
		b.WriteByte('\n')
	}
	if ev.Type != "" {
		b.WriteString("event: ")
		b.WriteString(ev.Type)
		b.WriteByte('\n')
	}
	if ev.ID != "" {
		b.WriteString("id: ")
		b.WriteString(ev.ID)
		b.WriteByte('\n')
	}
	if ev.HasRetry {
		b.WriteString("retry: ")
		b.WriteString(strconv.FormatInt(ev.Retry.Milliseconds(), 10))
		b.WriteByte('\n')
	}
	if ev.HasData {
		if ev.Data == "" {
			b.WriteString("data: \n")
		} else {
			for _, line := range strings.Split(ev.Data, "\n") {
				b.WriteString("data: ")
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
	}
	b.WriteByte('\n')
	return b.String()
}

// teardownTabStream detaches and closes a tab's live stream. Called when a tab
// is closed, its window is destroyed, or a new send supersedes the old one.
func teardownTabStream(tab *Tab) {
	if tab == nil || tab.sse == nil {
		return
	}
	tab.sse.close()
	tab.sse = nil
}
