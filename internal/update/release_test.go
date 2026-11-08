package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeHTTP serves canned responses keyed by full request URL. It never opens a
// socket, so the whole suite runs offline.
type fakeHTTP struct {
	responses map[string]fakeResponse
	requests  []string
}

type fakeResponse struct {
	status int
	body   string
	header http.Header
}

func (f *fakeHTTP) Do(req *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, req.URL.String())
	resp, ok := f.responses[req.URL.String()]
	if !ok {
		return nil, fmt.Errorf("unexpected request %s", req.URL.String())
	}
	h := resp.header
	if h == nil {
		h = http.Header{}
	}
	body := resp.body
	return &http.Response{
		StatusCode:    resp.status,
		Status:        fmt.Sprintf("%d %s", resp.status, http.StatusText(resp.status)),
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

func TestParseVersion(t *testing.T) {
	valid := []struct {
		in   string
		want string
	}{
		{"1.2.3", "1.2.3"},
		{"v1.2.3", "1.2.3"},
		{"V0.0.2-dev", "0.0.2-dev"},
		{"1.0.0-beta.1", "1.0.0-beta.1"},
		{"1.0.0+build.5", "1.0.0+build.5"},
		{" 1.2.3 ", "1.2.3"},
	}
	for _, tc := range valid {
		v, err := ParseVersion(tc.in)
		if err != nil {
			t.Errorf("ParseVersion(%q): %v", tc.in, err)
			continue
		}
		if got := v.String(); got != tc.want {
			t.Errorf("ParseVersion(%q).String() = %q, want %q", tc.in, got, tc.want)
		}
	}
	invalid := []string{"", "1.2", "1.2.3.4", "v", "a.b.c", "1.2.-3", "1.2.3-", "1.2.3-01", "1.2.3+bad_meta"}
	for _, in := range invalid {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) should fail", in)
		}
	}
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha", 1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"1.0.0+build.1", "1.0.0+build.2", 0},
		{"0.0.2-dev", "0.0.1-dev", 1},
	}
	for _, tc := range cases {
		a, err := ParseVersion(tc.a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ParseVersion(tc.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := Compare(a, b); got != tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsDevelopmentVersion(t *testing.T) {
	for _, s := range []string{"", "  ", "dev", "Development", "snapshot", "0.1.0 (MVP)", "unknown"} {
		if !IsDevelopmentVersion(s) {
			t.Errorf("IsDevelopmentVersion(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"0.0.1-dev", "v1.2.3", "1.0.0-beta.1"} {
		if IsDevelopmentVersion(s) {
			t.Errorf("IsDevelopmentVersion(%q) = true, want false", s)
		}
	}
}

func TestSelectAsset(t *testing.T) {
	assets := []Asset{
		{Name: "gorequest-0.0.2-arm64.dmg"},
		{Name: "gorequest-0.0.2-amd64.dmg"},
		{Name: "gorequest-setup-0.0.2-amd64.exe"},
		{Name: "gorequest-setup-0.0.2-arm64.exe"},
		{Name: "gorequest-0.0.2-windows-amd64.zip"},
		{Name: "gorequest-0.0.2-windows-arm64.zip"},
		{Name: "SHA256SUMS"},
	}
	cases := []struct {
		goos, goarch, want string
	}{
		{"darwin", "arm64", "gorequest-0.0.2-arm64.dmg"},
		{"darwin", "amd64", "gorequest-0.0.2-amd64.dmg"},
		{"windows", "amd64", "gorequest-setup-0.0.2-amd64.exe"},
		{"windows", "arm64", "gorequest-setup-0.0.2-arm64.exe"},
		{"linux", "amd64", ""},
		{"darwin", "386", ""},
	}
	for _, tc := range cases {
		got, ok := SelectAsset(assets, tc.goos, tc.goarch)
		if tc.want == "" {
			if ok {
				t.Errorf("SelectAsset(%s/%s) = %q, want none", tc.goos, tc.goarch, got.Name)
			}
			continue
		}
		if !ok || got.Name != tc.want {
			t.Errorf("SelectAsset(%s/%s) = %q, %v; want %q", tc.goos, tc.goarch, got.Name, ok, tc.want)
		}
	}

	// A portable .zip is not an installer this flow can start; report no
	// installer instead of pretending the archive was launched.
	if got, ok := SelectAsset([]Asset{{Name: "gorequest-1.0.0-windows-amd64.zip"}}, "windows", "amd64"); ok {
		t.Fatalf("zip-only Windows release selected %q as an installer", got.Name)
	}
}

func releaseJSON(tag string, prerelease bool, assets ...string) string {
	var list []string
	for _, a := range assets {
		list = append(list, fmt.Sprintf(`{"name":%q,"browser_download_url":"https://example.test/%s","size":10}`, a, a))
	}
	return fmt.Sprintf(`[{"tag_name":%q,"name":%q,"body":"notes","draft":false,"prerelease":%v,"published_at":"2026-11-01T00:00:00Z","assets":[%s]}]`,
		tag, tag, prerelease, strings.Join(list, ","))
}

func newTestChecker(client HTTPClient, goos, goarch string) *Checker {
	return &Checker{Client: client, APIBase: "https://api.test/releases", GOOS: goos, GOARCH: goarch}
}

func TestCheckDevelopmentMakesNoRequest(t *testing.T) {
	f := &fakeHTTP{}
	res, err := newTestChecker(f, "darwin", "arm64").Check(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDevelopment {
		t.Fatalf("status = %v, want development", res.Status)
	}
	if len(f.requests) != 0 {
		t.Fatalf("development check made requests: %v", f.requests)
	}
}

func TestCheckUpToDateAndNoDowngrade(t *testing.T) {
	url := "https://api.test/releases?per_page=100&page=1"
	f := &fakeHTTP{responses: map[string]fakeResponse{
		url: {status: 200, body: releaseJSON("0.0.2-dev", false, "gorequest-0.0.2-arm64.dmg")},
	}}
	res, err := newTestChecker(f, "darwin", "arm64").Check(context.Background(), "0.0.2-dev")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUpToDate {
		t.Fatalf("status = %v, want up-to-date", res.Status)
	}

	f2 := &fakeHTTP{responses: map[string]fakeResponse{
		url: {status: 200, body: releaseJSON("0.0.1-dev", false, "gorequest-0.0.1-arm64.dmg")},
	}}
	res, err = newTestChecker(f2, "darwin", "arm64").Check(context.Background(), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUpToDate || res.Candidate != nil {
		t.Fatalf("older release must not be offered: %+v", res)
	}
}

func TestCheckPicksHighestNewerWithAsset(t *testing.T) {
	url := "https://api.test/releases?per_page=100&page=1"
	body := `[
		{"tag_name":"0.0.4-dev","draft":false,"prerelease":false,"assets":[{"name":"gorequest-0.0.4-amd64.dmg","browser_download_url":"https://example.test/4"}]},
		{"tag_name":"0.0.3-dev","draft":false,"prerelease":false,"assets":[{"name":"gorequest-0.0.3-amd64.dmg","browser_download_url":"https://example.test/3"}]},
		{"tag_name":"0.0.2-dev","draft":false,"prerelease":false,"assets":[{"name":"gorequest-0.0.2-arm64.dmg","browser_download_url":"https://example.test/2"}]}
	]`
	f := &fakeHTTP{responses: map[string]fakeResponse{url: {status: 200, body: body}}}
	res, err := newTestChecker(f, "darwin", "arm64").Check(context.Background(), "0.0.1-dev")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUpdateAvailable || res.Candidate == nil {
		t.Fatalf("want update available, got %+v", res)
	}
	// 0.0.4 has no arm64 asset, so the highest version that does is 0.0.2.
	if got := res.Candidate.Release.Version.String(); got != "0.0.2-dev" {
		t.Fatalf("candidate = %s, want 0.0.2-dev", got)
	}
	if res.Candidate.Asset.Name != "gorequest-0.0.2-arm64.dmg" {
		t.Fatalf("asset = %q", res.Candidate.Asset.Name)
	}
}

func TestCheckSkipsDraftAndPrereleaseForStable(t *testing.T) {
	url := "https://api.test/releases?per_page=100&page=1"
	body := `[
		{"tag_name":"2.0.0","draft":true,"prerelease":false,"assets":[{"name":"gorequest-2.0.0-arm64.dmg","browser_download_url":"https://example.test/2"}]},
		{"tag_name":"1.5.0-beta.1","draft":false,"prerelease":true,"assets":[{"name":"gorequest-1.5.0-beta.1-arm64.dmg","browser_download_url":"https://example.test/b"}]},
		{"tag_name":"1.1.0","draft":false,"prerelease":false,"assets":[{"name":"gorequest-1.1.0-arm64.dmg","browser_download_url":"https://example.test/1"}]}
	]`
	f := &fakeHTTP{responses: map[string]fakeResponse{url: {status: 200, body: body}}}
	res, err := newTestChecker(f, "darwin", "arm64").Check(context.Background(), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidate == nil || res.Candidate.Release.Version.String() != "1.1.0" {
		t.Fatalf("stable check picked %+v, want 1.1.0", res.Candidate)
	}
}

func TestCheckPrereleaseChannel(t *testing.T) {
	url := "https://api.test/releases?per_page=100&page=1"
	body := `[{"tag_name":"1.5.0-beta.2","draft":false,"prerelease":true,"assets":[{"name":"gorequest-1.5.0-beta.2-arm64.dmg","browser_download_url":"https://example.test/b"}]}]`
	f := &fakeHTTP{responses: map[string]fakeResponse{url: {status: 200, body: body}}}
	res, err := newTestChecker(f, "darwin", "arm64").Check(context.Background(), "1.5.0-beta.1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidate == nil || res.Candidate.Release.Version.String() != "1.5.0-beta.2" {
		t.Fatalf("prerelease channel picked %+v", res.Candidate)
	}
}

func TestCheckPagination(t *testing.T) {
	page1 := "https://api.test/releases?per_page=2&page=1"
	page2 := "https://api.test/releases?per_page=2&page=2"
	firstPage := `[
		{"tag_name":"0.0.2-dev","draft":false,"prerelease":false,"assets":[{"name":"gorequest-0.0.2-arm64.dmg","browser_download_url":"https://example.test/2"}]},
		{"tag_name":"0.0.1-dev","draft":false,"prerelease":false,"assets":[{"name":"gorequest-0.0.1-arm64.dmg","browser_download_url":"https://example.test/1"}]}
	]`
	f := &fakeHTTP{responses: map[string]fakeResponse{
		page1: {status: 200, body: firstPage},
		page2: {status: 200, body: releaseJSON("0.0.3-dev", false, "gorequest-0.0.3-arm64.dmg")},
	}}
	c := newTestChecker(f, "darwin", "arm64")
	c.PerPage = 2
	res, err := c.Check(context.Background(), "0.0.1-dev")
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidate == nil || res.Candidate.Release.Version.String() != "0.0.3-dev" {
		t.Fatalf("pagination missed the newer release: %+v", res.Candidate)
	}
	if len(f.requests) != 2 {
		t.Fatalf("requests = %v, want two pages", f.requests)
	}
}

func TestCheckNoRelease(t *testing.T) {
	url := "https://api.test/releases?per_page=100&page=1"
	f := &fakeHTTP{responses: map[string]fakeResponse{url: {status: 200, body: `[]`}}}
	res, err := newTestChecker(f, "darwin", "arm64").Check(context.Background(), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusNoRelease {
		t.Fatalf("status = %v, want no-release", res.Status)
	}
}

func TestCheckErrors(t *testing.T) {
	url := "https://api.test/releases?per_page=100&page=1"
	cases := []struct {
		name string
		resp fakeResponse
		kind ErrorKind
	}{
		{"rate limit", fakeResponse{status: 403, body: `{"message":"API rate limit exceeded"}`, header: http.Header{"X-Ratelimit-Remaining": {"0"}}}, ErrorRateLimit},
		{"not found", fakeResponse{status: 404, body: `{}`}, ErrorNoRelease},
		{"server error", fakeResponse{status: 500, body: `{}`}, ErrorHTTP},
		{"bad json", fakeResponse{status: 200, body: `not json`}, ErrorHTTP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeHTTP{responses: map[string]fakeResponse{url: tc.resp}}
			_, err := newTestChecker(f, "darwin", "arm64").Check(context.Background(), "1.0.0")
			if err == nil {
				t.Fatal("expected error")
			}
			if kind, ok := KindOf(err); !ok || kind != tc.kind {
				t.Fatalf("kind = %v, %v; want %v (err=%v)", kind, ok, tc.kind, err)
			}
		})
	}
}

func TestCheckNoAsset(t *testing.T) {
	url := "https://api.test/releases?per_page=100&page=1"
	f := &fakeHTTP{responses: map[string]fakeResponse{
		url: {status: 200, body: releaseJSON("0.0.2-dev", false, "gorequest-0.0.2-amd64.dmg")},
	}}
	_, err := newTestChecker(f, "darwin", "arm64").Check(context.Background(), "0.0.1-dev")
	if kind, ok := KindOf(err); !ok || kind != ErrorNoAsset {
		t.Fatalf("kind = %v, %v; want no-asset (err=%v)", kind, ok, err)
	}
}

func TestCheckNetworkErrorAndCancel(t *testing.T) {
	c := &Checker{Client: errorClient{err: errors.New("offline")}, APIBase: "https://api.test/releases", GOOS: "darwin", GOARCH: "arm64"}
	_, err := c.Check(context.Background(), "1.0.0")
	if kind, ok := KindOf(err); !ok || kind != ErrorNetwork {
		t.Fatalf("kind = %v, %v; want network (err=%v)", kind, ok, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := &Checker{Client: contextClient{}, APIBase: "https://api.test/releases", GOOS: "darwin", GOARCH: "arm64"}
	_, err = canceled.Check(ctx, "1.0.0")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled check error = %v, want context.Canceled", err)
	}
}

type errorClient struct{ err error }

func (e errorClient) Do(*http.Request) (*http.Response, error) { return nil, e.err }

// contextClient reports the request context's error, so a canceled check
// propagates context.Canceled exactly as net/http does.
type contextClient struct{}

func (contextClient) Do(req *http.Request) (*http.Response, error) { return nil, req.Context().Err() }
