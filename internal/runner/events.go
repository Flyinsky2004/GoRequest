package runner

import (
	"sync"
	"time"
)

// EventKind identifies one observable Runner lifecycle step. The set is kept
// small and stable so the desktop runner panel (A02) can subscribe without
// depending on internal engine structure.
type EventKind string

const (
	EventRunStarted      EventKind = "run_started"
	EventRowStarted      EventKind = "row_started"
	EventStepStarted     EventKind = "step_started"
	EventAttemptStarted  EventKind = "attempt_started"
	EventAttemptFinished EventKind = "attempt_finished"
	EventExtraction      EventKind = "extraction_published"
	EventStepFinished    EventKind = "step_finished"
	EventRowFinished     EventKind = "row_finished"
	EventRunFinished     EventKind = "run_finished"
)

// Event is a flat, JSON-taggable progress record. Message fields are redacted
// before emission. Sensitive extraction values are never included.
type Event struct {
	Seq        int64     `json:"seq"`
	Time       time.Time `json:"time"`
	RunID      string    `json:"runId"`
	Row        int       `json:"row,omitempty"` // 1-based; 0 for run-level
	StepID     string    `json:"stepId,omitempty"`
	Attempt    int       `json:"attempt,omitempty"`
	Kind       EventKind `json:"kind"`
	Status     Status    `json:"status,omitempty"`
	HTTPStatus int       `json:"httpStatus,omitempty"`
	DurationMs int64     `json:"durationMs,omitempty"`
	Extraction string    `json:"extraction,omitempty"`
	Sensitive  bool      `json:"sensitive,omitempty"`
	Message    string    `json:"message,omitempty"`
}

// Emitter receives events synchronously. Calls from runner worker goroutines
// are serialized by the engine, so an Emitter does not need its own locking.
type Emitter func(Event)

type eventBus struct {
	mu     sync.Mutex
	seq    int64
	runID  string
	clock  func() time.Time
	emit   Emitter
	redact func(string) string
}

func newEventBus(runID string, clock func() time.Time, emit Emitter) *eventBus {
	if clock == nil {
		clock = time.Now
	}
	return &eventBus{runID: runID, clock: clock, emit: emit}
}

// SetRedactor attaches a final masking pass to all emitted event messages.
func (b *eventBus) SetRedactor(redact func(string) string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.redact = redact
}

func (b *eventBus) send(e Event) {
	if b == nil || b.emit == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	e.Seq = b.seq
	e.RunID = b.runID
	if e.Time.IsZero() {
		e.Time = b.clock()
	}
	if b.redact != nil {
		e.Message = b.redact(e.Message)
	}
	b.emit(e)
}
