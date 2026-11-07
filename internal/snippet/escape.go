package snippet

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Environment-variable placeholders. Secret references keep a visible
// runtime lookup rather than a masked constant so the snippet stays runnable
// after the user binds the variable; the value itself was never read.
const (
	envSecretPrefix = "GOREQ_SECRET_" // {{secret.<name>}} → GOREQ_SECRET_<NAME>
	envFieldPrefix  = "GOREQ_"        // sensitive literal fields → GOREQ_<KEY>
)

// normalizeEnvSuffix turns any definition string into an uppercase
// [A-Z0-9_] environment-variable suffix. The mapping is deterministic: same
// input, same variable name, on every machine.
func normalizeEnvSuffix(raw string) string {
	var b strings.Builder
	lastUnderscore := true // suppress leading underscores
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
			lastUnderscore = false
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" {
		return "VALUE"
	}
	return s
}

func envNameForSecret(secretName string) string {
	return envSecretPrefix + normalizeEnvSuffix(secretName)
}

func envNameForKey(keyName string) string {
	return envFieldPrefix + normalizeEnvSuffix(keyName)
}

// sortedEnv flattens the required-env map into sorted names.
func sortedEnv(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// secondsString renders a timeout duration the way each command runtime
// expects it on the command line or an integer literal: minimal decimals,
// no exponent.
func secondsString(d time.Duration) string {
	secs := d.Seconds()
	s := fmt.Sprintf("%.6f", secs)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		s = "0"
	}
	return s
}

// goQuote quotes a Go string literal (the generator itself is Go, so the
// escapes are the runtime's own).
func goQuote(s string) string {
	return quoteDouble(s)
}

// pyQuote quotes a Python (3.x) double-quoted string literal.
func pyQuote(s string) string {
	return quoteDouble(s)
}

// jsQuote quotes a JavaScript/TypeScript double-quoted string literal.
// JSON-safe and template-literal-safe: result never contains ` or ${.
func jsQuote(s string) string {
	return quoteDouble(s)
}

func quoteDouble(s string) string {
	if s == "" {
		return `""`
	}
	var b strings.Builder
	b.WriteRune('"')
	writeEscaped(&b, s)
	b.WriteRune('"')
	return b.String()
}

// shellLiteral quotes s as one POSIX single-quoted word, exact core
// posixQuote semantics: 'contents with '\” escapes.
func shellLiteral(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellValue renders segments as one POSIX shell word. Literal parts are
// single-quoted (never expand); secret placeholders break out to a
// double-quoted "$GOREQ_…" expansion so an exported environment variable is
// used at run time. The word is shell-exact regardless of content.
func shellValue(segs []segment) string {
	var b strings.Builder
	for _, s := range coalesceSegments(segs) {
		switch s.kind {
		case segSecret:
			b.WriteString(`"$` + s.text + `"`)
		case segLiteral, segUnresolved:
			b.WriteString(shellLiteral(s.text))
		}
	}
	if b.Len() == 0 {
		return "''"
	}
	return b.String()
}

// writeEscaped writes s into b with double-quote string escapes shared by
// Go, Python and JS: ", \, control characters; tab/newline/CR use short
// escapes, other control bytes use \uXXXX (all three accept that form).
func writeEscaped(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
}
