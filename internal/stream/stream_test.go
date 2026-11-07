package stream

import (
	"strings"
	"testing"

	"gorequest/internal/core"
)

// These tests are written but NOT run in the P01 phase-1 delivery (the task
// explicitly defers go test). They are the first execution step for the
// integrator.

func TestAppendAssignsSequenceAndBounds(t *testing.T) {
	log := NewLog(Options{StreamID: "s1", Capacity: 3, MaxPayloadBytes: 4})
	for i := 0; i < 5; i++ {
		log.Append(Event{Kind: KindInfo, Direction: DirNone, Summary: "e"})
	}
	stats := log.Stats()
	if stats.Appended != 5 || stats.Retained != 3 || stats.Overflowed != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stats.Capacity != 3 {
		t.Fatalf("capacity = %d, want 3", stats.Capacity)
	}
	events := log.Snapshot()
	if len(events) != 3 {
		t.Fatalf("retained = %d, want 3", len(events))
	}
	if events[0].Seq != 2 || events[2].Seq != 4 {
		t.Fatalf("sequence gap: %d..%d", events[0].Seq, events[2].Seq)
	}
	if events[0].StreamID != "s1" {
		t.Fatalf("stream id = %q", events[0].StreamID)
	}
}

func TestPayloadTruncationKeepsOriginalLength(t *testing.T) {
	log := NewLog(Options{Capacity: 4, MaxPayloadBytes: 4})
	ev := log.Append(Event{Kind: KindSend, Payload: []byte("abcdefgh"), IsText: true})
	if !ev.Truncated || string(ev.Payload) != "abcd" || ev.PayloadLen != 8 {
		t.Fatalf("bound mismatch: %+v", ev)
	}
	if log.Stats().Truncated != 1 {
		t.Fatalf("truncated counter = %d", log.Stats().Truncated)
	}
}

func TestSensitivePayloadIsRedactedAtAppend(t *testing.T) {
	red := core.NewRedactor()
	red.RegisterSecret("hunter2")
	log := NewLog(Options{Capacity: 4, Redactor: red})
	ev := log.Append(Event{Kind: KindSend, Payload: []byte("token=hunter2"), IsText: true, Sensitive: true})
	if strings.Contains(string(ev.Payload), "hunter2") {
		t.Fatalf("secret leaked: %q", ev.Payload)
	}
	if ev.PayloadLen != len("token=hunter2") {
		t.Fatalf("payload len = %d", ev.PayloadLen)
	}
	if log.Stats().Sensitive != 1 {
		t.Fatalf("sensitive counter = %d", log.Stats().Sensitive)
	}
}

func TestCursorAndSubscriberDropsAreObservable(t *testing.T) {
	log := NewLog(Options{Capacity: 10})
	first := log.Append(Event{Kind: KindInfo, Summary: "a"})
	log.Append(Event{Kind: KindInfo, Summary: "b"})

	since := log.SnapshotSince(Cursor(first.Seq))
	if len(since) != 1 || since[0].Summary != "b" {
		t.Fatalf("SnapshotSince = %+v", since)
	}
	if log.Latest() != Cursor(1) {
		t.Fatalf("latest = %d", log.Latest())
	}

	ch := make(chan Event) // unbuffered: every delivery drops
	stop := log.Subscribe(ch)
	defer stop()
	log.Append(Event{Kind: KindInfo, Summary: "c"})
	if log.Stats().SubscriberDrops != 1 {
		t.Fatalf("subscriber drops = %d", log.Stats().SubscriberDrops)
	}
}

func TestRedactFuncIsPreferredOverRedactor(t *testing.T) {
	red := core.NewRedactor()
	red.RegisterSecret("plain")
	log := NewLog(Options{
		Capacity: 2,
		Redactor: red,
		Redact: func(s string) string {
			return strings.ReplaceAll(s, "plain", "serialized")
		},
	})
	ev := log.Append(Event{Kind: KindSend, Payload: []byte("plain"), IsText: true, Sensitive: true})
	if string(ev.Payload) != "serialized" {
		t.Fatalf("redact func not used: %q", ev.Payload)
	}
}
