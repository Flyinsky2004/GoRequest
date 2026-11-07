package docmention

import (
	"fmt"
	"strings"
	"testing"

	"gorequest/internal/core"
)

func testProject() *core.Project {
	entry := func(id, name, method, url, rel string) *core.RequestEntry {
		return &core.RequestEntry{
			RelPath: rel,
			Request: &core.Request{ID: id, Name: name, HTTP: &core.HTTPDef{Method: method, URL: url}},
		}
	}
	return &core.Project{
		Requests: map[string]*core.RequestEntry{
			"auth.login":  entry("auth.login", "用户登录", "POST", "/users/login", "collections/auth/login.request.yaml"),
			"auth.logout": entry("auth.logout", "用户登出", "POST", "/users/logout", "collections/auth/logout.request.yaml"),
			"users.me":    entry("users.me", "当前用户", "GET", "/users/me", "collections/users/me.request.yaml"),
			"user.login":  entry("user.login", "user login", "PUT", "/v1/session", "collections/users/login.request.yaml"),
			"health":      entry("health", "健康检查", "GET", "{{baseUrl}}/health", "collections/health.request.yaml"),
			"用户接口":        entry("用户接口", "中文 ID", "GET", "/bad", "collections/bad.request.yaml"),
		},
		Collections: []*core.CollectionEntry{
			{RelPath: "collections/auth", Name: "auth"},
			{RelPath: "collections/users", Name: "users"},
		},
	}
}

func TestCollectLabelsFoldersAndValidity(t *testing.T) {
	reqs := Collect(testProject())
	byID := map[string]Request{}
	for _, r := range reqs {
		byID[r.ID] = r
	}
	cases := []struct {
		id, label, folder, detail string
		valid                     bool
	}{
		{"auth.login", "@auth/用户登录", "auth", "POST /users/login", true},
		{"users.me", "@users/当前用户", "users", "GET /users/me", true},
		{"health", "@健康检查", "", "GET {{baseUrl}}/health", true},
		{"用户接口", "@中文 ID", "", "GET /bad", false},
	}
	for _, c := range cases {
		r, ok := byID[c.id]
		if !ok {
			t.Fatalf("request %q missing from Collect", c.id)
		}
		if r.Label() != c.label || r.Folder != c.folder || r.Detail() != c.detail || r.Valid != c.valid {
			t.Errorf("%s: got label=%q folder=%q detail=%q valid=%v", c.id, r.Label(), r.Folder, r.Detail(), r.Valid)
		}
	}
	// Collect is stable: folder, then name, then ID.
	for i := 1; i < len(reqs); i++ {
		if lessRequest(reqs[i], reqs[i-1]) {
			t.Fatalf("Collect order not stable at %d: %+v after %+v", i, reqs[i], reqs[i-1])
		}
	}
}

func TestSearchMatchesFolderNameIDMethodAndURL(t *testing.T) {
	reqs := Collect(testProject())
	ids := func(query string) []string {
		var out []string
		for _, m := range Search(reqs, query) {
			out = append(out, m.ID)
		}
		return out
	}
	cases := []struct {
		query string
		want  []string
	}{
		{"用户", []string{"auth.login", "auth.logout", "users.me", "用户接口"}},
		{"auth", []string{"auth.login", "auth.logout"}},
		{"users.me", []string{"users.me"}},
		{"POST", []string{"auth.login", "auth.logout"}},
		{"/v1/session", []string{"user.login"}},
		{"ulg", []string{"user.login"}}, // subsequence of "user login"
	}
	for _, c := range cases {
		got := ids(c.query)
		if !sameSet(got, c.want) {
			t.Errorf("Search(%q) = %v, want the set %v", c.query, got, c.want)
		}
	}
	if got := ids("no-such-interface"); len(got) != 0 {
		t.Errorf("Search of a missing query returned %v", got)
	}
	if got := ids(""); len(got) != len(reqs) {
		t.Errorf("empty query returned %d of %d requests", len(got), len(reqs))
	}
}

func TestLinkUsesStableIDAndEscapesLabel(t *testing.T) {
	reqs := Collect(testProject())
	byID := map[string]Request{}
	for _, r := range reqs {
		byID[r.ID] = r
	}
	link, ok := byID["auth.login"].Link()
	if !ok {
		t.Fatal("auth.login should be referenceable")
	}
	want := "[@auth/用户登录](gorequest://request/auth.login)"
	if link != want {
		t.Fatalf("link = %q, want %q", link, want)
	}
	if _, ok := byID["用户接口"].Link(); ok {
		t.Fatal("an ID outside the URI grammar must not produce a link")
	}
	escaped, ok := Request{ID: "x.y", Name: `a[b]\c`}.Link()
	if !ok {
		t.Fatal("x.y should be referenceable")
	}
	if want := `[@a\[b\]\\c](gorequest://request/x.y)`; escaped != want {
		t.Fatalf("escaped link = %q, want %q", escaped, want)
	}
}

func TestFindTrigger(t *testing.T) {
	cases := []struct {
		src  string
		want string // query, empty means no trigger
		ok   bool
	}{
		{"hello @au", "au", true},
		{"(@au", "au", true},
		{"中文，@用户", "用户", true},
		{"@auth/用户", "auth/用户", true},
		{"user@example.com", "", false},
		{"你好@auth", "", false},
		{`\@auth`, "", false},
		{"```\n@auth\n```", "", false},
		{"`@auth`", "", false},
		{"[x](@auth", "", false},
		{"<https://@auth", "", false},
		{"@", "", true},
	}
	for _, c := range cases {
		got, ok := FindTrigger(c.src, len(c.src))
		if ok != c.ok || (ok && got.Query != c.want) {
			t.Errorf("FindTrigger(%q) = %+v, %v; want query %q ok=%v", c.src, got, ok, c.want, c.ok)
		}
	}
}

func TestScanRequestLinksResolvesAndSpans(t *testing.T) {
	src := "A [@auth/用户登录](gorequest://request/auth.login) and " +
		"[gone](gorequest://request/gone) and [bad](gorequest://request/中文).\n\n" +
		"```\n[code](gorequest://request/auth.login)\n```\n"
	refs := ScanRequestLinks(testProject(), src)
	if len(refs) != 3 {
		t.Fatalf("got %d links, want 3: %+v", len(refs), refs)
	}
	if refs[0].ID != "auth.login" || !refs[0].Found || refs[0].Malformed {
		t.Errorf("valid link = %+v", refs[0])
	}
	if refs[0].Label != "@auth/用户登录" {
		t.Errorf("label = %q", refs[0].Label)
	}
	if got := src[refs[0].DestStart:refs[0].DestEnd]; got != "gorequest://request/auth.login" {
		t.Errorf("destination span = %q", got)
	}
	if refs[0].Start != strings.Index(src, "[@auth") {
		t.Errorf("link start = %d", refs[0].Start)
	}
	if refs[1].ID != "gone" || refs[1].Found || refs[1].Malformed {
		t.Errorf("missing link = %+v", refs[1])
	}
	if !refs[2].Malformed || refs[2].Found {
		t.Errorf("malformed link = %+v", refs[2])
	}
}

func TestOffsetOfLineCol(t *testing.T) {
	src := "one\ntwo\nthree"
	if got := offsetOfLineCol(src, 2, 2); got != 5 {
		t.Fatalf("offset = %d, want 5", got)
	}
	if got := offsetOfLineCol(src, 99, 1); got != len(src) {
		t.Fatalf("past the end = %d", got)
	}
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, id := range got {
		seen[id]++
	}
	for _, id := range want {
		seen[id]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestSearchCoversWholeCatalogue(t *testing.T) {
	p := &core.Project{Requests: map[string]*core.RequestEntry{}}
	for i := 0; i < 150; i++ {
		id := fmt.Sprintf("req.%03d", i)
		p.Requests[id] = &core.RequestEntry{
			RelPath: "collections/" + id + ".request.yaml",
			Request: &core.Request{ID: id, Name: fmt.Sprintf("接口 %03d", i), HTTP: &core.HTTPDef{Method: "GET", URL: "/x"}},
		}
	}
	matches := Search(Collect(p), "接口")
	if len(matches) != 150 {
		t.Fatalf("Search returned %d of 150 matches; the catalogue must stay searchable", len(matches))
	}
}

func TestEscapeUnescapeLabelRoundTrip(t *testing.T) {
	raw := "@a[b]\\c*d_e`<f>"
	escaped := EscapeLabel(raw)
	if escaped == raw {
		t.Fatalf("label %q was not escaped", raw)
	}
	if got := UnescapeLabel(escaped); got != raw {
		t.Fatalf("round trip = %q, want %q (escaped %q)", got, raw, escaped)
	}
}
