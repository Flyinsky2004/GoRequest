package docgen

import (
	"testing"

	"gorequest/internal/core"
)

// This file builds the docgen test fixture project. The whole test suite in
// this package was authored but NOT executed in this phase (the phase's
// standing restriction forbids go test / go build / vet); the cases pin the
// expected behaviour for the verification pass after integration.

// newDocgenProject returns a project with two requests:
//
//	users.me  — GET \{\{baseUrl\}\}/users/me with a full OpenAPI contract
//	( parameters, request body, responses, scrubbed examples ).
//	health    — GET \{\{baseUrl\}\}/health without a contract, so tests can
//	cover derivation from the raw request definition.
func newDocgenProject(t *testing.T) *core.Project {
	t.Helper()
	dir := t.TempDir()
	p, err := core.NewProject(dir, "Docgen")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	users, err := p.NewRequest("", "Users Me")
	if err != nil {
		t.Fatalf("NewRequest users: %v", err)
	}
	delete(p.Requests, users.Request.ID)
	users.Request.ID = "users.me"
	for i, id := range p.Order {
		if id == "users-me" {
			p.Order[i] = "users.me"
		}
	}
	users.Request.HTTP.Method = "GET"
	users.Request.HTTP.URL = "{{baseUrl}}/users/me"
	users.Request.HTTP.Query = []core.KV{
		{Name: "verbose", Value: "true"},
	}
	if err := core.SetRequestOpenAPI(users.Request, usersContract()); err != nil {
		t.Fatalf("SetRequestOpenAPI users: %v", err)
	}
	if _, err := p.SaveRequest(users, users.Request, users.BaselineDigest); err != nil {
		t.Fatalf("SaveRequest users: %v", err)
	}

	health, err := p.NewRequest("", "Health")
	if err != nil {
		t.Fatalf("NewRequest health: %v", err)
	}
	health.Request.HTTP.Method = "GET"
	health.Request.HTTP.URL = "{{baseUrl}}/health"
	health.Request.HTTP.Headers = []core.KV{
		{Name: "X-Trace-Id", Value: "trace-1"},
		{Name: "Authorization", Value: "Bearer abc"},
	}
	if _, err := p.SaveRequest(health, health.Request, health.BaselineDigest); err != nil {
		t.Fatalf("SaveRequest health: %v", err)
	}
	return p
}

func usersContract() *core.OperationContract {
	return &core.OperationContract{
		OperationID:  "getUsersMe",
		Summary:      "Current user profile",
		Description:  "Returns the authenticated user's profile record.",
		PathTemplate: "/users/me",
		Parameters: []core.OperationParam{
			{Name: "verbose", In: "query", Required: false, Description: "include extra fields",
				Schema: map[string]any{"type": "boolean"}},
			{Name: "X-Request-Id", In: "header", Required: true, Description: "trace id",
				Schema: map[string]any{"type": "string"}},
		},
		RequestBody: &core.OperationRequestBody{
			Required: false,
			Content: map[string]core.MediaTypeContract{
				"application/json": {
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		Responses: map[string]core.ResponseContract{
			"200": {
				Description: "The profile record.",
				Headers: map[string]core.HeaderContract{
					"X-Rate-Limit": {Description: "remaining quota"},
				},
				Content: map[string]core.MediaTypeContract{
					"application/json": {
						Schema: map[string]any{"type": "object"},
						Example: map[string]any{
							"id":    "u_123",
							"token": "Bearer 8q6v2x0z4k9d7f3n1m5y8q6v2x0z4k9d7f3n1m5y", // credential-shaped: must be blocked
							"note":  "" + secretTemplateRef,
						},
					},
				},
			},
			"4XX": {
				Description: "Request refused.",
			},
		},
	}
}

// secretTemplateRef is an authored secret reference (template, not value).
const secretTemplateRef = `{{secret.api_token}}`

const apiDoc = `# API 总览

先读 [用户接口](gorequest://request/users.me)，或直接看下方卡片。

<!-- gorequest:request id="users.me" view="parameters,responses" -->

## 引用形态

这里有一个单独链接 [健康检查](gorequest://request/health)，本文未给它建卡片。

<!-- gorequest:request id="ghost.request" -->

<!-- gorequest:request id="users.me" extra="oops" -->

伪引用示例代码中不应展开：

` + "```text\n<!-- gorequest:request id=\"users.me\" -->\n```" + `

## 图片与链接

![图标](assets/图标%20example.png)

![缺失图](assets/missing.png)

![远程](https://example.com/remote.png)

[指南](guide.md)

[缺失](nope.md)

[锚点](guide.md#缺失锚点)

## Mermaid 未渲染

` + "```mermaid\ngraph TD\n\tA--\u003eB\n```" + `

<!-- gorequest:end -->
`

const guideDoc = `# 指南

一般说明。[返回 API](api.md#api-总览)

![图标](assets/图标%20example.png)
`

func writeDocgenDoc(t *testing.T, p *core.Project, rel, src string) {
	t.Helper()
	if err := p.Store.WriteAtomic(rel, []byte(src)); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// pngBytes returns a tiny non-empty payload standing in for an image asset.
func pngBytes() []byte { return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0} }

// newDocgenProjectWithDocs returns the fixture project with its documents.
func newDocgenProjectWithDocs(t *testing.T) *core.Project {
	t.Helper()
	p := newDocgenProject(t)
	writeDocgenDoc(t, p, "docs/api.md", apiDoc)
	writeDocgenDoc(t, p, "docs/guide.md", guideDoc)
	if err := p.Store.WriteAtomic("docs/assets/图标 example.png", pngBytes()); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	// Reload so Docs and assets are visible to store-backed helpers.
	reloaded, err := core.OpenProject(p.Dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	return reloaded
}

// docFixtureSource returns the on-disk content of the fixture api document.
func mustReadDoc(t *testing.T, p *core.Project, rel string) string {
	t.Helper()
	data, _, err := p.Store.Read(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}
