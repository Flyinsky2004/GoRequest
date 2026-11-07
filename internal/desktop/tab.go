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
	// KV batch-edit state (key:value text mode & raw query mode) per section.
	queryText       bool
	queryRawEncoded bool
	queryRaw        string
	headerText      bool
	headerRaw       string
	kvErr           string
	// kvFocusID/kvFocusVal ask the next frame to focus a KV cell (Enter flow).
	kvFocusID    string
	kvFocusVal   bool
	BodyIdx      int
	BodyText     string
	AuthMode     string
	AuthIdx      int
	AuthRefID    string
	AuthToken    string
	AuthUser     string
	AuthPass     string
	AuthKeyName  string
	AuthKeyIn    string
	AuthKeyInIdx int
	AuthKeyValue string
	// authInlineVer/authInlineID/authKeyInOmitted/authPassthrough preserve exact
	// round-trip semantics when non-auth fields (such as URL) are edited.
	authInlineVer    int
	authInlineID     string
	authKeyInOmitted bool
	authPassthrough  *core.AuthRef
	authDiagKey      string
	authDiagItems    []authDiagItem
	Timeout          string
	Section          int // 0 params, 1 headers, 2 body, 3 auth

	// doc editing state
	DocRel    string
	DocEditor *editor.State
	// docBuiltGen is the theme generation the doc editor was themed with, so
	// an appearance switch restyles it without rebuilding the buffer.
	docBuiltGen int

	// body codeView editor and rich B03 body state (built lazily per request)
	bodyEditor   *editor.State
	bodyBuiltIdx int
	bodyState    *tabBodyState

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
	// respBuiltGen is the theme generation the cached response editor was
	// themed with; a newer generation restyles it in place.
	respBuiltGen int

	// Response find (§7): the small in-panel find bar. respFindQuery drives the
	// editor's native find, and respFindOpen toggles the bar via ⌘F.
	respFindOpen  bool
	respFindQuery string
	respFindFocus bool
	respFindIdx   int
	respFindJump  bool

	// sendSeq identifies the newest send so a late result from an older one
	// (for example after a forced reset) cannot overwrite the tab or status.
	sendSeq int
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
	body, err := bodyFromTab(tab)
	if err != nil {
		return nil, err
	}
	httpDef.Body = body
	base.HTTP = httpDef
	base.Timeout = strings.TrimSpace(tab.Timeout)
	authRef, err := authRefFromTab(tab)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	base.Auth = authRef
	return &base, nil
}

// fillAuth copies the request's own declared auth semantics (inherit, explicit
// none, scheme ref, or inline bearer/basic/apikey) into a tab without flattening
// refs into inline schemes or turning inherit/unknown schemes into "none".
func fillAuth(_ *core.Project, tab *Tab, req *core.Request) {
	tab.AuthMode = "inherit"
	tab.AuthIdx = 0
	tab.AuthRefID = ""
	tab.AuthToken = ""
	tab.AuthUser = ""
	tab.AuthPass = ""
	tab.AuthKeyName = ""
	tab.AuthKeyIn = "header"
	tab.AuthKeyInIdx = 0
	tab.AuthKeyValue = ""
	tab.authInlineVer = core.SupportedSchemaVersion
	tab.authInlineID = ""
	tab.authKeyInOmitted = false
	tab.authPassthrough = nil
	tab.authDiagKey = ""
	tab.authDiagItems = nil

	if req != nil && req.Auth != nil && req.Auth.Mode != "" {
		switch req.Auth.Mode {
		case "none":
			tab.AuthMode = "none"
		case "ref":
			tab.AuthMode = "ref"
			tab.AuthRefID = req.Auth.Ref
		case "inline":
			if req.Auth.Inline == nil {
				tab.AuthMode = "none"
			} else {
				s := req.Auth.Inline
				tab.authInlineVer = s.SchemaVersion
				tab.authInlineID = s.ID
				switch strings.ToLower(strings.TrimSpace(s.Type)) {
				case "", "none":
					tab.AuthMode = "none"
				case "bearer":
					tab.AuthMode = "bearer"
					tab.AuthToken = s.Token
				case "basic":
					tab.AuthMode = "basic"
					tab.AuthUser = s.Username
					tab.AuthPass = s.Password
				case "apikey":
					tab.AuthMode = "apikey"
					tab.AuthKeyName = s.Name
					tab.AuthKeyIn = core.NormalizeAPIKeyLocation(s.In)
					tab.AuthKeyInIdx = indexOf(core.APIKeyLocations, tab.AuthKeyIn)
					tab.authKeyInOmitted = strings.TrimSpace(s.In) == ""
					tab.AuthKeyValue = s.Value
				default:
					tab.AuthMode = strings.ToLower(strings.TrimSpace(s.Type))
					tab.authPassthrough = core.CloneAuthRef(req.Auth)
				}
			}
		default:
			tab.AuthMode = req.Auth.Mode
			tab.authPassthrough = core.CloneAuthRef(req.Auth)
		}
	}
	tab.AuthIdx = indexOf(requestAuthModes, tab.AuthMode)
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
