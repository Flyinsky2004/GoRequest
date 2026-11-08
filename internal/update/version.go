// Package update implements GoRequest's own update check against the public
// GitHub Releases API. It is deliberately independent of MyGo's Updater: the
// published releases carry no MyGo-signed update feed, so calling
// mygo.Updater would fail with ErrUpdatesDisabled. This package compares real
// release versions, selects the asset for the running OS/arch, verifies its
// SHA-256 and only then hands the file to the platform installer.
//
// Nothing here touches the user's installed application: Download writes to a
// temporary directory and OpenInstaller starts the OS installer or opens the
// disk image. Whether the replacement actually happened is decided by the
// version the next launch reports, never by a successful download.
package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed Semantic Versioning 2.0.0 version. Build metadata is
// kept for display but ignored by Compare, as the specification requires.
type Version struct {
	Major int
	Minor int
	Patch int
	Pre   []string
	Build string
}

// ParseVersion parses a release tag or packaged version. A leading "v" or "V"
// (as in GitHub tags such as v1.2.3) is accepted and not part of the value.
// Exactly three numeric components are required; prerelease and build
// metadata follow SemVer's grammar.
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Version{}, fmt.Errorf("empty version")
	}
	raw = strings.TrimPrefix(raw, "v")
	raw = strings.TrimPrefix(raw, "V")

	var build string
	if i := strings.IndexByte(raw, '+'); i >= 0 {
		build = raw[i+1:]
		raw = raw[:i]
		if !validIdentifiers(build, true) {
			return Version{}, fmt.Errorf("invalid build metadata %q", build)
		}
	}

	var pre string
	if i := strings.IndexByte(raw, '-'); i >= 0 {
		pre = raw[i+1:]
		raw = raw[:i]
		if !validIdentifiers(pre, false) {
			return Version{}, fmt.Errorf("invalid prerelease %q", pre)
		}
	}

	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version %q must have three numeric components", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if p == "" {
			return Version{}, fmt.Errorf("version %q has an empty component", s)
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("version %q has a non-numeric component %q", s, p)
		}
		nums[i] = n
	}
	v := Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Build: build}
	if pre != "" {
		v.Pre = strings.Split(pre, ".")
	}
	return v, nil
}

// validIdentifiers reports whether s is a dot-separated list of non-empty
// identifiers using only ASCII letters, digits and hyphens. Numeric
// prerelease identifiers must not have leading zeros.
func validIdentifiers(s string, build bool) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '-' {
				return false
			}
		}
		if !build && isNumeric(part) && len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// IsPrerelease reports whether the version has a prerelease suffix.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// String renders the version in canonical form, without the leading "v".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// Compare returns -1, 0 or 1 as a is lower than, equal to, or higher than b
// according to SemVer precedence. Build metadata is ignored.
func Compare(a, b Version) int {
	if c := compareInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := compareInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := compareInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1 // a release outranks a prerelease
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := compareIdentifier(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(a.Pre), len(b.Pre))
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareIdentifier(a, b string) int {
	an, bn := isNumeric(a), isNumeric(b)
	switch {
	case an && bn:
		ai, _ := strconv.Atoi(a)
		bi, _ := strconv.Atoi(b)
		return compareInt(ai, bi)
	case an:
		return -1 // numeric identifiers have lower precedence
	case bn:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// IsDevelopmentVersion reports whether s cannot be compared with a release.
// An unpackaged development run reports no version at all; a value that is
// not SemVer (for example the old "0.1.0 (MVP)" display string) is treated the
// same way so the app never offers or installs an update based on a version it
// cannot order correctly.
func IsDevelopmentVersion(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	switch strings.ToLower(s) {
	case "dev", "development", "snapshot", "unknown":
		return true
	}
	_, err := ParseVersion(s)
	return err != nil
}
