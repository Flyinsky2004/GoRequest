package snippet

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"
)

// coalesceSegments merges adjacent non-secret segments into a single literal
// segment so a pure resolved value renders as one quoted string instead of
// concatenated pieces.
func coalesceSegments(segs []segment) []segment {
	var out []segment
	for _, s := range segs {
		if s.kind == segSecret {
			out = append(out, s)
			continue
		}
		if s.text == "" {
			continue
		}
		if len(out) > 0 && out[len(out)-1].kind != segSecret {
			out[len(out)-1].text += s.text
			continue
		}
		out = append(out, segment{kind: s.kind, text: s.text})
	}
	return out
}

// goSegExpr renders segments as a Go expression string, composing runtime
// env lookups: "Bearer " + mustEnv("GOREQ_X"). A pure literal renders as one
// quoted string.
func goSegExpr(segs []segment) string {
	var parts []string
	for _, s := range coalesceSegments(segs) {
		switch s.kind {
		case segSecret:
			parts = append(parts, fmt.Sprintf("mustEnv(%s)", goQuote(s.text)))
		default:
			parts = append(parts, goQuote(s.text))
		}
	}
	switch len(parts) {
	case 0:
		return `""`
	case 1:
		return parts[0]
	default:
		return strings.Join(parts, " + ")
	}
}

// pySegExpr renders segments as a Python expression using os.environ lookups.
func pySegExpr(segs []segment) string {
	var parts []string
	for _, s := range coalesceSegments(segs) {
		switch s.kind {
		case segSecret:
			parts = append(parts, fmt.Sprintf("os.environ[%s]", pyQuote(s.text)))
		default:
			parts = append(parts, pyQuote(s.text))
		}
	}
	switch len(parts) {
	case 0:
		return `""`
	case 1:
		return parts[0]
	default:
		return strings.Join(parts, " + ")
	}
}

// jsSegExpr renders segments as a JavaScript/TypeScript expression using the
// requireEnv helper (emitted only when needed).
func jsSegExpr(segs []segment) string {
	var parts []string
	for _, s := range coalesceSegments(segs) {
		switch s.kind {
		case segSecret:
			parts = append(parts, fmt.Sprintf("requireEnv(%s)", jsQuote(s.text)))
		default:
			parts = append(parts, jsQuote(s.text))
		}
	}
	switch len(parts) {
	case 0:
		return `""`
	case 1:
		return parts[0]
	default:
		return strings.Join(parts, " + ")
	}
}

// headerPairs splits snapshot headers from the Content-Type pair so every
// renderer can emit them in the runner's exact order (definition headers,
// folded auth headers, credentials, Content-Type last).
func headerPairs(snap *snapshot) (plain []pair, contentType pair, hasCT bool) {
	for _, h := range snap.Headers {
		if strings.EqualFold(h.Name, "Content-Type") {
			contentType = h
			hasCT = true
			continue
		}
		plain = append(plain, h)
	}
	return plain, contentType, hasCT
}

// valueWithPrefix prepends a literal prefix (for example "Authorization:
// Bearer ") to segmented value parts.
func valueWithPrefix(prefix string, segs []segment) []segment {
	if prefix == "" {
		return segs
	}
	return append([]segment{{kind: segLiteral, text: prefix}}, segs...)
}

// filePlaceholder is the visible per-file placeholder path. Host paths never
// exist in shared definitions, so every renderer emits this constant and a
// diagnostic telling the reader what to point it at.
func filePlaceholder(ref fileRefSnap) string {
	sug := ref.Suggested
	if sug == "" {
		sug = "input-file"
	}
	return "/path/to/" + strings.ReplaceAll(sug, ":", "-")
}

// fileVarName derives a stable identifier for one file input.
func fileVarName(ref fileRefSnap) string {
	base := ref.Suggested
	if base == "" {
		base = "inputfile"
	}
	base = strings.ReplaceAll(base, ":", "-")
	var b strings.Builder
	lastUnderscore := true
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = "file"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "file" + name
	}
	return name + "Path"
}

// collectFileInputs walks the snapshot for every file input in stable order.
func collectFileInputs(snap *snapshot) []fileRefSnap {
	var refs []fileRefSnap
	if snap.Body.HasFile {
		refs = append(refs, snap.Body.File)
	}
	for i := range snap.Body.Parts {
		if snap.Body.Parts[i].IsFile {
			refs = append(refs, snap.Body.Parts[i].File)
		}
	}
	return refs
}

// fileVarAssignments names every distinct file input deterministically,
// appending a numeric suffix on identifier collisions.
func fileVarAssignments(refs []fileRefSnap) map[fileRefSnap]string {
	out := map[fileRefSnap]string{}
	var seen []string
	for _, ref := range refs {
		if _, ok := out[ref]; ok {
			continue
		}
		name := fileVarName(ref)
		taken := false
		for _, s := range seen {
			if s == name {
				taken = true
				break
			}
		}
		if taken {
			name = fmt.Sprintf("%s%d", name, len(seen))
		}
		out[ref] = name
		seen = append(seen, name)
	}
	return out
}

// effectiveMultipartCT reproduces the runner's file-part Content-Type rule:
// explicit wins, otherwise guess from the transmitted filename's extension,
// otherwise application/octet-stream. The guess is frozen at generation time
// so the generated code stays deterministic on any machine.
func effectiveMultipartCT(part multipartPartSnap) string {
	if part.ContentType != "" {
		return part.ContentType
	}
	name := effectiveMultipartFileName(part)
	if guessed := mime.TypeByExtension(filepath.Ext(name)); guessed != "" {
		return guessed
	}
	return "application/octet-stream"
}

// effectiveMultipartFileName is the transmitted part filename: the explicit
// override, else the project-file base name, else the machine-local slot name
// (whose real disk name never enters shared definitions).
func effectiveMultipartFileName(part multipartPartSnap) string {
	if part.FileName != "" {
		return part.FileName
	}
	if !part.File.IsLocal {
		return filepath.Base(part.File.Path)
	}
	return part.File.Slot
}
