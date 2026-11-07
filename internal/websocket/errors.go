package websocket

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"

	"gorequest/internal/core"
)

// configErrorf builds the same *core.ConfigError the rest of GoRequest uses
// for pre-network failures, so core.IsConfig and core.Redactor.RedactError
// keep working unchanged.
func configErrorf(format string, args ...any) error {
	return &core.ConfigError{Err: fmt.Errorf(format, args...)}
}

// runErrorf marks runtime (mid-session) failures as *core.RunError.
func runErrorf(format string, args ...any) error {
	return &core.RunError{Err: fmt.Errorf(format, args...)}
}

// ErrConfirmRequired marks a pre-network refusal that waits for the user's
// explicit confirmation of the exact target (Interactive=true,
// Confirmed=false). It chains through *core.ConfigError, so consumers can
// errors.Is(err, ErrConfirmRequired) to render a confirm prompt.
var ErrConfirmRequired = errors.New("interactive confirmation required for this target")

// HandshakeError preserves the complete non-101 handshake response: status,
// headers and body digest, so the UI can show the exact failure facts
// instead of a generic "bad handshake".
type HandshakeError struct {
	Status  int
	Headers [][2]string
	Body    string // bounded preview (see handshakeBodyLimit)
}

// handshakeBodyLimit bounds the retained bad-handshake body preview.
const handshakeBodyLimit = 4 * 1024

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("websocket handshake failed with HTTP %d", e.Status)
}

// parseWSURL validates the endpoint shape before anything else: only ws/wss
// schemes, a host, and no userinfo credentials in the URL.
func parseWSURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, configErrorf("invalid websocket url %q: %v", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "ws" && scheme != "wss" {
		return nil, configErrorf("websocket url %q must use ws:// or wss:// scheme", raw)
	}
	if strings.TrimSpace(u.Host) == "" {
		return nil, configErrorf("websocket url %q is missing a host", raw)
	}
	if u.User != nil {
		return nil, configErrorf("websocket urls must not carry user credentials; use headers")
	}
	return u, nil
}

// classifyDialError maps gorilla's dial error to a close-to-fact error,
// preserving ErrBadHandshake as *HandshakeError with the response.
func classifyDialError(err error, resp *http.Response) *HandshakeError {
	if err == nil {
		return nil
	}
	if errors.Is(err, websocket.ErrBadHandshake) {
		hs := &HandshakeError{Status: 0}
		if resp != nil {
			hs.Status = resp.StatusCode
			for name, vals := range resp.Header {
				for _, v := range vals {
					hs.Headers = append(hs.Headers, [2]string{name, v})
				}
			}
			if resp.Body != nil {
				buf := make([]byte, handshakeBodyLimit)
				n, _ := resp.Body.Read(buf)
				hs.Body = strings.TrimRight(string(buf[:n]), "\x00")
				_ = resp.Body.Close()
			}
		}
		return hs
	}
	return nil
}
