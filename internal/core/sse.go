package core

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Server-sent events (SSE) are a text/event-stream response that stays open and
// delivers events incrementally. The desktop must show each event as it arrives
// instead of waiting for EOF, so ExecuteWithOptions grows an opt-in streaming
// path: when ExecuteOptions.Stream is set and the response media type is
// text/event-stream, the body is parsed incrementally and delivered through the
// callbacks while still being bounded and retained for the final ExecResult.
//
// Everything here is transport- and UI-neutral: the parser never performs I/O
// and the callbacks run on the reader goroutine. Callers that need UI updates
// must coalesce them (the desktop does this in sse_stream.go).

// SSE parser bounds. A malicious or broken server must not be able to grow the
// parser without limit: an over-long line is discarded, an over-long event is
// truncated and flagged, and the retained body is capped by MaxResponseBytes.
const (
	// MaxSSELineBytes caps one physical line. Longer lines are discarded up to
	// the next terminator rather than buffered. It is one event's worth plus the
	// field prefix, so a single-line JSON event at the event bound still parses.
	MaxSSELineBytes = MaxSSEEventBytes + 64
	// MaxSSEEventBytes caps the joined data of one event.
	MaxSSEEventBytes = 256 << 10
	// MaxSSEComments caps the comment lines attached to one event.
	MaxSSEComments = 64
)

// SSEEvent is one dispatched server-sent event. Data is the joined data field
// (multiple data: lines joined with "\n"), already redacted when a Redactor was
// configured. ID follows the EventSource rule: it is the last event ID seen on
// the stream, not only the id field of this event.
type SSEEvent struct {
	ID   string
	Type string
	Data string
	// HasData reports that the event carried at least one data field, which
	// distinguishes an empty data event from a comment-only event.
	HasData  bool
	Retry    time.Duration
	HasRetry bool
	Comments []string
	// Truncated reports that the event data exceeded MaxSSEEventBytes and was
	// cut at that bound.
	Truncated bool
}

// StreamPhase is the lifecycle phase of a streaming response.
type StreamPhase string

const (
	// StreamPhaseOpen means the stream is connected and receiving events.
	StreamPhaseOpen StreamPhase = "open"
	// StreamPhaseCompleted means the server ended the stream cleanly.
	StreamPhaseCompleted StreamPhase = "completed"
	// StreamPhaseCanceled means the caller canceled the request; already
	// received events are retained.
	StreamPhaseCanceled StreamPhase = "canceled"
	// StreamPhaseTimeout means the configured timeout fired; already received
	// events are retained.
	StreamPhaseTimeout StreamPhase = "timeout"
	// StreamPhaseFailed means the connection broke; already received events
	// are retained.
	StreamPhaseFailed StreamPhase = "failed"
)

// StreamOpen describes the response that opened a stream. It is delivered once,
// before the first event.
type StreamOpen struct {
	Status      int
	Proto       string
	Headers     [][2]string
	ContentType string
	FinalURL    string
}

// StreamEnd is delivered once when the stream stops for any reason.
type StreamEnd struct {
	Phase         StreamPhase
	EventCount    int
	BytesReceived int64
	Truncated     bool
	// Err is the redacted human-readable end reason, empty for a clean end.
	Err string
}

// StreamHandler receives incremental streaming callbacks from
// ExecuteWithOptions. All callbacks are optional and run on the reader
// goroutine, so they must not block for UI work.
type StreamHandler struct {
	OnOpen  func(StreamOpen)
	OnEvent func(SSEEvent)
	OnEnd   func(StreamEnd)
}

// StreamInfo records the streaming outcome on ExecResult. It is nil for
// ordinary responses.
type StreamInfo struct {
	Kind          string
	Phase         StreamPhase
	EventCount    int
	BytesReceived int64
	Truncated     bool
	EndReason     string
}

// IsEventStreamContentType reports whether raw is the SSE media type, with or
// without parameters (for example "text/event-stream; charset=utf-8").
func IsEventStreamContentType(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(raw)
	if err != nil {
		mt = strings.TrimSpace(strings.Split(raw, ";")[0])
	}
	return strings.EqualFold(strings.TrimSpace(mt), "text/event-stream")
}

// isSSEResponse reports whether resp should be handled by the streaming path.
func isSSEResponse(resp *http.Response, method string) bool {
	if resp == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(method), http.MethodHead) {
		return false
	}
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return false
	}
	return IsEventStreamContentType(resp.Header.Get("Content-Type"))
}

// sseParser implements the WHATWG EventSource line grammar incrementally. It
// buffers bytes, never a decoded rune, so a multi-byte UTF-8 sequence split
// across network chunks is reassembled before conversion. CRLF split across
// chunks is handled with skipLF.
type sseParser struct {
	line     []byte
	skipLF   bool
	started  bool
	discard  bool
	lastID   string
	eventTyp string
	data     []byte
	dataCut  bool
	comments []string
	retry    time.Duration
	hasRetry bool
}

func newSSEParser() *sseParser { return &sseParser{} }

// Feed consumes one network chunk and emits every event completed by it.
func (p *sseParser) Feed(chunk []byte, emit func(SSEEvent)) {
	if !p.started {
		p.started = true
		chunk = bytes.TrimPrefix(chunk, []byte{0xEF, 0xBB, 0xBF})
	}
	for len(chunk) > 0 {
		if p.skipLF {
			p.skipLF = false
			if chunk[0] == '\n' {
				chunk = chunk[1:]
				continue
			}
		}
		i := bytes.IndexAny(chunk, "\r\n")
		if i < 0 {
			p.appendLine(chunk)
			return
		}
		p.appendLine(chunk[:i])
		term := chunk[i]
		chunk = chunk[i+1:]
		if term == '\r' {
			if len(chunk) == 0 {
				// The matching '\n' may start the next chunk.
				p.skipLF = true
			} else if chunk[0] == '\n' {
				chunk = chunk[1:]
			}
		}
		p.endLine(emit)
	}
}

// Flush processes a trailing line and dispatches any pending event. The
// EventSource specification discards an incomplete event at EOF; GoRequest
// keeps it so a debugging view never loses content the server already sent.
func (p *sseParser) Flush(emit func(SSEEvent)) {
	if len(p.line) > 0 {
		p.endLine(emit)
	}
	if len(p.data) > 0 || len(p.comments) > 0 {
		p.dispatch(emit)
	}
}

func (p *sseParser) appendLine(b []byte) {
	if p.discard {
		return
	}
	if len(p.line)+len(b) > MaxSSELineBytes {
		p.discard = true
		p.dataCut = true
		p.line = p.line[:0]
		return
	}
	p.line = append(p.line, b...)
}

func (p *sseParser) endLine(emit func(SSEEvent)) {
	if p.discard {
		p.discard = false
		p.line = p.line[:0]
		return
	}
	line := string(p.line)
	p.line = p.line[:0]
	if line == "" {
		p.dispatch(emit)
		return
	}
	if strings.HasPrefix(line, ":") {
		comment := line[1:]
		if strings.HasPrefix(comment, " ") {
			comment = comment[1:]
		}
		if len(p.comments) < MaxSSEComments {
			p.comments = append(p.comments, comment)
		}
		return
	}
	field, value, hasColon := strings.Cut(line, ":")
	if hasColon && strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	switch field {
	case "data":
		p.appendData(value)
	case "event":
		p.eventTyp = value
	case "id":
		if !strings.ContainsRune(value, '\x00') {
			p.lastID = value
		}
	case "retry":
		if ms, err := strconv.Atoi(value); err == nil && ms >= 0 {
			p.retry = time.Duration(ms) * time.Millisecond
			p.hasRetry = true
		}
	}
}

func (p *sseParser) appendData(v string) {
	remaining := MaxSSEEventBytes - len(p.data)
	if remaining <= 0 {
		p.dataCut = true
		return
	}
	if len(v) > remaining-1 {
		v = v[:remaining-1]
		// Do not leave a split UTF-8 sequence in the retained prefix.
		for len(v) > 0 && !utf8.ValidString(v) {
			v = v[:len(v)-1]
		}
		p.dataCut = true
	}
	p.data = append(p.data, v...)
	p.data = append(p.data, '\n')
}

func (p *sseParser) dispatch(emit func(SSEEvent)) {
	if len(p.data) == 0 && len(p.comments) == 0 {
		p.eventTyp = ""
		return
	}
	ev := SSEEvent{
		ID:        p.lastID,
		Type:      p.eventTyp,
		Data:      strings.TrimSuffix(string(p.data), "\n"),
		HasData:   len(p.data) > 0,
		Truncated: p.dataCut,
	}
	if len(p.comments) > 0 {
		ev.Comments = append([]string(nil), p.comments...)
	}
	if p.hasRetry {
		ev.Retry = p.retry
		ev.HasRetry = true
		p.hasRetry = false
	}
	p.eventTyp = ""
	p.data = p.data[:0]
	p.dataCut = false
	p.comments = nil
	emit(ev)
}

// redactSSEEvent scrubs every user-visible field before it leaves core. The
// parser itself never knows about secrets; redaction happens at the callback
// boundary so a UI can never receive a raw secret through an event.
func redactSSEEvent(red *Redactor, ev SSEEvent) SSEEvent {
	if red == nil {
		return ev
	}
	ev.ID = red.RedactString(ev.ID)
	ev.Type = red.RedactString(ev.Type)
	ev.Data = red.RedactString(ev.Data)
	if len(ev.Comments) > 0 {
		comments := make([]string, len(ev.Comments))
		for i, c := range ev.Comments {
			comments[i] = red.RedactString(c)
		}
		ev.Comments = comments
	}
	return ev
}

// sseReadConfig carries the response facts and limits for one streaming read.
type sseReadConfig struct {
	parentCtx             context.Context
	timedOut              *atomic.Bool
	maxBody               int64
	disableAutoDecompress bool
	redactor              *Redactor
	handler               *StreamHandler
	timeoutLabel          string

	status      int
	proto       string
	headers     [][2]string
	contentType string
	finalURL    string
}

// openDecodedBody returns a reader that decodes body according to the
// response's Content-Encoding, mirroring readAndDecodeResponseBody but
// incrementally. body is normally resp.Body, possibly wrapped in a counting
// reader so the caller can report the wire size.
func openDecodedBody(body io.Reader, resp *http.Response, reqMethod string, disableAutoDecompress bool) (io.Reader, string, bool, error) {
	rawCE := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if strings.Contains(rawCE, ",") {
		rawCE = strings.TrimSpace(strings.Split(rawCE, ",")[0])
	}
	shouldDecompress := !disableAutoDecompress &&
		!resp.Uncompressed &&
		!strings.EqualFold(reqMethod, http.MethodHead) &&
		resp.StatusCode != http.StatusNoContent &&
		resp.StatusCode != http.StatusNotModified &&
		(rawCE == "gzip" || rawCE == "x-gzip" || rawCE == "deflate")
	if !shouldDecompress {
		return body, rawCE, resp.Uncompressed, nil
	}

	br := bufio.NewReaderSize(body, 32<<10)
	if _, err := br.Peek(1); err != nil {
		if err == io.EOF {
			return br, rawCE, true, nil
		}
		return nil, rawCE, false, err
	}
	switch rawCE {
	case "gzip", "x-gzip":
		gr, err := gzip.NewReader(br)
		if err != nil {
			return nil, rawCE, false, err
		}
		return gr, rawCE, true, nil
	case "deflate":
		hdr, _ := br.Peek(2)
		if isValidZlibHeader(hdr) {
			zr, err := zlib.NewReader(br)
			if err != nil {
				return nil, rawCE, false, err
			}
			return zr, rawCE, true, nil
		}
		return flate.NewReader(br), rawCE, true, nil
	}
	return br, rawCE, false, nil
}

// readSSEStream reads and parses an event stream until EOF, cancellation,
// timeout, an error, or the body bound. It always returns a non-nil payload so
// the caller can retain what was received, and always reports OnEnd exactly
// once.
func readSSEStream(ctx context.Context, resp *http.Response, reqMethod string, cfg sseReadConfig) (*responseBodyPayload, *StreamInfo, error) {
	if cfg.maxBody <= 0 {
		cfg.maxBody = DefaultMaxBody
	}
	wireLimit := cfg.maxBody*2 + (64 << 10)
	if wireLimit < cfg.maxBody {
		wireLimit = cfg.maxBody
	}

	wire := &wireCountingReader{ctx: ctx, r: resp.Body, wireLimit: wireLimit}
	reader, encoding, decompressed, err := openDecodedBody(wire, resp, reqMethod, cfg.disableAutoDecompress)
	if err != nil {
		info := &StreamInfo{Kind: "sse", Phase: StreamPhaseFailed, EndReason: "reading response body"}
		if cfg.redactor != nil {
			info.EndReason = cfg.redactor.RedactString(info.EndReason)
		}
		if cfg.handler != nil && cfg.handler.OnEnd != nil {
			cfg.handler.OnEnd(StreamEnd{Phase: info.Phase, Err: info.EndReason})
		}
		return &responseBodyPayload{contentEncoding: encoding}, info, runErrorf("reading response body: %v", err)
	}

	parser := newSSEParser()
	eventCount := 0
	emit := func(ev SSEEvent) {
		eventCount++
		if cfg.handler != nil && cfg.handler.OnEvent != nil {
			cfg.handler.OnEvent(redactSSEEvent(cfg.redactor, ev))
		}
	}
	if cfg.handler != nil && cfg.handler.OnOpen != nil {
		cfg.handler.OnOpen(StreamOpen{
			Status:      cfg.status,
			Proto:       cfg.proto,
			Headers:     cfg.headers,
			ContentType: cfg.contentType,
			FinalURL:    cfg.finalURL,
		})
	}

	raw, truncated, readErr := readSSEBody(ctx, reader, cfg.maxBody, parser, emit)
	if wire.Total() > wireLimit {
		truncated = true
	}
	body := raw
	if cfg.redactor != nil {
		body = []byte(cfg.redactor.RedactString(string(raw)))
	}

	info := &StreamInfo{
		Kind:          "sse",
		Phase:         StreamPhaseCompleted,
		EventCount:    eventCount,
		BytesReceived: int64(len(raw)),
		Truncated:     truncated,
	}
	var outErr error
	if readErr != nil {
		info.Phase, info.EndReason, outErr = classifyStreamError(cfg, readErr)
		if cfg.redactor != nil && outErr != nil {
			outErr = cfg.redactor.RedactError(outErr)
			info.EndReason = cfg.redactor.RedactString(info.EndReason)
		}
	}

	transferSize := wire.Total()
	if resp.Uncompressed && resp.ContentLength > 0 {
		transferSize = resp.ContentLength
	}
	payload := &responseBodyPayload{
		body:            body,
		transferSize:    transferSize,
		bodySize:        int64(len(raw)),
		truncated:       truncated,
		contentEncoding: encoding,
		decompressed:    decompressed,
	}
	if cfg.handler != nil && cfg.handler.OnEnd != nil {
		cfg.handler.OnEnd(StreamEnd{
			Phase:         info.Phase,
			EventCount:    info.EventCount,
			BytesReceived: info.BytesReceived,
			Truncated:     info.Truncated,
			Err:           info.EndReason,
		})
	}
	return payload, info, outErr
}

func classifyStreamError(cfg sseReadConfig, readErr error) (StreamPhase, string, error) {
	timeoutMsg := "request timed out"
	if cfg.timeoutLabel != "" {
		timeoutMsg = fmt.Sprintf("request timed out after %s", cfg.timeoutLabel)
	}
	if cfg.parentCtx != nil {
		if errors.Is(cfg.parentCtx.Err(), context.Canceled) {
			return StreamPhaseCanceled, "request canceled", &RunError{Err: errors.New("request canceled")}
		}
		if errors.Is(cfg.parentCtx.Err(), context.DeadlineExceeded) {
			return StreamPhaseTimeout, timeoutMsg, &RunError{Err: errors.New(timeoutMsg)}
		}
	}
	if cfg.timedOut != nil && cfg.timedOut.Load() {
		return StreamPhaseTimeout, timeoutMsg, &RunError{Err: errors.New(timeoutMsg)}
	}
	return StreamPhaseFailed, fmt.Sprintf("stream disconnected: %v", readErr), &RunError{Err: fmt.Errorf("reading response body: %w", readErr)}
}

// readSSEBody reads chunks, appends them to a bounded buffer and feeds the
// parser. It stops at maxBytes and reports truncation instead of growing
// without limit.
func readSSEBody(ctx context.Context, r io.Reader, maxBytes int64, parser *sseParser, emit func(SSEEvent)) ([]byte, bool, error) {
	var buf bytes.Buffer
	chunk := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			parser.Flush(emit)
			return buf.Bytes(), false, err
		}
		n, rerr := r.Read(chunk)
		if n > 0 {
			allowed := maxBytes - total
			if allowed < 0 {
				allowed = 0
			}
			take := int64(n)
			truncated := false
			if take > allowed {
				take = allowed
				truncated = true
			}
			if take > 0 {
				buf.Write(chunk[:take])
				total += take
				parser.Feed(chunk[:take], emit)
			}
			if truncated {
				parser.Flush(emit)
				return buf.Bytes(), true, nil
			}
		}
		if rerr == io.EOF {
			parser.Flush(emit)
			return buf.Bytes(), false, nil
		}
		if rerr != nil {
			parser.Flush(emit)
			return buf.Bytes(), false, rerr
		}
	}
}
