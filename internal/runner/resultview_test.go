package runner

import "testing"

func viewFixture() *RunResult {
	fail := func(msg string) *Failure {
		return &Failure{Category: FailureRun, Kind: KindAssertion, Message: msg}
	}
	return &RunResult{
		Rows: []RowResult{
			{Index: 1, Status: StatusFailed, Steps: []StepResult{
				{ID: "login", Status: StatusPassed, DurationMs: 10, Attempts: []AttemptResult{{Number: 1, Status: StatusPassed, HTTPStatus: 200}}},
				{ID: "query", Status: StatusFailed, Reason: "assertion failed", Failure: fail("status mismatch"),
					Attempts:   []AttemptResult{{Number: 1, Status: StatusFailed, HTTPStatus: 500, Failure: fail("status mismatch")}, {Number: 2, Status: StatusFailed, HTTPStatus: 500, Failure: fail("status mismatch")}},
					Assertions: []AssertionResult{{Kind: "status", Passed: true}, {Kind: "jsonpath", Target: "$.id", Passed: false}}},
				{ID: "update", Status: StatusSkipped, Reason: `dependency "query" failed`, Attempts: []AttemptResult{}},
			}},
			{Index: 2, Status: StatusPassed, Steps: []StepResult{
				{ID: "login", Status: StatusPassed, Attempts: []AttemptResult{{Number: 1, Status: StatusPassed}}},
				{ID: "query", Status: StatusPassed, Attempts: []AttemptResult{{Number: 1, Status: StatusPassed}}},
				{ID: "update", Status: StatusPassed, Attempts: []AttemptResult{{Number: 1, Status: StatusPassed}}},
			}},
		},
	}
}

func TestQueryResultPagingAndFilters(t *testing.T) {
	result := viewFixture()
	all := QueryResult(result, ResultFilter{}, 0, 0)
	if all.Total != 6 || len(all.Items) != 6 || all.HasMore {
		t.Fatalf("%+v", all)
	}
	page := QueryResult(result, ResultFilter{}, 2, 2)
	if page.Total != 6 || len(page.Items) != 2 || !page.HasMore || page.Items[0].Row != 1 || page.Items[0].StepID != "update" {
		t.Fatalf("%+v", page)
	}
	page = QueryResult(result, ResultFilter{}, 5, 2)
	if len(page.Items) != 1 || page.HasMore {
		t.Fatalf("last page = %+v", page)
	}
	failed := QueryResult(result, ResultFilter{Statuses: []Status{StatusFailed}}, 0, 10)
	if failed.Total != 1 || failed.Items[0].StepID != "query" || failed.Items[0].FailureKind != KindAssertion ||
		failed.Items[0].Attempts != 2 || failed.Items[0].Retries != 1 || failed.Items[0].HTTPStatus != 500 ||
		failed.Items[0].FailedAssertions != 1 {
		t.Fatalf("failed = %+v", failed.Items[0])
	}
	if got := QueryResult(result, ResultFilter{StepID: "update"}, 0, 10); got.Total != 2 || got.Items[0].Row != 1 || got.Items[1].Row != 2 {
		t.Fatalf("step filter = %+v", got)
	}
	if got := QueryResult(result, ResultFilter{Text: "DEPENDENCY"}, 0, 10); got.Total != 1 || got.Items[0].Status != StatusSkipped {
		t.Fatalf("text filter = %+v", got)
	}
	if got := QueryResult(result, ResultFilter{Text: "status mismatch"}, 0, 10); got.Total != 1 {
		t.Fatalf("failure text filter = %+v", got)
	}
	if got := QueryResult(result, ResultFilter{}, -3, 10000); got.Offset != 0 || got.Limit != MaxResultPageSize || len(got.Items) != 6 {
		t.Fatalf("bounds = %+v", got)
	}
	if got := QueryResult(nil, ResultFilter{}, 0, 0); got.Total != 0 || got.Items != nil {
		t.Fatalf("nil = %+v", got)
	}
}

func TestFailureLocationsExactTotalAndBound(t *testing.T) {
	result := viewFixture()
	index := FailureLocations(result, 2)
	// One step failure plus two attempt failures = three locations.
	if index.Total != 3 || len(index.Items) != 2 {
		t.Fatalf("%+v", index)
	}
	if index.Items[0].StepID != "query" || index.Items[0].Attempt != 0 || index.Items[1].Attempt != 1 {
		t.Fatalf("order = %+v", index.Items)
	}
	full := FailureLocations(result, 0)
	if full.Total != 3 || len(full.Items) != 3 {
		t.Fatalf("full = %+v", full)
	}
	if nilIndex := FailureLocations(nil, 10); nilIndex.Total != 0 || nilIndex.Items == nil {
		t.Fatalf("nil = %+v", nilIndex)
	}
}
