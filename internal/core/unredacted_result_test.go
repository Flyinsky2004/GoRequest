package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type captureErrorTransport struct{}

func (captureErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport failed: capture-test-secret")
}

func TestUnredactedResultPreservesErrorsOnlyWhenEnabled(t *testing.T) {
	for _, full := range []bool{false, true} {
		red := NewRedactor()
		red.RegisterSecret("capture-test-secret")
		_, err := ExecuteWithOptions(context.Background(), &Plan{Method: "GET", URL: "http://127.0.0.1/fake"}, ExecuteOptions{
			Client: &http.Client{Transport: captureErrorTransport{}}, Redactor: red, UnredactedResult: full,
		})
		if err == nil || strings.Contains(err.Error(), "capture-test-secret") != full {
			t.Fatalf("full=%v: unexpected execution error %v", full, err)
		}
	}
}

func TestUnredactedStreamKeepsCrossOriginCredentialFiltering(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprint(full), func(t *testing.T) {
			leaked := make(chan string, 1)
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				leaked <- r.Header.Get("X-Capture-Private")
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: capture-test-secret\n\n")
			}))
			defer destination.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL, http.StatusFound)
			}))
			defer source.Close()
			red := NewRedactor()
			red.RegisterSecret("capture-test-secret")
			red.RegisterSensitiveKey("header", "X-Capture-Private")
			var event SSEEvent
			result, err := ExecuteWithOptions(context.Background(), &Plan{
				Method: "GET", URL: source.URL, Headers: [][2]string{{"X-Capture-Private", "capture-test-secret"}},
			}, ExecuteOptions{Redactor: red, UnredactedResult: full, Stream: &StreamHandler{OnEvent: func(ev SSEEvent) { event = ev }}})
			if err != nil {
				t.Fatal(err)
			}
			if <-leaked != "" {
				t.Fatal("local capture changed cross-origin credential filtering")
			}
			if strings.Contains(string(result.Body), "capture-test-secret") != full || strings.Contains(event.Data, "capture-test-secret") != full {
				t.Fatalf("full=%v: body=%q event=%q", full, result.Body, event.Data)
			}
		})
	}
}
