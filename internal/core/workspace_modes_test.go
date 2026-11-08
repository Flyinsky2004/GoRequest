package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// --- in-place (existing code) projects ------------------------------------

func TestCreateProjectInPlaceKeepsCodeAndGit(t *testing.T) {
	dir := t.TempDir()
	code := filepath.Join(dir, "main.go")
	if err := os.WriteFile(code, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(dir, ".git", "HEAD")
	if err := os.MkdirAll(filepath.Dir(head), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root, err := CreateProjectInPlace(dir)
	if err != nil {
		t.Fatalf("CreateProjectInPlace: %v", err)
	}
	if root != dir {
		t.Fatalf("root = %q, want the chosen folder %q", root, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gorequest", "project.yaml")); err != nil {
		t.Fatalf(".gorequest not created directly in the code root: %v", err)
	}
	// No extra project-name folder is nested inside the code root.
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(dir))); !os.IsNotExist(err) {
		t.Fatalf("a nested project folder was created: %v", err)
	}
	if got, err := os.ReadFile(code); err != nil || string(got) != "package main\n" {
		t.Fatalf("existing code changed: %q (%v)", got, err)
	}
	if got, err := os.ReadFile(head); err != nil || string(got) != "ref: refs/heads/main\n" {
		t.Fatalf("existing .git changed: %q (%v)", got, err)
	}
	p, err := OpenProject(root)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if p.File.Name != filepath.Base(dir) {
		t.Fatalf("project name = %q, want the folder name %q", p.File.Name, filepath.Base(dir))
	}
}

func TestCreateProjectInPlaceDoesNotInitGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# code\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProjectInPlace(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("git init must not run implicitly: %v", err)
	}
}

func TestCreateProjectInPlaceRefusesExistingProject(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateProjectInPlace(dir); err != nil {
		t.Fatal(err)
	}
	projectFile := filepath.Join(dir, ".gorequest", "project.yaml")
	before, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProjectInPlace(dir); !errors.Is(err, ErrProjectExists) {
		t.Fatalf("second create error = %v, want ErrProjectExists", err)
	}
	after, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("refused create rewrote the existing project")
	}

	// Any .gorequest entry is owned by the user, even when it is not a valid
	// project directory.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, ".gorequest"), []byte("owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProjectInPlace(other); !errors.Is(err, ErrProjectExists) {
		t.Fatalf("bare .gorequest error = %v, want ErrProjectExists", err)
	}
}

func TestCreateProjectInPlaceRejectsBadLocations(t *testing.T) {
	if _, err := CreateProjectInPlace("   "); err == nil {
		t.Fatal("empty location should be rejected")
	}
	if _, err := CreateProjectInPlace(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing folder should be rejected")
	}
	file := filepath.Join(t.TempDir(), "code.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProjectInPlace(file); err == nil {
		t.Fatal("a file is not a code folder")
	}
}

func TestCreateProjectInPlaceRollsBackOnlyItsOwnFolder(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("scaffold failed")
	_, err := createProjectInPlace(dir, func(string, string) (*Project, error) {
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the scaffold error", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gorequest")); !os.IsNotExist(err) {
		t.Fatalf("a failed create left .gorequest behind: %v", err)
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "keep" {
		t.Fatalf("rollback touched existing content: %q (%v)", got, err)
	}
}

// --- independent projects and the shared preview --------------------------

func TestProjectDirForPreviewMatchesCreation(t *testing.T) {
	parent := t.TempDir()
	target, err := ProjectDirFor(parent, "订单 API")
	if err != nil {
		t.Fatalf("ProjectDirFor: %v", err)
	}
	if filepath.Base(target) != "订单 API" {
		t.Fatalf("preview folder = %q", filepath.Base(target))
	}
	root, err := CreateProjectAt(parent, "订单 API")
	if err != nil {
		t.Fatalf("CreateProjectAt: %v", err)
	}
	if root != target {
		t.Fatalf("preview %q and creation %q disagree", target, root)
	}
	for _, name := range []string{"", "   ", "../escape", "a/b", `a\b`, ".", ".."} {
		if _, err := ProjectDirFor(parent, name); err == nil {
			t.Fatalf("name %q should be rejected", name)
		}
	}
}

func TestCreateProjectAtReportsExistingProject(t *testing.T) {
	parent := t.TempDir()
	root, err := CreateProjectAt(parent, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProjectAt(parent, "Demo"); !errors.Is(err, ErrProjectExists) {
		t.Fatalf("error = %v, want ErrProjectExists", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".gorequest", "project.yaml")); err != nil {
		t.Fatal(err)
	}
	if !HasProject(root) {
		t.Fatal("HasProject did not recognize the created project")
	}
	if HasProject(filepath.Join(parent, "nothing-here")) {
		t.Fatal("HasProject reported a project in an empty folder")
	}
}

func TestHasProjectAcceptsTheGorequestDirItself(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateProjectInPlace(dir); err != nil {
		t.Fatal(err)
	}
	if !HasProject(filepath.Join(dir, ".gorequest")) {
		t.Fatal(".gorequest itself should report a project")
	}
}
