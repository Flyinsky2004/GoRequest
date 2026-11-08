package mockserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gorequest/internal/core"
)

// BindPolicyError is returned when a non-loopback / LAN bind address is requested
// without explicit AllowLAN opt-in, or when the host/port is invalid.
type BindPolicyError struct {
	Host   string
	Port   int
	Reason string
}

func (e *BindPolicyError) Error() string {
	return fmt.Sprintf("mock bind policy rejected %s:%d: %s", e.Host, e.Port, e.Reason)
}

// PortConflictError is returned when the requested port is already in use.
// The Mock Server never kills another process and never silently changes a
// fixed port.
type PortConflictError struct {
	Addr string
	Err  error
}

func (e *PortConflictError) Error() string {
	return fmt.Sprintf("mock listen port conflict on %s: address already in use (another service is listening; GoRequest will not terminate external processes or silently switch ports)", e.Addr)
}

func (e *PortConflictError) Unwrap() error { return e.Err }

// ServerConfig configures one Mock Server instance.
type ServerConfig struct {
	Host     string
	Port     int // 0 selects an ephemeral port; negative values are rejected
	AllowLAN bool
	Limits   ServerLimits
	Redactor *core.Redactor
	OnAccess func(AccessRecord)
}

// ValidateBindAddress checks the host and port against GoRequest's loopback-first
// security policy and returns the normalized host:port string and whether the
// bind address is a non-loopback (LAN/all-interfaces) address.
func ValidateBindAddress(host string, port int, allowLAN bool) (normalizedHost string, addr string, isLAN bool, err error) {
	h := strings.TrimSpace(host)
	if h == "" {
		h = DefaultBindHost
	}
	if port < 0 || port > 65535 {
		return "", "", false, &BindPolicyError{
			Host:   h,
			Port:   port,
			Reason: fmt.Sprintf("port %d must be between 0 and 65535 (use 0 for an ephemeral port)", port),
		}
	}

	loopback := false
	if strings.EqualFold(h, "localhost") {
		h = "127.0.0.1"
		loopback = true
	} else {
		ip := net.ParseIP(h)
		if ip == nil {
			return "", "", false, &BindPolicyError{
				Host:   h,
				Port:   port,
				Reason: "host must be a literal IP address (127.0.0.1, ::1, or an explicit LAN IP) or localhost",
			}
		}
		loopback = ip.IsLoopback()
	}

	if !loopback {
		if !allowLAN {
			return "", "", true, &BindPolicyError{
				Host:   h,
				Port:   port,
				Reason: "non-loopback / LAN binding is disabled by default; choose 127.0.0.1 or explicitly enable AllowLAN",
			}
		}
		isLAN = true
	}

	return h, net.JoinHostPort(h, strconv.Itoa(port)), isLAN, nil
}

// Server is a running or stopped deterministic HTTP Mock Server instance.
type Server struct {
	mu          sync.Mutex
	cfg         ServerConfig
	limits      ServerLimits
	accessLog   *AccessLog
	redMu       sync.RWMutex
	redactor    *core.Redactor
	snapPtr     atomic.Pointer[CompiledSnapshot]
	reqSeq      atomic.Uint64
	running     atomic.Bool
	boundAddr   string
	boundPort   int
	isLAN       bool
	listener    net.Listener
	httpServer  *http.Server
	serverCtx   context.Context
	serverStop  context.CancelFunc
	sem         chan struct{}
	serveDone   chan struct{}
	connsMu     sync.Mutex
	activeConns map[net.Conn]struct{}
}

// NewServer constructs a stopped Server with bounded limits and in-memory access log.
func NewServer(cfg ServerConfig) *Server {
	lim := cfg.Limits.Effective()
	red := cfg.Redactor
	if red == nil {
		red = core.NewRedactor()
	}
	return &Server{
		cfg:         cfg,
		limits:      lim,
		accessLog:   NewAccessLog(lim.MaxAccessLogEntries, lim.MaxLogBodyPreview),
		redactor:    red,
		activeConns: map[net.Conn]struct{}{},
	}
}

// AccessLog returns the server's bounded, sanitized in-memory access log.
func (s *Server) AccessLog() *AccessLog {
	if s == nil {
		return nil
	}
	return s.accessLog
}

// SetRedactor updates the Redactor used to scrub incoming/outgoing access logs.
func (s *Server) SetRedactor(red *core.Redactor) {
	if s == nil {
		return
	}
	if red == nil {
		red = core.NewRedactor()
	}
	s.redMu.Lock()
	s.redactor = red
	s.redMu.Unlock()
}

func (s *Server) currentRedactor() *core.Redactor {
	s.redMu.RLock()
	defer s.redMu.RUnlock()
	return s.redactor
}

// CurrentSnapshot returns the immutable CompiledSnapshot currently served by s.
func (s *Server) CurrentSnapshot() *CompiledSnapshot {
	if s == nil {
		return nil
	}
	return s.snapPtr.Load()
}

// SwapSnapshot atomically replaces the active CompiledSnapshot on s without
// mutating the previous snapshot pointer (so any Suite holding the previous
// snapshot pointer continues to see its frozen rules). Rejects snapshots with
// unresolved rule conflicts.
func (s *Server) SwapSnapshot(snap *CompiledSnapshot) error {
	if s == nil {
		return &core.ConfigError{Err: fmt.Errorf("mock server is nil")}
	}
	if snap == nil {
		return &core.ConfigError{Err: fmt.Errorf("mock snapshot is nil")}
	}
	if len(snap.Conflicts) > 0 {
		return &AmbiguityError{Conflicts: snap.Conflicts}
	}
	s.snapPtr.Store(snap)
	return nil
}

// Start validates the bind address and snapshot, opens the TCP listener, and
// begins serving requests in a tracked background goroutine.
func (s *Server) Start(ctx context.Context, snap *CompiledSnapshot) error {
	if s == nil {
		return &core.ConfigError{Err: fmt.Errorf("mock server is nil")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if snap == nil {
		return &core.ConfigError{Err: fmt.Errorf("cannot start mock server without a compiled rule snapshot")}
	}
	if len(snap.Conflicts) > 0 {
		return &AmbiguityError{Conflicts: snap.Conflicts}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running.Load() {
		return fmt.Errorf("mock server is already running on %s", s.boundAddr)
	}

	_, listenAddr, isLAN, err := ValidateBindAddress(s.cfg.Host, s.cfg.Port, s.cfg.AllowLAN)
	if err != nil {
		return err
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", listenAddr)
	if err != nil {
		if isAddrInUse(err) {
			return &PortConflictError{Addr: listenAddr, Err: err}
		}
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	actualAddr := ln.Addr().String()
	_, portStr, _ := net.SplitHostPort(actualAddr)
	actualPort, _ := strconv.Atoi(portStr)

	srvCtx, srvCancel := context.WithCancel(context.Background())
	s.snapPtr.Store(snap)
	s.listener = ln
	s.boundAddr = actualAddr
	s.boundPort = actualPort
	s.isLAN = isLAN
	s.serverCtx = srvCtx
	s.serverStop = srvCancel
	s.sem = make(chan struct{}, s.limits.MaxConcurrentRequests)
	s.serveDone = make(chan struct{})
	s.connsMu.Lock()
	s.activeConns = map[net.Conn]struct{}{}
	s.connsMu.Unlock()

	httpSrv := &http.Server{
		Handler:           http.HandlerFunc(s.handleHTTP),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      time.Duration(s.limits.MaxDelayMs)*time.Millisecond + 15*time.Second,
		IdleTimeout:       30 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return srvCtx
		},
		ConnState: func(c net.Conn, state http.ConnState) {
			s.connsMu.Lock()
			defer s.connsMu.Unlock()
			switch state {
			case http.StateNew, http.StateActive, http.StateIdle:
				s.activeConns[c] = struct{}{}
			case http.StateHijacked, http.StateClosed:
				delete(s.activeConns, c)
			}
		},
	}
	s.httpServer = httpSrv
	s.running.Store(true)

	done := s.serveDone
	go func() {
		defer close(done)
		_ = httpSrv.Serve(ln)
		s.running.Store(false)
	}()

	return nil
}

// WaitReady verifies that the server is running and its TCP listener is bound.
func (s *Server) WaitReady(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("mock server is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.running.Load() || s.BoundAddr() == "" {
		return fmt.Errorf("mock server is not running")
	}
	return nil
}

// Stop stops accepting new connections, cancels all in-flight request delays,
// closes active connections within the bounded shutdown timeout, and waits for
// the serve goroutine to exit. Safe to call multiple times.
func (s *Server) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.running.Load() && s.httpServer == nil {
		s.mu.Unlock()
		return nil
	}
	srvCancel := s.serverStop
	httpSrv := s.httpServer
	ln := s.listener
	done := s.serveDone
	timeout := s.limits.ShutdownTimeout
	s.httpServer = nil
	s.listener = nil
	s.serverStop = nil
	s.running.Store(false)
	s.mu.Unlock()

	if srvCancel != nil {
		srvCancel()
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
	} else if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var shutdownErr error
	if httpSrv != nil {
		shutdownErr = httpSrv.Shutdown(ctx)
		if shutdownErr != nil {
			_ = httpSrv.Close()
		}
	}
	// Shutdown owns the listener while Serve is running. Closing it before
	// Shutdown can make Shutdown report net.ErrClosed. Close it afterwards as
	// a fallback for a Serve goroutine that has not registered it yet.
	if ln != nil {
		_ = ln.Close()
	}
	s.closeTrackedConns()

	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
	return shutdownErr
}

func (s *Server) closeTrackedConns() {
	s.connsMu.Lock()
	conns := make([]net.Conn, 0, len(s.activeConns))
	for c := range s.activeConns {
		conns = append(conns, c)
	}
	s.activeConns = map[net.Conn]struct{}{}
	s.connsMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// IsRunning reports whether the Mock Server is currently listening.
func (s *Server) IsRunning() bool {
	return s != nil && s.running.Load()
}

// BoundAddr returns the actual bound `host:port` (including the OS-chosen port
// when configured with Port=0), or "" when stopped.
func (s *Server) BoundAddr() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running.Load() {
		return ""
	}
	return s.boundAddr
}

// BaseURL returns `http://<BoundAddr()>` when running, or "" when stopped.
func (s *Server) BaseURL() string {
	addr := s.BoundAddr()
	if addr == "" {
		return ""
	}
	return "http://" + addr
}

// Port returns the actual bound TCP port, or 0 when stopped.
func (s *Server) Port() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running.Load() {
		return 0
	}
	return s.boundPort
}

// IsLAN reports whether the server is bound to a non-loopback address.
func (s *Server) IsLAN() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isLAN
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	seq := s.reqSeq.Add(1)
	red := s.currentRedactor()
	snap := s.snapPtr.Load()
	scenario := DefaultScenario
	if snap != nil {
		scenario = snap.Scenario
	}

	// Enforce bounded concurrency
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		msg := `{"error":"mock_concurrency_limit_exceeded","message":"mock server max concurrent requests reached"}`
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(msg))
		s.recordAccess(AccessRecord{
			Seq:                 seq,
			Timestamp:           started.UTC(),
			Method:              r.Method,
			Path:                r.URL.Path,
			SanitizedQuery:      r.URL.RawQuery,
			Scenario:            scenario,
			Matched:             false,
			Status:              http.StatusServiceUnavailable,
			DurationMs:          time.Since(started).Milliseconds(),
			RequestHeaders:      HeaderMapToPairs(r.Header),
			ResponseHeaders:     [][2]string{{"Content-Type", "application/json; charset=utf-8"}},
			ResponseBodyPreview: msg,
			Diagnostic:          "rejected: max concurrent requests limit reached",
		}, red)
		return
	}

	// Read bounded request body
	var bodyBytes []byte
	if r.Body != nil {
		limited := http.MaxBytesReader(w, r.Body, s.limits.MaxRequestBodyBytes)
		b, err := io.ReadAll(limited)
		_ = limited.Close()
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) || strings.Contains(strings.ToLower(err.Error()), "request body too large") {
				msg := fmt.Sprintf(`{"error":"mock_request_body_too_large","maxBytes":%d}`, s.limits.MaxRequestBodyBytes)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_, _ = w.Write([]byte(msg))
				s.recordAccess(AccessRecord{
					Seq:                 seq,
					Timestamp:           started.UTC(),
					Method:              r.Method,
					Path:                r.URL.Path,
					SanitizedQuery:      r.URL.RawQuery,
					Scenario:            scenario,
					Matched:             false,
					Status:              http.StatusRequestEntityTooLarge,
					DurationMs:          time.Since(started).Milliseconds(),
					RequestHeaders:      HeaderMapToPairs(r.Header),
					ResponseHeaders:     [][2]string{{"Content-Type", "application/json; charset=utf-8"}},
					ResponseBodyPreview: msg,
					Diagnostic:          fmt.Sprintf("rejected: request body exceeds %d bytes", s.limits.MaxRequestBodyBytes),
				}, red)
				return
			}
			// Client canceled or disconnected during body read
			s.recordAccess(AccessRecord{
				Seq:            seq,
				Timestamp:      started.UTC(),
				Method:         r.Method,
				Path:           r.URL.Path,
				SanitizedQuery: r.URL.RawQuery,
				Scenario:       scenario,
				Matched:        false,
				Status:         499,
				DurationMs:     time.Since(started).Milliseconds(),
				Canceled:       true,
				RequestHeaders: HeaderMapToPairs(r.Header),
				Diagnostic:     "request body read aborted or canceled",
			}, red)
			return
		}
		bodyBytes = b
	}

	outcome := snap.Evaluate(IncomingRequest{
		Seq:     seq,
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.Query(),
		Headers: r.Header,
		Body:    bodyBytes,
	}, red)

	// Wait deterministic delay if configured, aborting immediately on client or server cancellation.
	if outcome.Delay > 0 {
		timer := time.NewTimer(outcome.Delay)
		select {
		case <-timer.C:
		case <-r.Context().Done():
			timer.Stop()
			s.recordAccess(AccessRecord{
				Seq:                seq,
				Timestamp:          started.UTC(),
				Method:             r.Method,
				Path:               r.URL.Path,
				SanitizedQuery:     r.URL.RawQuery,
				Scenario:           outcome.Scenario,
				Matched:            outcome.Matched,
				MatchedRuleID:      outcome.RuleID,
				Status:             499,
				DurationMs:         time.Since(started).Milliseconds(),
				Canceled:           true,
				RequestHeaders:     HeaderMapToPairs(r.Header),
				RequestBodyPreview: string(bodyBytes),
				Diagnostic:         "request canceled during simulated delay",
			}, red)
			return
		case <-s.serverCtx.Done():
			timer.Stop()
			s.recordAccess(AccessRecord{
				Seq:                seq,
				Timestamp:          started.UTC(),
				Method:             r.Method,
				Path:               r.URL.Path,
				SanitizedQuery:     r.URL.RawQuery,
				Scenario:           outcome.Scenario,
				Matched:            outcome.Matched,
				MatchedRuleID:      outcome.RuleID,
				Status:             499,
				DurationMs:         time.Since(started).Milliseconds(),
				Canceled:           true,
				RequestHeaders:     HeaderMapToPairs(r.Header),
				RequestBodyPreview: string(bodyBytes),
				Diagnostic:         "mock server stopped during simulated delay",
			}, red)
			return
		}
	}

	// Check cancellation right before writing response
	if r.Context().Err() != nil || s.serverCtx.Err() != nil {
		s.recordAccess(AccessRecord{
			Seq:                seq,
			Timestamp:          started.UTC(),
			Method:             r.Method,
			Path:               r.URL.Path,
			SanitizedQuery:     r.URL.RawQuery,
			Scenario:           outcome.Scenario,
			Matched:            outcome.Matched,
			MatchedRuleID:      outcome.RuleID,
			Status:             499,
			DurationMs:         time.Since(started).Milliseconds(),
			Canceled:           true,
			RequestHeaders:     HeaderMapToPairs(r.Header),
			RequestBodyPreview: string(bodyBytes),
			Diagnostic:         "canceled before response write",
		}, red)
		return
	}

	for _, kv := range outcome.Headers {
		w.Header().Add(kv[0], kv[1])
	}
	w.WriteHeader(outcome.Status)
	if !strings.EqualFold(r.Method, http.MethodHead) && len(outcome.Body) > 0 {
		_, _ = w.Write(outcome.Body)
	}

	s.recordAccess(AccessRecord{
		Seq:                 seq,
		Timestamp:           started.UTC(),
		Method:              r.Method,
		Path:                r.URL.Path,
		SanitizedQuery:      r.URL.RawQuery,
		Scenario:            outcome.Scenario,
		Matched:             outcome.Matched,
		MatchedRuleID:       outcome.RuleID,
		Status:              outcome.Status,
		DurationMs:          time.Since(started).Milliseconds(),
		RequestHeaders:      HeaderMapToPairs(r.Header),
		RequestBodyPreview:  string(bodyBytes),
		ResponseHeaders:     outcome.Headers,
		ResponseBodyPreview: string(outcome.Body),
		Diagnostic:          outcome.Diagnostic,
	}, red)
}

func (s *Server) recordAccess(rec AccessRecord, red *core.Redactor) {
	sanitized := s.accessLog.Record(rec, red)
	if s.cfg.OnAccess != nil {
		s.cfg.OnAccess(sanitized)
	}
}

func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "address already in use") || strings.Contains(lower, "only one usage of each socket address")
}
