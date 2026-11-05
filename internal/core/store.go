package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Digest returns a stable hex SHA-256 of b, used to detect external edits.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Store reads and writes files rooted at a single .gorequest directory. All
// relative paths are confined to that directory; path traversal and symlink
// escapes are rejected.
type Store struct {
	Root string
}

// NewStore returns a Store rooted at the given .gorequest directory.
func NewStore(root string) *Store { return &Store{Root: root} }

// resolve maps a project-relative path to an absolute path inside Root,
// rejecting traversal and symlink escape.
func (s *Store) resolve(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path not allowed: %q", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project: %q", rel)
	}
	abs := filepath.Join(s.Root, clean)
	// Confirm the deepest existing ancestor of the target, after resolving
	// symlinks, stays inside Root. New leaf files/directories (whose parent may
	// not exist yet) are checked through their nearest existing ancestor, so a
	// legitimate create never looks like an escape while a real symlink escape
	// is still rejected.
	existing := abs
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	resolvedExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		resolvedExisting = existing
	}
	resolvedRoot, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		resolvedRoot = s.Root
	}
	if resolvedExisting != resolvedRoot && !strings.HasPrefix(resolvedExisting, resolvedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project via symlink: %q", rel)
	}
	return abs, nil
}

// Read returns the bytes of rel and their digest.
func (s *Store) Read(rel string) ([]byte, string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, "", err
	}
	return b, Digest(b), nil
}

// WriteAtomic writes data to rel via a temporary file in the same directory
// followed by an atomic rename, so a crash never leaves a half-written file.
func (s *Store) WriteAtomic(rel string, data []byte) error {
	abs, err := s.resolve(rel)
	if err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, abs)
}

// SaveChecked writes data to rel only if the current on-disk content still
// matches baselineDigest. An empty baseline means "file must not exist yet".
func (s *Store) SaveChecked(rel string, data []byte, baselineDigest string) error {
	abs, err := s.resolve(rel)
	if err != nil {
		return err
	}
	current, readErr := os.ReadFile(abs)
	if baselineDigest == "" {
		if readErr == nil {
			return &ConflictError{Path: rel, Baseline: "", Disk: Digest(current)}
		}
		if !os.IsNotExist(readErr) {
			return readErr
		}
	} else {
		if readErr != nil {
			if os.IsNotExist(readErr) {
				return &ConflictError{Path: rel, Baseline: baselineDigest, Disk: ""}
			}
			return readErr
		}
		if got := Digest(current); got != baselineDigest {
			return &ConflictError{Path: rel, Baseline: baselineDigest, Disk: got}
		}
	}
	return s.WriteAtomic(rel, data)
}

// Exists reports whether rel exists.
func (s *Store) Exists(rel string) bool {
	abs, err := s.resolve(rel)
	if err != nil {
		return false
	}
	_, err = os.Stat(abs)
	return err == nil
}

// List returns the project-relative paths of files under relDir whose name
// matches the predicate. Directories are not returned.
func (s *Store) List(relDir string, match func(name string) bool) ([]string, error) {
	abs, err := s.resolve(relDir)
	if err != nil {
		return nil, err
	}
	var out []string
	err = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if match != nil && !match(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
