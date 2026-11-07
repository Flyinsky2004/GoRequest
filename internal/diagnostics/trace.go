package diagnostics

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"gorequest/internal/core"
)

type dialCapture struct {
	network  string
	addr     string
	startAt  time.Duration
	endAt    time.Duration
	started  bool
	observed bool
	winner   bool
	err      string
}

type hopCapture struct {
	hostPort string
	startAt  time.Duration

	getConnAt       time.Duration
	getConnObserved bool

	dnsStartAt  time.Duration
	dnsEndAt    time.Duration
	dnsStarted  bool
	dnsObserved bool
	dnsErr      string

	dials []dialCapture

	tlsStartAt     time.Duration
	tlsEndAt       time.Duration
	tlsStarted     bool
	tlsObserved    bool
	tlsErr         string
	tlsVersion     string
	tlsCipherSuite string
	tlsServerName  string

	gotConnAt       time.Duration
	gotConnObserved bool
	reused          bool
	reusedKnown     bool
	remoteAddr      string

	wroteReqAt       time.Duration
	wroteReqObserved bool
	writeErr         string

	firstByteAt       time.Duration
	firstByteObserved bool
}

type sampleTraceCollector struct {
	mu      sync.Mutex
	start   time.Time
	nowFunc func() time.Time
	hops    []*hopCapture
}

func newSampleTraceCollector(nowFunc func() time.Time) (*sampleTraceCollector, *httptrace.ClientTrace) {
	if nowFunc == nil {
		nowFunc = time.Now
	}
	t0 := nowFunc()
	stc := &sampleTraceCollector{
		start:   t0,
		nowFunc: nowFunc,
	}

	elapsed := func() time.Duration {
		d := stc.nowFunc().Sub(stc.start)
		if d < 0 {
			return 0
		}
		return d
	}

	currentHopLocked := func(startNewOnGetConn bool, hostPort string) *hopCapture {
		if len(stc.hops) == 0 {
			h := &hopCapture{hostPort: hostPort, startAt: elapsed()}
			stc.hops = append(stc.hops, h)
			return h
		}
		last := stc.hops[len(stc.hops)-1]
		if startNewOnGetConn && (last.gotConnObserved || last.wroteReqObserved || last.firstByteObserved) {
			h := &hopCapture{hostPort: hostPort, startAt: elapsed()}
			stc.hops = append(stc.hops, h)
			return h
		}
		if last.hostPort == "" && hostPort != "" {
			last.hostPort = hostPort
		}
		return last
	}

	ct := &httptrace.ClientTrace{
		GetConn: func(hostPort string) {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(true, hostPort)
			if !h.getConnObserved {
				h.getConnAt = elapsed()
				h.getConnObserved = true
				h.startAt = h.getConnAt
			}
		},
		DNSStart: func(httptrace.DNSStartInfo) {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(false, "")
			h.dnsStartAt = elapsed()
			h.dnsStarted = true
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(false, "")
			if h.dnsStarted {
				h.dnsEndAt = elapsed()
				h.dnsObserved = info.Err == nil
				if info.Err != nil {
					h.dnsErr = info.Err.Error()
				}
			}
		},
		ConnectStart: func(network, addr string) {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(false, "")
			h.dials = append(h.dials, dialCapture{
				network: network,
				addr:    addr,
				startAt: elapsed(),
				started: true,
			})
		},
		ConnectDone: func(network, addr string, err error) {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(false, "")
			now := elapsed()
			idx := -1
			for i := len(h.dials) - 1; i >= 0; i-- {
				if !h.dials[i].observed && h.dials[i].network == network && h.dials[i].addr == addr {
					idx = i
					break
				}
			}
			if idx < 0 {
				for i := len(h.dials) - 1; i >= 0; i-- {
					if !h.dials[i].observed {
						idx = i
						break
					}
				}
			}
			if idx >= 0 {
				h.dials[idx].endAt = now
				h.dials[idx].observed = err == nil
				if err != nil {
					h.dials[idx].err = err.Error()
				} else if addr != "" {
					h.remoteAddr = addr
				}
			}
		},
		TLSHandshakeStart: func() {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(false, "")
			h.tlsStartAt = elapsed()
			h.tlsStarted = true
		},
		TLSHandshakeDone: func(cs tls.ConnectionState, err error) {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(false, "")
			if h.tlsStarted {
				h.tlsEndAt = elapsed()
				h.tlsObserved = err == nil
				if err != nil {
					h.tlsErr = err.Error()
				} else {
					h.tlsVersion = tlsVersionLabel(cs.Version)
					h.tlsCipherSuite = tls.CipherSuiteName(cs.CipherSuite)
					h.tlsServerName = cs.ServerName
				}
			}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(false, "")
			h.gotConnAt = elapsed()
			h.gotConnObserved = true
			h.reused = info.Reused
			h.reusedKnown = true
			if info.Conn != nil && info.Conn.RemoteAddr() != nil {
				h.remoteAddr = info.Conn.RemoteAddr().String()
			}
			markWinningDial(h)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(false, "")
			h.wroteReqAt = elapsed()
			h.wroteReqObserved = info.Err == nil
			if info.Err != nil {
				h.writeErr = info.Err.Error()
			}
		},
		GotFirstResponseByte: func() {
			stc.mu.Lock()
			defer stc.mu.Unlock()
			h := currentHopLocked(false, "")
			if !h.firstByteObserved {
				h.firstByteAt = elapsed()
				h.firstByteObserved = true
			}
		},
	}

	return stc, ct
}

func markWinningDial(h *hopCapture) {
	if len(h.dials) == 0 {
		return
	}
	if h.remoteAddr != "" {
		for i := len(h.dials) - 1; i >= 0; i-- {
			if h.dials[i].observed && h.dials[i].addr == h.remoteAddr {
				h.dials[i].winner = true
				return
			}
		}
	}
	for i := len(h.dials) - 1; i >= 0; i-- {
		if h.dials[i].observed {
			h.dials[i].winner = true
			return
		}
	}
}

func tlsVersionLabel(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	default:
		return ""
	}
}

func makeInterval(start, end time.Duration) *TimeInterval {
	if end < start {
		end = start
	}
	sMs := roundMetric(float64(start.Microseconds()) / 1000.0)
	eMs := roundMetric(float64(end.Microseconds()) / 1000.0)
	dMs := roundMetric(float64((end - start).Microseconds()) / 1000.0)
	return &TimeInterval{
		StartOffsetMs: sMs,
		EndOffsetMs:   eMs,
		DurationMs:    dMs,
	}
}

func durationToMs(d time.Duration) float64 {
	if d < 0 {
		return 0
	}
	return roundMetric(float64(d.Microseconds()) / 1000.0)
}

func isHTTPSURL(rawURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawURL)), "https://")
}

func isNoBodyResponse(method string, status int) bool {
	return strings.EqualFold(strings.TrimSpace(method), http.MethodHead) ||
		status == http.StatusNoContent ||
		status == http.StatusNotModified
}

// buildSampleTimeline combines captured httptrace hop events with core.ExecResult
// and applies redaction to all URLs, remote addresses, proxy labels, and errors.
func buildSampleTimeline(
	stc *sampleTraceCollector,
	plan *core.Plan,
	res *core.ExecResult,
	execErr error,
	totalDur time.Duration,
	red *core.Redactor,
) ([]HopTimeline, []string) {
	redact := func(s string) string {
		if red != nil {
			return red.RedactString(s)
		}
		return s
	}
	sanitizePlan := plan
	if red != nil && plan != nil {
		sanitizePlan = red.SanitizePlan(plan)
	}
	var sanitizeRes *core.ExecResult
	if res != nil {
		if red != nil {
			sanitizeRes = red.SanitizeExecResult(res)
		} else {
			cp := *res
			sanitizeRes = &cp
		}
	}

	var captured []*hopCapture
	if stc != nil {
		stc.mu.Lock()
		captured = make([]*hopCapture, len(stc.hops))
		copy(captured, stc.hops)
		stc.mu.Unlock()
	}

	if len(captured) == 0 {
		return buildFallbackTimeline(sanitizePlan, sanitizeRes, execErr, totalDur, redact)
	}

	var redirects []core.RedirectHop
	var aggTrace *core.NetworkTrace
	if sanitizeRes != nil {
		redirects = sanitizeRes.Redirects
		aggTrace = sanitizeRes.Trace
	}

	hopCount := len(captured)
	if len(redirects)+1 > hopCount {
		hopCount = len(redirects) + 1
	}

	hops := make([]HopTimeline, 0, hopCount)
	var sampleNotes []string
	sampleNotes = append(sampleNotes, "Stage intervals use monotonic offsets relative to request start; overlapping intervals (e.g. ConnWait vs DNS/TCP/TLS, or concurrent Happy Eyeballs dials) must not be summed.")

	for i := 0; i < hopCount; i++ {
		var hc *hopCapture
		if i < len(captured) {
			hc = captured[i]
		}
		isFinalHop := sanitizeRes != nil && i == len(redirects)
		isRedirectHop := i < len(redirects)

		hopMethod := http.MethodGet
		if sanitizePlan != nil && sanitizePlan.Method != "" {
			hopMethod = sanitizePlan.Method
		}
		hopURL := ""
		if sanitizePlan != nil {
			hopURL = sanitizePlan.URL
		}
		fromURL := ""
		statusCode := 0
		proto := ""
		originChanged := false
		strippedCreds := false

		if isRedirectHop {
			rh := redirects[i]
			hopURL = rh.FromURL
			statusCode = rh.StatusCode
			if i > 0 {
				prev := redirects[i-1]
				hopMethod = prev.Method
				fromURL = prev.FromURL
				originChanged = prev.OriginChanged
				strippedCreds = prev.StrippedCredentials
			}
			proto = "unobserved (intermediate 3xx)"
		} else if isFinalHop {
			if i > 0 {
				prev := redirects[i-1]
				hopMethod = prev.Method
				fromURL = prev.FromURL
				originChanged = prev.OriginChanged
				strippedCreds = prev.StrippedCredentials
			}
			if sanitizeRes.FinalURL != "" {
				hopURL = sanitizeRes.FinalURL
			}
			statusCode = sanitizeRes.Status
			proto = sanitizeRes.Proto
		} else if i > 0 && i-1 < len(redirects) {
			prev := redirects[i-1]
			hopMethod = prev.Method
			hopURL = prev.ToURL
			fromURL = prev.FromURL
			originChanged = prev.OriginChanged
			strippedCreds = prev.StrippedCredentials
		}

		ht := HopTimeline{
			Hop:                 i,
			Method:              hopMethod,
			URL:                 redact(hopURL),
			FromURL:             redact(fromURL),
			StatusCode:          statusCode,
			Proto:               proto,
			OriginChanged:       originChanged,
			StrippedCredentials: strippedCreds,
			Stages:              make(map[StageKind]StageMeasurement, len(AllStageKinds)),
		}

		if aggTrace != nil {
			ht.ProxyUsed = redact(aggTrace.ProxyUsed)
			if isFinalHop {
				ht.CustomCAUsed = aggTrace.CustomCAUsed
				ht.ClientCertUsed = aggTrace.ClientCertUsed
				ht.TLSInsecureSkipVerify = aggTrace.TLSInsecureSkipVerify
				if ht.TLSVersion == "" {
					ht.TLSVersion = aggTrace.TLSVersion
				}
				if ht.TLSCipherSuite == "" {
					ht.TLSCipherSuite = aggTrace.TLSCipherSuite
				}
				if ht.TLSServerName == "" {
					ht.TLSServerName = aggTrace.TLSServerName
				}
			}
		}

		if hc == nil {
			for _, k := range AllStageKinds {
				ht.Stages[k] = StageMeasurement{
					Stage:  k,
					Status: ObsUnobserved,
					Note:   "hop trace not captured by httptrace",
				}
			}
			ht.Limitations = append(ht.Limitations, "intermediate hop httptrace events were not observed")
			hops = append(hops, ht)
			continue
		}

		ht.ReusedConn = hc.reused
		ht.ReusedKnown = hc.reusedKnown
		if hc.remoteAddr != "" {
			ht.RemoteAddr = redact(hc.remoteAddr)
		} else if isFinalHop && aggTrace != nil && aggTrace.RemoteAddr != "" {
			ht.RemoteAddr = redact(aggTrace.RemoteAddr)
		}
		if hc.tlsVersion != "" {
			ht.TLSVersion = hc.tlsVersion
			ht.TLSCipherSuite = hc.tlsCipherSuite
			ht.TLSServerName = hc.tlsServerName
		}

		// 1. ConnWait: GetConn -> GotConn (overlaps DNS/TCP/TLS).
		if hc.getConnObserved && hc.gotConnObserved {
			iv := makeInterval(hc.getConnAt, hc.gotConnAt)
			ht.Stages[StageConnWait] = StageMeasurement{
				Stage:      StageConnWait,
				Status:     ObsObserved,
				DurationMs: iv.DurationMs,
				Interval:   iv,
				Note:       "includes connection pool wait and overlaps DNS/TCP/TLS; do not sum with setup stages",
			}
		} else if hc.getConnObserved && execErr != nil {
			iv := makeInterval(hc.getConnAt, totalDur)
			ht.Stages[StageConnWait] = StageMeasurement{
				Stage:      StageConnWait,
				Status:     ObsIncomplete,
				DurationMs: iv.DurationMs,
				Interval:   iv,
				Error:      redact(execErr.Error()),
			}
		} else {
			ht.Stages[StageConnWait] = StageMeasurement{
				Stage:  StageConnWait,
				Status: ObsUnobserved,
			}
		}

		// 2. DNS: DNSStart -> DNSDone.
		switch {
		case hc.dnsObserved:
			iv := makeInterval(hc.dnsStartAt, hc.dnsEndAt)
			ht.Stages[StageDNS] = StageMeasurement{
				Stage:      StageDNS,
				Status:     ObsObserved,
				DurationMs: iv.DurationMs,
				Interval:   iv,
			}
		case hc.dnsStarted:
			end := hc.dnsEndAt
			if end < hc.dnsStartAt {
				end = totalDur
			}
			iv := makeInterval(hc.dnsStartAt, end)
			ht.Stages[StageDNS] = StageMeasurement{
				Stage:      StageDNS,
				Status:     ObsIncomplete,
				DurationMs: iv.DurationMs,
				Interval:   iv,
				Error:      redact(hc.dnsErr),
			}
		case hc.reusedKnown && hc.reused:
			ht.Stages[StageDNS] = StageMeasurement{
				Stage:  StageDNS,
				Status: ObsSkippedReused,
				Note:   "not occurred (connection reused)",
			}
		case ht.ProxyUsed != "" && ht.ProxyUsed != "direct":
			ht.Stages[StageDNS] = StageMeasurement{
				Stage:  StageDNS,
				Status: ObsUnobserved,
				Note:   "unobserved (proxy-side DNS resolution or literal proxy IP)",
			}
		default:
			ht.Stages[StageDNS] = StageMeasurement{
				Stage:  StageDNS,
				Status: ObsUnobserved,
				Note:   "unobserved (literal IP, resolver cache, or custom dialer)",
			}
		}

		// 3. TCP Connect: ConnectStart -> ConnectDone, preserving concurrent dials.
		var observedDials []dialCapture
		for _, d := range hc.dials {
			end := d.endAt
			if end < d.startAt {
				end = totalDur
			}
			iv := makeInterval(d.startAt, end)
			ht.DialAttempts = append(ht.DialAttempts, DialAttempt{
				Network:    d.network,
				RemoteAddr: redact(d.addr),
				Observed:   d.observed,
				Winner:     d.winner,
				DurationMs: iv.DurationMs,
				Interval:   iv,
				Error:      redact(d.err),
			})
			if d.observed {
				observedDials = append(observedDials, d)
			}
		}
		switch {
		case len(observedDials) > 0:
			win := observedDials[len(observedDials)-1]
			for _, d := range observedDials {
				if d.winner {
					win = d
					break
				}
			}
			iv := makeInterval(observedDials[0].startAt, win.endAt)
			note := ""
			if len(hc.dials) > 1 {
				note = fmt.Sprintf("%d dial attempts recorded (concurrent/fallback); winner=%s, attempts not summed", len(hc.dials), redact(win.addr))
				ht.Limitations = append(ht.Limitations, note)
			}
			ht.Stages[StageTCPConnect] = StageMeasurement{
				Stage:      StageTCPConnect,
				Status:     ObsObserved,
				DurationMs: iv.DurationMs,
				Interval:   iv,
				Note:       note,
			}
		case len(hc.dials) > 0:
			first := hc.dials[0]
			last := hc.dials[len(hc.dials)-1]
			end := last.endAt
			if end < first.startAt {
				end = totalDur
			}
			iv := makeInterval(first.startAt, end)
			ht.Stages[StageTCPConnect] = StageMeasurement{
				Stage:      StageTCPConnect,
				Status:     ObsIncomplete,
				DurationMs: iv.DurationMs,
				Interval:   iv,
				Error:      redact(last.err),
			}
		case hc.reusedKnown && hc.reused:
			ht.Stages[StageTCPConnect] = StageMeasurement{
				Stage:  StageTCPConnect,
				Status: ObsSkippedReused,
				Note:   "not occurred (connection reused)",
			}
		default:
			ht.Stages[StageTCPConnect] = StageMeasurement{
				Stage:  StageTCPConnect,
				Status: ObsUnobserved,
				Note:   "unobserved (custom transport or proxy tunnel)",
			}
		}

		// 4. TLS Handshake: TLSHandshakeStart -> TLSHandshakeDone.
		httpsTarget := isHTTPSURL(ht.URL) || hc.tlsStarted || ht.TLSVersion != ""
		switch {
		case hc.tlsObserved:
			iv := makeInterval(hc.tlsStartAt, hc.tlsEndAt)
			ht.Stages[StageTLSHandshake] = StageMeasurement{
				Stage:      StageTLSHandshake,
				Status:     ObsObserved,
				DurationMs: iv.DurationMs,
				Interval:   iv,
			}
		case hc.tlsStarted:
			end := hc.tlsEndAt
			if end < hc.tlsStartAt {
				end = totalDur
			}
			iv := makeInterval(hc.tlsStartAt, end)
			ht.Stages[StageTLSHandshake] = StageMeasurement{
				Stage:      StageTLSHandshake,
				Status:     ObsIncomplete,
				DurationMs: iv.DurationMs,
				Interval:   iv,
				Error:      redact(hc.tlsErr),
			}
		case !httpsTarget:
			ht.Stages[StageTLSHandshake] = StageMeasurement{
				Stage:  StageTLSHandshake,
				Status: ObsNotApplicable,
				Note:   "not applicable (plain HTTP)",
			}
		case hc.reusedKnown && hc.reused:
			ht.Stages[StageTLSHandshake] = StageMeasurement{
				Stage:  StageTLSHandshake,
				Status: ObsSkippedReused,
				Note:   "not occurred (connection reused)",
			}
		default:
			ht.Stages[StageTLSHandshake] = StageMeasurement{
				Stage:  StageTLSHandshake,
				Status: ObsUnobserved,
			}
		}

		// 5. Request Write: GotConn -> WroteRequest.
		switch {
		case hc.gotConnObserved && hc.wroteReqObserved:
			iv := makeInterval(hc.gotConnAt, hc.wroteReqAt)
			ht.Stages[StageRequestWrite] = StageMeasurement{
				Stage:      StageRequestWrite,
				Status:     ObsObserved,
				DurationMs: iv.DurationMs,
				Interval:   iv,
				Note:       "GotConn to WroteRequest; includes request framing and client scheduling",
			}
		case hc.gotConnObserved && (hc.writeErr != "" || execErr != nil):
			end := hc.wroteReqAt
			if end < hc.gotConnAt {
				end = totalDur
			}
			iv := makeInterval(hc.gotConnAt, end)
			ht.Stages[StageRequestWrite] = StageMeasurement{
				Stage:      StageRequestWrite,
				Status:     ObsIncomplete,
				DurationMs: iv.DurationMs,
				Interval:   iv,
				Error:      redact(hc.writeErr),
			}
		default:
			ht.Stages[StageRequestWrite] = StageMeasurement{
				Stage:  StageRequestWrite,
				Status: ObsUnobserved,
			}
		}

		// 6. Server Wait (WroteRequest -> GotFirstResponseByte) and
		// 7. TTFB (Hop Start -> GotFirstResponseByte).
		if hc.firstByteObserved {
			hopStart := hc.startAt
			ttfbIV := makeInterval(hopStart, hc.firstByteAt)
			ht.Stages[StageTTFB] = StageMeasurement{
				Stage:      StageTTFB,
				Status:     ObsObserved,
				DurationMs: ttfbIV.DurationMs,
				Interval:   ttfbIV,
				Note:       "hop start to GotFirstResponseByte; includes network RTT and server wait",
			}
			if hc.wroteReqObserved {
				waitIV := makeInterval(hc.wroteReqAt, hc.firstByteAt)
				ht.Stages[StageServerWait] = StageMeasurement{
					Stage:      StageServerWait,
					Status:     ObsObserved,
					DurationMs: waitIV.DurationMs,
					Interval:   waitIV,
					Note:       "WroteRequest to GotFirstResponseByte; includes wire RTT and server wait (not pure backend execution time)",
				}
			} else {
				ht.Stages[StageServerWait] = StageMeasurement{
					Stage:  StageServerWait,
					Status: ObsUnobserved,
				}
			}
		} else if execErr != nil {
			ht.Stages[StageTTFB] = StageMeasurement{
				Stage:      StageTTFB,
				Status:     ObsIncomplete,
				DurationMs: durationToMs(totalDur - hc.startAt),
				Error:      redact(execErr.Error()),
			}
			ht.Stages[StageServerWait] = StageMeasurement{
				Stage:  StageServerWait,
				Status: ObsIncomplete,
				Error:  redact(execErr.Error()),
			}
		} else {
			ht.Stages[StageTTFB] = StageMeasurement{Stage: StageTTFB, Status: ObsUnobserved}
			ht.Stages[StageServerWait] = StageMeasurement{Stage: StageServerWait, Status: ObsUnobserved}
		}

		// 8. Download and 9. HopTotal.
		if isRedirectHop {
			ht.Stages[StageDownload] = StageMeasurement{
				Stage:  StageDownload,
				Status: ObsUnobserved,
				Note:   "intermediate 3xx redirect body drain is handled inside net/http prior to next hop",
			}
			hopEnd := hc.firstByteAt
			if i+1 < len(captured) && captured[i+1].startAt >= hc.startAt {
				hopEnd = captured[i+1].startAt
			}
			if hopEnd > 0 && hopEnd >= hc.startAt {
				iv := makeInterval(hc.startAt, hopEnd)
				ht.Stages[StageHopTotal] = StageMeasurement{
					Stage:      StageHopTotal,
					Status:     ObsObserved,
					DurationMs: iv.DurationMs,
					Interval:   iv,
				}
			} else {
				ht.Stages[StageHopTotal] = StageMeasurement{
					Stage:  StageHopTotal,
					Status: ObsUnobserved,
				}
			}
			ht.Limitations = append(ht.Limitations, "intermediate 3xx redirect protocol version and body drain duration are not exposed by stdlib CheckRedirect")
		} else if isFinalHop {
			if isNoBodyResponse(ht.Method, ht.StatusCode) {
				endAt := hc.firstByteAt
				if endAt == 0 {
					endAt = totalDur
				}
				ht.Stages[StageDownload] = StageMeasurement{
					Stage:  StageDownload,
					Status: ObsNotApplicable,
					Note:   fmt.Sprintf("no response body boundary (%s / HTTP %d)", ht.Method, ht.StatusCode),
				}
				iv := makeInterval(hc.startAt, endAt)
				ht.Stages[StageHopTotal] = StageMeasurement{
					Stage:      StageHopTotal,
					Status:     ObsObserved,
					DurationMs: iv.DurationMs,
					Interval:   iv,
				}
			} else if sanitizeRes.Truncated {
				startDl := hc.firstByteAt
				if startDl == 0 && totalDur >= aggTraceDownload(aggTrace) {
					startDl = totalDur - aggTraceDownload(aggTrace)
				}
				iv := makeInterval(startDl, totalDur)
				ht.Stages[StageDownload] = StageMeasurement{
					Stage:      StageDownload,
					Status:     ObsIncomplete,
					DurationMs: iv.DurationMs,
					Interval:   iv,
					Note:       "response body truncated at MaxResponseBytes limit; excluded from complete download statistics",
				}
				hopIV := makeInterval(hc.startAt, totalDur)
				ht.Stages[StageHopTotal] = StageMeasurement{
					Stage:      StageHopTotal,
					Status:     ObsIncomplete,
					DurationMs: hopIV.DurationMs,
					Interval:   hopIV,
					Note:       "incomplete hop due to response body truncation",
				}
				ht.Limitations = append(ht.Limitations, "response body exceeded maximum size limit and was truncated")
			} else if hc.firstByteObserved {
				iv := makeInterval(hc.firstByteAt, totalDur)
				ht.Stages[StageDownload] = StageMeasurement{
					Stage:      StageDownload,
					Status:     ObsObserved,
					DurationMs: iv.DurationMs,
					Interval:   iv,
				}
				hopIV := makeInterval(hc.startAt, totalDur)
				ht.Stages[StageHopTotal] = StageMeasurement{
					Stage:      StageHopTotal,
					Status:     ObsObserved,
					DurationMs: hopIV.DurationMs,
					Interval:   hopIV,
				}
			} else {
				dlDur := aggTraceDownload(aggTrace)
				ht.Stages[StageDownload] = StageMeasurement{
					Stage:      StageDownload,
					Status:     ObsObserved,
					DurationMs: durationToMs(dlDur),
				}
				hopIV := makeInterval(hc.startAt, totalDur)
				ht.Stages[StageHopTotal] = StageMeasurement{
					Stage:      StageHopTotal,
					Status:     ObsObserved,
					DurationMs: hopIV.DurationMs,
					Interval:   hopIV,
				}
			}
		} else {
			// Failed or canceled hop before receiving a complete response.
			errStr := ""
			if execErr != nil {
				errStr = redact(execErr.Error())
			}
			ht.Stages[StageDownload] = StageMeasurement{
				Stage:  StageDownload,
				Status: ObsIncomplete,
				Error:  errStr,
			}
			hopIV := makeInterval(hc.startAt, totalDur)
			ht.Stages[StageHopTotal] = StageMeasurement{
				Stage:      StageHopTotal,
				Status:     ObsIncomplete,
				DurationMs: hopIV.DurationMs,
				Interval:   hopIV,
				Error:      errStr,
			}
		}

		if ht.ReusedConn {
			ht.Limitations = append(ht.Limitations, "connection reused: DNS, TCP connect, and TLS handshake did not occur and are excluded from stage sample counts (not 0ms)")
		}
		if ht.ProxyUsed != "" && ht.ProxyUsed != "direct" {
			ht.Limitations = append(ht.Limitations, fmt.Sprintf("routed via proxy (%s): proxy-side DNS resolution and internal CONNECT/SOCKS5 tunnel timings cannot be decomposed on the client", ht.ProxyUsed))
		}
		if strings.HasPrefix(strings.ToUpper(ht.Proto), "HTTP/2") {
			ht.Limitations = append(ht.Limitations, "HTTP/2 multiplexed stream: connection setup and flow-control windows may be shared across streams")
		}

		hops = append(hops, ht)
	}

	return hops, sampleNotes
}

func aggTraceDownload(tr *core.NetworkTrace) time.Duration {
	if tr == nil {
		return 0
	}
	return tr.DownloadDuration
}

// buildFallbackTimeline constructs a conservative timeline from core.ExecResult
// when a custom/synthetic test Executor returns an ExecResult without firing
// net/http/httptrace hooks. It never fabricates unobserved per-hop timings.
func buildFallbackTimeline(
	plan *core.Plan,
	res *core.ExecResult,
	execErr error,
	totalDur time.Duration,
	redact func(string) string,
) ([]HopTimeline, []string) {
	notes := []string{
		"Timeline constructed from aggregate core.ExecResult (custom executor did not emit httptrace interval hooks).",
	}
	method := http.MethodGet
	targetURL := ""
	if plan != nil {
		if plan.Method != "" {
			method = plan.Method
		}
		targetURL = redact(plan.URL)
	}

	if res == nil {
		errMsg := ""
		if execErr != nil {
			errMsg = redact(execErr.Error())
		}
		stages := make(map[StageKind]StageMeasurement, len(AllStageKinds))
		for _, k := range AllStageKinds {
			stages[k] = StageMeasurement{Stage: k, Status: ObsIncomplete, Error: errMsg}
		}
		stages[StageHopTotal] = StageMeasurement{
			Stage:      StageHopTotal,
			Status:     ObsIncomplete,
			DurationMs: durationToMs(totalDur),
			Error:      errMsg,
		}
		return []HopTimeline{{
			Hop:    0,
			Method: method,
			URL:    targetURL,
			Stages: stages,
		}}, notes
	}

	var hops []HopTimeline
	multiHop := len(res.Redirects) > 0
	if multiHop {
		notes = append(notes, "B04 aggregate NetworkTrace does not isolate per-hop setup timings across redirects without httptrace hooks; intermediate redirect hop stages are marked unobserved.")
	}

	for i, rh := range res.Redirects {
		hMethod := method
		fromURL := ""
		originChanged := false
		stripped := false
		if i > 0 {
			hMethod = res.Redirects[i-1].Method
			fromURL = redact(res.Redirects[i-1].FromURL)
			originChanged = res.Redirects[i-1].OriginChanged
			stripped = res.Redirects[i-1].StrippedCredentials
		}
		stages := make(map[StageKind]StageMeasurement, len(AllStageKinds))
		for _, k := range AllStageKinds {
			stages[k] = StageMeasurement{
				Stage:  k,
				Status: ObsUnobserved,
				Note:   "unobserved in aggregate trace for intermediate redirect hop",
			}
		}
		hops = append(hops, HopTimeline{
			Hop:                 i,
			Method:              hMethod,
			URL:                 redact(rh.FromURL),
			FromURL:             fromURL,
			StatusCode:          rh.StatusCode,
			Proto:               "unobserved (intermediate 3xx)",
			OriginChanged:       originChanged,
			StrippedCredentials: stripped,
			Stages:              stages,
			Limitations:         []string{"intermediate redirect hop timing not decomposable from aggregate NetworkTrace"},
		})
	}

	finalIdx := len(res.Redirects)
	finalMethod := method
	finalFrom := ""
	finalOriginChanged := false
	finalStripped := false
	if finalIdx > 0 {
		prev := res.Redirects[finalIdx-1]
		finalMethod = prev.Method
		finalFrom = redact(prev.FromURL)
		finalOriginChanged = prev.OriginChanged
		finalStripped = prev.StrippedCredentials
	}
	finalURL := targetURL
	if res.FinalURL != "" {
		finalURL = redact(res.FinalURL)
	}

	stages := make(map[StageKind]StageMeasurement, len(AllStageKinds))
	tr := res.Trace
	if tr == nil {
		for _, k := range AllStageKinds {
			stages[k] = StageMeasurement{Stage: k, Status: ObsUnobserved}
		}
		if res.Truncated {
			stages[StageHopTotal] = StageMeasurement{Stage: StageHopTotal, Status: ObsIncomplete, DurationMs: durationToMs(totalDur)}
		} else {
			stages[StageHopTotal] = StageMeasurement{Stage: StageHopTotal, Status: ObsObserved, DurationMs: durationToMs(totalDur)}
		}
		hops = append(hops, HopTimeline{
			Hop:                 finalIdx,
			Method:              finalMethod,
			URL:                 finalURL,
			FromURL:             finalFrom,
			StatusCode:          res.Status,
			Proto:               res.Proto,
			OriginChanged:       finalOriginChanged,
			StrippedCredentials: finalStripped,
			Stages:              stages,
		})
		return hops, notes
	}

	reused := tr.ReusedConn
	httpsTarget := isHTTPSURL(finalURL) || tr.TLSObserved || tr.TLSVersion != ""

	// Setup stages: if multiHop is true and only aggregate trace exists, we
	// cannot know whether DNS/Connect/TLS belonged to hop 0 or the final hop.
	if multiHop {
		for _, k := range []StageKind{StageDNS, StageTCPConnect, StageTLSHandshake, StageConnWait} {
			stages[k] = StageMeasurement{
				Stage:  k,
				Status: ObsUnobserved,
				Note:   "ambiguous across redirect hops in aggregate trace",
			}
		}
	} else {
		stages[StageConnWait] = StageMeasurement{
			Stage:  StageConnWait,
			Status: ObsUnobserved,
			Note:   "GetConn->GotConn wait not recorded in aggregate NetworkTrace",
		}
		if tr.DNSObserved {
			stages[StageDNS] = StageMeasurement{Stage: StageDNS, Status: ObsObserved, DurationMs: durationToMs(tr.DNSDuration)}
		} else if reused {
			stages[StageDNS] = StageMeasurement{Stage: StageDNS, Status: ObsSkippedReused, Note: "not occurred (connection reused)"}
		} else {
			stages[StageDNS] = StageMeasurement{Stage: StageDNS, Status: ObsUnobserved}
		}

		if tr.ConnectObserved {
			stages[StageTCPConnect] = StageMeasurement{Stage: StageTCPConnect, Status: ObsObserved, DurationMs: durationToMs(tr.ConnectDuration)}
		} else if reused {
			stages[StageTCPConnect] = StageMeasurement{Stage: StageTCPConnect, Status: ObsSkippedReused, Note: "not occurred (connection reused)"}
		} else {
			stages[StageTCPConnect] = StageMeasurement{Stage: StageTCPConnect, Status: ObsUnobserved}
		}

		if tr.TLSObserved {
			stages[StageTLSHandshake] = StageMeasurement{Stage: StageTLSHandshake, Status: ObsObserved, DurationMs: durationToMs(tr.TLSDuration)}
		} else if !httpsTarget {
			stages[StageTLSHandshake] = StageMeasurement{Stage: StageTLSHandshake, Status: ObsNotApplicable, Note: "not applicable (plain HTTP)"}
		} else if reused {
			stages[StageTLSHandshake] = StageMeasurement{Stage: StageTLSHandshake, Status: ObsSkippedReused, Note: "not occurred (connection reused)"}
		} else {
			stages[StageTLSHandshake] = StageMeasurement{Stage: StageTLSHandshake, Status: ObsUnobserved}
		}
	}

	if tr.WriteObserved {
		stages[StageRequestWrite] = StageMeasurement{Stage: StageRequestWrite, Status: ObsObserved, DurationMs: durationToMs(tr.WriteDuration)}
	} else {
		stages[StageRequestWrite] = StageMeasurement{Stage: StageRequestWrite, Status: ObsUnobserved}
	}

	if tr.TTFBObserved {
		// Note: core.NetworkTrace.TTFBDuration measures WroteRequest -> GotFirstResponseByte.
		stages[StageServerWait] = StageMeasurement{
			Stage:      StageServerWait,
			Status:     ObsObserved,
			DurationMs: durationToMs(tr.TTFBDuration),
			Note:       "WroteRequest to GotFirstResponseByte",
		}
		// Compute hop start -> first byte when single-hop and component stages are known.
		if !multiHop {
			var setupDur time.Duration
			if !reused {
				if tr.DNSObserved {
					setupDur += tr.DNSDuration
				}
				if tr.ConnectObserved {
					setupDur += tr.ConnectDuration
				}
				if tr.TLSObserved {
					setupDur += tr.TLSDuration
				}
			}
			if tr.WriteObserved {
				setupDur += tr.WriteDuration
			}
			stages[StageTTFB] = StageMeasurement{
				Stage:      StageTTFB,
				Status:     ObsObserved,
				DurationMs: durationToMs(setupDur + tr.TTFBDuration),
			}
		} else {
			stages[StageTTFB] = StageMeasurement{
				Stage:      StageTTFB,
				Status:     ObsObserved,
				DurationMs: durationToMs(tr.TTFBDuration),
				Note:       "final hop wait to first response byte",
			}
		}
	} else {
		stages[StageServerWait] = StageMeasurement{Stage: StageServerWait, Status: ObsUnobserved}
		stages[StageTTFB] = StageMeasurement{Stage: StageTTFB, Status: ObsUnobserved}
	}

	if isNoBodyResponse(finalMethod, res.Status) {
		stages[StageDownload] = StageMeasurement{Stage: StageDownload, Status: ObsNotApplicable, Note: "no response body"}
	} else if res.Truncated {
		stages[StageDownload] = StageMeasurement{
			Stage:      StageDownload,
			Status:     ObsIncomplete,
			DurationMs: durationToMs(tr.DownloadDuration),
			Note:       "response body truncated at limit",
		}
	} else {
		stages[StageDownload] = StageMeasurement{
			Stage:      StageDownload,
			Status:     ObsObserved,
			DurationMs: durationToMs(tr.DownloadDuration),
		}
	}

	if res.Truncated {
		stages[StageHopTotal] = StageMeasurement{Stage: StageHopTotal, Status: ObsIncomplete, DurationMs: durationToMs(totalDur)}
	} else if !multiHop {
		stages[StageHopTotal] = StageMeasurement{Stage: StageHopTotal, Status: ObsObserved, DurationMs: durationToMs(totalDur)}
	} else {
		stages[StageHopTotal] = StageMeasurement{Stage: StageHopTotal, Status: ObsUnobserved, Note: "multi-hop aggregate trace"}
	}

	var lims []string
	if reused {
		lims = append(lims, "connection reused: DNS, TCP connect, and TLS handshake did not occur and are not 0ms")
	}
	if tr.ProxyUsed != "" && tr.ProxyUsed != "direct" {
		lims = append(lims, fmt.Sprintf("routed via proxy (%s): proxy-side DNS and tunnel setup are not decomposable", redact(tr.ProxyUsed)))
	}
	if strings.HasPrefix(strings.ToUpper(res.Proto), "HTTP/2") {
		lims = append(lims, "HTTP/2 multiplexed stream")
	}

	hops = append(hops, HopTimeline{
		Hop:                   finalIdx,
		Method:                finalMethod,
		URL:                   finalURL,
		FromURL:               finalFrom,
		StatusCode:            res.Status,
		Proto:                 res.Proto,
		RemoteAddr:            redact(tr.RemoteAddr),
		ReusedConn:            tr.ReusedConn,
		ReusedKnown:           true,
		ProxyUsed:             redact(tr.ProxyUsed),
		OriginChanged:         finalOriginChanged,
		StrippedCredentials:   finalStripped,
		TLSVersion:            tr.TLSVersion,
		TLSCipherSuite:        tr.TLSCipherSuite,
		TLSServerName:         tr.TLSServerName,
		TLSInsecureSkipVerify: tr.TLSInsecureSkipVerify,
		CustomCAUsed:          tr.CustomCAUsed,
		ClientCertUsed:        tr.ClientCertUsed,
		Stages:                stages,
		Limitations:           lims,
	})

	return hops, notes
}
