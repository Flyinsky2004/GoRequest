package snippet

import (
	"fmt"
	"net/url"
	"strings"
)

// renderCURL builds a POSIX-shell cURL command. Literal parts are
// single-quoted so nothing expands; secret placeholders expand through
// double-quoted "$GOREQ_…" references. Generation and copy never execute
// the shell — the line is a reviewable artifact only.
func renderCURL(snap *snapshot, res *Result) string {
	var b strings.Builder
	writeReviewHeader(&b, snap, res, "#")

	args := []string{"curl"}
	if snap.Method != "" {
		args = append(args, "-X", shellLiteral(snap.Method))
	}
	args = append(args, shellLiteral(literal(snap.URL)))

	plain, contentType, hasCT := headerPairs(snap)
	for _, h := range plain {
		segs := valueWithPrefix(h.Name+": ", h.Value)
		args = append(args, "-H", shellValue(segs))
	}

	switch snap.Auth.Carrier {
	case authBearer, authOAuth:
		segs := valueWithPrefix("Authorization: Bearer ", snap.Auth.Token)
		args = append(args, "-H", shellValue(segs))
	case authBasic:
		userPass := append(append([]segment{}, snap.Auth.User...),
			append([]segment{{kind: segLiteral, text: ":"}}, snap.Auth.Pass...)...)
		args = append(args, "-u", shellValue(userPass))
	}

	isMultipart := snap.Body.Kind == bodyMultipart
	if hasCT && !isMultipart {
		// multipart: curl -F sets its own boundary Content-Type, matching the
		// runner's random-boundary behavior.
		segs := valueWithPrefix(contentType.Name+": ", contentType.Value)
		args = append(args, "-H", shellValue(segs))
	}

	curlBodyArgs(snap, res, &args)

	if snap.Timeout > 0 {
		args = append(args, "--max-time", shellLiteral(secondsString(snap.Timeout)))
	}

	writeCommand(&b, args)
	return b.String()
}

// writeReviewHeader writes the reviewable provenance block shared by all
// language renderers (comment prefix differs by language).
func writeReviewHeader(b *strings.Builder, snap *snapshot, res *Result, comment string) {
	fmt.Fprintf(b, "%s Snippet for GoRequest request %s (id: %s)\n", comment, goQuote(snap.Name), snap.ID)
	fmt.Fprintf(b, "%s Runtime: %s\n", comment, res.Runtime)
	if snap.Auth.Source != "" && snap.Auth.Source != "none" {
		fmt.Fprintf(b, "%s Auth source: %s\n", comment, snap.Auth.Source)
	}
	if n := len(res.RequiredEnv); n > 0 {
		fmt.Fprintf(b, "%s Environment variables to bind before running:\n", comment)
		for _, env := range res.RequiredEnv {
			desc := snap.requiredEnv[env]
			if desc == "" {
				desc = "required value"
			}
			fmt.Fprintf(b, "%s   %s = <%s>\n", comment, env, desc)
		}
	}
	fmt.Fprintf(b, "%s Secrets and local file paths are never embedded by GoRequest.\n", comment)
	b.WriteByte('\n')
}

func writeCommand(b *strings.Builder, args []string) {
	for i, a := range args {
		if i > 0 {
			b.WriteString(" \\\n  ")
		}
		b.WriteString(a)
	}
	b.WriteByte('\n')
}

// curlBodyArgs appends body flags for every body kind.
func curlBodyArgs(snap *snapshot, res *Result, args *[]string) {
	body := snap.Body
	switch body.Kind {
	case bodyJSON, bodyText, bodyXML, bodyBinary:
		if body.HasFile {
			*args = append(*args, "--data-binary", shellLiteral("@"+filePlaceholder(body.File)))
			return
		}
		var text string
		switch body.Kind {
		case bodyJSON:
			text = body.JSONText
		default:
			text = literal(body.Text)
		}
		if text != "" || body.HasBody {
			*args = append(*args, "--data-raw", shellLiteral(text))
		}

	case bodyURLEncoded:
		if body.HasFile {
			*args = append(*args, "--data-binary", shellLiteral("@"+filePlaceholder(body.File)))
			return
		}
		if body.HasLineup {
			*args = append(*args, "--data-raw", shellLiteral(body.Lineup))
			return
		}
		// Runtime-composed fields: pre-build the exact core encoding unless a
		// secret must be read at run time.
		needsRuntime := false
		for _, f := range body.Fields {
			if hasSecret(f.Value) {
				needsRuntime = true
				break
			}
		}
		if !needsRuntime {
			var parts []string
			for _, f := range body.Fields {
				parts = append(parts, url.QueryEscape(f.Name)+"="+url.QueryEscape(literal(f.Value)))
			}
			*args = append(*args, "--data-raw", shellLiteral(strings.Join(parts, "&")))
			return
		}
		for _, f := range body.Fields {
			segs := valueWithPrefix(f.Name+"=", f.Value)
			*args = append(*args, "--data-urlencode", shellValue(segs))
		}
		res.Notes = append(res.Notes, Note{Code: "snippet-urlencoded-runtime-curl",
			Severity: SeverityWarning,
			Message:  "urlencoded secret values are encoded by curl --data-urlencode, which may differ from GoRequest's Go-style encoding for spaces (%20 instead of +); verify against the runner if the value is encoding-sensitive"})

	case bodyMultipart:
		for _, part := range body.Parts {
			*args = append(*args, "-F", shellMultipartField(part))
		}
	}
}

func shellMultipartField(part multipartPartSnap) string {
	if !part.IsFile {
		return shellValue(valueWithPrefix(part.Name+"=", part.Text))
	}
	seg := "@" + filePlaceholder(part.File)
	if part.FileName != "" {
		seg += ";filename=" + quoteMultipartName(part.FileName)
	}
	if part.ContentType != "" {
		seg += ";type=" + part.ContentType
	}
	return shellLiteral(part.Name + "=" + seg)
}

// quoteMultipartName wraps a curl -F filename override in double quotes,
// escaping backslashes and quotes so the value cannot escape the field.
func quoteMultipartName(name string) string {
	esc := strings.ReplaceAll(name, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	return `"` + esc + `"`
}
