package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Default storage resource limits enforced by bounded read and sync operations.
const (
	DefaultMaxYAMLBytes    int64 = 4 << 20  // 4 MiB per structured YAML file
	DefaultMaxDocBytes     int64 = 16 << 20 // 16 MiB per Markdown document
	DefaultMaxTrackedFiles int   = 10000    // maximum tracked project files per scan
)

// StoreLimits caps file sizes and file counts when scanning or reading a
// project so an untrusted repository cannot exhaust memory. Zero fields fall
// back to the Default* constants.
type StoreLimits struct {
	MaxYAMLBytes    int64
	MaxDocBytes     int64
	MaxTrackedFiles int
}

// Effective returns a copy of l with zero fields replaced by defaults.
func (l StoreLimits) Effective() StoreLimits {
	out := l
	if out.MaxYAMLBytes <= 0 {
		out.MaxYAMLBytes = DefaultMaxYAMLBytes
	}
	if out.MaxDocBytes <= 0 {
		out.MaxDocBytes = DefaultMaxDocBytes
	}
	if out.MaxTrackedFiles <= 0 {
		out.MaxTrackedFiles = DefaultMaxTrackedFiles
	}
	return out
}

// Digest returns a stable hex SHA-256 of b, used to detect external edits.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Store reads and writes files rooted at a single .gorequest directory. All
// relative paths are confined to that directory; path traversal and symlink
// escapes (including dangling symlinks) are rejected.
type Store struct {
	Root   string
	Limits StoreLimits
}

// NewStore returns a Store rooted at the given .gorequest directory.
func NewStore(root string) *Store { return &Store{Root: root} }

// EffectiveLimits returns the store's limits with defaults filled in.
func (s *Store) EffectiveLimits() StoreLimits {
	if s == nil {
		return (StoreLimits{}).Effective()
	}
	return s.Limits.Effective()
}

// resolve maps a project-relative path to an absolute path inside Root,
// rejecting traversal and symlink escape (both active and dangling symlinks).
func (s *Store) resolve(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path not allowed: %q", rel)
	}
	// Also reject Windows-style drive or UNC prefixes on non-Windows hosts when
	// callers pass raw relative strings from portable archives or configs.
	if strings.HasPrefix(rel, `\\`) || (len(rel) >= 2 && rel[1] == ':') {
		return "", fmt.Errorf("absolute path not allowed: %q", rel)
	}
	norm := strings.ReplaceAll(rel, `\`, "/")
	clean := filepath.Clean(filepath.FromSlash(norm))
	if clean == "." {
		absRoot := filepath.Clean(s.Root)
		return absRoot, nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project: %q", rel)
	}
	resolvedRoot, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		resolvedRoot = filepath.Clean(s.Root)
	}

	// Walk existing components under Root so symlinks (including dangling ones
	// whose target does not exist yet) are caught before any read/write.
	cur := s.Root
	for _, seg := range strings.Split(clean, string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return "", fmt.Errorf("path escapes project: %q", rel)
		}
		cur = filepath.Join(cur, seg)
		fi, lerr := os.Lstat(cur)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				// Neither cur nor any deeper child exists yet; all existing
				// ancestors have already been verified to stay inside Root.
				break
			}
			return "", lerr
		}
		resolvedCur, evalErr := filepath.EvalSymlinks(cur)
		if evalErr != nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("dangling or invalid symlink not allowed: %q", rel)
			}
			return "", evalErr
		}
		if resolvedCur != resolvedRoot && !strings.HasPrefix(resolvedCur, resolvedRoot+string(filepath.Separator)) {
			return "", fmt.Errorf("path escapes project via symlink: %q", rel)
		}
	}
	return filepath.Join(s.Root, clean), nil
}

// ResolvePath validates rel against traversal and symlink escape without
// touching the disk contents, returning the normalized slash-separated path.
func (s *Store) ResolvePath(rel string) (string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	out, err := filepath.Rel(s.Root, abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(out), nil
}

// Read returns the bytes of rel and their digest.
func (s *Store) Read(rel string) ([]byte, string, error) {
	limit := s.EffectiveLimits().MaxYAMLBytes
	if strings.HasSuffix(strings.ToLower(rel), ".md") {
		limit = s.EffectiveLimits().MaxDocBytes
	}
	return s.ReadBounded(rel, limit)
}

// ReadBounded reads rel while enforcing maxBytes (or DefaultMaxYAMLBytes when
// maxBytes <= 0) before allocating the full buffer. Oversized or non-regular
// files return a *ConfigError.
func (s *Store) ReadBounded(rel string, maxBytes int64) ([]byte, string, error) {
	if maxBytes <= 0 {
		maxBytes = s.EffectiveLimits().MaxYAMLBytes
	}
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, "", &ConfigError{Err: err}
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !fi.Mode().IsRegular() {
		return nil, "", configErrorf("file %q is not a regular file", rel)
	}
	if fi.Size() > maxBytes {
		return nil, "", configErrorf("file %q exceeds size limit (%d > %d bytes)", rel, fi.Size(), maxBytes)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > maxBytes {
		return nil, "", configErrorf("file %q exceeds size limit (%d > %d bytes)", rel, len(b), maxBytes)
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
	// Refuse to overwrite a symlink directly with an unverified path; resolve()
	// already verified confinement, and if abs itself is an internal symlink we
	// still atomic-replace the link entry rather than following a dangling link.
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

// RemoveChecked deletes rel only if its current on-disk digest matches
// baselineDigest. If the file was modified or already deleted externally when a
// non-empty baseline was expected, *ConflictError is returned without mutating
// disk.
func (s *Store) RemoveChecked(rel string, baselineDigest string) error {
	abs, err := s.resolve(rel)
	if err != nil {
		return err
	}
	current, readErr := os.ReadFile(abs)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			if baselineDigest == "" {
				return nil
			}
			return &ConflictError{Path: rel, Baseline: baselineDigest, Disk: ""}
		}
		return readErr
	}
	if baselineDigest != "" {
		if got := Digest(current); got != baselineDigest {
			return &ConflictError{Path: rel, Baseline: baselineDigest, Disk: got}
		}
	}
	return os.Remove(abs)
}

// Exists reports whether rel exists inside Root.
func (s *Store) Exists(rel string) bool {
	abs, err := s.resolve(rel)
	if err != nil {
		return false
	}
	_, err = os.Stat(abs)
	return err == nil
}

// List returns the project-relative paths of files under relDir whose name
// matches the predicate. Directories, temporary swap files, and escaping/broken
// symlinks are skipped.
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
		if IsTempOrIgnoredName(d.Name()) {
			return nil
		}
		if match != nil && !match(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		slashRel := filepath.ToSlash(rel)
		// Reject symlinked files that point outside Root or are broken.
		if d.Type()&os.ModeSymlink != 0 {
			if _, rerr := s.resolve(slashRel); rerr != nil {
				return nil
			}
		}
		out = append(out, slashRel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// IsTempOrIgnoredName reports whether a base filename is an atomic-write temp
// file or editor swap/backup artifact that should never be treated as project
// data.
func IsTempOrIgnoredName(name string) bool {
	if name == "" || name == ".DS_Store" {
		return true
	}
	if strings.HasPrefix(name, ".tmp-") || (strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#")) {
		return true
	}
	if strings.HasSuffix(name, "~") || strings.HasSuffix(name, ".swp") || strings.HasSuffix(name, ".swo") || strings.HasSuffix(name, ".swx") {
		return true
	}
	return false
}

// NormalizeLF returns data with CRLF and bare CR line endings normalized to LF.
// It returns a fresh slice when normalization occurs or a copy when unchanged.
func NormalizeLF(data []byte) []byte {
	if !bytes.ContainsRune(data, '\r') {
		out := make([]byte, len(data))
		copy(out, data)
		return out
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == '\r' {
			if i+1 < len(data) && data[i+1] == '\n' {
				i++
			}
			out = append(out, '\n')
			continue
		}
		out = append(out, data[i])
	}
	return out
}

// DetectGitConflictMarkers scans data for standard Git merge conflict markers
// (<<<<<<< / ======= / >>>>>>> at the start of lines). It returns the 1-based
// line number of the opening <<<<<<< marker and true when a conflict region is
// present.
func DetectGitConflictMarkers(data []byte) (int, bool) {
	if !bytes.Contains(data, []byte("<<<<<<<")) {
		return 0, false
	}
	lines := bytes.Split(data, []byte{'\n'})
	startLine := 0
	sawSep := false
	for idx, raw := range lines {
		line := bytes.TrimSuffix(raw, []byte{'\r'})
		switch {
		case isConflictMarkerPrefix(line, '<'):
			if startLine == 0 {
				startLine = idx + 1
				sawSep = false
			}
		case startLine > 0 && bytes.Equal(line, []byte("=======")):
			sawSep = true
		case startLine > 0 && sawSep && isConflictMarkerPrefix(line, '>'):
			return startLine, true
		}
	}
	return 0, false
}

// HasGitConflictMarkers reports whether data contains unresolved Git merge
// conflict markers.
func HasGitConflictMarkers(data []byte) bool {
	_, ok := DetectGitConflictMarkers(data)
	return ok
}

func isConflictMarkerPrefix(line []byte, ch byte) bool {
	if len(line) < 7 {
		return false
	}
	for i := 0; i < 7; i++ {
		if line[i] != ch {
			return false
		}
	}
	return len(line) == 7 || line[7] == ' ' || line[7] == '\t'
}
