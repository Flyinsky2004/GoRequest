package runner

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gorequest/internal/core"
)

const (
	maxDatasetRowsLimit  = 100000
	maxDatasetBytesLimit = 64 << 20
)

// DataRow is one isolated iteration input. Index is 1-based; row 0 means "no
// dataset, single implicit row".
type DataRow struct {
	Index int
	Vars  map[string]string
}

type dataset struct {
	rows   []DataRow
	info   *DatasetInfo
	digest string
}

func (d *dataset) fields() []string {
	if d == nil || d.info == nil {
		return nil
	}
	return d.info.Fields
}

func (d *dataset) placeholderVars() map[string]string {
	out := map[string]string{}
	for _, f := range d.fields() {
		out[f] = "preflight"
	}
	return out
}

// datasetParams validates a dataset spec and returns the effective format and
// resource limits. It is shared by execution (loadDataset) and the read-only
// editor preview so both apply exactly the same hard limits.
func datasetParams(spec *DatasetSpec) (format string, maxRows int, maxBytes int64, err error) {
	if spec == nil {
		return "", 0, 0, fmt.Errorf("dataset is not configured")
	}
	format = strings.ToLower(strings.TrimSpace(spec.Format))
	if format != "csv" && format != "json" {
		return "", 0, 0, fmt.Errorf("dataset format must be csv or json, got %q", spec.Format)
	}
	if strings.TrimSpace(spec.File) == "" {
		return "", 0, 0, fmt.Errorf("dataset file is empty")
	}
	maxBytes = spec.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDatasetBytes
	}
	if maxBytes > maxDatasetBytesLimit {
		return "", 0, 0, fmt.Errorf("dataset maxBytes %d exceeds hard limit %d", maxBytes, maxDatasetBytesLimit)
	}
	maxRows = spec.MaxRows
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}
	if maxRows > maxDatasetRowsLimit {
		return "", 0, 0, fmt.Errorf("dataset maxRows %d exceeds hard limit %d", maxRows, maxDatasetRowsLimit)
	}
	return format, maxRows, maxBytes, nil
}

// loadDataset reads a bounded CSV/JSON dataset through core.Store so relative
// paths cannot escape the project. Format, columns, row count and value types
// are all validated before the first request is sent.
func loadDataset(project *core.Project, spec *DatasetSpec) (*dataset, error) {
	if spec == nil {
		return &dataset{rows: []DataRow{{Index: 0, Vars: map[string]string{}}}}, nil
	}
	if project == nil || project.Store == nil {
		return nil, fmt.Errorf("dataset requires a loaded project")
	}
	format, maxRows, maxBytes, err := datasetParams(spec)
	if err != nil {
		return nil, err
	}

	raw, _, err := project.Store.ReadBounded(spec.File, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("dataset %q: %w", spec.File, err)
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("dataset %q is not valid UTF-8", spec.File)
	}
	sum := sha256.Sum256(raw)
	d := &dataset{digest: hex.EncodeToString(sum[:])}

	var fieldOrder []string
	var rows []map[string]string
	switch format {
	case "csv":
		fieldOrder, rows, err = parseCSVDataset(raw, spec.Delimiter, maxRows)
	case "json":
		fieldOrder, rows, err = parseJSONDataset(raw, maxRows)
	}
	if err != nil {
		return nil, fmt.Errorf("dataset %q: %w", spec.File, err)
	}
	if len(fieldOrder) == 0 {
		return nil, fmt.Errorf("dataset %q has no columns", spec.File)
	}
	if len(fieldOrder) > DefaultMaxColumns {
		return nil, fmt.Errorf("dataset %q has %d columns; limit is %d", spec.File, len(fieldOrder), DefaultMaxColumns)
	}

	selected := fieldOrder
	if len(spec.Fields) > 0 {
		selected, err = projectFields(fieldOrder, spec.Fields)
		if err != nil {
			return nil, fmt.Errorf("dataset %q: %w", spec.File, err)
		}
	}
	for f := range spec.Types {
		if !containsString(selected, f) {
			return nil, fmt.Errorf("dataset %q: type declared for unselected/unknown field %q", spec.File, f)
		}
	}
	for f, kind := range spec.Types {
		switch kind {
		case "string", "number", "boolean":
		default:
			return nil, fmt.Errorf("dataset %q: field %q has unsupported type %q", spec.File, f, kind)
		}
	}

	for i, row := range rows {
		vars := make(map[string]string, len(selected))
		for _, f := range selected {
			val, err := convertFieldValue(row[f], spec.Types[f])
			if err != nil {
				return nil, fmt.Errorf("dataset %q row %d field %q: %w", spec.File, i+1, f, err)
			}
			vars[f] = val
		}
		d.rows = append(d.rows, DataRow{Index: i + 1, Vars: vars})
	}
	d.info = &DatasetInfo{Format: format, File: spec.File, Fields: selected, Rows: len(d.rows)}
	return d, nil
}

func parseCSVDataset(raw []byte, delimiter string, maxRows int) ([]string, []map[string]string, error) {
	fields, rows, _, err := parseCSVDatasetRows(raw, delimiter, maxRows, false)
	return fields, rows, err
}

// parseCSVDatasetRows is the shared CSV reader. With truncate=true it stops
// after maxRows complete rows (or at a prefix boundary) and reports truncation
// instead of failing, which is what the editor preview needs. Execution always
// passes truncate=false so exceeding maxRows stays a hard config error.
func parseCSVDatasetRows(raw []byte, delimiter string, maxRows int, truncate bool) ([]string, []map[string]string, bool, error) {
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	r.LazyQuotes = false
	r.ReuseRecord = false
	if delimiter != "" {
		runes := []rune(delimiter)
		if len(runes) != 1 {
			return nil, nil, false, fmt.Errorf("delimiter must be exactly one character")
		}
		r.Comma = runes[0]
	}
	header, err := r.Read()
	if err != nil {
		return nil, nil, false, fmt.Errorf("reading header: %w", err)
	}
	fields := make([]string, 0, len(header))
	seen := map[string]bool{}
	for _, h := range header {
		h = strings.TrimSpace(h)
		if h == "" {
			return nil, nil, false, fmt.Errorf("header contains an empty column name")
		}
		if seen[h] {
			return nil, nil, false, fmt.Errorf("duplicate column %q", h)
		}
		seen[h] = true
		fields = append(fields, h)
	}
	var rows []map[string]string
	truncated := false
	for {
		rec, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if truncate && errors.Is(err, io.ErrUnexpectedEOF) {
				truncated = true
				break
			}
			return nil, nil, false, fmt.Errorf("row %d: %w", len(rows)+1, err)
		}
		// A bounded preview stops before inspecting the first row beyond its
		// display cap; execution keeps the original column-check-first order.
		if truncate && len(rows) >= maxRows {
			truncated = true
			break
		}
		if len(rec) != len(fields) {
			return nil, nil, false, fmt.Errorf("row %d has %d columns, header has %d", len(rows)+1, len(rec), len(fields))
		}
		if len(rows) >= maxRows {
			return nil, nil, false, fmt.Errorf("dataset exceeds maxRows %d", maxRows)
		}
		m := make(map[string]string, len(fields))
		for i, f := range fields {
			m[f] = rec[i]
		}
		rows = append(rows, m)
	}
	if len(rows) == 0 && !truncated {
		return nil, nil, false, fmt.Errorf("dataset has no data rows")
	}
	return fields, rows, truncated, nil
}

func parseJSONDataset(raw []byte, maxRows int) ([]string, []map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, nil, fmt.Errorf("dataset must contain exactly one JSON document")
	}
	arr, ok := parsed.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("dataset must be an array of objects")
	}
	if len(arr) == 0 {
		return nil, nil, fmt.Errorf("dataset has no data rows")
	}
	if len(arr) > maxRows {
		return nil, nil, fmt.Errorf("dataset exceeds maxRows %d", maxRows)
	}
	var fields []string
	fieldSeen := map[string]bool{}
	rows := make([]map[string]string, 0, len(arr))
	for i, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("row %d is not an object", i+1)
		}
		if i == 0 {
			for k := range obj {
				fields = append(fields, k)
			}
			sort.Strings(fields)
			for _, f := range fields {
				fieldSeen[f] = true
			}
		}
		if len(obj) != len(fields) {
			return nil, nil, fmt.Errorf("row %d has a different field set than row 1", i+1)
		}
		row := make(map[string]string, len(obj))
		for k, v := range obj {
			if !fieldSeen[k] {
				return nil, nil, fmt.Errorf("row %d has unknown field %q", i+1, k)
			}
			s, err := jsonCellString(v)
			if err != nil {
				return nil, nil, fmt.Errorf("row %d field %q: %w", i+1, k, err)
			}
			row[k] = s
		}
		rows = append(rows, row)
	}
	return fields, rows, nil
}

// parseJSONDatasetRows is the shared JSON entry point. Execution passes
// truncate=false and keeps the exact full-document validation above. The editor
// preview passes truncate=true so a large array is streamed only until the
// display row cap, without decoding the rest of the document.
func parseJSONDatasetRows(raw []byte, maxRows int, truncate bool) ([]string, []map[string]string, bool, error) {
	if !truncate {
		fields, rows, err := parseJSONDataset(raw, maxRows)
		return fields, rows, false, err
	}
	return parseJSONDatasetPreview(raw, maxRows)
}

// parseJSONDatasetPreview streams array elements and stops at maxRows. A prefix
// cut mid-element yields the complete rows read so far plus truncated=true.
func parseJSONDatasetPreview(raw []byte, maxRows int) ([]string, []map[string]string, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, false, fmt.Errorf("invalid JSON: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '[' {
		return nil, nil, false, fmt.Errorf("dataset must be an array of objects")
	}
	var fields []string
	fieldSeen := map[string]bool{}
	rows := make([]map[string]string, 0, maxRows)
	truncated := false
	for dec.More() {
		if len(rows) >= maxRows {
			truncated = true
			break
		}
		var item any
		if err := dec.Decode(&item); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return fields, rows, true, nil
			}
			return nil, nil, false, fmt.Errorf("invalid JSON: %w", err)
		}
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, nil, false, fmt.Errorf("row %d is not an object", len(rows)+1)
		}
		if len(rows) == 0 {
			for k := range obj {
				fields = append(fields, k)
			}
			sort.Strings(fields)
			for _, f := range fields {
				fieldSeen[f] = true
			}
		}
		if len(obj) != len(fields) {
			return nil, nil, false, fmt.Errorf("row %d has a different field set than row 1", len(rows)+1)
		}
		row := make(map[string]string, len(obj))
		for k, v := range obj {
			if !fieldSeen[k] {
				return nil, nil, false, fmt.Errorf("row %d has unknown field %q", len(rows)+1, k)
			}
			s, err := jsonCellString(v)
			if err != nil {
				return nil, nil, false, fmt.Errorf("row %d field %q: %w", len(rows)+1, k, err)
			}
			row[k] = s
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 && !truncated {
		return nil, nil, false, fmt.Errorf("dataset has no data rows")
	}
	return fields, rows, truncated, nil
}

// jsonCellString accepts only scalars. Nested objects/arrays are rejected
// rather than flattened silently.
func jsonCellString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case json.Number:
		return t.String(), nil
	case bool:
		return strconv.FormatBool(t), nil
	default:
		return "", fmt.Errorf("unsupported nested JSON value type %T", v)
	}
}

func convertFieldValue(raw, kind string) (string, error) {
	switch kind {
	case "", "string":
		return raw, nil
	case "number":
		s := strings.TrimSpace(raw)
		if s == "" {
			return "", fmt.Errorf("empty value cannot convert to number")
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return "", fmt.Errorf("value %q is not a number", raw)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("value %q is not a finite number", raw)
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true":
			return "true", nil
		case "false":
			return "false", nil
		default:
			return "", fmt.Errorf("value %q is not a boolean", raw)
		}
	default:
		return "", fmt.Errorf("unsupported type %q", kind)
	}
}

func projectFields(available, requested []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(requested))
	for _, f := range requested {
		f = strings.TrimSpace(f)
		if f == "" {
			return nil, fmt.Errorf("empty field name in fields")
		}
		if seen[f] {
			return nil, fmt.Errorf("duplicate field %q in fields", f)
		}
		if !containsString(available, f) {
			return nil, fmt.Errorf("field %q is not present in the dataset", f)
		}
		seen[f] = true
		out = append(out, f)
	}
	return out, nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
