// Package stream defines GoRequest's shared bounded event stream contract for
// long-lived streaming protocols (P01 WebSocket today; P02 GraphQL
// subscriptions and P03 gRPC streams later, plus the A01 runner's future
// streaming scheduler).
//
// The contract is deliberately small and free of UI or transport knowledge:
//
//   - Every event carries a monotonic sequence number (Seq), a wall-clock
//     timestamp, the owning stream's correlation ID (StreamID), an optional
//     operation correlation ID (OpID), a direction (in/out/none) and a kind.
//   - The log is a bounded ring: capacity and per-event payload size are
//     enforced, overflow and truncation are counted — never silently ignored.
//   - Consumers read incrementally with a Cursor, or subscribe with their own
//     channel; subscription delivery is non-blocking and dropped deliveries
//     are counted in Stats, so backpressure is observable rather than hidden
//     behind an unbounded buffer.
//   - Events flagged Sensitive carry payloads and summaries redacted at
//     append time via core.Redactor; redaction is never retroactive so a
//     secret registered later cannot leak into already-retained events only
//     because it was registered late — the publisher decides sensitivity.
//   - DeadlinePolicy names the four streaming deadlines (connect, handshake,
//     per-message and total) that streaming adapters must honor, plus
//     cancellation through the caller's context.
//
// This package never performs network I/O, spawns goroutines, or touches the
// UI. Transport adapters own their goroutines and call Log.Append; consumers
// own theirs and use SnapshotSince or Subscribe.
package stream

import (
	"sync"
	"time"

	"gorequest/internal/core"
)

// Direction marks which side of the wire an event belongs to.
type Direction string

const (
	// DirNone marks session-level facts that are not payload traffic.
	DirNone Direction = "none"
	// DirIn marks traffic received from the remote peer.
	DirIn Direction = "in"
	// DirOut marks traffic the local client sent.
	DirOut Direction = "out"
)

// Kind classifies an event. Adapters may only use these kinds so report and
// UI consumers can rely on a stable set (protocol details ride in Summary,
// Payload, Code and Reason).
type Kind string

const (
	// KindState reports a session state change (connecting/open/closing/
	// closed/error); the new state rides in Summary and Code is unused.
	KindState Kind = "state"
	// KindInfo reports session facts (handshake response, TLS state, proxy
	// in use, policy decisions) that are not payload traffic.
	KindInfo Kind = "info"
	// KindSend and KindReceive carry payload messages sent / received.
	KindSend    Kind = "send"
	KindReceive Kind = "receive"
	// KindPing/KindPong carry control frames; Pong events reuse the matching
	// ping's OpID so round-trip correlation works via cursor reads.
	KindPing Kind = "ping"
	KindPong Kind = "pong"
	// KindClose reports the close frame exchange; Code and Reason carry the
	// close code and (redacted) close reason.
	KindClose Kind = "close"
	// KindError reports a failure fact (bad handshake, unexpected EOF, dead
	// line, deadline exceeded, cancellation). It never carries payload data.
	KindError Kind = "error"
	// KindDropped reports retained-event loss caused by the bounded ring.
	// Adapters may append it when they surface an eviction to users; the Log
	// itself reports eviction through Stats.Overflowed and LastOverflow.
	KindDropped Kind = "dropped"
)

// IsPayloadKind reports whether the kind carries payload/control traffic
// rather than session-level facts.
func IsPayloadKind(k Kind) bool {
	return k == KindSend || k == KindReceive || k == KindPing || k == KindPong
}

// Event is one immutable record of streaming activity. Append assigns Seq,
// Time and StreamID; the publisher fills everything else. Payload pointers
// must not be mutated after Append hands the event out.
type Event struct {
	// Seq is the log-wide monotonic sequence number assigned by Append.
	Seq uint64
	// Time is the wall-clock time assigned by Append (UTC).
	Time time.Time
	// StreamID correlates this event with one stream instance (one WebSocket
	// connection, one GraphQL subscription, one gRPC stream call).
	StreamID string
	// Kind classifies the event (see Kind constants).
	Kind Kind
	// Direction marks in/out/none.
	Direction Direction
	// OpID optionally correlates operations with their responses, e.g. a
	// ping with its pong so awaiters can match without payload guessing.
	OpID string
	// Summary is a short human-readable fact line. When Sensitive is true the
	// summary is stored redacted via the configured core.Redactor.
	Summary string
	// Payload is the bounded raw frame/message payload; nil when the event
	// carries no payload. It is truncated to Options.MaxPayloadBytes and the
	// original length is preserved in PayloadLen.
	Payload []byte
	// IsText distinguishes text payloads from binary ones for renderers.
	IsText bool
	// Truncated reports that Payload was cut at MaxPayloadBytes.
	Truncated bool
	// PayloadLen is the payload's full original length in bytes.
	PayloadLen int
	// Sensitive marks payloads/summaries that may contain credentials; the
	// Log redacts them at append time via core.Redactor.
	Sensitive bool
	// Code carries the close code for KindClose (and is reused by KindState
	// to carry the terminal state code).
	Code int
	// Reason carries the (redacted) close reason for KindClose.
	Reason string
}

// redactedPayloadMask replaces binary payloads on sensitive events: binary
// data cannot be run through RedactString without lossy guessing, so it is
// masked instead of partially scrubbed.
const redactedPayloadMask = "***"

// Options configures a Log. Zero values select the documented defaults.
type Options struct {
	// StreamID is the correlation ID stamped on every event. When empty,
	// Append will still work but correlation is lost; NewLog generates one
	// when empty. Callers that want human-meaningful correlation (e.g.
	// "<project>#<url-hash>") set it explicitly.
	StreamID string
	// Capacity bounds the retained event ring. DefaultCapacity when <= 0.
	// Overflowed events are counted in Stats and LastOverflow reports the
	// sequence of the newest event that caused an eviction.
	Capacity int
	// MaxPayloadBytes bounds each retained payload. DefaultMaxPayloadBytes
	// when <= 0. Larger payloads are truncated and Truncated=true is set.
	MaxPayloadBytes int
	// Redactor scrubs Sensitive events at append time; nil keeps sensitive
	// payloads raw — only tests and non-UI tooling may do that.
	Redactor *core.Redactor
	// Redact, when set, replaces Redactor.RedactString. It lets an owner
	// serialize access to a shared redactor that other goroutines also update
	// (the WebSocket session registers send-time secrets while its read pump
	// appends events). Redactor may stay nil when Redact is set.
	Redact func(string) string
}

// DefaultCapacity is the retained-event ring default.
const DefaultCapacity = 500

// DefaultMaxPayloadBytes is the per-event retained payload bound.
const DefaultMaxPayloadBytes = 64 * 1024

// EffectiveCapacity returns the active ring capacity.
func (o Options) EffectiveCapacity() int {
	if o.Capacity > 0 {
		return o.Capacity
	}
	return DefaultCapacity
}

// EffectiveMaxPayloadBytes returns the active per-event payload bound.
func (o Options) EffectiveMaxPayloadBytes() int {
	if o.MaxPayloadBytes > 0 {
		return o.MaxPayloadBytes
	}
	return DefaultMaxPayloadBytes
}

// Stats observes the log's memory behavior: what was appended, what the
// bounded ring evicted, what got truncated, and what subscribers missed.
// Backpressure must be observable, never silently dropped.
type Stats struct {
	// Appended counts every accepted event.
	Appended uint64
	// Capacity is the ring's configured bound.
	Capacity int
	// Retained is the number of events currently in the ring.
	Retained int
	// Overflowed counts events evicted by the bounded ring.
	Overflowed uint64
	// Truncated counts events whose payload exceeded MaxPayloadBytes.
	Truncated uint64
	// Sensitive counts events stored redacted via Redactor.
	Sensitive uint64
	// SubscriberDrops counts delivery attempts that failed because a
	// subscriber's channel was full. Slow consumers observe their own loss
	// through cursor reads (SnapshotSince) or Stats.
	SubscriberDrops uint64
}

// Cursor points at the last event a consumer has processed. Zero means
// "nothing consumed"; SnapshotSince(Cursor) returns everything after it.
type Cursor uint64

// Log is the bounded store for one stream's events. It is safe for
// concurrent use; Append comes from the transport adapter's goroutine and
// consumers read from theirs.
type Log struct {
	opts Options

	mu     sync.Mutex
	events []Event
	next   uint64
	appended,
	overflowed,
	truncated,
	sensitive,
	subscriberDrops uint64
	lastOverflow uint64

	subMu      sync.Mutex
	subscriber []subscriber
}

type subscriber struct {
	ch chan<- Event
}

// NewLog creates a bounded event log. When opts.StreamID is empty a
// process-unique correlation ID is generated ("stream-<seq>").
func NewLog(opts Options) *Log {
	if opts.StreamID == "" {
		opts.StreamID = newStreamID()
	}
	return &Log{
		opts:   opts,
		events: make([]Event, 0, opts.EffectiveCapacity()),
	}
}

// Options returns the log's frozen options.
func (l *Log) Options() Options { return l.opts }

// StreamID returns the log's correlation ID.
func (l *Log) StreamID() string { return l.opts.StreamID }

// Append normalizes and stores one event: assigns Seq/Time/StreamID, enforces
// the payload bound (truncation recorded), redacts Sensitive events through
// the configured Redactor, evicts the oldest ring members when at capacity
// and delivers to subscribers without blocking. It returns the stored event so
// callers can report the assigned sequence.
func (l *Log) Append(ev Event) Event {
	l.mu.Lock()
	now := time.Now().UTC()
	ev.Time = now
	ev.StreamID = l.opts.StreamID
	ev.Seq = l.next
	l.next++
	l.appended++

	// PayloadLen records the original size before redaction/truncation so a
	// masked sensitive payload still reports how large it was.
	ev.PayloadLen = len(ev.Payload)
	l.applyRedactionLocked(&ev)
	l.applyBoundLocked(&ev)

	l.events = append(l.events, ev)
	capacity := l.opts.EffectiveCapacity()
	if len(l.events) > capacity {
		evicted := len(l.events) - capacity
		copy(l.events, l.events[evicted:evicted+capacity])
		l.events = l.events[:capacity]
		l.overflowed += uint64(evicted)
		l.lastOverflow = ev.Seq
	}
	l.mu.Unlock()

	l.notify(ev)
	return ev
}

// redactString routes through the configured redaction function, falling back
// to the plain Redactor when no serialized function was supplied.
func (l *Log) redactString(s string) string {
	if l.opts.Redact != nil {
		return l.opts.Redact(s)
	}
	if l.opts.Redactor != nil {
		return l.opts.Redactor.RedactString(s)
	}
	return s
}

// applyRedactionLocked scrubs sensitive summaries/payloads at append time.
func (l *Log) applyRedactionLocked(ev *Event) {
	if !ev.Sensitive || (l.opts.Redact == nil && l.opts.Redactor == nil) {
		return
	}
	l.sensitive++
	ev.Summary = l.redactString(ev.Summary)
	ev.Reason = l.redactString(ev.Reason)
	if len(ev.Payload) == 0 {
		return
	}
	if !ev.IsText {
		// Binary payloads cannot be string-redacted without guessing; mask
		// them outright instead of shipping partially scrubbed bytes.
		ev.Payload = []byte(redactedPayloadMask)
		ev.IsText = true
		ev.Truncated = false
		return
	}
	ev.Payload = []byte(l.redactString(string(ev.Payload)))
}

// applyBoundLocked truncates oversized payloads. PayloadLen was recorded
// before redaction, so it stays the original wire length.
func (l *Log) applyBoundLocked(ev *Event) {
	max := l.opts.EffectiveMaxPayloadBytes()
	if len(ev.Payload) > max {
		ev.Truncated = true
		l.truncated++
	}
	ev.Payload = append([]byte(nil), ev.Payload[:min(max, len(ev.Payload))]...)
}

// notify delivers to subscribers without blocking. A full channel counts as
// a drop in Stats — that is the observable backpressure contract.
func (l *Log) notify(ev Event) {
	l.subMu.Lock()
	defer l.subMu.Unlock()
	for _, s := range l.subscriber {
		select {
		case s.ch <- ev:
		default:
			l.mu.Lock()
			l.subscriberDrops++
			l.mu.Unlock()
		}
	}
}

// Subscribe registers ch to receive live copies of every appended event.
// Delivery is non-blocking: full channels drop the delivery and the drop is
// counted in Stats (SnapshotSince remains lossless for cursor consumers).
// The returned stop function unregisters the channel.
func (l *Log) Subscribe(ch chan<- Event) (stop func()) {
	l.subMu.Lock()
	s := subscriber{ch: ch}
	l.subscriber = append(l.subscriber, s)
	l.subMu.Unlock()
	return func() {
		l.subMu.Lock()
		defer l.subMu.Unlock()
		for i := range l.subscriber {
			if l.subscriber[i].ch == ch {
				l.subscriber = append(l.subscriber[:i:i], l.subscriber[i+1:]...)
				return
			}
		}
	}
}

// Snapshot returns a copy of all currently retained events, oldest first.
func (l *Log) Snapshot() []Event { return l.snapshotFrom(0) }

// SnapshotSince returns copies of retained events with Seq greater than cur,
// oldest first. Events already evicted by the ring cannot be recovered; a
// consumer that falls behind by more than the ring capacity observes the gap
// via Stats.Overflowed / LastOverflow.
func (l *Log) SnapshotSince(cur Cursor) []Event { return l.snapshotFrom(uint64(cur)) }

func (l *Log) snapshotFrom(after uint64) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := 0
	for start < len(l.events) && l.events[start].Seq <= after {
		start++
	}
	out := make([]Event, len(l.events)-start)
	copy(out, l.events[start:])
	return out
}

// Latest returns the cursor of the newest appended event (0 before any).
func (l *Log) Latest() Cursor {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) == 0 {
		return 0
	}
	return Cursor(l.events[len(l.events)-1].Seq)
}

// LastOverflow reports the Seq of the newest eviction marker (0 = none yet).
func (l *Log) LastOverflow() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastOverflow
}

// Stats returns the log's observable counters.
func (l *Log) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{
		Appended:        l.appended,
		Capacity:        l.opts.EffectiveCapacity(),
		Retained:        len(l.events),
		Overflowed:      l.overflowed,
		Truncated:       l.truncated,
		Sensitive:       l.sensitive,
		SubscriberDrops: l.subscriberDrops,
	}
}

// SanitizedSnapshot returns the retained events with ALL summaries, reasons
// and text payloads passed through the configured Redactor (binary payloads
// are masked). Intended for report/fixture/evidence writers; interactive UIs
// keep using Snapshot since the user owns the raw traffic they observe.
func (l *Log) SanitizedSnapshot() []Event { return l.sanitizeFrom(0) }

// SanitizedSince is the cursor form of SanitizedSnapshot.
func (l *Log) SanitizedSince(cur Cursor) []Event { return l.sanitizeFrom(uint64(cur)) }

func (l *Log) sanitizeFrom(after uint64) []Event {
	events := l.snapshotFrom(after)
	if l.opts.Redact == nil && l.opts.Redactor == nil {
		return events
	}
	for i := range events {
		events[i].Summary = l.redactString(events[i].Summary)
		events[i].Reason = l.redactString(events[i].Reason)
		if len(events[i].Payload) > 0 {
			if events[i].IsText {
				events[i].Payload = []byte(l.redactString(string(events[i].Payload)))
			} else {
				events[i].Payload = []byte(redactedPayloadMask)
				events[i].IsText = true
				events[i].Truncated = false
			}
		}
	}
	return events
}

// DeadlinePolicy declares the four deadlines every streaming adapter must
// honor, mirroring core.StageTimeouts' staged model for long-lived streams.
// Zero fields select the adapter's documented defaults; adapters must expose
// their concrete Effective* helpers so policy audit shows real values.
type DeadlinePolicy struct {
	// Connect bounds DNS + TCP + (proxy/ SOCKS) dial time.
	Connect time.Duration
	// Handshake bounds the protocol upgrade/negotiation after dial.
	Handshake time.Duration
	// Message bounds each single send/receive operation (write deadline /
	// await deadline). It is NOT an idle timeout: a silent but healthy
	// server must not be killed by Message.
	Message time.Duration
	// Total bounds the whole session including every pending await. When it
	// fires, the adapter must terminate the stream with a clear fact.
	Total time.Duration
}
