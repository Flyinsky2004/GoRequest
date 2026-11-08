package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The update source is fixed to the project's public GitHub releases. No
// credential is used or stored: only public, read-only API requests.
const (
	RepoOwner      = "Flyinsky2004"
	RepoName       = "GoRequest"
	ReleasesPage   = "https://github.com/" + RepoOwner + "/" + RepoName + "/releases"
	DefaultAPIBase = "https://api.github.com/repos/" + RepoOwner + "/" + RepoName + "/releases"

	// DefaultPerPage and DefaultMaxPages bound pagination. GitHub returns
	// releases newest-first; three pages (300 releases) is far beyond this
	// project's history and keeps a broken or hostile response from looping.
	DefaultPerPage  = 100
	DefaultMaxPages = 3

	// Response bodies are bounded before they are parsed or written.
	maxAPIPageBytes    = 2 << 20
	maxChecksumBytes   = 1 << 20
	maxReleaseNotes    = 4000
	DefaultMaxDownload = 1 << 30 // 1 GiB
)

// HTTPClient is the slice of *http.Client the checker and downloader need.
// Tests inject an in-memory implementation; no test performs a real request.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name   string
	URL    string
	Size   int64
	Digest string // "sha256:<hex>" when GitHub provides it
}

// Release is one GitHub release, reduced to the fields the updater uses.
type Release struct {
	Tag         string
	Version     Version
	Name        string
	Notes       string
	Prerelease  bool
	Draft       bool
	PublishedAt time.Time
	Assets      []Asset
}

// Candidate is a release newer than the running version together with the
// asset selected for the running OS/arch.
type Candidate struct {
	Release Release
	Asset   Asset
}

// Status is the outcome of a successful check.
type Status int

const (
	// StatusUpdateAvailable means Candidate is set and newer than Current.
	StatusUpdateAvailable Status = iota
	// StatusUpToDate means the newest applicable release is not newer.
	StatusUpToDate
	// StatusNoRelease means the repository has no published release.
	StatusNoRelease
	// StatusDevelopment means the running build has no comparable version;
	// no network request is made and no update is offered.
	StatusDevelopment
)

// CheckResult is the result of Checker.Check.
type CheckResult struct {
	Status    Status
	Current   string
	Candidate *Candidate
}

// ErrorKind classifies a check or download failure so the UI can explain it
// without parsing error text.
type ErrorKind int

const (
	ErrorNetwork ErrorKind = iota
	ErrorRateLimit
	ErrorNoRelease
	ErrorHTTP
	ErrorNoAsset
	ErrorChecksum
	ErrorDownload
)

// Error is a classified updater error. Err preserves the underlying cause, so
// errors.Is(err, context.Canceled) still works.
type Error struct {
	Kind ErrorKind
	Op   string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Op
	}
	return e.Op + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// KindOf reports the classified kind of err, if it is an *Error.
func KindOf(err error) (ErrorKind, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}

// Checker checks the public GitHub releases API.
type Checker struct {
	Client   HTTPClient
	APIBase  string
	PerPage  int
	MaxPages int
	GOOS     string
	GOARCH   string
}

// Check returns the newest release newer than current that has an asset for
// the checker's GOOS/GOARCH. A development or unparseable current version is
// reported as StatusDevelopment without any network access, so a dev build can
// never be compared, downgraded or offered an install.
func (c *Checker) Check(ctx context.Context, current string) (*CheckResult, error) {
	if IsDevelopmentVersion(current) {
		return &CheckResult{Status: StatusDevelopment, Current: current}, nil
	}
	cur, err := ParseVersion(current)
	if err != nil {
		return &CheckResult{Status: StatusDevelopment, Current: current}, nil
	}

	releases, err := c.fetchReleases(ctx)
	if err != nil {
		return nil, err
	}
	if len(releases) == 0 {
		return &CheckResult{Status: StatusNoRelease, Current: current}, nil
	}

	// Only releases strictly newer than the running version are candidates.
	// A release marked prerelease is offered only to a running prerelease,
	// matching GitHub's own semantics; a tag that merely contains a
	// prerelease suffix (for example 0.0.2-dev published with
	// prerelease=false) is still a normal release of this project.
	candidates := make([]Release, 0, len(releases))
	for _, r := range releases {
		if r.Draft {
			continue
		}
		if r.Prerelease && !cur.IsPrerelease() {
			continue
		}
		if Compare(r.Version, cur) <= 0 {
			continue
		}
		candidates = append(candidates, r)
	}
	if len(candidates) == 0 {
		return &CheckResult{Status: StatusUpToDate, Current: current}, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return Compare(candidates[i].Version, candidates[j].Version) > 0
	})

	// Take the highest version that actually ships an asset for this
	// platform. Never fall back to the first list entry and never offer a
	// version that is not newer than the running one.
	for i := range candidates {
		r := &candidates[i]
		if asset, ok := SelectAsset(r.Assets, c.GOOS, c.GOARCH); ok {
			return &CheckResult{
				Status:    StatusUpdateAvailable,
				Current:   current,
				Candidate: &Candidate{Release: *r, Asset: asset},
			}, nil
		}
	}
	return nil, &Error{
		Kind: ErrorNoAsset,
		Op:   "select asset",
		Err:  fmt.Errorf("no %s/%s asset in %s", c.GOOS, c.GOARCH, candidates[0].Version.String()),
	}
}

// apiRelease is the subset of the GitHub release JSON the updater reads.
type apiRelease struct {
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name"`
	Body        string     `json:"body"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt time.Time  `json:"published_at"`
	Assets      []apiAsset `json:"assets"`
}

type apiAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

func (c *Checker) fetchReleases(ctx context.Context) ([]Release, error) {
	base := strings.TrimSpace(c.APIBase)
	if base == "" {
		base = DefaultAPIBase
	}
	perPage := c.PerPage
	if perPage <= 0 {
		perPage = DefaultPerPage
	}
	maxPages := c.MaxPages
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	var all []Release
	for page := 1; page <= maxPages; page++ {
		url := fmt.Sprintf("%s?per_page=%d&page=%d", base, perPage, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, &Error{Kind: ErrorHTTP, Op: "build releases request", Err: err}
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "GoRequest-Updater")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := client.Do(req)
		if err != nil {
			return nil, &Error{Kind: ErrorNetwork, Op: "fetch releases", Err: err}
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAPIPageBytes+1))
		resp.Body.Close()
		if readErr != nil {
			return nil, &Error{Kind: ErrorNetwork, Op: "read releases", Err: readErr}
		}
		if len(body) > maxAPIPageBytes {
			return nil, &Error{Kind: ErrorHTTP, Op: "read releases", Err: fmt.Errorf("response exceeds %d bytes", maxAPIPageBytes)}
		}

		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusNotFound:
			return nil, &Error{Kind: ErrorNoRelease, Op: "fetch releases", Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
		case http.StatusForbidden, http.StatusTooManyRequests:
			if isRateLimited(resp, body) {
				return nil, &Error{Kind: ErrorRateLimit, Op: "fetch releases", Err: fmt.Errorf("GitHub API rate limit reached")}
			}
			return nil, &Error{Kind: ErrorHTTP, Op: "fetch releases", Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
		default:
			return nil, &Error{Kind: ErrorHTTP, Op: "fetch releases", Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
		}

		var items []apiRelease
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, &Error{Kind: ErrorHTTP, Op: "parse releases", Err: err}
		}
		for _, item := range items {
			r, ok := releaseFromAPI(item)
			if !ok {
				// A tag that is not SemVer cannot be ordered; skip it rather
				// than guess. The running app still reports its own version.
				continue
			}
			all = append(all, r)
		}
		if len(items) < perPage {
			break
		}
	}
	return all, nil
}

func isRateLimited(resp *http.Response, body []byte) bool {
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return true
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "rate limit") || strings.Contains(lower, "secondary rate")
}

func releaseFromAPI(item apiRelease) (Release, bool) {
	v, err := ParseVersion(item.TagName)
	if err != nil {
		return Release{}, false
	}
	notes := strings.TrimSpace(item.Body)
	if runes := []rune(notes); len(runes) > maxReleaseNotes {
		notes = string(runes[:maxReleaseNotes]) + "…"
	}
	r := Release{
		Tag:         item.TagName,
		Version:     v,
		Name:        strings.TrimSpace(item.Name),
		Notes:       notes,
		Prerelease:  item.Prerelease,
		Draft:       item.Draft,
		PublishedAt: item.PublishedAt,
	}
	for _, a := range item.Assets {
		name := strings.TrimSpace(a.Name)
		if name == "" || strings.TrimSpace(a.BrowserDownloadURL) == "" {
			continue
		}
		r.Assets = append(r.Assets, Asset{
			Name:   name,
			URL:    a.BrowserDownloadURL,
			Size:   a.Size,
			Digest: strings.TrimSpace(a.Digest),
		})
	}
	return r, true
}

// SelectAsset picks the installer for goos/goarch from a release's assets.
// macOS uses the .dmg; Windows prefers the setup .exe and falls back to the
// portable .zip. Names are matched by arch token, so the amd64 build is never
// offered to an arm64 machine (or the reverse). Checksum assets are excluded.
func SelectAsset(assets []Asset, goos, goarch string) (Asset, bool) {
	arch := normalizeArch(goarch)
	if arch == "" {
		return Asset{}, false
	}
	var best Asset
	bestRank := -1
	for _, a := range assets {
		name := strings.ToLower(strings.TrimSpace(a.Name))
		if name == "" || isChecksumName(name) {
			continue
		}
		if assetArch(name) != arch {
			continue
		}
		rank := -1
		switch strings.ToLower(goos) {
		case "darwin":
			if strings.HasSuffix(name, ".dmg") {
				rank = 10
			}
		case "windows":
			// The release workflow ships a setup .exe; prefer it over the
			// portable archive because it is the executable installer this
			// flow can actually start. A release with only a .zip reports no
			// installer instead of pretending a zip was launched.
			if strings.HasSuffix(name, ".exe") {
				if strings.Contains(name, "setup") {
					rank = 20
				} else {
					rank = 15
				}
			}
		}
		if rank > bestRank {
			best, bestRank = a, rank
		}
	}
	return best, bestRank >= 0
}

func isChecksumName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "sha256") || strings.Contains(lower, "checksum") ||
		strings.HasSuffix(lower, "sums") || strings.HasSuffix(lower, "sums.txt")
}

func normalizeArch(goarch string) string {
	switch strings.ToLower(strings.TrimSpace(goarch)) {
	case "amd64", "x86_64", "x64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return ""
	}
}

// assetArch returns the normalized architecture named by an asset file name,
// or "" when it names none. Tokens are compared exactly so "amd64" is never
// mistaken for "arm64".
func assetArch(name string) string {
	for _, token := range strings.FieldsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')
	}) {
		switch strings.ToLower(token) {
		case "amd64", "x86_64", "x64":
			return "amd64"
		case "arm64", "aarch64":
			return "arm64"
		}
	}
	return ""
}
