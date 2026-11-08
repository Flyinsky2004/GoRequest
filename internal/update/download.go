package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Downloaded describes a verified update file in the temporary directory.
type Downloaded struct {
	Path   string
	Name   string
	Size   int64
	SHA256 string
}

// Updater downloads a candidate's asset, verifies it against the published
// SHA-256, and exposes the platform installer. It never replaces the running
// application.
type Updater struct {
	Client   HTTPClient
	TempDir  string
	MaxBytes int64
}

// Download fetches the candidate asset into a fresh temporary directory,
// computes its SHA-256 while writing, and verifies it against the asset's
// GitHub digest or the release's SHA256SUMS file. A missing checksum is an
// error: an unverified installer is never handed to the user.
func (u *Updater) Download(ctx context.Context, cand *Candidate, progress func(done, total int64)) (*Downloaded, error) {
	if cand == nil {
		return nil, &Error{Kind: ErrorDownload, Op: "download", Err: errors.New("no update candidate")}
	}
	if strings.TrimSpace(cand.Asset.URL) == "" {
		return nil, &Error{Kind: ErrorDownload, Op: "download", Err: errors.New("asset has no download URL")}
	}
	maxBytes := u.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDownload
	}
	if cand.Asset.Size > maxBytes {
		return nil, &Error{Kind: ErrorDownload, Op: "download", Err: fmt.Errorf("asset is %d bytes, limit is %d", cand.Asset.Size, maxBytes)}
	}
	client := u.Client
	if client == nil {
		client = &http.Client{}
	}

	dir, err := os.MkdirTemp(u.TempDir, "gorequest-update-")
	if err != nil {
		return nil, &Error{Kind: ErrorDownload, Op: "create temporary directory", Err: err}
	}
	name := safeFileName(cand.Asset.Name)
	dest := filepath.Join(dir, name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cand.Asset.URL, nil)
	if err != nil {
		return nil, &Error{Kind: ErrorDownload, Op: "build download request", Err: err}
	}
	req.Header.Set("User-Agent", "GoRequest-Updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, &Error{Kind: ErrorNetwork, Op: "download", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Kind: ErrorHTTP, Op: "download", Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if resp.ContentLength > maxBytes {
		return nil, &Error{Kind: ErrorDownload, Op: "download", Err: fmt.Errorf("response is %d bytes, limit is %d", resp.ContentLength, maxBytes)}
	}

	file, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, &Error{Kind: ErrorDownload, Op: "create file", Err: err}
	}
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)

	var written int64
	buf := make([]byte, 64<<10)
	lastReport := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			os.Remove(dest)
			return nil, &Error{Kind: ErrorDownload, Op: "download", Err: err}
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxBytes {
				file.Close()
				os.Remove(dest)
				return nil, &Error{Kind: ErrorDownload, Op: "download", Err: fmt.Errorf("download exceeds %d bytes", maxBytes)}
			}
			if _, err := writer.Write(buf[:n]); err != nil {
				file.Close()
				os.Remove(dest)
				return nil, &Error{Kind: ErrorDownload, Op: "write file", Err: err}
			}
			if progress != nil && (written-lastReport >= 256<<10 || readErr == io.EOF) {
				lastReport = written
				progress(written, totalFor(cand, resp.ContentLength))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			os.Remove(dest)
			return nil, &Error{Kind: ErrorNetwork, Op: "download", Err: readErr}
		}
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(dest)
		return nil, &Error{Kind: ErrorDownload, Op: "sync file", Err: err}
	}
	if err := file.Close(); err != nil {
		os.Remove(dest)
		return nil, &Error{Kind: ErrorDownload, Op: "close file", Err: err}
	}

	got := hex.EncodeToString(hash.Sum(nil))
	expected, err := u.expectedChecksum(ctx, client, cand)
	if err != nil {
		os.Remove(dest)
		return nil, err
	}
	if !strings.EqualFold(got, expected) {
		os.Remove(dest)
		return nil, &Error{Kind: ErrorChecksum, Op: "verify checksum", Err: fmt.Errorf("SHA-256 mismatch: got %s, want %s", got, expected)}
	}
	if progress != nil {
		progress(written, totalFor(cand, resp.ContentLength))
	}
	return &Downloaded{Path: dest, Name: name, Size: written, SHA256: got}, nil
}

func totalFor(cand *Candidate, contentLength int64) int64 {
	if cand.Asset.Size > 0 {
		return cand.Asset.Size
	}
	return contentLength
}

func safeFileName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.ReplaceAll(base, string(filepath.Separator), "_")
	if base == "" || base == "." || base == ".." {
		return "update.bin"
	}
	return base
}

// expectedChecksum prefers the digest GitHub reports for the asset and falls
// back to the release's SHA256SUMS file. Both are public, published values.
func (u *Updater) expectedChecksum(ctx context.Context, client HTTPClient, cand *Candidate) (string, error) {
	if digest := strings.TrimSpace(cand.Asset.Digest); digest != "" {
		lower := strings.ToLower(digest)
		if strings.HasPrefix(lower, "sha256:") {
			sum := strings.TrimSpace(lower[len("sha256:"):])
			if isHexSHA256(sum) {
				return sum, nil
			}
		}
	}
	for _, a := range cand.Release.Assets {
		if !isChecksumName(a.Name) {
			continue
		}
		data, err := fetchLimited(ctx, client, a.URL, maxChecksumBytes)
		if err != nil {
			return "", &Error{Kind: ErrorChecksum, Op: "fetch SHA256SUMS", Err: err}
		}
		if sum, ok := checksumFor(data, cand.Asset.Name); ok {
			return sum, nil
		}
	}
	return "", &Error{Kind: ErrorChecksum, Op: "verify checksum", Err: errors.New("release publishes no SHA-256 for this asset")}
}

func fetchLimited(ctx context.Context, client HTTPClient, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoRequest-Updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("checksum file exceeds %d bytes", limit)
	}
	return data, nil
}

// checksumFor finds the SHA-256 of name in a SHA256SUMS file. Both the
// "hash  file" and "hash *file" forms are accepted; comparison is by base
// name so a path prefix in the sums file does not hide the match.
func checksumFor(data []byte, name string) (string, bool) {
	want := filepath.Base(strings.TrimSpace(name))
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		if !isHexSHA256(sum) {
			continue
		}
		file := strings.TrimPrefix(fields[1], "*")
		if filepath.Base(file) == want {
			return sum, true
		}
	}
	return "", false
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// OpenInstaller hands a verified file to the operating system. On macOS this
// opens the disk image (the user still drags the app to Applications); on
// Windows it starts the setup executable. It never claims the update is
// installed and never quits the running app.
func OpenInstaller(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command(path).Start()
	default:
		return fmt.Errorf("automatic installer launch is not supported on %s", runtime.GOOS)
	}
}
