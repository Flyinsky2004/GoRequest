package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func candidateWithAssets(assetName, assetURL, digest string, extra ...Asset) *Candidate {
	r := Release{
		Tag:     "v0.0.2-dev",
		Version: Version{Major: 0, Minor: 0, Patch: 2, Pre: []string{"dev"}},
		Assets:  append([]Asset{{Name: assetName, URL: assetURL, Digest: digest}}, extra...),
	}
	return &Candidate{Release: r, Asset: r.Assets[0]}
}

func TestDownloadVerifiesSHA256SUMS(t *testing.T) {
	body := "installer-bytes"
	sum := sha256Hex(body)
	cand := candidateWithAssets("gorequest-0.0.2-arm64.dmg", "https://example.test/app.dmg", "",
		Asset{Name: "SHA256SUMS", URL: "https://example.test/SHA256SUMS"})
	f := &fakeHTTP{responses: map[string]fakeResponse{
		"https://example.test/app.dmg":    {status: 200, body: body},
		"https://example.test/SHA256SUMS": {status: 200, body: sum + "  gorequest-0.0.2-arm64.dmg\n"},
	}}
	u := &Updater{Client: f, TempDir: t.TempDir()}
	got, err := u.Download(context.Background(), cand, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != sum {
		t.Fatalf("sha = %s, want %s", got.SHA256, sum)
	}
	data, err := os.ReadFile(got.Path)
	if err != nil || string(data) != body {
		t.Fatalf("downloaded file = %q, %v", data, err)
	}
	if filepath.Base(got.Path) != "gorequest-0.0.2-arm64.dmg" {
		t.Fatalf("file name = %q", got.Path)
	}
}

func TestDownloadVerifiesGitHubDigest(t *testing.T) {
	body := "digest-bytes"
	sum := sha256Hex(body)
	cand := candidateWithAssets("gorequest-setup-0.0.2-amd64.exe", "https://example.test/setup.exe", "sha256:"+sum)
	f := &fakeHTTP{responses: map[string]fakeResponse{
		"https://example.test/setup.exe": {status: 200, body: body},
	}}
	u := &Updater{Client: f, TempDir: t.TempDir()}
	got, err := u.Download(context.Background(), cand, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != sum {
		t.Fatalf("sha = %s, want %s", got.SHA256, sum)
	}
}

func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	cand := candidateWithAssets("app.dmg", "https://example.test/app.dmg", "sha256:"+strings.Repeat("0", 64))
	f := &fakeHTTP{responses: map[string]fakeResponse{
		"https://example.test/app.dmg": {status: 200, body: "tampered"},
	}}
	u := &Updater{Client: f, TempDir: t.TempDir()}
	_, err := u.Download(context.Background(), cand, nil)
	if kind, ok := KindOf(err); !ok || kind != ErrorChecksum {
		t.Fatalf("kind = %v, %v; want checksum (err=%v)", kind, ok, err)
	}
}

func TestDownloadRequiresChecksum(t *testing.T) {
	cand := candidateWithAssets("app.dmg", "https://example.test/app.dmg", "")
	f := &fakeHTTP{responses: map[string]fakeResponse{
		"https://example.test/app.dmg": {status: 200, body: "bytes"},
	}}
	u := &Updater{Client: f, TempDir: t.TempDir()}
	_, err := u.Download(context.Background(), cand, nil)
	if kind, ok := KindOf(err); !ok || kind != ErrorChecksum {
		t.Fatalf("kind = %v, %v; want checksum (err=%v)", kind, ok, err)
	}
}

func TestDownloadCancellation(t *testing.T) {
	cand := candidateWithAssets("app.dmg", "https://example.test/app.dmg", "sha256:"+strings.Repeat("a", 64))
	f := &fakeHTTP{responses: map[string]fakeResponse{
		"https://example.test/app.dmg": {status: 200, body: strings.Repeat("x", 4096)},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u := &Updater{Client: f, TempDir: t.TempDir()}
	_, err := u.Download(ctx, cand, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestDownloadRejectsOversize(t *testing.T) {
	cand := candidateWithAssets("app.dmg", "https://example.test/app.dmg", "sha256:"+strings.Repeat("a", 64))
	f := &fakeHTTP{responses: map[string]fakeResponse{
		"https://example.test/app.dmg": {status: 200, body: strings.Repeat("x", 100)},
	}}
	u := &Updater{Client: f, TempDir: t.TempDir(), MaxBytes: 10}
	_, err := u.Download(context.Background(), cand, nil)
	if kind, ok := KindOf(err); !ok || kind != ErrorDownload {
		t.Fatalf("kind = %v, %v; want download (err=%v)", kind, ok, err)
	}
}

func TestChecksumFor(t *testing.T) {
	sum := strings.Repeat("a", 64)
	data := []byte(sum + "  app.dmg\n" + sum + " *nested/setup.exe\n")
	if got, ok := checksumFor(data, "app.dmg"); !ok || got != sum {
		t.Fatalf("checksumFor app.dmg = %q, %v", got, ok)
	}
	if got, ok := checksumFor(data, "setup.exe"); !ok || got != sum {
		t.Fatalf("checksumFor setup.exe = %q, %v", got, ok)
	}
	if _, ok := checksumFor(data, "missing.dmg"); ok {
		t.Fatal("unexpected checksum match")
	}
}

func TestSafeFileName(t *testing.T) {
	if got := safeFileName("../../etc/passwd"); got != "passwd" {
		t.Fatalf("safeFileName = %q", got)
	}
	if got := safeFileName(""); got != "update.bin" {
		t.Fatalf("safeFileName empty = %q", got)
	}
}
