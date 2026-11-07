package websocket

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorequest/internal/core"
)

// resolvedWSProxy is the machine-local resolution of core.ProxyConfig for one
// dial. It mirrors core's resolvedProxy semantics: displayLabel never carries
// credentials, credentials register into the Redactor as basic pairs, and
// NoProxy rules apply in every proxied mode.
type resolvedWSProxy struct {
	mode       core.ProxyMode
	proxyURL   *url.URL
	username   string
	password   string
	noProxy    string
	displayURL string
}

// resolveProxy validates and resolves the proxy configuration BEFORE any
// network activity. mode=none/system/http/socks5 are all supported; explicit
// credentials resolve from ProxyConfig.Username/Password or PasswordSecret
// via core.SecretProvider. A missing configuration is a pre-network config
// error — never a silent fallback.
func resolveProxy(o *Options, red *core.Redactor) (*resolvedWSProxy, error) {
	cfg := o.Proxy
	mode := cfg.EffectiveMode()

	switch mode {
	case core.ProxyNone:
		return &resolvedWSProxy{mode: core.ProxyNone, displayURL: "direct"}, nil
	case core.ProxySystem:
		return &resolvedWSProxy{
			mode:       core.ProxySystem,
			noProxy:    strings.TrimSpace(cfg.NoProxy),
			displayURL: "system",
		}, nil
	case core.ProxyHTTP, core.ProxySOCKS5:
	default:
		return nil, configErrorf("unsupported proxy mode %q", string(cfg.Mode))
	}

	rawURL := strings.TrimSpace(cfg.URL)
	if rawURL == "" {
		return nil, configErrorf("proxy URL is required when proxy mode is %q", mode)
	}
	if !strings.Contains(rawURL, "://") {
		if mode == core.ProxySOCKS5 {
			rawURL = "socks5://" + rawURL
		} else {
			rawURL = "http://" + rawURL
		}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, configErrorf("invalid proxy URL %q: %v", cfg.URL, err)
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return nil, configErrorf("proxy URL %q is missing a host", cfg.URL)
	}
	switch mode {
	case core.ProxyHTTP:
		if scheme != "http" && scheme != "https" {
			return nil, configErrorf("HTTP proxy URL must use http:// or https:// scheme, got %q", parsed.Scheme)
		}
	case core.ProxySOCKS5:
		if scheme != "socks5" && scheme != "socks5h" {
			return nil, configErrorf("SOCKS5 proxy URL must use socks5:// or socks5h:// scheme, got %q", parsed.Scheme)
		}
	}
	if port := parsed.Port(); port != "" {
		if n, ok := parsePort(port); !ok || n < 1 || n > 65535 {
			return nil, configErrorf("proxy URL %q has invalid port %q", cfg.URL, port)
		}
	}

	user := strings.TrimSpace(cfg.Username)
	pass := cfg.Password
	if parsed.User != nil {
		if user == "" {
			user = parsed.User.Username()
		}
		if p, ok := parsed.User.Password(); ok && pass == "" {
			pass = p
		}
	}
	if pass == "" && strings.TrimSpace(cfg.PasswordSecret) != "" {
		secName := strings.TrimSpace(cfg.PasswordSecret)
		if o.Secrets == nil {
			return nil, configErrorf("proxy password secret %q requires a secret provider", secName)
		}
		val, ok := o.Secrets.Get(secName)
		if !ok {
			return nil, configErrorf("proxy password secret %q is not bound on this machine", secName)
		}
		pass = val
	}
	if red != nil && (user != "" || pass != "") {
		red.RegisterBasicPair(user, pass)
	}

	cleanURL := *parsed
	if user != "" || pass != "" {
		cleanURL.User = url.UserPassword(user, pass)
	} else {
		cleanURL.User = nil
	}
	display := *parsed
	display.User = nil
	display.RawQuery = ""
	display.Fragment = ""
	return &resolvedWSProxy{
		mode:       mode,
		proxyURL:   &cleanURL,
		username:   user,
		password:   pass,
		noProxy:    strings.TrimSpace(cfg.NoProxy),
		displayURL: display.String(),
	}, nil
}

// wsDialPlan is the resolved, pre-network dial strategy for one target:
//
//   - netDial is the NetDialContext gorilla uses (direct TCP or an HTTP/HTTPS
//     CONNECT tunnel).
//   - proxyFn is gorilla's Proxy callback, used only for SOCKS5 where gorilla's
//     bundled RFC 1928/1929 client dials the proxy itself.
//
// Exactly one of them is non-nil; both nil means direct.
type wsDialPlan struct {
	netDial func(ctx context.Context, network, addr string) (net.Conn, error)
	proxyFn func(*http.Request) (*url.URL, error)
}

// plan resolves the actual proxy for target BEFORE any network byte moves.
// gorilla's Dialer.Proxy supports SOCKS5 only, so HTTP/HTTPS proxies are
// honored through a locally implemented CONNECT tunnel instead of being
// silently ignored. An unsupported scheme is a pre-network config error.
func (rp *resolvedWSProxy) plan(target string, base *net.Dialer, onUse func(string)) (wsDialPlan, error) {
	if base == nil {
		base = &net.Dialer{Timeout: core.DefaultDialTimeout}
	}
	direct := wsDialPlan{netDial: base.DialContext}
	if rp == nil || rp.mode == core.ProxyNone {
		return direct, nil
	}
	policyURL, err := policyURLFor(strings.TrimSpace(target))
	if err != nil {
		return wsDialPlan{}, err
	}

	if rp.mode == core.ProxySystem {
		if core.ShouldBypassProxy(policyURL, rp.noProxy) {
			return direct, nil
		}
		u, err := http.ProxyFromEnvironment(&http.Request{URL: policyURL})
		if err != nil {
			return wsDialPlan{}, configErrorf("resolving the system proxy: %v", err)
		}
		if u == nil {
			return direct, nil
		}
		return planForProxyURL(u, "", "", base, onUse)
	}

	if core.ShouldBypassProxy(policyURL, rp.noProxy) {
		return direct, nil
	}
	if rp.proxyURL == nil {
		return wsDialPlan{}, configErrorf("proxy mode %q is missing its resolved URL", rp.mode)
	}
	return planForProxyURL(rp.proxyURL, rp.username, rp.password, base, onUse)
}

// planForProxyURL maps one proxy URL onto a dial strategy.
func planForProxyURL(u *url.URL, username, password string, base *net.Dialer, onUse func(string)) (wsDialPlan, error) {
	if u == nil {
		return wsDialPlan{}, configErrorf("proxy URL is nil")
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	label := displayProxyURL(u)
	switch scheme {
	case "socks5", "socks5h":
		// gorilla's bundled SOCKS5 client resolves the target host at the
		// proxy, matching socks5h semantics for both spellings.
		cp := *u
		cp.Scheme = "socks5"
		return wsDialPlan{
			proxyFn: func(*http.Request) (*url.URL, error) {
				if onUse != nil {
					onUse(label)
				}
				out := cp
				return &out, nil
			},
		}, nil
	case "http", "https":
		if username == "" && u.User != nil {
			username = u.User.Username()
			if p, ok := u.User.Password(); ok {
				password = p
			}
		}
		return wsDialPlan{
			netDial: httpConnectDialer(u, username, password, base, onUse, label),
		}, nil
	default:
		return wsDialPlan{}, configErrorf("unsupported proxy scheme %q; WebSocket sessions support http, https and socks5 proxies", u.Scheme)
	}
}

// displayProxyURL strips credentials and query material for facts.
func displayProxyURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	cp := *u
	cp.User = nil
	cp.RawQuery = ""
	cp.Fragment = ""
	return cp.String()
}

// httpConnectDialer returns a NetDialContext that opens an HTTP/HTTPS CONNECT
// tunnel to the target address. The proxy hop is dialed with the session's
// connect timeout; an https:// proxy is wrapped in TLS first. Proxy credentials
// are sent as Proxy-Authorization: Basic and never logged.
func httpConnectDialer(proxyURL *url.URL, username, password string, base *net.Dialer, onUse func(string), label string) func(context.Context, string, string) (net.Conn, error) {
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		port := "80"
		if strings.EqualFold(proxyURL.Scheme, "https") {
			port = "443"
		}
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), port)
	}
	useTLS := strings.EqualFold(proxyURL.Scheme, "https")
	serverName := proxyURL.Hostname()

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		switch network {
		case "tcp", "tcp4", "tcp6", "":
		default:
			return nil, fmt.Errorf("HTTP proxy does not support network %q", network)
		}
		if onUse != nil {
			onUse(label)
		}
		conn, err := base.DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("proxy dial failed (%s): %w", proxyAddr, err)
		}
		ok := false
		defer func() {
			if !ok {
				_ = conn.Close()
			}
		}()

		if useTLS {
			tlsConn := tls.Client(conn, &tls.Config{
				ServerName: serverName,
				MinVersion: tls.VersionTLS12,
			})
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				return nil, fmt.Errorf("TLS handshake with proxy %s failed: %w", proxyAddr, err)
			}
			conn = tlsConn
		}

		if deadline, has := ctx.Deadline(); has {
			_ = conn.SetDeadline(deadline)
		} else if base.Timeout > 0 {
			_ = conn.SetDeadline(time.Now().Add(base.Timeout))
		}

		// Abort the CONNECT exchange immediately if the caller cancels.
		handshakeDone := make(chan struct{})
		defer close(handshakeDone)
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-handshakeDone:
			}
		}()

		var req strings.Builder
		req.WriteString("CONNECT " + addr + " HTTP/1.1\r\n")
		req.WriteString("Host: " + addr + "\r\n")
		if username != "" || password != "" {
			basic := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
			req.WriteString("Proxy-Authorization: Basic " + basic + "\r\n")
		}
		req.WriteString("\r\n")
		if _, err := io.WriteString(conn, req.String()); err != nil {
			return nil, fmt.Errorf("writing CONNECT to proxy %s failed: %w", proxyAddr, err)
		}

		br := bufio.NewReader(conn)
		tp := textproto.NewReader(br)
		statusLine, err := tp.ReadLine()
		if err != nil {
			return nil, fmt.Errorf("reading CONNECT response from proxy %s failed: %w", proxyAddr, err)
		}
		if _, err := tp.ReadMIMEHeader(); err != nil {
			return nil, fmt.Errorf("reading CONNECT headers from proxy %s failed: %w", proxyAddr, err)
		}
		if code, perr := connectStatusCode(statusLine); perr != nil {
			return nil, fmt.Errorf("proxy %s returned an invalid CONNECT response %q", proxyAddr, statusLine)
		} else if code != http.StatusOK {
			return nil, fmt.Errorf("proxy %s refused CONNECT to %s: %s", proxyAddr, addr, statusLine)
		}

		_ = conn.SetDeadline(time.Time{})
		ok = true
		return &bufferedConn{Conn: conn, r: br}, nil
	}
}

// connectStatusCode parses "HTTP/1.1 200 Connection established".
func connectStatusCode(line string) (int, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, fmt.Errorf("missing status code")
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil || code < 100 || code > 599 {
		return 0, fmt.Errorf("invalid status code %q", fields[1])
	}
	return code, nil
}

// bufferedConn preserves bytes already buffered while reading the CONNECT
// response (a proxy may pipeline tunnel data immediately after the headers).
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func parsePort(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}
