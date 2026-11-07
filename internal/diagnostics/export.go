package diagnostics

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gorequest/internal/core"
)

// ExportFormat identifies the local file format for diagnostic report export.
type ExportFormat string

const (
	ExportJSON ExportFormat = "json"
	ExportCSV  ExportFormat = "csv"
)

// NormalizeExportFormat validates and normalizes a report export format.
func NormalizeExportFormat(raw ExportFormat) (ExportFormat, error) {
	switch strings.ToLower(strings.TrimSpace(string(raw))) {
	case "", string(ExportJSON):
		return ExportJSON, nil
	case string(ExportCSV):
		return ExportCSV, nil
	default:
		return "", fmt.Errorf("unsupported export format %q (expected json or csv)", raw)
	}
}

// MarshalJSONReport serializes a Report as indented JSON with a trailing
// newline, applying red as an extra safeguard if non-nil.
func MarshalJSONReport(rep *Report, red *core.Redactor) ([]byte, error) {
	if rep == nil {
		return nil, errors.New("cannot marshal nil diagnostics report")
	}
	cp := *rep
	if cp.FormatVersion == 0 {
		cp.FormatVersion = ReportFormatVersion
	}
	if cp.ScopeDisclaimer == "" {
		cp.ScopeDisclaimer = ScopeDisclaimer
	}
	raw, err := json.MarshalIndent(&cp, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling diagnostics report JSON: %w", err)
	}
	raw = append(raw, '\n')
	if red != nil {
		raw = []byte(red.RedactString(string(raw)))
	}
	return raw, nil
}

// WriteJSONReport writes the JSON-serialized Report to w.
func WriteJSONReport(w io.Writer, rep *Report, red *core.Redactor) error {
	if w == nil {
		return errors.New("nil output writer")
	}
	data, err := MarshalJSONReport(rep, red)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// csvColumns defines the deterministic column order for CSV exports.
// Stage durations are paired with stage observation statuses so unobserved or
// reused stages are represented with an empty numeric cell rather than 0ms.
var csvColumns = []string{
	"run_id",
	"snapshot_digest",
	"request_id",
	"request_path",
	"method",
	"environment",
	"connection_strategy",
	"reuse_support",
	"seed",
	"sample_index",
	"phase",
	"started_at",
	"outcome",
	"status_code",
	"proto",
	"final_url",
	"reused_conn",
	"proxy_used",
	"redirect_count",
	"truncated",
	"total_duration_ms",
	"dns_status",
	"dns_ms",
	"tcp_connect_status",
	"tcp_connect_ms",
	"tls_handshake_status",
	"tls_handshake_ms",
	"conn_wait_status",
	"conn_wait_ms",
	"request_write_status",
	"request_write_ms",
	"server_wait_status",
	"server_wait_ms",
	"ttfb_status",
	"ttfb_ms",
	"download_status",
	"download_ms",
	"hop_total_status",
	"hop_total_ms",
	"error",
}

// MarshalCSVReport serializes a Report's warmup and measured samples into CSV.
// Unobserved, reused, not-applicable, and incomplete stage durations are left
// blank (never coerced to 0) alongside their explicit *_status column.
func MarshalCSVReport(rep *Report, red *core.Redactor) ([]byte, error) {
	if rep == nil {
		return nil, errors.New("cannot marshal nil diagnostics report")
	}
	redact := func(s string) string {
		if red != nil {
			return red.RedactString(s)
		}
		return s
	}

	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	if err := cw.Write(csvColumns); err != nil {
		return nil, fmt.Errorf("writing CSV header: %w", err)
	}

	writeSampleRow := func(s SampleRecord, phase string) error {
		reusedCell := "unknown"
		if s.ReusedKnown {
			if s.ReusedConn {
				reusedCell = "true"
			} else {
				reusedCell = "false"
			}
		}
		statusCodeCell := ""
		if s.StatusCode > 0 {
			statusCodeCell = strconv.Itoa(s.StatusCode)
		}
		startedAtCell := ""
		if !s.StartedAt.IsZero() {
			startedAtCell = s.StartedAt.UTC().Format("2006-01-02T15:04:05.000Z")
		}

		row := []string{
			redact(rep.RunID),
			redact(rep.SnapshotDigest),
			redact(rep.RequestID),
			redact(rep.RequestPath),
			redact(rep.Method),
			redact(rep.Environment),
			string(rep.Config.ConnectionStrategy),
			string(rep.ReuseSupport),
			strconv.FormatInt(rep.Config.Seed, 10),
			strconv.Itoa(s.Index),
			phase,
			startedAtCell,
			string(s.Outcome),
			statusCodeCell,
			redact(s.Proto),
			redact(s.FinalURL),
			reusedCell,
			redact(s.ProxyUsed),
			strconv.Itoa(s.RedirectCount),
			strconv.FormatBool(s.Truncated),
			formatCSVFloat(s.DurationMs),
		}

		for _, kind := range AllStageKinds {
			meas, ok := selectSampleStageMeasurement(s, kind)
			if !ok {
				row = append(row, string(ObsUnobserved), "")
				continue
			}
			statusStr := string(meas.Status)
			if statusStr == "" {
				statusStr = string(ObsUnobserved)
			}
			durStr := ""
			if meas.Status == ObsObserved && s.Outcome == OutcomeSuccess && !s.Truncated {
				durStr = formatCSVFloat(meas.DurationMs)
			}
			row = append(row, statusStr, durStr)
		}

		row = append(row, redact(s.Error))
		return cw.Write(row)
	}

	for _, s := range rep.WarmupSamples {
		if err := writeSampleRow(s, "warmup"); err != nil {
			return nil, fmt.Errorf("writing warmup CSV row: %w", err)
		}
	}
	for _, s := range rep.Samples {
		if err := writeSampleRow(s, "measured"); err != nil {
			return nil, fmt.Errorf("writing measured CSV row: %w", err)
		}
	}

	cw.Flush()
	if err := cw.Error(); err != nil {
		return nil, fmt.Errorf("flushing CSV report: %w", err)
	}
	out := buf.Bytes()
	if red != nil {
		out = []byte(red.RedactString(string(out)))
	}
	return out, nil
}

// WriteCSVReport writes the CSV-serialized Report to w.
func WriteCSVReport(w io.Writer, rep *Report, red *core.Redactor) error {
	if w == nil {
		return errors.New("nil output writer")
	}
	data, err := MarshalCSVReport(rep, red)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func formatCSVFloat(v float64) string {
	return strconv.FormatFloat(roundMetric(v), 'f', 3, 64)
}

// ExportReportFile writes the report in JSON or CSV format to a user-selected
// local file path outside the shared project directory. It resolves parent
// directory symlinks before checking confinement against projectRoot, .gorequest,
// and .git, and rejects a symbolic link or non-regular file at the final target.
func ExportReportFile(
	targetPath string,
	projectRoot string,
	format ExportFormat,
	rep *Report,
	red *core.Redactor,
) error {
	if rep == nil {
		return errors.New("no diagnostics report available to export")
	}
	normFmt, err := NormalizeExportFormat(format)
	if err != nil {
		return err
	}
	cleanTarget := strings.TrimSpace(targetPath)
	if cleanTarget == "" || !filepath.IsAbs(cleanTarget) {
		return errors.New("choose an absolute local export file path")
	}
	for _, seg := range strings.Split(filepath.ToSlash(cleanTarget), "/") {
		if seg == ".gorequest" || seg == ".git" {
			return fmt.Errorf("refusing to write diagnostic report inside reserved directory %q", seg)
		}
	}
	if info, err := os.Lstat(cleanTarget); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("export destination must be a regular file (not a symlink or directory)")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	parentDir, err := filepath.EvalSymlinks(filepath.Dir(cleanTarget))
	if err != nil {
		return fmt.Errorf("export parent directory is unavailable: %w", err)
	}
	parentInfo, err := os.Stat(parentDir)
	if err != nil || !parentInfo.IsDir() {
		return errors.New("export parent path must be an existing directory")
	}
	for dir := parentDir; ; dir = filepath.Dir(dir) {
		base := filepath.Base(dir)
		if base == ".gorequest" || base == ".git" {
			return fmt.Errorf("refusing to write diagnostic report inside reserved directory %q", base)
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}

	if trimmedRoot := strings.TrimSpace(projectRoot); trimmedRoot != "" {
		realRoot, err := filepath.EvalSymlinks(trimmedRoot)
		if err != nil {
			realRoot, err = filepath.Abs(trimmedRoot)
			if err != nil {
				return err
			}
		}
		rel, err := filepath.Rel(realRoot, parentDir)
		if err != nil {
			return err
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return errors.New("refusing to write diagnostic report inside the shared project directory; choose an external local path")
		}
	}

	resolvedTarget := filepath.Join(parentDir, filepath.Base(cleanTarget))
	if info, err := os.Lstat(resolvedTarget); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("export destination must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	var payload []byte
	switch normFmt {
	case ExportJSON:
		payload, err = MarshalJSONReport(rep, red)
	case ExportCSV:
		payload, err = MarshalCSVReport(rep, red)
	}
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(parentDir, ".gorequest-diag-export-*")
	if err != nil {
		return fmt.Errorf("creating temporary export file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("setting export file permissions: %w", err)
	}
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing export file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("syncing export file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temporary export file: %w", err)
	}

	if err := os.Rename(tmpPath, resolvedTarget); err != nil {
		return fmt.Errorf("committing exported report file: %w", err)
	}
	cleanupTmp = false
	return nil
}
