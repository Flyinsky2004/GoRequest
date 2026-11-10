package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"weak"

	"gorequest/internal/core"
	"gorequest/internal/stream"
	gows "gorequest/internal/websocket"
)

// gql_subscription.go owns the graphql-transport-ws subscription lifecycle for
// a GraphQL tab. A subscription starts only when the user presses Send with a
// selected subscription operation; there is no silent reconnect and completion
// is reported as a distinct fact, never as HTTP success.

const (
	gqlSubMessageLimit = 200
	gqlSubPreviewBytes = 2048
)

// gqlSubMessage is one retained subscription event for the lower pane.
type gqlSubMessage struct {
	Type    string
	Seq     uint64
	Preview string
	IsError bool
}

// gqlSubscriptionState is UI-thread-owned connection state. Background
// callbacks land here only through Window.Update and a generation check.
type gqlSubscriptionState struct {
	generation  uint64
	running     bool
	state       string // connecting | open | completed | canceled | error
	notice      string
	messages    []gqlSubMessage
	truncated   bool
	closeCode   int
	closeReason string

	cancel context.CancelFunc
	sub    *gows.Subscription
	op     *gows.SubscriptionOperation
	red    *core.Redactor

	confirmChecked bool
	confirmAsked   bool
	promptFacts    string
}

// gqlTeardownSubscription releases a tab's live subscription and invalidates
// stale callbacks. Idempotent and safe on tabs without a subscription.
func gqlTeardownSubscription(tab *Tab) {
	if tab == nil || tab.gql == nil || tab.gql.sub == nil {
		return
	}
	st := tab.gql.sub
	st.generation++
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	if st.sub != nil {
		st.sub.Terminate()
	}
	st.running = false
	st.op = nil
}

// gqlSubStart validates the tab, resolves the environment and starts the
// subscription off the UI thread.
func (a *App) gqlSubStart(tab *Tab) {
	st := tab.ensureGQLState()
	if st == nil || st.sub != nil && st.sub.running {
		return
	}
	m := a.gqlMsg()
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	if ps == nil || ps.project == nil {
		st.notice = m.SubNoProject
		return
	}
	st.gqlRefreshAnalysis()
	opInfo := st.selected
	if opInfo.Type != "subscription" {
		st.notice = m.SubSelectSubscription
		return
	}
	doc := st.document
	if st.docEditor != nil {
		doc = st.docEditor.Buffer.String()
	}
	if strings.TrimSpace(doc) == "" {
		st.notice = m.SubNoDocument
		return
	}
	entry := tab.Entry
	vars, _, err := ps.project.ResolveVars(entry, ps.currentEnv())
	if err != nil {
		st.notice = fmt.Sprintf(m.SubResolveFailedFmt, err)
		return
	}
	resolver := core.NewResolver(vars, ps.secrets)
	red := core.NewRedactor()

	endpoint, _, err := wsResolveTemplate(resolver, m.SubNoURL, strings.TrimSpace(tab.URL), red)
	if err != nil || strings.TrimSpace(endpoint) == "" {
		st.notice = m.SubNoURL
		return
	}
	wsURL, err := graphQLWSURL(endpoint)
	if err != nil {
		st.notice = err.Error()
		return
	}

	req, err := requestFromTab(tab)
	if err != nil {
		st.notice = err.Error()
		return
	}
	var pairs [][2]string
	hasCreds := false
	if req.HTTP != nil {
		for _, kv := range req.HTTP.Headers {
			if !kv.IsEnabled() {
				continue
			}
			name := strings.TrimSpace(kv.Name)
			value, sensitive, err := wsResolveTemplate(resolver, name, kv.Value, red)
			if err != nil {
				st.notice = fmt.Sprintf(m.SubResolveFailedFmt, err)
				return
			}
			pairs = append(pairs, [2]string{name, value})
			if sensitive || gowsHeaderCarriesCredentials(name) {
				hasCreds = true
			}
		}
	}
	wsEndpoint, authCreds, err := wsApplyAuth(ps, tab, req, vars, resolver, red, wsURL, &pairs)
	if err != nil {
		st.notice = fmt.Sprintf(m.SubResolveFailedFmt, err)
		return
	}
	wsURL = wsEndpoint
	hasCreds = hasCreds || authCreds

	variables, varSecrets, err := core.ResolveGraphQLVariables(st.variables, resolver, red)
	if err != nil {
		st.notice = red.RedactError(err).Error()
		return
	}
	varsSensitive := len(varSecrets) > 0

	var total time.Duration
	if raw := strings.TrimSpace(tab.Timeout); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			st.notice = m.SubBadTimeout
			return
		}
		total = d
	}
	net := a.buildExecuteOptionsForTab(ps, tab, req, nil, red)
	var jar = net.CookieJar
	if net.DisableCookies {
		jar = nil
	}
	if jar != nil {
		jar.RegisterSecrets(red)
	}

	opts := gows.Options{
		URL:              wsURL,
		Headers:          pairs,
		Subprotocols:     []string{gows.GraphQLTransportWSSubprotocol},
		Deadlines:        stream.DeadlinePolicy{Connect: net.Timeouts.DialTimeout, Handshake: net.Timeouts.TLSHandshakeTimeout, Message: gows.DefaultMessageTimeout, Total: total},
		Proxy:            net.Proxy,
		TLS:              net.TLS,
		TargetPolicy:     net.TargetPolicy,
		HasTargetPolicy:  net.HasTargetPolicy,
		EnvID:            net.EnvID,
		Interactive:      true,
		Confirmed:        st.sub != nil && st.sub.confirmChecked,
		HasCredentials:   hasCreds,
		Secrets:          net.Secrets,
		LocalFiles:       net.LocalFiles,
		ProjectRoot:      net.ProjectRoot,
		Redactor:         red,
		CookieJar:        jar,
		InboundSensitive: true,
	}

	// Preflight the exact target so a confirmation is visible before an attempt.
	if decision, perr := gows.PromptRequiredBy(&opts); perr == nil && decision != nil &&
		decision.Outcome == core.DecisionConfirmRequired && (st.sub == nil || !st.sub.confirmChecked) {
		if st.sub == nil {
			st.sub = &gqlSubscriptionState{}
		}
		st.sub.confirmAsked = true
		st.sub.notice = m.SubConfirmNeeded
		st.sub.promptFacts = strings.Join(decision.Reasons, "; ")
		return
	}

	prevGen := uint64(0)
	if st.sub != nil {
		prevGen = st.sub.generation
		gqlTeardownSubscription(tab)
	}
	opName := strings.TrimSpace(st.operationName)
	ctx, cancel := context.WithCancel(context.Background())
	state := &gqlSubscriptionState{
		generation: prevGen + 1,
		running:    true,
		state:      "connecting",
		notice:     m.SubConnecting,
		cancel:     cancel,
		red:        red,
	}
	state.confirmChecked = false
	st.sub = state
	gen := state.generation
	win := a.win
	key := weak.Make(a)

	go func() {
		sub, err := gows.ConnectSubscription(ctx, opts, gows.SubscriptionConfig{})
		if err != nil {
			cancel()
			a.gqlSubPost(win, key, tab, ps, gen, func(d *gqlSubscriptionState) {
				d.running = false
				d.state = "error"
				d.notice = fmt.Sprintf(m.SubConnectFailedFmt, red.RedactError(err).Error())
			})
			return
		}
		op, err := sub.Subscribe(ctx, doc, opName, variables, varsSensitive)
		if err != nil {
			_ = sub.Close(1000, "subscribe failed")
			cancel()
			a.gqlSubPost(win, key, tab, ps, gen, func(d *gqlSubscriptionState) {
				d.running = false
				d.state = "error"
				d.notice = fmt.Sprintf(m.SubConnectFailedFmt, red.RedactError(err).Error())
			})
			return
		}
		a.gqlSubPost(win, key, tab, ps, gen, func(d *gqlSubscriptionState) {
			d.state = "open"
			d.notice = m.SubOpen
			d.sub = sub
			d.op = op
			d.closeCode = sub.Snapshot().Transport.CloseCode
		})

		terminal := ""
		for {
			msg, err := op.Next(ctx)
			if err != nil {
				switch {
				case errors.Is(err, gows.ErrSubscriptionComplete):
					terminal = "completed"
				case errors.Is(err, context.Canceled):
					terminal = "canceled"
				default:
					terminal = "error"
					preview := red.RedactString(err.Error())
					a.gqlSubPost(win, key, tab, ps, gen, func(d *gqlSubscriptionState) {
						d.notice = fmt.Sprintf(m.SubErrorFmt, preview)
					})
				}
				break
			}
			preview := graphQLSubscriptionPreview(msg.Payload, red)
			a.gqlSubPost(win, key, tab, ps, gen, func(d *gqlSubscriptionState) {
				d.appendMessage(gqlSubMessage{
					Type:    msg.Type,
					Seq:     msg.Seq,
					Preview: preview,
					IsError: msg.Type == "error",
				})
			})
		}
		_ = sub.Close(1000, "subscription complete")
		a.gqlSubPost(win, key, tab, ps, gen, func(d *gqlSubscriptionState) {
			d.running = false
			d.state = terminal
			d.sub = sub
			d.op = nil
			d.cancel = nil
			d.closeCode = sub.Snapshot().Transport.CloseCode
			d.closeReason = sub.Snapshot().Transport.CloseReason
			if terminal == "completed" {
				d.notice = m.SubCompleted
			} else if terminal == "canceled" {
				d.notice = m.SubCanceled
			}
		})
	}()
}

// gqlSubPost delivers a state mutation to the owning window thread with the
// same identity/generation guards as the WebSocket workspace.
func (a *App) gqlSubPost(win interface {
	IsDestroyed() bool
	Update(func())
}, key weak.Pointer[App], tab *Tab, ps *projectState, gen uint64, fn func(*gqlSubscriptionState)) {
	if win == nil || win.IsDestroyed() {
		return
	}
	win.Update(func() {
		owner := key.Value()
		if owner == nil || owner.closed {
			return
		}
		d := tab.gql
		if d == nil || d.sub == nil || d.sub.generation != gen {
			return
		}
		if ps != nil && ps.indexOfTab(tab) < 0 {
			return
		}
		fn(d.sub)
	})
}

// appendMessage retains a bounded event list and reports truncation.
func (d *gqlSubscriptionState) appendMessage(msg gqlSubMessage) {
	if len(d.messages) >= gqlSubMessageLimit {
		d.messages = append([]gqlSubMessage(nil), d.messages[1:]...)
		d.truncated = true
	}
	d.messages = append(d.messages, msg)
}

// gqlSubCancel stops the live subscription without blocking the UI thread.
func (a *App) gqlSubCancel(tab *Tab) {
	if tab == nil || tab.gql == nil || tab.gql.sub == nil {
		return
	}
	d := tab.gql.sub
	op := d.op
	sub := d.sub
	cancel := d.cancel
	d.running = false
	d.state = "canceled"
	d.notice = a.gqlMsg().SubCanceled
	d.cancel = nil
	go func() {
		if op != nil {
			_ = op.Cancel()
		}
		if sub != nil {
			_ = sub.Close(1000, "user canceled")
		}
		if cancel != nil {
			cancel()
		}
	}()
}

// graphQLWSURL converts an http(s) endpoint to its ws(s) equivalent, keeping
// an existing ws(s) URL untouched and rejecting anything else.
func graphQLWSURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid subscription URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("subscription URL must use http(s) or ws(s)")
	}
	return u.String(), nil
}

// graphQLSubscriptionPreview redacts and bounds the retained message preview.
func graphQLSubscriptionPreview(payload []byte, red *core.Redactor) string {
	s := string(payload)
	if red != nil {
		s = red.RedactString(s)
	}
	if len(s) > gqlSubPreviewBytes {
		cut := gqlSubPreviewBytes
		for cut > 0 && s[cut]&0xC0 == 0x80 {
			cut--
		}
		return s[:cut] + "…"
	}
	return s
}
