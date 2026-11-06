package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultMaxUploadBytes caps the total encoded request body / attachment upload
// size in memory or on a stream (64 MiB by default). Callers may set a smaller
// or larger positive cap via PlanOptions.MaxUploadBytes.
const DefaultMaxUploadBytes int64 = 64 << 20

const uploadReadChunkSize = 32 << 10

// OpenedFile is an opened regular file ready for bounded upload reading.
type OpenedFile struct {
	Reader io.ReadCloser
	Name   string // base filename (used as default multipart filename)
	Size   int64  // >= 0 when known
}

// ProjectFileOpener opens files strictly confined to a project's .gorequest
// directory.
type ProjectFileOpener interface {
	OpenProjectFile(ctx context.Context, rel string) (*OpenedFile, error)
}

// LocalFileProvider resolves an explicit local-machine file mapping slot (from
// "local:<slot>" or localFile) to an absolute path on the current device.
// Machine paths are never stored in shared .gorequest files.
type LocalFileProvider interface {
	ResolveLocalFile(slot string) (absPath string, ok bool)
}

// MapLocalFiles is an in-memory LocalFileProvider for tests and explicit
// bindings.
type MapLocalFiles map[string]string

// ResolveLocalFile returns the bound absolute path for slot, if present.
func (m MapLocalFiles) ResolveLocalFile(slot string) (string, bool) {
	if m == nil {
		return "", false
	}
	p, ok := m[slot]
	return p, ok
}

// DirFileOpener opens files relative to Root (typically .gorequest/), rejecting
// path traversal, symlink escapes, broken symlinks and non-regular files.
type DirFileOpener struct {
	Root string
}

// NewDirFileOpener returns a ProjectFileOpener rooted at root.
func NewDirFileOpener(root string) *DirFileOpener {
	return &DirFileOpener{Root: root}
}

// OpenProjectFile resolves and opens rel inside d.Root. Every failure before
// transfer is classified as a ConfigError (unless ctx was already canceled).
func (d *DirFileOpener) OpenProjectFile(ctx context.Context, rel string) (*OpenedFile, error) {
	if err := checkUploadContext(ctx); err != nil {
		return nil, err
	}
	if d == nil || strings.TrimSpace(d.Root) == "" {
		return nil, configErrorf("body file %q requires a project directory", rel)
	}
	cleanRel, err := ValidateProjectRelPath(rel)
	if err != nil {
		return nil, err
	}

	rootAbs, err := filepath.Abs(d.Root)
	if err != nil {
		return nil, configErrorf("invalid project root: %v", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, configErrorf("project root %q not accessible: %v", d.Root, err)
	}

	candidate := filepath.Join(rootAbs, filepath.FromSlash(cleanRel))
	if _, err := os.Lstat(candidate); err != nil {
		if os.IsNotExist(err) {
			return nil, configErrorf("attachment file %q not found in project", cleanRel)
		}
		return nil, configErrorf("cannot stat attachment file %q: %v", cleanRel, err)
	}

	targetReal, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, configErrorf("attachment file %q not found (broken symlink or missing target)", cleanRel)
		}
		return nil, configErrorf("cannot resolve attachment file %q: %v", cleanRel, err)
	}

	prefix := rootReal + string(filepath.Separator)
	if targetReal == rootReal || !strings.HasPrefix(targetReal, prefix) {
		return nil, configErrorf("attachment file %q escapes project directory via symlink", cleanRel)
	}

	f, err := os.Open(targetReal)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, configErrorf("attachment file %q not found in project", cleanRel)
		}
		return nil, configErrorf("cannot open attachment file %q: %v", cleanRel, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, configErrorf("cannot stat attachment file %q: %v", cleanRel, err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, configErrorf("attachment %q must be a regular file", cleanRel)
	}

	return &OpenedFile{
		Reader: f,
		Name:   filepath.Base(cleanRel),
		Size:   info.Size(),
	}, nil
}

func openLocalMappedFile(ctx context.Context, provider LocalFileProvider, slot string) (*OpenedFile, error) {
	if err := checkUploadContext(ctx); err != nil {
		return nil, err
	}
	if err := ValidateLocalSlot(slot); err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, configErrorf("local file mapping %q is not bound on this machine", slot)
	}
	mapped, ok := provider.ResolveLocalFile(slot)
	mapped = strings.TrimSpace(mapped)
	if !ok || mapped == "" {
		return nil, configErrorf("local file mapping %q is not bound on this machine", slot)
	}
	if !filepath.IsAbs(mapped) {
		return nil, configErrorf("local file mapping %q must resolve to an absolute local path, got %q", slot, mapped)
	}
	f, err := os.Open(mapped)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, configErrorf("mapped local file for %q (%s) does not exist", slot, mapped)
		}
		return nil, configErrorf("cannot open mapped local file for %q: %v", slot, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, configErrorf("cannot stat mapped local file for %q: %v", slot, err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, configErrorf("mapped local path for %q (%s) is not a regular file", slot, mapped)
	}
	return &OpenedFile{
		Reader: f,
		Name:   filepath.Base(mapped),
		Size:   info.Size(),
	}, nil
}

func openBodyFileRef(ctx context.Context, ref FileRef, opts PlanOptions) (*OpenedFile, error) {
	switch ref.Kind {
	case FileRefProject:
		opener := opts.ProjectFiles
		if opener == nil && strings.TrimSpace(opts.ProjectRoot) != "" {
			opener = NewDirFileOpener(opts.ProjectRoot)
		}
		if opener == nil {
			return nil, configErrorf("body file %q requires a project file resolver", ref.Path)
		}
		return opener.OpenProjectFile(ctx, ref.Path)
	case FileRefLocal:
		return openLocalMappedFile(ctx, opts.LocalFiles, ref.Slot)
	default:
		return nil, configErrorf("no file reference configured")
	}
}

func checkUploadContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return &RunError{Err: fmt.Errorf("request timed out")}
		}
		return &RunError{Err: fmt.Errorf("request canceled")}
	default:
		return nil
	}
}

// copyBoundedWithContext copies from src into dst in chunks, honoring ctx
// cancellation between chunks and enforcing that *used never exceeds maxBytes.
func copyBoundedWithContext(ctx context.Context, dst io.Writer, src io.Reader, knownSize int64, used *int64, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxUploadBytes
	}
	if knownSize >= 0 && *used+knownSize > maxBytes {
		return configErrorf("request body exceeds maximum upload size of %d bytes", maxBytes)
	}
	buf := make([]byte, uploadReadChunkSize)
	for {
		if err := checkUploadContext(ctx); err != nil {
			return err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			*used += int64(n)
			if *used > maxBytes {
				return configErrorf("request body exceeds maximum upload size of %d bytes", maxBytes)
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return configErrorf("encoding request body: %v", werr)
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			if cerr := checkUploadContext(ctx); cerr != nil {
				return cerr
			}
			return configErrorf("reading upload file: %v", rerr)
		}
	}
}

func readFileRefBounded(ctx context.Context, ref FileRef, opts PlanOptions, used *int64, maxBytes int64) ([]byte, string, error) {
	opened, err := openBodyFileRef(ctx, ref, opts)
	if err != nil {
		return nil, "", err
	}
	defer opened.Reader.Close()

	capHint := 0
	if opened.Size > 0 && opened.Size <= maxBytes {
		capHint = int(opened.Size)
	}
	var buf bytes.Buffer
	if capHint > 0 {
		buf.Grow(capHint)
	}
	if err := copyBoundedWithContext(ctx, &buf, opened.Reader, opened.Size, used, maxBytes); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), opened.Name, nil
}

// EncodedBodyStream is a streaming upload port (for binary / multipart / large
// bodies) that enforces ctx cancellation and MaxUploadBytes on every Read.
// B04's transport layer can consume Reader directly without buffering entire
// files in memory.
type EncodedBodyStream struct {
	Reader           io.ReadCloser
	ContentType      string
	ContentLength    int64 // -1 when unknown (chunked multipart stream)
	ForceContentType bool
	HasBody          bool
}

type boundedCancelReadCloser struct {
	ctx      context.Context
	rc       io.ReadCloser
	used     int64
	maxBytes int64
}

func (b *boundedCancelReadCloser) Read(p []byte) (int, error) {
	if err := checkUploadContext(b.ctx); err != nil {
		return 0, err
	}
	n, err := b.rc.Read(p)
	if n > 0 {
		b.used += int64(n)
		if b.used > b.maxBytes {
			_ = b.rc.Close()
			return 0, configErrorf("request body exceeds maximum upload size of %d bytes", b.maxBytes)
		}
	}
	if err != nil && err != io.EOF {
		if cerr := checkUploadContext(b.ctx); cerr != nil {
			return n, cerr
		}
	}
	return n, err
}

func (b *boundedCancelReadCloser) Close() error {
	if b.rc == nil {
		return nil
	}
	return b.rc.Close()
}
