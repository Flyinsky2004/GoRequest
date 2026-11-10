package sdkgen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// goKeywords is the Go keyword set; identifiers must never collide with these.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// reservedOperationNames are package-level or generated helper identifiers an
// operation method or type must not shadow.
var reservedOperationNames = map[string]bool{
	"Client": true, "Response": true, "APIError": true, "Option": true,
	"NewClient": true, "WithHTTPClient": true, "WithAuth": true,
	"WithUserAgent": true, "WithMaxResponseBytes": true,
	"DefaultTimeout": true, "DefaultMaxResponseBytes": true,
	"Validate": true, "Body": true,
}

// reservedTypeNames are generated helper types and package-level identifiers
// that a model name must not shadow.
var reservedTypeNames = map[string]bool{
	"Client": true, "Response": true, "APIError": true, "Option": true,
	"Optional": true, "Some": true, "Null": true,
	"NewClient": true, "WithHTTPClient": true, "WithAuth": true,
	"WithUserAgent": true, "WithMaxResponseBytes": true,
	"DefaultTimeout": true, "DefaultMaxResponseBytes": true,
	"Requests": true, "RawResponse": true, "SDKError": true,
	"ApiError": true, "TimeoutError": true, "ResponseTooLargeError": true,
}

// reservedFieldNames are generated method or helper names an object field must
// not shadow.
var reservedFieldNames = map[string]bool{
	"Validate": true, "Body": true, "AdditionalProperties": true,
	"MarshalJSON": true, "UnmarshalJSON": true, "Error": true,
}

// nameAllocator hands out deterministic, case-insensitively unique Go and
// TypeScript type identifiers.
type nameAllocator struct {
	used map[string]bool
}

func newNameAllocator(reserved map[string]bool) *nameAllocator {
	used := make(map[string]bool, len(reserved))
	for name := range reserved {
		used[strings.ToLower(name)] = true
	}
	return &nameAllocator{used: used}
}

func (a *nameAllocator) clone() *nameAllocator {
	used := make(map[string]bool, len(a.used))
	for k, v := range a.used {
		used[k] = v
	}
	return &nameAllocator{used: used}
}

// Reserve marks name as used without returning it.
func (a *nameAllocator) Reserve(name string) {
	if name == "" {
		return
	}
	a.used[strings.ToLower(name)] = true
}

// Allocate returns a unique exported identifier based on base.
func (a *nameAllocator) Allocate(base string) string {
	base = exportedIdentifier(base)
	if base == "" {
		base = "Type"
	}
	name := base
	if reservedTypeNames[name] || goKeywords[strings.ToLower(name)] {
		name += "Model"
	}
	for n := 2; a.used[strings.ToLower(name)]; n++ {
		name = base + strconv.Itoa(n)
	}
	a.used[strings.ToLower(name)] = true
	return name
}

// normalizePackageName validates and returns a package name. It is intentionally
// strict: the caller (desktop UI) derives a default and the user can edit it,
// but an invalid name fails before any file is generated.
func normalizePackageName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("sdkgen: package name is required")
	}
	if !isValidGoIdentifier(name) {
		return "", fmt.Errorf("sdkgen: package name %q is not a valid Go identifier", raw)
	}
	if goKeywords[name] {
		return "", fmt.Errorf("sdkgen: package name %q is a Go keyword", raw)
	}
	if name == "main" || name == "_" {
		return "", fmt.Errorf("sdkgen: package name %q is not allowed for a library", raw)
	}
	return name, nil
}

// normalizeModulePath validates a Go module path conservatively.
func normalizeModulePath(raw string) (string, error) {
	mod := strings.TrimSpace(raw)
	if mod == "" {
		return "", fmt.Errorf("sdkgen: module path is required")
	}
	if strings.ContainsAny(mod, " \t\r\n\\") {
		return "", fmt.Errorf("sdkgen: module path %q contains whitespace or a backslash", raw)
	}
	if strings.HasPrefix(mod, "/") || strings.HasSuffix(mod, "/") {
		return "", fmt.Errorf("sdkgen: module path %q must not start or end with a slash", raw)
	}
	for _, seg := range strings.Split(mod, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("sdkgen: module path %q contains an empty or relative segment", raw)
		}
		for _, r := range seg {
			if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' || r == '~') {
				return "", fmt.Errorf("sdkgen: module path %q contains an unsupported character %q", raw, r)
			}
		}
	}
	return mod, nil
}

func isValidGoIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// exportedIdentifier converts an arbitrary name into an exported Go identifier.
// It splits on non-alphanumeric runes, title-cases each word, prefixes a letter
// when the result starts with a digit, and appends "Op" for keywords.
func exportedIdentifier(raw string) string {
	words := splitWords(raw)
	if len(words) == 0 {
		return "Op"
	}
	var b strings.Builder
	for _, w := range words {
		b.WriteString(titleWord(w))
	}
	out := b.String()
	if out == "" {
		return "Op"
	}
	first, _ := utf8FirstRune(out)
	if unicode.IsDigit(first) {
		out = "Op" + out
	}
	if goKeywords[strings.ToLower(out)] {
		out += "Op"
	}
	return out
}

func splitWords(raw string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return words
}

func titleWord(w string) string {
	if w == "" {
		return ""
	}
	runes := []rune(w)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func utf8FirstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

// deriveOperationName builds a stable name from method and path when the
// contract has no operationId. GET /users/{id} becomes GetUsersById.
func deriveOperationName(method, path string) string {
	var b strings.Builder
	b.WriteString(titleWord(strings.ToLower(method)))
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			b.WriteString("By")
			b.WriteString(exportedIdentifier(strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")))
			continue
		}
		b.WriteString(exportedIdentifier(seg))
	}
	out := b.String()
	if out == "" {
		out = "Op"
	}
	return out
}

// assignOperationNames assigns unique exported Go method names to operations.
// Operations must already be in a deterministic order (sorted by path/method).
func assignOperationNames(ops []Operation) {
	used := map[string]bool{}
	for i := range ops {
		base := exportedIdentifier(ops[i].OperationID)
		if ops[i].OperationID == "" {
			base = deriveOperationName(ops[i].Method, ops[i].Path)
		}
		if reservedOperationNames[base] {
			base += "Op"
		}
		name := base
		for n := 2; used[strings.ToLower(name)]; n++ {
			name = base + strconv.Itoa(n)
		}
		used[strings.ToLower(name)] = true
		ops[i].GoName = name
	}
}

// assignFieldNames assigns unique exported Go field names to parameters.
// Reserved names (Body, Validate, AdditionalProperties) are avoided
// deterministically.
func assignFieldNames(rawNames []string) []string {
	used := map[string]bool{}
	out := make([]string, len(rawNames))
	for i, raw := range rawNames {
		base := exportedIdentifier(raw)
		if reservedFieldNames[base] || reservedOperationNames[base] {
			base += "Param"
		}
		name := base
		for n := 2; used[strings.ToLower(name)]; n++ {
			name = base + strconv.Itoa(n)
		}
		used[strings.ToLower(name)] = true
		out[i] = name
	}
	return out
}

// sortedStrings returns a sorted copy of s.
func sortedStrings(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
