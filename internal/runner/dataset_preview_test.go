package runner

import (
	"fmt"
	"strings"
	"testing"

	"gorequest/internal/core"
)

func previewProject(t *testing.T) *core.Project {
	t.Helper()
	dir := t.TempDir()
	p, err := core.NewProject(dir, "Preview")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	return p
}

func TestPreviewDatasetCSVTruncatesAndMasks(t *testing.T) {
	p := previewProject(t)
	var b strings.Builder
	b.WriteString("user,token\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "u%d,t%d\n", i, i)
	}
	if err := p.Store.WriteAtomic("data.csv", []byte(b.String())); err != nil {
		t.Fatal(err)
	}
	spec := &DatasetSpec{Format: "csv", File: "data.csv"}
	pv, err := PreviewDataset(p, spec, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Rows) != DefaultPreviewRows || !pv.RowsTruncated || pv.BytesTruncated {
		t.Fatalf("preview: rows=%d rowsTrunc=%v bytesTrunc=%v", len(pv.Rows), pv.RowsTruncated, pv.BytesTruncated)
	}
	if pv.Rows[0]["token"] != "***" || pv.Rows[0]["user"] != "u0" {
		t.Fatalf("masking wrong: %+v", pv.Rows[0])
	}
	if len(pv.SensitiveFields) != 1 || pv.SensitiveFields[0] != "token" {
		t.Fatalf("sensitive fields: %v", pv.SensitiveFields)
	}
	if pv.EffectiveMaxRows != DefaultMaxRows || pv.EffectiveMaxBytes != DefaultMaxDatasetBytes {
		t.Fatalf("effective limits: %+v", pv)
	}
}

func TestPreviewDatasetStopsBeforeMalformedRowBeyondCap(t *testing.T) {
	p := previewProject(t)
	var b strings.Builder
	b.WriteString("a,b\n")
	for i := 0; i < DefaultPreviewRows; i++ {
		fmt.Fprintf(&b, "%d,%d\n", i, i)
	}
	b.WriteString("malformed\n") // 21st row has one column
	if err := p.Store.WriteAtomic("data.csv", []byte(b.String())); err != nil {
		t.Fatal(err)
	}
	pv, err := PreviewDataset(p, &DatasetSpec{Format: "csv", File: "data.csv"}, 0, 0)
	if err != nil {
		t.Fatalf("preview must stop before the row beyond its cap: %v", err)
	}
	if len(pv.Rows) != DefaultPreviewRows || !pv.RowsTruncated {
		t.Fatalf("preview: rows=%d trunc=%v", len(pv.Rows), pv.RowsTruncated)
	}
	// Execution still validates the whole file and reports the malformed row.
	if err := ValidateDataset(p, &DatasetSpec{Format: "csv", File: "data.csv"}); err == nil {
		t.Fatal("execution validation must reject the malformed row")
	}
}

func TestPreviewDatasetJSONStreamsAndStops(t *testing.T) {
	p := previewProject(t)
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 50; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"user":"u%d"}`, i)
	}
	b.WriteString("]")
	if err := p.Store.WriteAtomic("data.json", []byte(b.String())); err != nil {
		t.Fatal(err)
	}
	pv, err := PreviewDataset(p, &DatasetSpec{Format: "json", File: "data.json"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Rows) != DefaultPreviewRows || !pv.RowsTruncated {
		t.Fatalf("json preview: rows=%d trunc=%v", len(pv.Rows), pv.RowsTruncated)
	}
}

func TestPreviewDatasetBytePrefix(t *testing.T) {
	p := previewProject(t)
	// A single long CSV field pushes the file past the 256 KiB preview cap.
	long := strings.Repeat("x", int(DefaultPreviewBytes)+1024)
	content := "user,note\nu1," + long + "\nu2,short\n"
	if err := p.Store.WriteAtomic("data.csv", []byte(content)); err != nil {
		t.Fatal(err)
	}
	pv, err := PreviewDataset(p, &DatasetSpec{Format: "csv", File: "data.csv"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.BytesTruncated {
		t.Fatalf("expected byte truncation: %+v", pv)
	}
	// The first row is cut mid-field; the preview must still report the header
	// and whatever complete rows it could read, never decode the whole file.
	if len(pv.Fields) != 2 {
		t.Fatalf("fields: %v", pv.Fields)
	}
}

func TestPreviewDatasetRejectsBadInput(t *testing.T) {
	p := previewProject(t)
	if err := p.Store.WriteAtomic("bad.csv", []byte("a,b\n1\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewDataset(p, &DatasetSpec{Format: "csv", File: "bad.csv"}, 0, 0); err == nil {
		t.Fatal("short row must fail")
	}
	if _, err := PreviewDataset(p, &DatasetSpec{Format: "csv", File: "missing.csv"}, 0, 0); err == nil {
		t.Fatal("missing file must fail")
	}
	if _, err := PreviewDataset(p, &DatasetSpec{Format: "csv", File: "../escape.csv"}, 0, 0); err == nil {
		t.Fatal("path escape must fail")
	}
	if _, err := PreviewDataset(p, &DatasetSpec{Format: "yaml", File: "bad.csv"}, 0, 0); err == nil {
		t.Fatal("unsupported format must fail")
	}
}

func TestValidateDatasetUsesExecutionLimits(t *testing.T) {
	p := previewProject(t)
	var b strings.Builder
	b.WriteString("n\n")
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	if err := p.Store.WriteAtomic("data.csv", []byte(b.String())); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDataset(p, &DatasetSpec{Format: "csv", File: "data.csv", MaxRows: 3}); err == nil || !strings.Contains(err.Error(), "maxRows") {
		t.Fatalf("want maxRows error, got %v", err)
	}
	if err := ValidateDataset(p, &DatasetSpec{Format: "csv", File: "data.csv", MaxRows: 10}); err != nil {
		t.Fatalf("valid dataset: %v", err)
	}
}
