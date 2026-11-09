package sdkgen

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyPreservesFileAppearingDuringWrite(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(opts.TargetDir, 0755); err != nil {
		t.Fatal(err)
	}
	first := plan.Actions[0].RelPath
	plan.beforeWrite = func(rel string) error {
		if rel == first {
			return os.WriteFile(filepath.Join(opts.TargetDir, rel), []byte("external work"), 0644)
		}
		return nil
	}
	if _, err := Apply(plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	data, err := os.ReadFile(filepath.Join(opts.TargetDir, first))
	if err != nil || string(data) != "external work" {
		t.Fatalf("external work overwritten: %q %v", data, err)
	}
}

func TestApplyRollbackPreservesExternalEdit(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatal(err)
	}
	first := plan.Actions[0].RelPath
	second := plan.Actions[1].RelPath
	plan.beforeWrite = func(rel string) error {
		if rel == second {
			if err := os.WriteFile(filepath.Join(opts.TargetDir, first), []byte("external edit"), 0644); err != nil {
				return err
			}
			return errors.New("injected failure")
		}
		return nil
	}
	if _, err := Apply(plan); err == nil {
		t.Fatal("expected write failure")
	}
	data, err := os.ReadFile(filepath.Join(opts.TargetDir, first))
	if err != nil || string(data) != "external edit" {
		t.Fatalf("rollback overwrote external edit: %q %v", data, err)
	}
}

func TestManifestCannotDeleteThroughDirectorySymlink(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	data := []byte("outside manual content")
	if err := os.WriteFile(filepath.Join(outside, "manual.txt"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(opts.TargetDir, "bridge")); err != nil {
		t.Skip(err)
	}
	manifest := plan.Result.Manifest
	manifest.Files = append(manifest.Files, ManifestEntry{Path: "bridge/manual.txt", Digest: digestBytes(data), Kind: "source"})
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opts.TargetDir, ManifestName), raw, 0644); err != nil {
		t.Fatal(err)
	}
	opts.AllowDeleteStale = true
	plan, err = Preview([]byte(petSpec), opts)
	if err == nil {
		if len(plan.Conflicts) == 0 {
			t.Fatal("manifest followed a directory symlink")
		}
		if _, err := Apply(plan); err == nil {
			t.Fatal("unsafe manifest applied")
		}
	}
	got, err := os.ReadFile(filepath.Join(outside, "manual.txt"))
	if err != nil || string(got) != string(data) {
		t.Fatalf("outside file was changed: %q %v", got, err)
	}
}
