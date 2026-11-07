// Package snippet turns a saved GoRequest request definition plus a chosen
// environment into reviewable call-example code in cURL, Go, Python,
// JavaScript and TypeScript (documentation-codegen.md §4).
//
// The generator only reads the project and request definitions. It never
// resolves secret values: not-yet-bound secrets keep a visible environment
// variable placeholder, and values that a plain variable chain taints with a
// secret are reduced to placeholders as well. Live OAuth tokens, cookies,
// passwords and private keys from the local secret store are never consulted,
// so the output is safe to review and copy by design.
//
// Semantics follow the core runner (core.BuildPlanWithOptions + core.BuildBody):
// query re-encoding, header order and duplicates, Content-Type injection and
// replacement, auth injection, timeout parsing and body limits are reproduced
// or reported — never silently changed. Anything a target runtime cannot
// express equivalently is either refused (Result.Blocked) or carried as a
// visible diagnostic (Result.Notes).
package snippet

import (
	"errors"
	"fmt"

	"gorequest/internal/core"
)

// Language identifies one supported snippet target.
type Language string

// Supported languages, in UI display order.
const (
	LangCURL       Language = "curl"
	LangGo         Language = "go"
	LangPython     Language = "python"
	LangJavaScript Language = "javascript"
	LangTypeScript Language = "typescript"
)

// Languages lists the generator targets in display order.
func Languages() []Language {
	return []Language{LangCURL, LangGo, LangPython, LangJavaScript, LangTypeScript}
}

// RuntimeDecl is the per-template runtime/dependency declaration each result
// carries so the reader knows what the example requires (spec §4: every
// language template declares runtime/dependency versions and gaps).
func RuntimeDecl(lang Language) string {
	switch lang {
	case LangCURL:
		return "curl 7.68+ (POSIX shell quoting; no shell execution happens on generation or copy)"
	case LangGo:
		return "Go 1.21+, standard library only (net/http, mime/multipart) — no external modules"
	case LangPython:
		return "Python 3.8+, standard library only (urllib.request / http.client) — no pip packages"
	case LangJavaScript:
		return "Node.js 18+ built-in fetch/undici; node:fs only when the request has file parts"
	case LangTypeScript:
		return "TypeScript 5+ on Node.js 18+ (built-in fetch); @types/node devDependency when file input is used"
	default:
		return "unsupported language"
	}
}

// FileExtension is the suggested file suffix for explicit exports.
func FileExtension(lang Language) string {
	switch lang {
	case LangCURL:
		return ".sh"
	case LangGo:
		return ".go"
	case LangPython:
		return ".py"
	case LangJavaScript:
		return ".mjs"
	case LangTypeScript:
		return ".ts"
	default:
		return ".txt"
	}
}

// Note severities mirror the core export loss taxonomy so the UI can color
// diagnostics consistently without parsing message text.
const (
	SeverityInfo        = "info"        // neutral context the reader should know
	SeverityWarning     = "warning"     // semantics differ in a named, visible way
	SeverityScrubbed    = "scrubbed"    // a sensitive literal was replaced by a placeholder
	SeverityUnsupported = "unsupported" // an option cannot be represented equivalently
	SeverityBlocked     = "blocked"     // generation refused; no code emitted
)

// Note is one visible diagnostic about the generated snapshot: what was
// scrubbed, what the target runtime does differently, or why nothing was
// generated. Codes are stable; messages are deterministic English text meant
// for the reviewable artifact, not localized UI copy.
type Note struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// Options selects the environment snapshot used to resolve plain variables.
// EnvID may be empty (project + collection + request variables only); when
// set it must exist in the project. Secret values are never resolved for any
// option combination.
type Options struct {
	EnvID string
}

// Result is the outcome of one generation. When Blocked is non-empty the
// snapshot could not be built honestly and Code/RequiredEnv are empty; Notes
// carries the reason at severity "blocked".
type Result struct {
	Language    Language
	Runtime     string
	Code        string
	Notes       []Note
	RequiredEnv []string
	Blocked     string
}

// Generate renders one request in one language. p and entry must be non-nil
// and entry must belong to p; otherwise a usage error is returned. Semantic
// refusals (non-HTTP protocol, invalid definition, undefined variable…) are
// reported through Result.Blocked, not as errors.
func Generate(p *core.Project, entry *core.RequestEntry, lang Language, opts Options) (*Result, error) {
	if p == nil {
		return nil, errors.New("snippet: nil project")
	}
	if entry == nil || entry.Request == nil {
		return nil, errors.New("snippet: nil request entry")
	}
	res := &Result{Language: lang, Runtime: RuntimeDecl(lang)}
	blocked := func(code, format string, args ...any) *Result {
		res.Blocked = fmt.Sprintf(format, args...)
		res.Notes = append(res.Notes, Note{Code: code, Severity: SeverityBlocked, Message: res.Blocked})
		return res
	}

	snap, notes, berr := buildSnapshot(p, entry, opts)
	res.Notes = append(res.Notes, notes...)
	if berr != nil {
		res.Notes = append(res.Notes, Note{Code: berr.code, Severity: SeverityBlocked, Message: berr.Text})
		res.Blocked = berr.Text
		return res, nil
	}

	// Shared review facts: TLS is never downgraded and device-level network
	// configuration is out of a static snippet's reach.
	res.Notes = append(res.Notes,
		Note{Code: "snippet-network-policy",
			Severity: SeverityInfo,
			Message:  "GoRequest may run with device-level proxy, custom CA/mTLS, stage timeouts, redirect policy or a cookie jar (Network settings); generated code runs with the target runtime's defaults (TLS verification on) — review the network profile before treating behavior as identical"})

	var code string
	switch lang {
	case LangCURL:
		code = renderCURL(snap, res)
	case LangGo:
		code = renderGo(snap, res)
	case LangPython:
		code = renderPython(snap, res)
	case LangJavaScript:
		code = renderJavaScript(snap, res)
	case LangTypeScript:
		code = renderTypeScript(snap, res)
	default:
		return blocked("snippet-unsupported-language", "unsupported snippet language %q", lang)
	}
	if res.Blocked != "" {
		// Refusals may be raised during rendering too (e.g. a target runtime
		// cannot hold the request); Code stays empty in that case.
		return res, nil
	}
	res.Code = code
	res.RequiredEnv = sortedEnv(snap.requiredEnv)
	return res, nil
}
