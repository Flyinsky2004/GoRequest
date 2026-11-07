package docgen

import (
	"strings"
	"testing"

	"gorequest/internal/core"
)

// All tests in this package are authored but NOT executed in phase 1; they
// define the acceptance assertions for the post-integration verification run.

func TestBuildRequestCardFromContract(t *testing.T) {
	p := newDocgenProject(t)
	entry := p.Requests["users.me"]
	card, err := BuildRequestCard(p, entry, CardOptions{Labels: ChineseCardLabels()})
	if err != nil {
		t.Fatalf("BuildRequestCard: %v", err)
	}
	if !card.HasContract || card.OperationID != "getUsersMe" {
		t.Fatalf("contract not picked up: HasContract=%v op=%q", card.HasContract, card.OperationID)
	}
	if card.Method != "GET" || card.URL != "{{baseUrl}}/users/me" || card.PathTemplate != "/users/me" {
		t.Fatalf("basics wrong: %+v", card)
	}
	if len(card.Parameters) != 2 {
		t.Fatalf("parameter rows = %d, want 2", len(card.Parameters))
	}
	p0 := card.Parameters[0]
	if p0.Name != "verbose" || p0.In != "query" || p0.Required {
		t.Fatalf("param row 0 wrong: %+v", p0)
	}
	if p1 := card.Parameters[1]; p1.Name != "X-Request-Id" || !p1.Required || p1.In != "header" {
		t.Fatalf("param row 1 wrong: %+v", p1)
	}
	if card.Body == nil || len(card.Body.MediaTypes) != 1 || card.Body.MediaTypes[0].Name != "application/json" {
		t.Fatalf("body media types wrong: %+v", card.Body)
	}
	if !strings.HasPrefix(card.Body.MediaTypes[0].Schema, "object{") {
		t.Fatalf("body schema summary wrong: %q", card.Body.MediaTypes[0].Schema)
	}
	if len(card.Responses) < 2 || card.Responses[0].Status != "200" {
		t.Fatalf("responses missing or unsorted: %+v", card.Responses)
	}
	if card.Responses[len(card.Responses)-1].Status != "4XX" {
		t.Fatalf("range statuses must sort after concrete codes: %+v", card.Responses)
	}
	if v, ok := card.responseDesc["200"]; !ok || v != "The profile record." {
		t.Fatalf("response description lost: %v", card.responseDesc)
	}
}

func TestHealthCardDerivesFromDefinitionOnly(t *testing.T) {
	p := newDocgenProject(t)
	card, err := BuildRequestCard(p, p.Requests["health"], CardOptions{Labels: EnglishCardLabels()})
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	if card.HasContract {
		t.Fatalf("health has no contract")
	}
	if !card.ParamsDerived && len(card.Parameters) != 1 {
		t.Fatalf("only the enabled header should become a row: %+v", card.Parameters)
	}
	if card.Parameters[0].Name != "X-Trace-Id" || card.Parameters[0].Example != "trace-1" {
		t.Fatalf("derived parameter wrong: %+v", card.Parameters[0])
	}
	if strings.Contains(card.Parameters[1].Name, "Authorization") && !strings.Contains(card.Parameters[1].Example, "***") {
		// The authorization value is masked by name before publication.
		t.Fatalf("authorization value must be masked: %+v", card.Parameters[1])
	}
	if len(card.Responses) != 0 {
		t.Fatalf("no responses may be invented without a contract: %+v", card.Responses)
	}
	if strings.Count(BuildCardMarkdown(card, nil), "No response definitions") == 0 {
		t.Fatalf("card must state that no responses are recorded")
	}
}

func TestBuildCardMarkdownLayout(t *testing.T) {
	p := newDocgenProject(t)
	card, err := BuildRequestCard(p, p.Requests["users.me"], CardOptions{Labels: ChineseCardLabels()})
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	md := BuildCardMarkdown(card, nil)
	if !strings.HasPrefix(md, "#### `users.me` — GET /users/me\n") {
		t.Fatalf("card heading wrong:\n%s", md)
	}
	if !strings.Contains(md, "**参数**") || !strings.Contains(md, "| 名称 | 位置 | 必填 | 类型 | 说明 |") {
		t.Fatalf("Chinese parameter table missing:\n%s", md)
	}
	if !strings.Contains(md, "**响应**") || !strings.Contains(md, "| 200 |") {
		t.Fatalf("responses table missing:\n%s", md)
	}
	if !strings.Contains(md, "[在 GoRequest 中打开请求 users.me](gorequest://request/users.me)") {
		t.Fatalf("open-request link missing:\n%s", md)
	}
	if strings.Contains(md, "8q6v2x0z4k9d7f3n1m5y") {
		t.Fatalf("masked example leaked its value:\n%s", md)
	}
	if !strings.Contains(md, maskedValue) {
		t.Fatalf("masked value placeholder missing")
	}
	if !strings.Contains(md, secretTemplateRef) {
		t.Fatalf("authored secret template reference must stay literal:\n%s", md)
	}
}

func TestDeprecatedCardMarksHeading(t *testing.T) {
	p := newDocgenProject(t)
	entry := p.Requests["users.me"]
	if err := core.SetRequestOpenAPI(entry.Request, func() *core.OperationContract {
		c := usersContract()
		c.Deprecated = true
		return c
	}()); err != nil {
		t.Fatalf("SetRequestOpenAPI: %v", err)
	}
	card, err := BuildRequestCard(p, entry, CardOptions{Labels: ChineseCardLabels()})
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	if !card.Deprecated || !strings.Contains(BuildCardMarkdown(card, nil), "已弃用") {
		t.Fatalf("deprecated badge missing")
	}
}
