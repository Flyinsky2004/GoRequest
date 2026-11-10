package runner

// Bounded final-result query support. The desktop renders one compact line per
// step and pages/filters it; the full RunResult remains the machine report.
// Nothing here mutates the result.

import "strings"

const (
	// DefaultResultPageSize is used when a caller passes limit 0.
	DefaultResultPageSize = 50
	// MaxResultPageSize bounds one rendered page regardless of caller input.
	MaxResultPageSize = 200
	// MaxFailureLocations bounds the failure index while Total stays accurate.
	MaxFailureLocations = 500
)

// ResultFilter selects step-level result lines. An empty Statuses list means
// all statuses. StepID is an exact step identifier; Text is a case-insensitive
// substring over row/step id/name/request/reason/failure message.
type ResultFilter struct {
	Statuses []Status
	StepID   string
	Text     string
}

// ResultItem is one final step verdict in report order. Attempts and assertion
// details remain available on RunResult; the page stays compact on purpose.
type ResultItem struct {
	Kind              ResultItemKind  `json:"kind"`
	Row               int             `json:"row"`
	RowStatus         Status          `json:"rowStatus"`
	StepID            string          `json:"stepId"`
	Name              string          `json:"name,omitempty"`
	Request           string          `json:"request,omitempty"`
	Status            Status          `json:"status"`
	DurationMs        int64           `json:"durationMs"`
	Attempts          int             `json:"attempts"`
	Retries           int             `json:"retries"`
	HTTPStatus        int             `json:"httpStatus,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	FailureCategory   FailureCategory `json:"failureCategory,omitempty"`
	FailureKind       FailureKind     `json:"failureKind,omitempty"`
	FailureMessage    string          `json:"failureMessage,omitempty"`
	Assertions        int             `json:"assertions,omitempty"`
	FailedAssertions  int             `json:"failedAssertions,omitempty"`
	Extractions       int             `json:"extractions,omitempty"`
	FailedExtractions int             `json:"failedExtractions,omitempty"`
}

// ResultItemKind identifies the item shape; currently only step lines exist.
type ResultItemKind string

// ResultItemStep is the compact per-step line.
const ResultItemStep ResultItemKind = "step"

// ResultPage is one bounded page plus the exact match count.
type ResultPage struct {
	Items   []ResultItem `json:"items"`
	Total   int          `json:"total"`
	Offset  int          `json:"offset"`
	Limit   int          `json:"limit"`
	HasMore bool         `json:"hasMore"`
}

// QueryResult filters and pages the final result without allocating the whole
// filtered view. Total is exact even when only one page is materialized.
func QueryResult(result *RunResult, filter ResultFilter, offset, limit int) ResultPage {
	if limit <= 0 {
		limit = DefaultResultPageSize
	}
	if limit > MaxResultPageSize {
		limit = MaxResultPageSize
	}
	if offset < 0 {
		offset = 0
	}
	page := ResultPage{Offset: offset, Limit: limit}
	if result == nil {
		return page
	}
	statuses := map[Status]bool{}
	for _, s := range filter.Statuses {
		if s != "" {
			statuses[s] = true
		}
	}
	needle := strings.ToLower(strings.TrimSpace(filter.Text))
	stepID := strings.TrimSpace(filter.StepID)
	for _, row := range result.Rows {
		for _, step := range row.Steps {
			if len(statuses) > 0 && !statuses[step.Status] {
				continue
			}
			if stepID != "" && step.ID != stepID {
				continue
			}
			if needle != "" && !resultItemMatches(row, step, needle) {
				continue
			}
			page.Total++
			if page.Total <= offset || len(page.Items) >= limit {
				continue
			}
			page.Items = append(page.Items, resultItem(row, step))
		}
	}
	page.HasMore = offset+len(page.Items) < page.Total
	return page
}

func resultItemMatches(row RowResult, step StepResult, needle string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		step.ID, step.Name, step.Request, step.Reason, failureText(step.Failure),
	}, "\x00"))
	return strings.Contains(haystack, needle)
}

func failureText(f *Failure) string {
	if f == nil {
		return ""
	}
	return string(f.Category) + " " + string(f.Kind) + " " + f.Message
}

func resultItem(row RowResult, step StepResult) ResultItem {
	item := ResultItem{
		Kind:       ResultItemStep,
		Row:        row.Index,
		RowStatus:  row.Status,
		StepID:     step.ID,
		Name:       step.Name,
		Request:    step.Request,
		Status:     step.Status,
		DurationMs: step.DurationMs,
		Attempts:   len(step.Attempts),
		Retries:    step.RetryCount(),
		Reason:     step.Reason,
	}
	if len(step.Attempts) > 0 {
		item.HTTPStatus = step.Attempts[len(step.Attempts)-1].HTTPStatus
	}
	if step.Failure != nil {
		item.FailureCategory = step.Failure.Category
		item.FailureKind = step.Failure.Kind
		item.FailureMessage = step.Failure.Message
	}
	item.Assertions = len(step.Assertions)
	for _, a := range step.Assertions {
		if !a.Passed {
			item.FailedAssertions++
		}
	}
	item.Extractions = len(step.Extractions)
	for _, e := range step.Extractions {
		if !e.Published {
			item.FailedExtractions++
		}
	}
	return item
}

// FailureLocation points at a failed verdict for quick navigation.
type FailureLocation struct {
	Row      int             `json:"row"`
	StepID   string          `json:"stepId"`
	Name     string          `json:"name,omitempty"`
	Request  string          `json:"request,omitempty"`
	Attempt  int             `json:"attempt,omitempty"`
	Status   Status          `json:"status"`
	Category FailureCategory `json:"category"`
	Kind     FailureKind     `json:"kind"`
	Message  string          `json:"message"`
}

// FailureIndex lists failure locations. Items is capped; Total is exact.
type FailureIndex struct {
	Total int               `json:"total"`
	Items []FailureLocation `json:"items"`
}

// FailureLocations collects step-level and per-attempt failures. Failed
// assertions/extractions surface through the step failure they produce.
func FailureLocations(result *RunResult, max int) FailureIndex {
	if max <= 0 {
		max = MaxFailureLocations
	}
	index := FailureIndex{Items: []FailureLocation{}}
	if result == nil {
		return index
	}
	for _, row := range result.Rows {
		for _, step := range row.Steps {
			if step.Failure != nil {
				index.Total++
				if len(index.Items) < max {
					index.Items = append(index.Items, FailureLocation{
						Row: row.Index, StepID: step.ID, Name: step.Name, Request: step.Request,
						Status: step.Status, Category: step.Failure.Category, Kind: step.Failure.Kind,
						Message: step.Failure.Message,
					})
				}
			}
			for _, attempt := range step.Attempts {
				if attempt.Failure == nil {
					continue
				}
				index.Total++
				if len(index.Items) < max {
					index.Items = append(index.Items, FailureLocation{
						Row: row.Index, StepID: step.ID, Name: step.Name, Request: step.Request,
						Attempt: attempt.Number, Status: attempt.Status,
						Category: attempt.Failure.Category, Kind: attempt.Failure.Kind,
						Message: attempt.Failure.Message,
					})
				}
			}
		}
	}
	return index
}
