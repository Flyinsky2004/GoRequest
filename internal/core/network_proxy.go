package core

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ProxyMode specifies how outbound HTTP/HTTPS connections are routed.
type ProxyMode string

const (
	// ProxySystem uses standard environment variables (HTTP_PROXY, HTTPS_PROXY, NO_PROXY).
	ProxySystem ProxyMode = "system"
	// ProxyNone connects directly to the target host, ignoring environment proxy variables.
	ProxyNone ProxyMode = "none"
	// ProxyHTTP routes through an explicit HTTP or HTTPS forward proxy.
	ProxyHTTP ProxyMode = "http"
	// ProxySOCKS5 routes through an explicit SOCKS5 (or SOCKS5H) proxy.
	ProxySOCKS5 ProxyMode = "socks5"
)

// ProxyConfig holds machine-local or per-request proxy settings. Proxy
// credentials live in machine-local storage / OS Keychain and must never be
// written to shared .gorequest/ repository files.
type ProxyConfig struct {
	Mode           ProxyMode `yaml:"mode,omitempty" json:"mode,omitempty"`
	URL            string    `yaml:"url,omitempty" json:"url,omitempty"`
	NoProxy        string    `yaml:"noProxy,omitempty" json:"noProxy,omitempty"`
	Username       string    `yaml:"-" json:"username,omitempty"`
	Password       string    `yaml:"-" json:"-"`
	PasswordSecret string    `yaml:"passwordSecret,omitempty" json:"passwordSecret,omitempty"`
}

// EffectiveMode normalizes ProxyConfig.Mode. When Mode is empty but URL is
// set, the mode is inferred from the URL scheme ("socks5"/"socks5h" ->
// ProxySOCKS5, otherwise ProxyHTTP). When both are empty, ProxySystem is used.
func (p ProxyConfig) EffectiveMode() ProxyMode {
	switch strings.ToLower(strings.TrimSpace(string(p.Mode))) {
	case string(ProxyNone), "direct", "off", "false":
		return ProxyNone
	case string(ProxyHTTP), "https":
		return ProxyHTTP
	case string(ProxySOCKS5), "socks5h", "socks":
		return ProxySOCKS5
	case string(ProxySystem), "env":
		return ProxySystem
	case "":
		rawURL := strings.TrimSpace(p.URL)
		if rawURL == "" {
			return ProxySystem
		}
		if u, err := url.Parse(rawURL); err == nil {
			switch strings.ToLower(u.Scheme) {
			case "socks5", "socks5h":
				return ProxySOCKS5
			case "http", "https":
				return ProxyHTTP
			}
		}
		return ProxyHTTP
	default:
		return ProxyMode(strings.ToLower(strings.TrimSpace(string(p.Mode))))
	}
}

type resolvedProxy struct {
	mode        ProxyMode
	proxyURL    *url.URL
	username    string
	password    string
	noProxy     string
	displayLabel string
}

func resolveProxyConfig(cfg ProxyConfig, secrets SecretProvider, red *Redactor) (*resolvedProxy, error) {
	mode := cfg.EffectiveMode()
	switch mode {
	case ProxyNone:
		return &resolvedProxy{mode: ProxyNone, displayLabel: "direct"}, nil
	case ProxySystem:
		return &resolvedProxy{
			mode:         ProxySystem,
			noProxy:      strings.TrimSpace(cfg.NoProxy),
			displayLabel: "system",
		}, nil
	case ProxyHTTP, ProxySOCKS5:
	default:
		return nil, configErrorf("unsupported proxy mode %q", cfg.Mode)
	}

	rawURL := strings.TrimSpace(cfg.URL)
	if rawURL == "" {
		return nil, configErrorf("proxy URL is required when proxy mode is %q", mode)
	}
	if !strings.Contains(rawURL, "://") {
		if mode == ProxySOCKS5 {
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
	if port := parsed.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return nil, configErrorf("proxy URL %q has invalid port %q", cfg.URL, port)
		}
	}

	switch mode {
	case ProxyHTTP:
		if scheme != "http" && scheme != "https" {
			return nil, configErrorf("HTTP proxy URL must use http:// or https:// scheme, got %q", parsed.Scheme)
		}
	case ProxySOCKS5:
		if scheme != "socks5" && scheme != "socks5h" {
			return nil, configErrorf("SOCKS5 proxy URL must use socks5:// or socks5h:// scheme, got %q", parsed.Scheme)
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
		if secrets == nil {
			return nil, configErrorf("proxy password secret %q requires a secret provider", secName)
		}
		val, ok := secrets.Get(secName)
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

	displayURL := *parsed
	displayURL.User = nil
	displayURL.RawQuery = ""
	displayURL.Fragment = ""

	return &resolvedProxy{
		mode:         mode,
		proxyURL:     &cleanURL,
		username:     user,
		password:     pass,
		noProxy:      strings.TrimSpace(cfg.NoProxy),
		displayLabel: displayURL.String(),
	}, nil
}

// ShouldBypassProxy reports whether a request to targetURL should bypass the
// configured proxy according to the comma-separated noProxy list.
func ShouldBypassProxy(targetURL *url.URL, noProxy string) bool {
	if targetURL == nil || strings.TrimSpace(noProxy) == "" {
		return false
	}
	host := normalizeHost(targetURL.Hostname())
	if host == "" {
		return false
	}
	port := targetURL.Port()
	if port == "" {
		if strings.EqualFold(targetURL.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	ip := net.ParseIP(host)

	for _, rawRule := range strings.Split(noProxy, ",") {
		rule := strings.ToLower(strings.TrimSpace(rawRule))
		if rule == "" {
			continue
		}
		if rule == "*" {
			return true
		}
		if ip != nil && strings.Contains(rule, "/") {
			if _, cidr, err := net.ParseCIDR(rule); err == nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		ruleHost := rule
		rulePort := ""
		if h, p, err := net.SplitHostPort(rule); err == nil {
			ruleHost = normalizeHost(h)
			rulePort = p
		} else {
			ruleHost = normalizeHost(rule)
		}
		if rulePort != "" && rulePort != port {
			continue
		}
		if ruleHost == host {
			return true
		}
		trimmed := strings.TrimPrefix(ruleHost, "*")
		if strings.HasPrefix(trimmed, ".") {
			if host == trimmed[1:] || strings.HasSuffix(host, trimmed) {
				return true
			}
		} else if len(host) > len(ruleHost)+1 && strings.HasSuffix(host, "."+ruleHost) {
			return true
		}
	}
	return false
}

func configureTransportProxy(tr *http.Transport, baseDialer *net.Dialer, rp *resolvedProxy, onProxyUsed func(string)) {
	if rp == nil || rp.mode == ProxyNone {
		tr.Proxy = nil
		return
	}
	if rp.mode == ProxySystem {
		tr.Proxy = func(req *http.Request) (*url.URL, error) {
			if ShouldBypassProxy(req.URL, rp.noProxy) {
				return nil, nil
			}
			u, err := http.ProxyFromEnvironment(req)
			if u != nil && onProxyUsed != nil {
				disp := *u
				disp.User = nil
				onProxyUsed(disp.String())
			}
			return u, err
		}
		return
	}

	if rp.mode == ProxyHTTP && rp.proxyURL != nil {
		if rp.username != "" || rp.password != "" {
			basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(rp.username+":"+rp.password))
			tr.ProxyConnectHeader = http.Header{
				"Proxy-Authorization": []string{basic},
			}
		}
		tr.Proxy = func(req *http.Request) (*url.URL, error) {
			if ShouldBypassProxy(req.URL, rp.noProxy) {
				return nil, nil
			}
			if onProxyUsed != nil {
				onProxyUsed(rp.displayLabel)
			}
			cp := *rp.proxyURL
			return &cp, nil
		}
		return
	}

	if rp.mode == ProxySOCKS5 && rp.proxyURL != nil {
		tr.Proxy = nil
		origDial := tr.DialContext
		if origDial == nil {
			origDial = baseDialer.DialContext
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			h, _, err := net.SplitHostPort(addr)
			if err == nil {
				u := &url.URL{Scheme: "http", Host: addr}
				if h != "" && ShouldBypassProxy(u, rp.noProxy) {
					return origDial(ctx, network, addr)
				}
			}
			if onProxyUsed != nil {
				onProxyUsed(rp.displayLabel)
			}
			return dialSOCKS5(ctx, baseDialer, rp.proxyURL, rp.username, rp.password, network, addr)
		}
	}
}

// dialSOCKS5 establishes a TCP connection to targetAddr through a SOCKS5
// (RFC 1928) proxy with optional RFC 1929 username/password authentication.
func dialSOCKS5(
	ctx context.Context,
	baseDialer *net.Dialer,
	proxyURL *url.URL,
	username, password string,
	network, targetAddr string,
) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6", "":
	default:
		return nil, fmt.Errorf("SOCKS5 proxy does not support network %q", network)
	}

	targetHost, targetPortStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid target address %q: %w", targetAddr, err)
	}
	targetPort, err := strconv.Atoi(targetPortStr)
	if err != nil || targetPort < 1 || targetPort > 65535 {
		return nil, fmt.Errorf("invalid target port %q", targetPortStr)
	}

	proxyHost := proxyURL.Hostname()
	proxyPort := proxyURL.Port()
	if proxyPort == "" {
		proxyPort = "1080"
	}
	proxyAddr := net.JoinHostPort(proxyHost, proxyPort)

	dialer := baseDialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: DefaultDialTimeout}
	}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("proxy dial failed (%s): %w", proxyAddr, err)
	}

	// Ensure context cancellation or timeout aborts the SOCKS5 handshake immediately.
	handshakeDone := make(chan struct{})
	defer close(handshakeDone)
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else if dialer.Timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(dialer.Timeout))
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-handshakeDone:
		}
	}()

	if err := performSOCKS5Handshake(conn, username, password, targetHost, targetPort); err != nil {
		_ = conn.Close()
		return nil, err
	}

	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func performSOCKS5Handshake(conn net.Conn, username, password, targetHost string, targetPort int) error {
	const (
		socksVersion5       = 0x05
		authNone            = 0x00
		authUsernamePass    = 0x02
		authNoAcceptable    = 0xFF
		cmdConnect          = 0x01
		atypIPv4            = 0x01
		atypDomain          = 0x03
		atypIPv6            = 0x04
		userPassAuthVersion = 0x01
	)

	hasAuth := username != "" || password != ""
	var greeting []byte
	if hasAuth {
		greeting = []byte{socksVersion5, 2, authNone, authUsernamePass}
	} else {
		greeting = []byte{socksVersion5, 1, authNone}
	}
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("SOCKS5 proxy handshake write failed: %w", err)
	}

	var methodReply [2]byte
	if _, err := io.ReadFull(conn, methodReply[:]); err != nil {
		return fmt.Errorf("SOCKS5 proxy handshake read failed: %w", err)
	}
	if methodReply[0] != socksVersion5 {
		return fmt.Errorf("SOCKS5 proxy returned unsupported version %d", methodReply[0])
	}

	switch methodReply[1] {
	case authNone:
		// No authentication required.
	case authUsernamePass:
		if !hasAuth {
			return fmt.Errorf("SOCKS5 proxy requires username/password authentication")
		}
		if len(username) > 255 || len(password) > 255 {
			return fmt.Errorf("SOCKS5 proxy username or password exceeds 255 bytes")
		}
		authReq := make([]byte, 0, 3+len(username)+len(password))
		authReq = append(authReq, userPassAuthVersion, byte(len(username)))
		authReq = append(authReq, username...)
		authReq = append(authReq, byte(len(password)))
		authReq = append(authReq, password...)
		if _, err := conn.Write(authReq); err != nil {
			return fmt.Errorf("SOCKS5 proxy authentication write failed: %w", err)
		}
		var authResp [2]byte
		if _, err := io.ReadFull(conn, authResp[:]); err != nil {
			return fmt.Errorf("SOCKS5 proxy authentication read failed: %w", err)
		}
		if authResp[1] != 0x00 {
			return fmt.Errorf("SOCKS5 proxy authentication failed (status 0x%02x)", authResp[1])
		}
	case authNoAcceptable:
		return fmt.Errorf("SOCKS5 proxy rejected offered authentication methods")
	default:
		return fmt.Errorf("SOCKS5 proxy selected unsupported authentication method 0x%02x", methodReply[1])
	}

	req := make([]byte, 0, 6+len(targetHost))
	req = append(req, socksVersion5, cmdConnect, 0x00)
	if ip := net.ParseIP(targetHost); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, atypIPv4)
			req = append(req, ip4...)
		} else {
			req = append(req, atypIPv6)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(targetHost) > 255 {
			return fmt.Errorf("SOCKS5 target host %q exceeds 255 bytes", targetHost)
		}
		req = append(req, atypDomain, byte(len(targetHost)))
		req = append(req, targetHost...)
	}
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(targetPort))
	req = append(req, portBytes[:]...)

	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("SOCKS5 proxy connect request write failed: %w", err)
	}

	var respHdr [4]byte
	if _, err := io.ReadFull(conn, respHdr[:]); err != nil {
		return fmt.Errorf("SOCKS5 proxy connect reply read failed: %w", err)
	}
	if respHdr[0] != socksVersion5 {
		return fmt.Errorf("SOCKS5 proxy connect reply returned invalid version %d", respHdr[0])
	}
	if respHdr[1] != 0x00 {
		return fmt.Errorf("SOCKS5 proxy connect failed: %s (code 0x%02x)", socks5ReplyMessage(respHdr[1]), respHdr[1])
	}

	var discardLen int
	switch respHdr[3] {
	case atypIPv4:
		discardLen = 4 + 2
	case atypIPv6:
		discardLen = 16 + 2
	case atypDomain:
		var domLen [1]byte
		if _, err := io.ReadFull(conn, domLen[:]); err != nil {
			return fmt.Errorf("SOCKS5 proxy reading bound domain length: %w", err)
		}
		discardLen = int(domLen[0]) + 2
	default:
		return fmt.Errorf("SOCKS5 proxy returned unknown address type 0x%02x", respHdr[3])
	}
	if discardLen > 0 {
		buf := make([]byte, discardLen)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return fmt.Errorf("SOCKS5 proxy reading bound address: %w", err)
		}
	}
	return nil
}

func socks5ReplyMessage(code byte) string {
	switch code {
	case 0x01:
		return "general SOCKS server failure"
	case 0x02:
		return "connection not allowed by ruleset"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused by destination"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return "unknown SOCKS error"
	}
}
