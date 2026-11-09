package runner

// Read-only dataset ports for the Suite editor (A02 phase 3). Execution still
// goes through loadDataset; these functions only expose the same parsing code to
// the editor so it never reimplements CSV/JSON semantics. Nothing here reads a
// SecretProvider: dataset files contain plain values or {{secret.name}}
// references, and the preview never resolves those references.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"gorequest/internal/core"
)

const (
	// DefaultPreviewRows bounds how many dataset rows the editor displays.
	DefaultPreviewRows = 20
	// DefaultPreviewBytes bounds how much of a dataset file the editor reads.
	DefaultPreviewBytes int64 = 256 << 10
)

// DatasetPreview is a bounded, read-only view of one dataset file. Sensitive
// column values are masked before they leave this package.
type DatasetPreview struct {
	Format            string
	File              string
	Fields            []string
	Rows              []map[string]string
	SensitiveFields   []string
	RowsTruncated     bool
	BytesTruncated    bool
	EffectiveMaxRows  int
	EffectiveMaxBytes int64
}

// IsSensitiveField reports whether a dataset column name matches the same
// heuristic the engine uses for extraction and row redaction.
func IsSensitiveField(name string) bool {
	return sensitiveJSONKeyPattern.MatchString(name)
}

// ValidateDataset runs the exact execution-time loader (format, columns, row
// count, field set and type conversion) without sending any request. Saving a
// suite calls this so a stored dataset is known to be runnable.
func ValidateDataset(project *core.Project, spec *DatasetSpec) error {
	if spec == nil {
		return nil
	}
	_, err := loadDataset(project, spec)
	return err
}

// PreviewDataset reads at most maxBytes (default 256 KiB) from the dataset and
// parses at most maxRows (default 20) rows with the shared CSV/JSON readers.
// The effective execution limits are returned so the UI can state them
// explicitly; truncation is reported rather than silently hidden.
func PreviewDataset(project *core.Project, spec *DatasetSpec, maxRows int, maxBytes int64) (*DatasetPreview, error) {
	if spec == nil {
		return nil, fmt.Errorf("dataset is not configured")
	}
	if project == nil || project.Store == nil {
		return nil, fmt.Errorf("dataset requires a loaded project")
	}
	format, effRows, effBytes, err := datasetParams(spec)
	if err != nil {
		return nil, err
	}
	if maxRows <= 0 {
		maxRows = DefaultPreviewRows
	}
	if maxBytes <= 0 {
		maxBytes = DefaultPreviewBytes
	}
	if maxBytes > effBytes {
		maxBytes = effBytes
	}
	raw, bytesTruncated, err := readDatasetPrefix(project.Store, spec.File, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("dataset %q: %w", spec.File, err)
	}
	if bytesTruncated {
		raw = trimIncompleteUTF8(raw)
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("dataset %q is not valid UTF-8", spec.File)
	}

	var fields []string
	var rows []map[string]string
	var rowsTruncated bool
	switch format {
	case "csv":
		fields, rows, rowsTruncated, err = parseCSVDatasetRows(raw, spec.Delimiter, maxRows, true)
	case "json":
		fields, rows, rowsTruncated, err = parseJSONDatasetRows(raw, maxRows, true)
	}
	if err != nil {
		if bytesTruncated {
			return nil, fmt.Errorf("dataset %q: preview of the first %d bytes failed: %w", spec.File, len(raw), err)
		}
		return nil, fmt.Errorf("dataset %q: %w", spec.File, err)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("dataset %q has no columns (preview truncated at %d bytes before a complete row)", spec.File, len(raw))
	}

	var sensitive []string
	for _, f := range fields {
		if IsSensitiveField(f) {
			sensitive = append(sensitive, f)
		}
	}
	masked := make([]map[string]string, len(rows))
	for i, row := range rows {
		cp := make(map[string]string, len(row))
		for k, v := range row {
			if IsSensitiveField(k) && v != "" {
				cp[k] = "***"
				continue
			}
			cp[k] = v
		}
		masked[i] = cp
	}

	return &DatasetPreview{
		Format:            format,
		File:              spec.File,
		Fields:            fields,
		Rows:              masked,
		SensitiveFields:   sensitive,
		RowsTruncated:     rowsTruncated,
		BytesTruncated:    bytesTruncated,
		EffectiveMaxRows:  effRows,
		EffectiveMaxBytes: effBytes,
	}, nil
}

// readDatasetPrefix resolves the path through core.Store (rejecting traversal
// and symlink escapes) and reads at most maxBytes from a regular file.
func readDatasetPrefix(store *core.Store, rel string, maxBytes int64) ([]byte, bool, error) {
	if store == nil {
		return nil, false, fmt.Errorf("store is nil")
	}
	resolved, err := store.ResolvePath(rel)
	if err != nil {
		return nil, false, err
	}
	if resolved == "" || resolved == "." {
		return nil, false, fmt.Errorf("path is not a file")
	}
	abs := filepath.Join(store.Root, filepath.FromSlash(resolved))
	f, err := os.Open(abs)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !fi.Mode().IsRegular() {
		return nil, false, fmt.Errorf("file %q is not a regular file", rel)
	}
	size := fi.Size()
	limit := maxBytes
	if limit <= 0 || limit > size {
		limit = size
	}
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, false, err
	}
	return b, size > int64(len(b)), nil
}

// trimIncompleteUTF8 drops a trailing partial rune produced by a byte-prefix
// read. A genuinely invalid byte (bad leading byte or bad continuation) is left
// in place so the caller still reports it.
func trimIncompleteUTF8(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	i := len(b) - 1
	for i >= 0 && b[i]&0xC0 == 0x80 { // skip continuation bytes
		i--
	}
	if i < 0 {
		return b
	}
	want := utf8SequenceLen(b[i])
	if want == 0 {
		return b // invalid leading byte: report it
	}
	if len(b)-i < want {
		return b[:i] // incomplete final rune
	}
	return b
}

func utf8SequenceLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	default:
		return 0
	}
}
