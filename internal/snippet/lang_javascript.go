package snippet

import (
	"fmt"
	"net/url"
	"strings"
)

// renderJavaScript builds a Node.js 18+ ESM snippet using the built-in
// fetch. No npm dependencies. Duplicate headers switch to the Headers
// constructor with an array of pairs (fetch combines duplicate header values
// into one comma-joined line; the difference is visibly noted).
func renderJavaScript(snap *snapshot, res *Result) string {
	return renderJSFamily(snap, res, false)
}

// renderTypeScript builds a TypeScript 5+ snippet on Node.js 18+ built-in
// fetch; it requires @types/node when file input or Buffer types appear
// (declared in the runtime line and notes).
func renderTypeScript(snap *snapshot, res *Result) string {
	return renderJSFamily(snap, res, true)
}

func renderJSFamily(snap *snapshot, res *Result, ts bool) string {
	usesFiles := len(collectFileInputs(snap)) > 0
	dupHeaders := hasDuplicateEnabledHeaders(snap)
	runtimeForm := false
	if snap.Body.Kind == bodyURLEncoded && !snap.Body.HasFile && !snap.Body.HasLineup {
		runtimeForm = runtimeSecret(snap)
	}
	needsRequireEnv := snapshotUsesSecret(snap)

	var b strings.Builder
	writeReviewHeader(&b, snap, res, "//")

	if usesFiles {
		b.WriteString("import { readFileSync } from \"node:fs\";\n")
		if ts {
			res.Notes = append(res.Notes, Note{Code: "snippet-ts-types-node",
				Severity: SeverityInfo,
				Message:  "file input uses the node:fs API; compile with @types/node in scope (devDependency) or run via tsx"})
		}
	}
	b.WriteString("\n")

	if needsRequireEnv {
		if ts {
			b.WriteString("function requireEnv(name: string): string {\n")
		} else {
			b.WriteString("function requireEnv(name) {\n")
		}
		b.WriteString("  const value = process.env[name];\n")
		b.WriteString("  if (!value) {\n")
		b.WriteString("    throw new Error(`Missing environment variable: ${name}`);\n")
		b.WriteString("  }\n")
		b.WriteString("  return value;\n")
		b.WriteString("}\n\n")
	}

	// file path constants
	fileVars := fileVarAssignments(collectFileInputs(snap))
	for _, ref := range collectFileInputs(snap) {
		if ref.IsLocal {
			fmt.Fprintf(&b, "// GoRequest local file slot %s — host paths never enter shared definitions.\n", ref.Suggested)
		} else {
			fmt.Fprintf(&b, "// GoRequest project file %s — point this at the local copy.\n", ref.Suggested)
		}
		fmt.Fprintf(&b, "const %s = %s;\n", jsFileVar(fileVars[ref]), jsQuote(filePlaceholder(ref)))
	}
	if usesFiles {
		b.WriteString("\n")
	}

	pick := func(segs []segment) string { return jsSegExpr(segs) }

	typePrefix := ""
	if ts {
		typePrefix = ": string"
	}

	fmt.Fprintf(&b, "const url%s = %s;\n", typePrefix, jsQuote(literal(snap.URL)))
	fmt.Fprintf(&b, "const method%s = %s;\n", typePrefix, jsQuote(snap.Method))

	if dupHeaders {
		fmt.Fprintf(&b, "const headers = new Headers([\n")
		plain, contentType, hasCT := headerPairs(snap)
		for _, h := range plain {
			fmt.Fprintf(&b, "  [%s, %s],\n", jsQuote(h.Name), pick(h.Value))
		}
		writeJSAuthPairs(&b, snap, pick)
		if hasCT && snap.Body.Kind != bodyMultipart {
			fmt.Fprintf(&b, "  [%s, %s],\n", jsQuote(contentType.Name), pick(contentType.Value))
		}
		b.WriteString("]);\n\n")
		res.Notes = append(res.Notes, Note{Code: "snippet-js-combined-duplicates",
			Severity: SeverityWarning,
			Message:  "fetch Headers combines duplicate header names into one comma-joined line; GoRequest sends them as separate header lines — per RFC 9110 both are list-equivalent, but strict servers may see a difference"})
	} else {
		plain, contentType, hasCT := headerPairs(snap)
		recordType := "const headers"
		if ts {
			recordType = "const headers: Record<string, string>"
		}
		fmt.Fprintf(&b, "%s = {\n", recordType)
		for _, h := range plain {
			fmt.Fprintf(&b, "  %s: %s,\n", jsQuote(h.Name), pick(h.Value))
		}
		writeJSAuthDict(&b, snap, pick)
		if hasCT && snap.Body.Kind != bodyMultipart {
			fmt.Fprintf(&b, "  %s: %s,\n", jsQuote(contentType.Name), pick(contentType.Value))
		}
		b.WriteString("};\n\n")
	}

	jsWriteBody(&b, snap, res, runtimeForm && snap.Body.Kind == bodyURLEncoded, pick)

	bodyArg := "undefined"
	if snap.Body.HasBody || snap.Body.Kind == bodyMultipart {
		bodyArg = "body"
	}
	timeoutMS := snap.Timeout.Milliseconds()
	fmt.Fprintf(&b, "try {\n")
	fmt.Fprintf(&b, "  const response = await fetch(url, {\n")
	fmt.Fprintf(&b, "    method,\n    headers,%s\n", bodyLine(bodyArg))
	fmt.Fprintf(&b, "    signal: AbortSignal.timeout(%d),\n", timeoutMS)
	fmt.Fprintf(&b, "  });\n")
	b.WriteString("  console.log(response.status);\n")
	b.WriteString("  const text = await response.text();\n")
	b.WriteString("  if (text.length > 0) {\n    console.log(text);\n  }\n")
	b.WriteString("} catch (error) {\n  console.error(error);\n  process.exitCode = 1;\n}\n")

	res.Notes = append(res.Notes, Note{Code: "snippet-ua-default-js",
		Severity: SeverityInfo,
		Message:  "User-Agent is undici's default; GoRequest sends Go's Go-http-client/1.1"})
	return b.String()
}

func bodyLine(arg string) string {
	if arg == "undefined" {
		return ""
	}
	return "\n    body: " + arg + ","
}

// writeJSAuthPairs emits credential headers for the Headers-array variant.
func writeJSAuthPairs(b *strings.Builder, snap *snapshot, pick func([]segment) string) {
	switch snap.Auth.Carrier {
	case authBearer, authOAuth:
		fmt.Fprintf(b, "  [%s, %s],\n", jsQuote("Authorization"), "\"Bearer \" + "+pick(snap.Auth.Token))
	case authBasic:
		fmt.Fprintf(b, "  [%s, %s],\n", jsQuote("Authorization"),
			"\"Basic \" + Buffer.from("+pick(snap.Auth.User)+" + \":\" + "+pick(snap.Auth.Pass)+").toString(\"base64\")")
	}
}

// writeJSAuthDict emits credential headers for the object-literal variant.
func writeJSAuthDict(b *strings.Builder, snap *snapshot, pick func([]segment) string) {
	switch snap.Auth.Carrier {
	case authBearer, authOAuth:
		fmt.Fprintf(b, "  %s: %s,\n", jsQuote("Authorization"), "\"Bearer \" + "+pick(snap.Auth.Token))
	case authBasic:
		fmt.Fprintf(b, "  %s: %s,\n", jsQuote("Authorization"),
			"\"Basic \" + Buffer.from("+pick(snap.Auth.User)+" + \":\" + "+pick(snap.Auth.Pass)+").toString(\"base64\")")
	}
}

// jsWriteBody writes the body variable for every body kind.
func jsWriteBody(b *strings.Builder, snap *snapshot, res *Result, runtimeForm bool, pick func([]segment) string) {
	fileVars := fileVarAssignments(collectFileInputs(snap))
	switch snap.Body.Kind {
	case bodyNone:
		return
	case bodyJSON, bodyText, bodyXML:
		if snap.Body.HasFile {
			fmt.Fprintf(b, "const body = readFileSync(%s, \"utf-8\");\n\n", jsFileVar(fileVars[snap.Body.File]))
			return
		}
		text := snap.Body.JSONText
		if snap.Body.Kind != bodyJSON {
			text = literal(snap.Body.Text)
		}
		fmt.Fprintf(b, "const body = %s;\n\n", jsQuote(text))
	case bodyBinary:
		fmt.Fprintf(b, "const body = new Uint8Array(readFileSync(%s));\n\n", jsFileVar(fileVars[snap.Body.File]))
	case bodyURLEncoded:
		switch {
		case snap.Body.HasFile:
			fmt.Fprintf(b, "const body = new Uint8Array(readFileSync(%s));\n\n", jsFileVar(fileVars[snap.Body.File]))
		case snap.Body.HasLineup:
			fmt.Fprintf(b, "const body = %s;\n\n", jsQuote(snap.Body.Lineup))
		case runtimeForm:
			b.WriteString("const form = new URLSearchParams();\n")
			for _, f := range snap.Body.Fields {
				fmt.Fprintf(b, "form.set(%s, %s);\n", jsQuote(f.Name), pick(f.Value))
			}
			b.WriteString("const body = form.toString();\n\n")
			res.Notes = append(res.Notes, Note{Code: "snippet-urlencoded-runtime-js",
				Severity: SeverityWarning,
				Message:  "urlencoded secret values are encoded by URLSearchParams at run time, which may percent-encode a few characters differently than GoRequest (notably !, ', (, *) — verify against the runner if the value is encoding-sensitive"})
		default:
			var parts []string
			for _, f := range snap.Body.Fields {
				parts = append(parts, url.QueryEscape(f.Name)+"="+url.QueryEscape(literal(f.Value)))
			}
			fmt.Fprintf(b, "const body = %s;\n\n", jsQuote(strings.Join(parts, "&")))
		}
	case bodyMultipart:
		b.WriteString("const form = new FormData();\n")
		for _, part := range snap.Body.Parts {
			if part.IsFile {
				fmt.Fprintf(b, "const blob = new Blob([readFileSync(%s)], { type: %s });\n",
					jsFileVar(fileVars[part.File]), jsQuote(effectiveMultipartCT(part)))
				fmt.Fprintf(b, "form.append(%s, blob, %s);\n",
					jsQuote(part.Name), jsQuote(effectiveMultipartFileName(part)))
			} else {
				fmt.Fprintf(b, "form.append(%s, %s);\n", jsQuote(part.Name), pick(part.Text))
			}
		}
		b.WriteString("const body = form;\n\n")
		res.Notes = append(res.Notes, Note{Code: "snippet-multipart-boundary-js",
			Severity: SeverityInfo,
			Message:  "fetch/undici generates its own multipart boundary; GoRequest's runner uses a random boundary as well (both are protocol-legal)"})
	}
}

func jsFileVar(varName string) string {
	var b strings.Builder
	for i, r := range varName {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	name := b.String()
	name = strings.ReplaceAll(name, "Path", "_path")
	name = strings.ReplaceAll(name, "__", "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = "input_path"
	}
	return name
}
