package core

import (
	"context"
	"strings"
	"testing"
)

func TestNewWebSocketRequestPersistsProtocolAndOptions(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "WS")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := p.NewWebSocketRequest("", "Socket")
	if err != nil {
		t.Fatal(err)
	}
	entry.Request.WS = &WSDef{Subprotocols: []string{"graphql-transport-ws"}, Compression: true}
	entry.Request.HTTP.URL = "wss://example.test/socket"
	entry.Request.HTTP.Headers = []KV{{Name: "Authorization", Value: "Bearer {{secret.token}}"}}
	if _, err := p.SaveRequestPreserving(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := ErrorCount(reloaded.Issues); n != 0 {
		t.Fatalf("validation errors: %v", reloaded.Issues)
	}
	re := reloaded.Requests[entry.Request.ID]
	if re == nil || re.Request.Protocol != ProtocolWebSocket {
		t.Fatalf("protocol not preserved: %+v", re)
	}
	if re.Request.HTTP == nil || re.Request.HTTP.Method != WebSocketMethod {
		t.Fatalf("websocket method tag not preserved: %+v", re.Request.HTTP)
	}
	if re.Request.WS == nil || len(re.Request.WS.Subprotocols) != 1 || !re.Request.WS.Compression {
		t.Fatalf("ws options not preserved: %+v", re.Request.WS)
	}
	if got := re.Request.HTTP.Headers[0].Value; got != "Bearer {{secret.token}}" {
		t.Fatalf("header template not preserved: %q", got)
	}
}

func TestValidateWebSocketRequest(t *testing.T) {
	p, err := NewProject(t.TempDir(), "WS")
	if err != nil {
		t.Fatal(err)
	}
	base := func() *Request {
		return &Request{
			SchemaVersion: SupportedSchemaVersion,
			ID:            "ws",
			Name:          "WS",
			Protocol:      ProtocolWebSocket,
			HTTP:          &HTTPDef{Method: WebSocketMethod, URL: "wss://example.test/socket"},
			WS:            &WSDef{},
		}
	}

	if issues := validateRequest(base(), "ws.request.yaml", p); ErrorCount(issues) != 0 {
		t.Fatalf("valid websocket request rejected: %v", issues)
	}

	badMethod := base()
	badMethod.HTTP.Method = "GET"
	if issues := validateRequest(badMethod, "ws.request.yaml", p); ErrorCount(issues) == 0 {
		t.Fatal("websocket request with an HTTP method must be rejected")
	}

	badSub := base()
	badSub.WS = &WSDef{Subprotocols: []string{" "}}
	if issues := validateRequest(badSub, "ws.request.yaml", p); ErrorCount(issues) == 0 {
		t.Fatal("empty websocket subprotocol must be rejected")
	}

	unknown := base()
	unknown.Protocol = "graphql"
	if issues := validateRequest(unknown, "ws.request.yaml", p); ErrorCount(issues) == 0 {
		t.Fatal("unknown protocol must still be rejected")
	}
}

func TestBuildPlanRejectsWebSocketRequest(t *testing.T) {
	req := &Request{
		SchemaVersion: SupportedSchemaVersion,
		ID:            "ws",
		Name:          "WS",
		Protocol:      ProtocolWebSocket,
		HTTP:          &HTTPDef{Method: WebSocketMethod, URL: "wss://example.test/socket"},
	}
	_, err := BuildPlanWithOptions(context.Background(), req, nil, nil, nil, PlanOptions{})
	if err == nil || !strings.Contains(err.Error(), "WebSocket") {
		t.Fatalf("expected a websocket HTTP-execution refusal, got %v", err)
	}
}
