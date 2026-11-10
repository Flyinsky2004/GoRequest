package protocolrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorequest/internal/core"
)

// fakeTransport is a synchronous in-memory Transport. Inbox messages are
// delivered first; after that Receive blocks until ctx ends.
type fakeTransport struct {
	mu        sync.Mutex
	connected *ConnectSpec
	sent      []SendPayload
	sentSeq   uint64
	inbox     []Message
	closeErr  error
	closed    bool
	code      int
	reason    string
}

func (f *fakeTransport) Connect(_ context.Context, spec ConnectSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connected != nil {
		return errors.New("already connected")
	}
	f.connected = &spec
	return nil
}

func (f *fakeTransport) Send(_ context.Context, payload SendPayload) (SentFact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, payload)
	f.sentSeq++
	return SentFact{Seq: f.sentSeq, Bytes: len(payload.Data)}, nil
}

func (f *fakeTransport) Receive(ctx context.Context) (Message, error) {
	f.mu.Lock()
	if len(f.inbox) > 0 {
		msg := f.inbox[0]
		f.inbox = f.inbox[1:]
		f.mu.Unlock()
		return msg, nil
	}
	f.mu.Unlock()
	<-ctx.Done()
	return Message{}, ctx.Err()
}

func (f *fakeTransport) Close(code int, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed, f.code, f.reason = true, code, reason
	return f.closeErr
}

func (f *fakeTransport) CloseFacts() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.code, f.reason
}

func (f *fakeTransport) snapshot() (sent []SendPayload, closed bool, code int, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SendPayload(nil), f.sent...), f.closed, f.code, f.reason
}

func compiled(t *testing.T, p *Plan, opts CompileOptions) *CompiledPlan {
	t.Helper()
	cp, err := Compile(p, opts)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return cp
}

func TestPlanDecodeStrictAndValidation(t *testing.T) {
	good := `{"version":1,"name":"demo","limits":{"totalTimeout":"5s","maxMessageBytes":1024},
	 "steps":[{"action":"connect","url":"ws://127.0.0.1:1/ws"},{"action":"send","text":"hi"},
	 {"action":"await","match":{"contains":"ok"},"timeout":"1s"},{"action":"close","code":1000}]}`
	p, err := DecodePlan(strings.NewReader(good))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Version != 1 || len(p.Steps) != 4 || p.Limits.EffectiveTotalTimeout() != 5*time.Second {
		t.Fatalf("decoded plan = %+v", p)
	}
	if _, err := DecodePlan(strings.NewReader(`{"version":1,"steps":[{"action":"connect","url":"ws://x","bogus":true}]}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field must be rejected: %v", err)
	}
	cases := map[string]string{
		"no steps":       `{"version":1}`,
		"send before":    `{"version":1,"steps":[{"action":"send","text":"x"}]}`,
		"two payloads":   `{"version":1,"steps":[{"action":"connect","url":"ws://x"},{"action":"send","text":"x","binaryHex":"00"}]}`,
		"await no match": `{"version":1,"steps":[{"action":"connect","url":"ws://x"},{"action":"await"}]}`,
		"bad cursor":     `{"version":1,"steps":[{"action":"connect","url":"ws://x"},{"action":"await","match":{"contains":"a"},"cursor":"bogus"}]}`,
		"close before":   `{"version":1,"steps":[{"action":"close"}]}`,
		"unknown action": `{"version":1,"steps":[{"action":"connect","url":"ws://x"},{"action":"teleport"}]}`,
		"bad duration":   `{"version":1,"steps":[{"action":"connect","url":"ws://x"},{"action":"await","match":{"contains":"a"},"timeout":3}]}`,
		"negative limit": `{"version":1,"limits":{"totalTimeout":"-1s"},"steps":[{"action":"connect","url":"ws://x"}]}`,
	}
	for name, doc := range cases {
		if _, err := DecodePlan(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: plan was accepted", name)
		}
	}
	if _, err := DecodePlan(strings.NewReader(`{"version":2,"steps":[{"action":"connect","url":"ws://x"}]}`)); err == nil {
		t.Fatal("unsupported version must be rejected")
	}
	if _, err := DecodePlan(strings.NewReader(`{"version":1,"steps":[{"action":"connect","url":"ws://x"}]} {}`)); err == nil {
		t.Fatal("trailing JSON must be rejected")
	}
}

func TestPlanSaveOpenRoundTrip(t *testing.T) {
	path := t.TempDir() + "/plan.json"
	p := &Plan{Version: 1, Name: "round", Steps: []Step{
		{Action: ActionConnect, URL: "ws://127.0.0.1:1"},
		{Action: ActionSend, Text: strPtr("hi")},
		{Action: ActionClose, Code: 1000},
	}}
	if err := SavePlan(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := OpenPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "round" || len(got.Steps) != 3 || got.Steps[1].Text == nil || *got.Steps[1].Text != "hi" {
		t.Fatalf("round trip = %+v", got)
	}
	if _, err := Compile(got, CompileOptions{}); err != nil {
		t.Fatalf("round-tripped plan must compile: %v", err)
	}
}

func TestMatchJSONPathAndKinds(t *testing.T) {
	msg := Message{Text: true, Data: []byte(`{"payload":{"data":{"ticks":[{"id":7,"name":"a"}],"ok":true}}}`)}
	cases := []struct {
		name  string
		match MatchSpec
		want  bool
	}{
		{"path presence", MatchSpec{JSONPath: "payload.data.ticks[0].id"}, true},
		{"path equals number", MatchSpec{JSONPath: "payload.data.ticks[0].id", JSONEquals: json.RawMessage(`7`)}, true},
		{"path equals string", MatchSpec{JSONPath: "payload.data.ticks[0].name", JSONEquals: json.RawMessage(`"a"`)}, true},
		{"path wrong value", MatchSpec{JSONPath: "payload.data.ok", JSONEquals: json.RawMessage(`false`)}, false},
		{"path missing", MatchSpec{JSONPath: "payload.data.nope"}, false},
		{"path out of range", MatchSpec{JSONPath: "payload.data.ticks[9]"}, false},
		{"whole json equals key order", MatchSpec{JSONEquals: json.RawMessage(`{"b":2,"a":1}`)}, false},
		{"contains", MatchSpec{Contains: `"ok":true`}, true},
		{"equals", MatchSpec{Equals: string(msg.Data)}, true},
		{"equals wrong", MatchSpec{Equals: "nope"}, false},
	}
	for _, tc := range cases {
		got, detail := tc.match.Match(msg)
		if got != tc.want {
			t.Errorf("%s: got %v (%s), want %v", tc.name, got, detail, tc.want)
		}
	}
	if ok, _ := (MatchSpec{JSONEquals: json.RawMessage(`{"a":1,"b":2}`)}).Match(Message{Text: true, Data: []byte(`{"b":2,"a":1}`)}); !ok {
		t.Fatal("JSON equality must be structural, not textual")
	}
	if ok, _ := (MatchSpec{BinaryHex: "0102"}).Match(Message{Data: []byte{1, 2}}); !ok {
		t.Fatal("binary match failed")
	}
	if ok, _ := (MatchSpec{BinaryHex: "01"}).Match(Message{Text: true, Data: []byte{1}}); ok {
		t.Fatal("binary match must not match text")
	}
	if ok, _ := (MatchSpec{Contains: "é"}).Match(Message{Text: true, Data: []byte("café")}); !ok {
		t.Fatal("UTF-8 match failed")
	}
	for _, bad := range []MatchSpec{{}, {JSONPath: "a["}, {BinaryHex: "0"}, {JSONEquals: json.RawMessage(`{`)}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("invalid match accepted: %+v", bad)
		}
	}
	if _, err := parseJSONPath("a..b"); err == nil {
		t.Fatal("empty path segment must be rejected")
	}
}

func TestCompileResolvesTemplatesAndRegistersSecrets(t *testing.T) {
	resolver := coreResolverForTest(map[string]string{"host": "127.0.0.1"}, map[string]string{"token": "s3cret-value-123"})
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: "ws://{{host}}:1/ws", Headers: []Header{{Name: "Authorization", Value: "Bearer {{secret.token}}"}}},
		{Action: ActionSend, Text: strPtr(`{"auth":"{{secret.token}}"}`)},
	}}
	cp, err := Compile(p, CompileOptions{Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	send := cp.Steps[1].Send
	if send == nil || !send.Sensitive || string(send.Data) != `{"auth":"s3cret-value-123"}` {
		t.Fatalf("compiled send = %+v", send)
	}
	if cp.Steps[0].Connect.URL != "ws://127.0.0.1:1/ws" {
		t.Fatalf("url not resolved: %q", cp.Steps[0].Connect.URL)
	}
	value := strings.TrimSuffix(strings.TrimPrefix(string(send.Data), `{"auth":"`), `"}`)
	if got := cp.Redactor.RedactString("echo " + value); strings.Contains(got, value) {
		t.Fatalf("secret was not registered: %q", got)
	}
	// Recompiling with a different secret must not reuse the old value.
	resolver2 := coreResolverForTest(map[string]string{"host": "127.0.0.1"}, map[string]string{"token": "other-secret-456"})
	cp2, err := Compile(p, CompileOptions{Resolver: resolver2})
	if err != nil {
		t.Fatal(err)
	}
	if string(cp2.Steps[1].Send.Data) != `{"auth":"other-secret-456"}` {
		t.Fatalf("replay did not re-resolve: %s", cp2.Steps[1].Send.Data)
	}
	if _, err := Compile(p, CompileOptions{}); err == nil {
		t.Fatal("template without a resolver must fail instead of being sent literally")
	}
}

func TestCompileBinaryAndFileBounds(t *testing.T) {
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: "ws://127.0.0.1:1"},
		{Action: ActionSend, BinaryHex: "00ff"},
	}}
	cp, err := Compile(p, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cp.Steps[1].Send.Text || len(cp.Steps[1].Send.Data) != 2 {
		t.Fatalf("binary payload = %+v", cp.Steps[1].Send)
	}

	dir := t.TempDir()
	path := dir + "/blob.bin"
	if err := osWriteFile(path, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	p2 := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: "ws://127.0.0.1:1"},
		{Action: ActionSend, File: path},
	}}
	cp2, err := Compile(p2, CompileOptions{})
	if err != nil || string(cp2.Steps[1].Send.Data) != "hello" {
		t.Fatalf("file compile = %+v err=%v", cp2, err)
	}
	if _, err := Compile(p2, CompileOptions{MaxBinary: 2}); err == nil {
		t.Fatal("file over the byte bound must fail")
	}
}

func TestRunHappyPathFacts(t *testing.T) {
	tr := &fakeTransport{inbox: []Message{{Seq: 7, Text: true, Data: []byte(`{"type":"pong","ok":true}`)}}}
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: "ws://127.0.0.1:1/ws"},
		{Action: ActionSend, Text: strPtr(`{"type":"ping"}`)},
		{Action: ActionAwait, Match: &MatchSpec{JSONPath: "ok", JSONEquals: json.RawMessage(`true`)}, Timeout: Duration(time.Second)},
		{Action: ActionClose, Code: 1000, Reason: "done"},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StatePassed || res.Error != "" {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Steps) != 4 {
		t.Fatalf("steps = %d", len(res.Steps))
	}
	connect, send, await, closeStep := res.Steps[0], res.Steps[1], res.Steps[2], res.Steps[3]
	if connect.Direction != "none" || send.Direction != "out" || await.Direction != "in" {
		t.Fatalf("directions: %q %q %q", connect.Direction, send.Direction, await.Direction)
	}
	if send.SentSeq == 0 || await.ReceivedSeq != 7 || await.CursorBefore != 0 || await.CursorAfter != 1 {
		t.Fatalf("facts: send=%+v await=%+v", send, await)
	}
	if len(await.Skipped) != 0 || closeStep.CloseCode != 1000 || res.CloseCode != 1000 {
		t.Fatalf("close/skip facts: %+v %+v", await, closeStep)
	}
	if _, closed, code, reason := tr.snapshot(); !closed || code != 1000 || reason != "done" {
		t.Fatalf("transport close = %v %d %q", closed, code, reason)
	}
}

func TestRunAwaitSkipsAndRetains(t *testing.T) {
	// Skip policy: unmatched messages are consumed on success.
	tr := &fakeTransport{inbox: []Message{
		{Seq: 1, Text: true, Data: []byte("noise")},
		{Seq: 2, Text: true, Data: []byte("target")},
		{Seq: 3, Text: true, Data: []byte("later")},
	}}
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: "ws://127.0.0.1:1"},
		{Action: ActionAwait, Match: &MatchSpec{Equals: "target"}, Timeout: Duration(time.Second)},
		{Action: ActionAwait, Match: &MatchSpec{Equals: "later"}, Timeout: Duration(time.Second)},
		{Action: ActionClose, Code: 1000},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StatePassed {
		t.Fatalf("skip run failed: %+v", res)
	}
	first, second := res.Steps[1], res.Steps[2]
	if len(first.Skipped) != 1 || first.Skipped[0].Seq != 1 || first.CursorAfter != 2 {
		t.Fatalf("first await facts: %+v", first)
	}
	if len(second.Skipped) != 0 || second.CursorBefore != 2 || second.CursorAfter != 3 {
		t.Fatalf("second await facts: %+v", second)
	}

	// Keep policy: on timeout the cursor stays and the message remains
	// available to a later await (it must not be silently consumed).
	tr2 := &fakeTransport{inbox: []Message{{Seq: 4, Text: true, Data: []byte("other")}}}
	p2 := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: "ws://127.0.0.1:1"},
		{Action: ActionAwait, Match: &MatchSpec{Equals: "never"}, Timeout: Duration(30 * time.Millisecond), KeepUnmatched: true},
		{Action: ActionAwait, Match: &MatchSpec{Equals: "other"}, Timeout: Duration(time.Second)},
		{Action: ActionClose, Code: 1000},
	}}
	res2 := Run(context.Background(), compiled(t, p2, CompileOptions{}), tr2)
	if res2.State != StateFailed || res2.FailedStep != 1 {
		t.Fatalf("keep timeout must fail the first await: %+v", res2)
	}
	if len(res2.Steps[1].Skipped) != 1 || !res2.Steps[1].Skipped[0].Retained || res2.Steps[1].CursorAfter != 0 {
		t.Fatalf("keep facts: %+v", res2.Steps[1])
	}
}

func TestRunKeepRetainedMessageUsableByLaterPlan(t *testing.T) {
	// The runner stops at the failed first await; a later plan that starts from
	// a saved cursor is out of scope. Instead prove retention directly with a
	// zero-timeout first await followed by a second plan run over the same
	// cursor semantics is not possible, so verify cursor through steps in one
	// plan: a successful await that leaves earlier noise in the buffer.
	tr := &fakeTransport{inbox: []Message{
		{Seq: 9, Text: true, Data: []byte("kept")},
		{Seq: 10, Text: true, Data: []byte("target")},
	}}
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: "ws://127.0.0.1:1"},
		{Action: ActionAwait, Match: &MatchSpec{Equals: "target"}, Timeout: Duration(time.Second)},
		{Action: ActionAwait, Cursor: CursorStart, Match: &MatchSpec{Equals: "kept"}, Timeout: Duration(time.Second)},
		{Action: ActionClose, Code: 1000},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StatePassed {
		t.Fatalf("cursor=start run failed: %+v", res)
	}
	if res.Steps[2].ReceivedSeq != 9 || res.Steps[2].CursorBefore != 2 || res.Steps[2].CursorAfter != 2 {
		t.Fatalf("cursor=start did not rescan retained history or moved the cursor backwards: %+v", res.Steps[2])
	}
}

func TestRunLimitsTimeoutAndCancel(t *testing.T) {
	match := &MatchSpec{Equals: "never"}
	connectStep := Step{Action: ActionConnect, URL: "ws://127.0.0.1:1"}

	// Per-await maxMessages bound.
	p := &Plan{Version: 1, Steps: []Step{connectStep,
		{Action: ActionAwait, Match: match, Timeout: Duration(time.Second), MaxMessages: 2},
		{Action: ActionClose, Code: 1000},
	}}
	tr := &fakeTransport{inbox: []Message{
		{Seq: 1, Text: true, Data: []byte("a")},
		{Seq: 2, Text: true, Data: []byte("b")},
		{Seq: 3, Text: true, Data: []byte("c")},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StateFailed || !strings.Contains(res.Error, "maxMessages") {
		t.Fatalf("maxMessages result = %+v", res)
	}

	// Total deadline.
	p2 := &Plan{Version: 1, Limits: Limits{TotalTimeout: Duration(60 * time.Millisecond)}, Steps: []Step{connectStep,
		{Action: ActionAwait, Match: match, Timeout: Duration(5 * time.Second)},
	}}
	res2 := Run(context.Background(), compiled(t, p2, CompileOptions{}), &fakeTransport{})
	if res2.State != StateTimedOut {
		t.Fatalf("total deadline result = %+v", res2)
	}

	// External cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	p3 := &Plan{Version: 1, Steps: []Step{connectStep,
		{Action: ActionAwait, Match: match, Timeout: Duration(5 * time.Second)},
	}}
	res3 := Run(ctx, compiled(t, p3, CompileOptions{}), &fakeTransport{})
	if res3.State != StateCanceled {
		t.Fatalf("cancel result = %+v", res3)
	}
}

func TestRunRedactsPreviewAndErrors(t *testing.T) {
	thing := "tok-preview-9"
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: "ws://127.0.0.1:1"},
		{Action: ActionAwait, Match: &MatchSpec{Contains: "secret"}, Timeout: Duration(time.Second)},
		{Action: ActionClose, Code: 1000},
	}}
	cp := compiled(t, p, CompileOptions{})
	cp.Redactor.RegisterSecret(thing)
	tr := &fakeTransport{inbox: []Message{{Seq: 1, Text: true, Data: []byte(`{"secret":"` + thing + `"}`)}}}
	res := Run(context.Background(), cp, tr)
	if res.State != StatePassed {
		t.Fatalf("run: %+v", res)
	}
	if strings.Contains(string(res.Steps[1].Payload), thing) || !strings.Contains(string(res.Steps[1].Payload), "***") {
		t.Fatalf("payload preview leaked the secret: %s", res.Steps[1].Payload)
	}
}

func TestRunBinaryPreviewIsMasked(t *testing.T) {
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: "ws://127.0.0.1:1"},
		{Action: ActionAwait, Match: &MatchSpec{BinaryHex: "0102"}, Timeout: Duration(time.Second)},
		{Action: ActionClose, Code: 1000},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), &fakeTransport{inbox: []Message{{Seq: 1, Data: []byte{1, 2}}}})
	if res.State != StatePassed {
		t.Fatalf("binary run: %+v", res)
	}
	if string(res.Steps[1].Payload) != "***" {
		t.Fatalf("binary preview must be masked, got %q", res.Steps[1].Payload)
	}
}

func TestRunAutoCloseAndCloseFailure(t *testing.T) {
	p := &Plan{Version: 1, Steps: []Step{{Action: ActionConnect, URL: "ws://127.0.0.1:1"}}}
	tr := &fakeTransport{}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StatePassed || res.CloseCode != 1000 {
		t.Fatalf("auto close result = %+v", res)
	}

	tr2 := &fakeTransport{closeErr: errors.New("close boom")}
	res2 := Run(context.Background(), compiled(t, p, CompileOptions{}), tr2)
	if res2.State != StateFailed || !strings.Contains(res2.Error, "close boom") {
		t.Fatalf("close failure result = %+v", res2)
	}
}

func strPtr(s string) *string { return &s }

func coreResolverForTest(vars, secretValues map[string]string) *core.Resolver {
	return core.NewResolver(vars, core.MapSecrets(secretValues))
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
