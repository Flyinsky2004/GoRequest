package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newHandler())
	t.Cleanup(srv.Close)
	return srv
}

func request(t *testing.T, client *http.Client, method, url string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, data
}

func decodeJSON(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %q: %v", data, err)
	}
}

func TestHealthAndMethodErrors(t *testing.T) {
	srv := newTestServer(t)

	resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	var got map[string]string
	decodeJSON(t, data, &got)
	if got["status"] != "ok" {
		t.Fatalf("health body = %v", got)
	}

	resp, data = request(t, srv.Client(), http.MethodPost, srv.URL+"/health", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /health status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", allow)
	}
	var e errorBody
	decodeJSON(t, data, &e)
	if e.Error == "" {
		t.Fatalf("405 body should be a JSON error, got %q", data)
	}
}

func TestLoginCompatibility(t *testing.T) {
	srv := newTestServer(t)

	resp, data := request(t, srv.Client(), http.MethodPost, srv.URL+"/auth/login",
		strings.NewReader(`{"username":"alice","password":"secret"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body %s", resp.StatusCode, data)
	}
	var ok struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	decodeJSON(t, data, &ok)
	if ok.Data.AccessToken != "demo-access-token-alice" {
		t.Fatalf("accessToken = %q", ok.Data.AccessToken)
	}

	cases := []struct {
		name   string
		method string
		body   string
		want   int
	}{
		{"missing field", http.MethodPost, `{"username":"alice"}`, http.StatusBadRequest},
		{"bad json", http.MethodPost, `{`, http.StatusBadRequest},
		{"wrong method", http.MethodGet, ``, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := request(t, srv.Client(), tc.method, srv.URL+"/auth/login", strings.NewReader(tc.body))
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestMeAuth(t *testing.T) {
	srv := newTestServer(t)

	resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+"/users/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no auth status = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/users/me", nil)
	req.Header.Set("Authorization", "Bearer demo-token")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bearer status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	data, _ := io.ReadAll(resp.Body)
	decodeJSON(t, data, &body)
	if body.Data.Username != "alice" {
		t.Fatalf("username = %q", body.Data.Username)
	}
}

func TestEchoRepeatedQueryHeadersAndBody(t *testing.T) {
	srv := newTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/echo?tag=a&tag=b&tag=c&q=hello%20world",
		strings.NewReader(`{"hello":"world"}`))
	req.Header.Set("X-Custom", "value")
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	var got echoResponse
	decodeJSON(t, data, &got)
	if got.Method != http.MethodPost {
		t.Fatalf("method = %q", got.Method)
	}
	if got.RawQuery != "tag=a&tag=b&tag=c&q=hello%20world" {
		t.Fatalf("rawQuery = %q", got.RawQuery)
	}
	if len(got.Query) != 4 {
		t.Fatalf("query len = %d, want 4 (%v)", len(got.Query), got.Query)
	}
	wantQuery := []kvPair{{"tag", "a"}, {"tag", "b"}, {"tag", "c"}, {"q", "hello world"}}
	for i, want := range wantQuery {
		if got.Query[i] != want {
			t.Fatalf("query[%d] = %v, want %v", i, got.Query[i], want)
		}
	}
	if v := got.Headers["X-Custom"]; len(v) != 1 || v[0] != "value" {
		t.Fatalf("X-Custom = %v", v)
	}
	if got.Body != `{"hello":"world"}` {
		t.Fatalf("body = %q", got.Body)
	}
	if got.ContentType != "application/json" {
		t.Fatalf("contentType = %q", got.ContentType)
	}
}

func TestEchoBodyTooLarge(t *testing.T) {
	srv := newTestServer(t)
	big := bytes.Repeat([]byte("a"), maxEchoBody+10)
	resp, err := srv.Client().Post(srv.URL+"/echo", "text/plain", bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got echoResponse
	data, _ := io.ReadAll(resp.Body)
	decodeJSON(t, data, &got)
	if !got.BodyTruncated {
		t.Fatalf("expected bodyTruncated=true")
	}
	if len(got.Body) != maxEchoBody {
		t.Fatalf("echoed body len = %d, want %d", len(got.Body), maxEchoBody)
	}
}

func TestUsersPaginationHugePage(t *testing.T) {
	srv := newTestServer(t)
	maxInt := int(^uint(0) >> 1)
	resp, data := request(t, srv.Client(), http.MethodGet,
		srv.URL+"/users?page="+strconv.Itoa(maxInt)+"&pageSize=100", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("huge page status = %d, body %s", resp.StatusCode, data)
	}
	var result struct {
		Data []user `json:"data"`
	}
	decodeJSON(t, data, &result)
	if len(result.Data) != 0 {
		t.Fatalf("huge page must be empty, got %v", result.Data)
	}
}

func TestUsersPagination(t *testing.T) {
	srv := newTestServer(t)

	var listResp struct {
		Data       []user `json:"data"`
		Pagination struct {
			Page       int `json:"page"`
			PageSize   int `json:"pageSize"`
			Total      int `json:"total"`
			TotalPages int `json:"totalPages"`
		} `json:"pagination"`
	}

	resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/users", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	decodeJSON(t, data, &listResp)
	if listResp.Pagination.Total != 5 || len(listResp.Data) != 5 {
		t.Fatalf("default list = %+v", listResp.Pagination)
	}

	resp, data = request(t, srv.Client(), http.MethodGet, srv.URL+"/users?page=2&pageSize=2", nil)
	decodeJSON(t, data, &listResp)
	if listResp.Pagination.Page != 2 || listResp.Pagination.PageSize != 2 ||
		listResp.Pagination.Total != 5 || listResp.Pagination.TotalPages != 3 {
		t.Fatalf("pagination meta = %+v", listResp.Pagination)
	}
	if len(listResp.Data) != 2 || listResp.Data[0].ID != 3 || listResp.Data[1].ID != 4 {
		t.Fatalf("page 2 data = %+v", listResp.Data)
	}

	invalid := []string{"page=0", "page=abc", "pageSize=0", "pageSize=101", "pageSize=x"}
	for _, q := range invalid {
		t.Run(q, func(t *testing.T) {
			resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+"/users?"+q, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s status = %d, want 400", q, resp.StatusCode)
			}
		})
	}
}

func TestUsersCRUD(t *testing.T) {
	srv := newTestServer(t)
	client := srv.Client()

	// Create.
	resp, data := request(t, client, http.MethodPost, srv.URL+"/users",
		strings.NewReader(`{"name":"张三","email":"zhangsan@example.com","role":"admin"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", resp.StatusCode, data)
	}
	var created struct {
		Data user `json:"data"`
	}
	decodeJSON(t, data, &created)
	if created.Data.ID != 6 || created.Data.Name != "张三" || created.Data.Role != "admin" {
		t.Fatalf("created user = %+v", created.Data)
	}
	if loc := resp.Header.Get("Location"); loc != "/users/6" {
		t.Fatalf("Location = %q", loc)
	}

	// Read.
	resp, data = request(t, client, http.MethodGet, srv.URL+"/users/6", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", resp.StatusCode)
	}
	var got struct {
		Data user `json:"data"`
	}
	decodeJSON(t, data, &got)
	if got.Data.Name != "张三" {
		t.Fatalf("get user = %+v", got.Data)
	}

	// Update (PUT).
	resp, data = request(t, client, http.MethodPut, srv.URL+"/users/6",
		strings.NewReader(`{"name":"李四","email":"lisi@example.com","role":"member"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put status = %d, body %s", resp.StatusCode, data)
	}
	decodeJSON(t, data, &got)
	if got.Data.Name != "李四" || got.Data.Role != "member" {
		t.Fatalf("after PUT = %+v", got.Data)
	}

	// Patch partial update.
	resp, data = request(t, client, http.MethodPatch, srv.URL+"/users/6",
		strings.NewReader(`{"role":"viewer"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, body %s", resp.StatusCode, data)
	}
	decodeJSON(t, data, &got)
	if got.Data.Name != "李四" || got.Data.Role != "viewer" {
		t.Fatalf("after PATCH = %+v", got.Data)
	}

	// Delete.
	resp, _ = request(t, client, http.MethodDelete, srv.URL+"/users/6", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", resp.StatusCode)
	}
	resp, _ = request(t, client, http.MethodGet, srv.URL+"/users/6", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", resp.StatusCode)
	}
	resp, _ = request(t, client, http.MethodDelete, srv.URL+"/users/6", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("double delete = %d, want 404", resp.StatusCode)
	}
}

func TestUsersValidationAndMissing(t *testing.T) {
	srv := newTestServer(t)
	client := srv.Client()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"create missing name", http.MethodPost, "/users", `{"email":"a@b.com"}`, http.StatusBadRequest},
		{"create bad role", http.MethodPost, "/users", `{"name":"x","role":"superuser"}`, http.StatusBadRequest},
		{"create bad email", http.MethodPost, "/users", `{"name":"x","email":"nope"}`, http.StatusBadRequest},
		{"create bad json", http.MethodPost, "/users", `{`, http.StatusBadRequest},
		{"invalid id", http.MethodGet, "/users/abc", "", http.StatusBadRequest},
		{"zero id", http.MethodGet, "/users/0", "", http.StatusBadRequest},
		{"missing user", http.MethodGet, "/users/999", "", http.StatusNotFound},
		{"put missing", http.MethodPut, "/users/999", `{"name":"x"}`, http.StatusNotFound},
		{"patch invalid role", http.MethodPatch, "/users/1", `{"role":"root"}`, http.StatusBadRequest},
		{"patch empty name", http.MethodPatch, "/users/1", `{"name":"  "}`, http.StatusBadRequest},
		{"post to item", http.MethodPost, "/users/1", `{}`, http.StatusMethodNotAllowed},
		{"deep path", http.MethodGet, "/users/1/extra", "", http.StatusNotFound},
		{"me wins over id", http.MethodGet, "/users/me", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := request(t, client, tc.method, srv.URL+tc.path, strings.NewReader(tc.body))
			if resp.StatusCode != tc.want {
				t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
			}
		})
	}
}

func TestUsersConcurrentCreates(t *testing.T) {
	srv := newTestServer(t)
	client := srv.Client()

	const n = 40
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := strings.NewReader(`{"name":"并发用户"}`)
			resp, err := client.Post(srv.URL+"/users", "application/json", body)
			if err != nil {
				errs <- err.Error()
				return
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != http.StatusCreated {
				errs <- "unexpected status " + resp.Status
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent create failed: %s", e)
	}

	var listResp struct {
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	resp, data := request(t, client, http.MethodGet, srv.URL+"/users?pageSize=100", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	decodeJSON(t, data, &listResp)
	if listResp.Pagination.Total != 5+n {
		t.Fatalf("total = %d, want %d", listResp.Pagination.Total, 5+n)
	}
}

func TestStatusEndpoint(t *testing.T) {
	srv := newTestServer(t)

	resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/status/418", nil)
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", resp.StatusCode)
	}
	var body struct {
		Status int    `json:"status"`
		Msg    string `json:"message"`
	}
	decodeJSON(t, data, &body)
	if body.Status != 418 || body.Msg == "" {
		t.Fatalf("body = %+v", body)
	}

	for _, path := range []string{"/status/200", "/status/999", "/status/abc"} {
		resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+path, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, resp.StatusCode)
		}
	}
}

func TestDelaySuccessAndCancellation(t *testing.T) {
	srv := newTestServer(t)

	// A short delay completes normally.
	resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/delay?ms=10", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delay status = %d", resp.StatusCode)
	}
	var body map[string]int
	decodeJSON(t, data, &body)
	if body["delayedMs"] != 10 {
		t.Fatalf("delayedMs = %d", body["delayedMs"])
	}

	// A client timeout shorter than the delay must abort the request.
	fast := &http.Client{Timeout: 50 * time.Millisecond}
	resp, err := fast.Get(srv.URL + "/delay?ms=2000")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected timeout error from delayed request")
	}

	// Context cancellation also aborts promptly.
	ctxClient := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/delay?ms=2000", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	if resp, err := ctxClient.Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("expected cancellation error")
	}

	for _, path := range []string{"/delay?ms=-1", "/delay?ms=abc", "/delay?ms=999999"} {
		resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+path, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, resp.StatusCode)
		}
	}
}

func TestRedirectBehavior(t *testing.T) {
	srv := newTestServer(t)

	// Do not follow so we can inspect the raw 302.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, _ := request(t, noFollow, http.MethodGet, srv.URL+"/redirect?to=/users&status=302", nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("redirect status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/users" {
		t.Fatalf("Location = %q", loc)
	}

	// Following the redirect lands on the health endpoint.
	resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/redirect?to=/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("followed status = %d", resp.StatusCode)
	}
	var health map[string]string
	decodeJSON(t, data, &health)
	if health["status"] != "ok" {
		t.Fatalf("followed body = %v", health)
	}

	// Multi-hop chain ends at remaining=0.
	resp, data = request(t, srv.Client(), http.MethodGet, srv.URL+"/redirect/chain/2", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chain status = %d", resp.StatusCode)
	}
	var chain map[string]any
	decodeJSON(t, data, &chain)
	if chain["redirected"] != true {
		t.Fatalf("chain body = %v", chain)
	}

	for _, path := range []string{
		"/redirect?to=//evil.com",
		"/redirect?to=https://evil.com",
		"/redirect?to=/%5cevil.com", // %5c decodes to "\"; must not become //evil.com
		"/redirect?status=200",
	} {
		resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+path, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, resp.StatusCode)
		}
	}

	// Every allowed 3xx code must be honored, not just 302.
	for _, code := range []int{301, 302, 303, 307, 308} {
		path := "/redirect?to=/health&status=" + strconv.Itoa(code)
		resp, _ := request(t, noFollow, http.MethodGet, srv.URL+path, nil)
		if resp.StatusCode != code {
			t.Fatalf("%s status = %d, want %d", path, resp.StatusCode, code)
		}
	}

	// Chain validation: out-of-range and non-numeric return 400, and n==0 succeeds.
	for _, path := range []string{"/redirect/chain/-1", "/redirect/chain/21", "/redirect/chain/abc"} {
		resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+path, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, resp.StatusCode)
		}
	}
	resp, data = request(t, srv.Client(), http.MethodGet, srv.URL+"/redirect/chain/0", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chain/0 status = %d, want 200", resp.StatusCode)
	}
	var zero map[string]any
	decodeJSON(t, data, &zero)
	if zero["remaining"] != float64(0) {
		t.Fatalf("chain/0 body = %v", zero)
	}
}

func TestFormatVariants(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		kind        string
		contentType string
		contains    string
	}{
		{"json", "application/json", `"name":"示例资源"`},
		{"text", "text/plain", "name=示例资源"},
		{"xml", "application/xml", "<name>示例资源</name>"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/format/"+tc.kind, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, tc.contentType) {
				t.Fatalf("Content-Type = %q, want %s", ct, tc.contentType)
			}
			if !strings.Contains(string(data), tc.contains) {
				t.Fatalf("body %q missing %q", data, tc.contains)
			}
		})
	}

	resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+"/format/yaml", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown format status = %d, want 404", resp.StatusCode)
	}
}

func TestMultiHeaderAndCookies(t *testing.T) {
	srv := newTestServer(t)

	resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+"/response/multi", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Values("X-Multi"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("X-Multi = %v", got)
	}
	if got := resp.Header.Values("Set-Cookie"); len(got) != 2 {
		t.Fatalf("Set-Cookie = %v", got)
	}
}

func TestLargeResponse(t *testing.T) {
	srv := newTestServer(t)

	resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/large?size=1024", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(data) != 1024 {
		t.Fatalf("body len = %d, want 1024", len(data))
	}
	if cl := resp.Header.Get("X-Content-Length"); cl != "1024" {
		t.Fatalf("X-Content-Length = %q", cl)
	}

	for _, path := range []string{"/large?size=-1", "/large?size=abc", "/large?size=999999999"} {
		resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+path, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, resp.StatusCode)
		}
	}
}

func TestDelayEmptyFallsBackToDefault(t *testing.T) {
	srv := newTestServer(t)

	// An absent or empty ms falls back to the documented 500ms default rather
	// than erroring; only malformed/out-of-range values are rejected.
	resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/delay?ms=", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty ms status = %d, want 200", resp.StatusCode)
	}
	var body map[string]int
	decodeJSON(t, data, &body)
	if body["delayedMs"] != 500 {
		t.Fatalf("empty ms delayedMs = %d, want 500", body["delayedMs"])
	}
}

func TestMethodNotAllowedAcrossEndpoints(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/users"},           // collection only GET/POST
		{http.MethodPost, "/status/404"},     // status only GET
		{http.MethodPost, "/delay"},          // delay only GET
		{http.MethodPost, "/redirect"},       // redirect only GET
		{http.MethodPost, "/format/json"},    // format only GET
		{http.MethodPost, "/response/multi"}, // multi only GET
		{http.MethodPost, "/large"},          // large only GET
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp, data := request(t, srv.Client(), tc.method, srv.URL+tc.path, nil)
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s status = %d, want 405", tc.method, tc.path, resp.StatusCode)
			}
			if allow := resp.Header.Get("Allow"); allow == "" {
				t.Fatalf("%s %s missing Allow header", tc.method, tc.path)
			}
			var e errorBody
			decodeJSON(t, data, &e)
			if e.Error == "" {
				t.Fatalf("%s %s body is not a JSON error: %q", tc.method, tc.path, data)
			}
		})
	}
}

func TestPatchMissingUserAndLargeZero(t *testing.T) {
	srv := newTestServer(t)

	// PATCH on a nonexistent user must 404 (found==false path), not 400.
	resp, _ := request(t, srv.Client(), http.MethodPatch, srv.URL+"/users/999",
		strings.NewReader(`{"role":"member"}`))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch missing status = %d, want 404", resp.StatusCode)
	}

	// size=0 yields an empty 200 body.
	resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/large?size=0", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("large size=0 status = %d", resp.StatusCode)
	}
	if len(data) != 0 {
		t.Fatalf("large size=0 body len = %d, want 0", len(data))
	}
}

func TestJSONResponsesCarryDemoMarker(t *testing.T) {
	srv := newTestServer(t)

	for _, path := range []string{"/health", "/users", "/format/json", "/status/404"} {
		resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+path, nil)
		if got := resp.Header.Get("X-Demo-Server"); got != "gorequest-demo" {
			t.Fatalf("%s X-Demo-Server = %q, want gorequest-demo", path, got)
		}
	}
}

func TestNotFoundIsJSON(t *testing.T) {
	srv := newTestServer(t)
	resp, data := request(t, srv.Client(), http.MethodGet, srv.URL+"/does-not-exist", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var e errorBody
	decodeJSON(t, data, &e)
	if e.Error == "" {
		t.Fatalf("expected JSON error body, got %q", data)
	}
}
