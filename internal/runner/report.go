package runner

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// WriteJSONReport writes a deterministic, indented JSON report. The parent
// directory is created only when explicitly requested by ReportOptions; the
// default writer operates on an already open io.Writer so callers decide where
// run state lives.
func WriteJSONReport(w io.Writer, result *RunResult) error {
	if w == nil {
		return fmt.Errorf("JSON report writer is nil")
	}
	if result == nil {
		return fmt.Errorf("JSON report result is nil")
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("writing JSON report: %w", err)
	}
	return nil
}

// MarshalJSONReport is the in-memory version of WriteJSONReport.
func MarshalJSONReport(result *RunResult) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteJSONReport(&buf, result); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// JUnitReport is the minimal JUnit XML structure consumed by external CI
// tools. One testsuite covers the run; each row/step is one testcase.
type JUnitReport struct {
	XMLName    xml.Name        `xml:"testsuites"`
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Errors     int             `xml:"errors,attr"`
	Skipped    int             `xml:"skipped,attr"`
	Time       string          `xml:"time,attr"`
	Timestamp  string          `xml:"timestamp,attr"`
	Properties []JUnitProperty `xml:"properties>property,omitempty"`
	Suites     []JUnitSuite    `xml:"testsuite"`
}

// JUnitProperty is a report-safe key/value property.
type JUnitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// JUnitSuite groups one row.
type JUnitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Time     string      `xml:"time,attr"`
	Cases    []JUnitCase `xml:"testcase"`
}

// JUnitCase is one step in one row.
type JUnitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Status    string        `xml:"status,attr,omitempty"`
	Skipped   *JUnitSkipped `xml:"skipped,omitempty"`
	Failure   *JUnitFailure `xml:"failure,omitempty"`
	Error     *JUnitFailure `xml:"error,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

// JUnitSkipped is a skip marker with a reason.
type JUnitSkipped struct {
	Message string `xml:"message,attr"`
}

// JUnitFailure carries a redacted failure message.
type JUnitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

// BuildJUnitReport converts a RunResult into a JUnit XML model. Skipped steps
// are never reported as successes. Sensitive values are assumed already
// redacted by the engine; this function performs no raw-body access.
func BuildJUnitReport(result *RunResult) (*JUnitReport, error) {
	if result == nil {
		return nil, fmt.Errorf("JUnit report result is nil")
	}
	report := &JUnitReport{
		Name:      "GoRequest Runner",
		Tests:     result.Summary.StepsTotal,
		Failures:  result.Summary.Failed,
		Errors:    result.Summary.ConfigErrors + result.Summary.InternalErrors,
		Skipped:   result.Summary.Skipped + result.Summary.Canceled,
		Time:      seconds(result.DurationMs),
		Timestamp: result.StartedAt.UTC().Format(time.RFC3339Nano),
		Properties: []JUnitProperty{
			{Name: "runId", Value: result.RunID},
			{Name: "projectId", Value: result.ProjectID},
			{Name: "suiteId", Value: result.SuiteID},
			{Name: "snapshotDigest", Value: result.Snapshot},
			{Name: "environment", Value: result.Environment},
			{Name: "formatVersion", Value: fmt.Sprintf("%d", result.FormatVersion)},
		},
	}
	for i := range result.Rows {
		row := &result.Rows[i]
		suite := JUnitSuite{Name: rowName(row), Time: seconds(row.DurationMs)}
		for j := range row.Steps {
			step := &row.Steps[j]
			tc := JUnitCase{
				Name:      stepNameForReport(row, step),
				Classname: result.SuiteID + "." + step.Request,
				Time:      seconds(step.DurationMs),
				Status:    string(step.Status),
			}
			switch step.Status {
			case StatusSkipped:
				tc.Skipped = &JUnitSkipped{Message: step.Reason}
				suite.Skipped++
			case StatusFailed:
				tc.Failure = failureFor(step)
				suite.Failures++
			case StatusConfigError, StatusInternalError, StatusCanceled:
				tc.Error = failureFor(step)
				if step.Status == StatusCanceled {
					tc.Skipped = &JUnitSkipped{Message: step.Reason}
					suite.Skipped++
				} else {
					suite.Errors++
				}
			}
			tc.SystemOut = attemptsSummary(step)
			suite.Tests++
			suite.Cases = append(suite.Cases, tc)
		}
		report.Suites = append(report.Suites, suite)
	}
	return report, nil
}

func rowName(row *RowResult) string {
	if row.Index < 1 {
		return "single run"
	}
	return fmt.Sprintf("row %d", row.Index)
}

func stepNameForReport(row *RowResult, step *StepResult) string {
	name := step.ID
	if step.Name != "" {
		name = step.Name + " [" + step.ID + "]"
	}
	return fmt.Sprintf("%s/%s", rowName(row), name)
}

func failureFor(step *StepResult) *JUnitFailure {
	if step == nil {
		return nil
	}
	msg := step.Reason
	kind := ""
	if step.Failure != nil {
		msg = step.Failure.Message
		kind = string(step.Failure.Category) + "/" + string(step.Failure.Kind)
	}
	body := attemptsSummary(step)
	return &JUnitFailure{Message: msg, Type: kind, Body: body}
}

func attemptsSummary(step *StepResult) string {
	if len(step.Attempts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(step.Attempts)+len(step.Extractions))
	for _, a := range step.Attempts {
		line := fmt.Sprintf("attempt %d: %s", a.Number, a.Status)
		if a.HTTPStatus > 0 {
			line += fmt.Sprintf(" http=%d", a.HTTPStatus)
		}
		if a.Failure != nil {
			line += ": " + a.Failure.Message
		}
		if len(a.Extracted) > 0 {
			sort.Strings(a.Extracted)
			line += " outputs=" + strings.Join(a.Extracted, ",")
		}
		parts = append(parts, line)
	}
	for _, e := range step.Extractions {
		if e.Published {
			parts = append(parts, fmt.Sprintf("output %s: %s%s", e.Name, e.Value,
				map[bool]string{true: " [sensitive]", false: ""}[e.Sensitive]))
		} else {
			parts = append(parts, fmt.Sprintf("output %s: not published (%s)", e.Name, e.Message))
		}
	}
	return strings.Join(parts, "\n")
}

func seconds(ms int64) string {
	return fmt.Sprintf("%.3f", float64(ms)/1000.0)
}

// WriteJUnitReport writes the XML report with a standard declaration.
func WriteJUnitReport(w io.Writer, result *RunResult) error {
	if w == nil {
		return fmt.Errorf("JUnit report writer is nil")
	}
	report, err := BuildJUnitReport(result)
	if err != nil {
		return err
	}
	raw, err := xml.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JUnit report: %w", err)
	}
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		return fmt.Errorf("writing JUnit XML header: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("writing JUnit report: %w", err)
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		return fmt.Errorf("writing JUnit report terminator: %w", err)
	}
	return nil
}

// MarshalJUnitReport is the in-memory version of WriteJUnitReport.
func MarshalJUnitReport(result *RunResult) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteJUnitReport(&buf, result); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ReportOptions controls optional file output. The runner does not write into
// the shared project; ExportReports only creates files under the caller's
// chosen local run directory.
type ReportOptions struct {
	Dir           string
	JSONFilename  string
	JUnitFilename string
	FileMode      os.FileMode
}

// ExportReports writes both reports into a caller-selected local directory. On
// failure no partial file is left: each report is encoded into memory first,
// then written with a same-directory temp file and atomic rename.
func ExportReports(result *RunResult, opts ReportOptions) (jsonPath, junitPath string, err error) {
	if result == nil {
		return "", "", fmt.Errorf("export reports: result is nil")
	}
	if strings.TrimSpace(opts.Dir) == "" {
		return "", "", fmt.Errorf("export reports: local output directory is empty")
	}
	jsonBytes, err := MarshalJSONReport(result)
	if err != nil {
		return "", "", err
	}
	junitBytes, err := MarshalJUnitReport(result)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return "", "", fmt.Errorf("creating report directory: %w", err)
	}
	jsonName := opts.JSONFilename
	if jsonName == "" {
		jsonName = "runner-report.json"
	}
	junitName := opts.JUnitFilename
	if junitName == "" {
		junitName = "runner-report.junit.xml"
	}
	mode := opts.FileMode
	if mode == 0 {
		mode = 0o600
	}
	write := func(name string, data []byte) (string, error) {
		if filepath.Base(name) != name || name == "." || name == ".." {
			return "", fmt.Errorf("report filename %q must be a plain filename", name)
		}
		return atomicWriteLocal(opts.Dir, name, data, mode)
	}
	jp, err := write(jsonName, jsonBytes)
	if err != nil {
		return "", "", fmt.Errorf("JSON report: %w", err)
	}
	xm, err := write(junitName, junitBytes)
	if err != nil {
		// Keep the first report: callers can retry with both files. We do not
		// remove a successfully written report as part of a failed export.
		return jp, "", fmt.Errorf("JUnit report: %w", err)
	}
	return jp, xm, nil
}

func atomicWriteLocal(dir, name string, data []byte, mode os.FileMode) (string, error) {
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".gorequest-report-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return path, nil
}
