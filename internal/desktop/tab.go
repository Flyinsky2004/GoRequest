package desktop

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
)

// Tab is one open editor: a request or a Markdown document. Each tab belongs
// to exactly one projectState through Owner, so that a background request
// result always updates the tab of the project that started it even when the
// user has switched projects.
type Tab struct {
	Kind  string // "request" or "doc"
	Title string
	Owner *projectState

	// request editing state
	Entry      *core.RequestEntry
	Method     string
	URL        string
	QueryRows  []*KVRow
	HeaderRows []*KVRow
	// KV batch-edit state (key:value text mode) per section.
	queryText  bool
	queryRaw   string
	headerText bool
	headerRaw  string
	kvErr      string
	// kvFocusID/kvFocusVal ask the next frame to focus a KV cell (Enter flow).
	kvFocusID  string
	kvFocusVal bool
	BodyIdx    int
	BodyText   string
	AuthMode   string
	AuthIdx    int
	AuthToken  string
	AuthUser   string
	AuthPass   string
	Timeout    string
	Section    int // 0 params, 1 headers, 2 body, 3 auth

	// doc editing state
	DocRel    string
	DocEditor *editor.State

	// body codeView editor (built lazily; rebuilt when the body type changes)
	bodyEditor   *editor.State
	bodyBuiltIdx int

	// shared
	Dirty    bool
	Baseline string
	// renaming/renameText drive in-place tab renaming (§4).
	renaming   bool
	renameText string
	// focusURL asks the next frame to focus the URL input (after a send, so the
	// user can tweak and resend without hunting for the field).
	focusURL bool

	// response
	Resp         *core.ExecResult
	RespErr      string
	RespRawIdx   int
	RespSection  int
	RespSize     float32
	Sending      bool
	cancel       context.CancelFunc
	respEditor   *editor.State
	respVersion  int
	respBuiltVer int
	respBuiltIdx int
	// respBuiltDegraded records whether the cached response editor was built in
	// the large-response plain-text mode, so a size change rebuilds it.
	respBuiltDegraded bool

	// Response find (§7): the small in-panel find bar. respFindQuery drives the
	// editor's native find, and respFindOpen toggles the bar via ⌘F.
	respFindOpen  bool
	respFindQuery string
	respFindFocus bool
	respFindIdx   int
	respFindJump  bool
}

// requestFromTab builds a core.Request from the tab's edited fields.
func requestFromTab(tab *Tab) (*core.Request, error) {
	if tab.Entry == nil || tab.Entry.Request == nil {
		return nil, fmt.Errorf("no request")
	}
	base := *tab.Entry.Request
	query, err := kvFromRows(tab.QueryRows)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	headers, err := kvFromRows(tab.HeaderRows)
	if err != nil {
		return nil, fmt.Errorf("headers: %w", err)
	}
	httpDef := &core.HTTPDef{
		Method:  tab.Method,
		URL:     tab.URL,
		Query:   query,
		Headers: headers,
	}
	if strings.TrimSpace(tab.BodyText) != "" {
		body, err := core.ParseBodyText(bodyTypes[tab.BodyIdx], tab.BodyText)
		if err != nil {
			return nil, err
		}
		httpDef.Body = body
	}
	base.HTTP = httpDef
	base.Timeout = strings.TrimSpace(tab.Timeout)
	switch tab.AuthMode {
	case "bearer":
		base.Auth = &core.AuthRef{Mode: "inline", Inline: &core.AuthScheme{SchemaVersion: 1, Type: "bearer", Token: tab.AuthToken}}
	case "basic":
		base.Auth = &core.AuthRef{Mode: "inline", Inline: &core.AuthScheme{SchemaVersion: 1, Type: "basic", Username: tab.AuthUser, Password: tab.AuthPass}}
	default:
		base.Auth = &core.AuthRef{Mode: "none"}
	}
	return &base, nil
}

// fillAuth copies the effective auth scheme of a request into a fresh tab.
func fillAuth(p *core.Project, tab *Tab, req *core.Request) {
	scheme, err := p.AuthFor(req)
	if err != nil || scheme == nil {
		tab.AuthMode = "none"
		tab.AuthIdx = 0
		return
	}
	switch scheme.Type {
	case "bearer":
		tab.AuthMode = "bearer"
		tab.AuthToken = scheme.Token
	case "basic":
		tab.AuthMode = "basic"
		tab.AuthUser = scheme.Username
		tab.AuthPass = scheme.Password
	default:
		tab.AuthMode = "none"
	}
	tab.AuthIdx = indexOf(authModes, tab.AuthMode)
}

// --- helpers -------------------------------------------------------------

// allRequests returns every request of a project, including ones created this
// session (which are not yet in the loaded collection tree), sorted by name.
func allRequests(p *core.Project) []*core.RequestEntry {
	out := make([]*core.RequestEntry, 0, len(p.Requests))
	for _, e := range p.Requests {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Request.Name == out[j].Request.Name {
			return out[i].RelPath < out[j].RelPath
		}
		return out[i].Request.Name < out[j].Request.Name
	})
	return out
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0
}
