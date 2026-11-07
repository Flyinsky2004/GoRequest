package snippet

import (
	"fmt"
	"net/url"
	"strings"
)

// renderPython builds a Python 3 script from the standard library only.
// Requests with duplicate headers use an http.client variant because
// urllib.request cannot carry two values for the same header name — the
// switch and its behavior difference are visibly noted.
func renderPython(snap *snapshot, res *Result) string {
	dupHeaders := hasDuplicateEnabledHeaders(snap)
	needsBase64 := snap.Auth.Carrier == authBasic
	needsFiles := len(collectFileInputs(snap)) > 0
	runtimeForm := runtimeSecret(snap)
	usesParse := runtimeForm || dupHeaders

	var b strings.Builder
	writeReviewHeader(&b, snap, res, "#")

	if dupHeaders {
		b.WriteString("import os\n")
		b.WriteString("import sys\n")
		if needsBase64 {
			b.WriteString("import base64\n")
		}
		b.WriteString("import http.client\n")
		b.WriteString("import urllib.parse\n")
		res.Notes = append(res.Notes, Note{Code: "snippet-pill-httpclient-redirects",
			Severity: SeverityWarning,
			Message:  "duplicate headers require http.client, which does not follow redirects; GoRequest follows up to 10 by default — handle 3xx manually or merge the headers"})
	} else {
		b.WriteString("import os\n")
		b.WriteString("import sys\n")
		if needsBase64 {
			b.WriteString("import base64\n")
		}
		if usesParse {
			b.WriteString("import urllib.parse\n")
		}
		b.WriteString("import urllib.error\n")
		b.WriteString("import urllib.request\n")
	}
	b.WriteString("\n")

	fileVars := fileVarAssignments(collectFileInputs(snap))
	for _, ref := range collectFileInputs(snap) {
		if ref.IsLocal {
			fmt.Fprintf(&b, "# GoRequest local file slot %s — host paths never enter shared definitions.\n", ref.Suggested)
		} else {
			fmt.Fprintf(&b, "# GoRequest project file %s — point this at the local copy.\n", ref.Suggested)
		}
		fmt.Fprintf(&b, "%s = %s\n", pyFileVar(fileVars[ref]), pyQuote(filePlaceholder(ref)))
	}
	if needsFiles {
		b.WriteString("\n")
	}
	res.Notes = append(res.Notes, Note{Code: "snippet-ua-default",
		Severity: SeverityInfo,
		Message:  "User-Agent is the runtime default (Python-urllib or http.client); GoRequest sends Go's Go-http-client/1.1"})

	pyWriteBody(&b, snap, res)

	writePyURLAndHeaders(&b, snap, dupHeaders)

	if dupHeaders {
		writePyHTTPClientSend(&b, snap)
	} else {
		writePyUrllibSend(&b, snap)
	}
	return b.String()
}

// pyWriteBody writes the data variable for every body kind.
func pyWriteBody(b *strings.Builder, snap *snapshot, res *Result) {
	fileVars := fileVarAssignments(collectFileInputs(snap))
	switch snap.Body.Kind {
	case bodyNone:
		return
	case bodyJSON, bodyText, bodyXML:
		if snap.Body.HasFile {
			fmt.Fprintf(b, "with open(%s, \"rb\") as f:\n    data = f.read()\n\n", pyFileVar(fileVars[snap.Body.File]))
			return
		}
		text := snap.Body.JSONText
		if snap.Body.Kind != bodyJSON {
			text = literal(snap.Body.Text)
		}
		fmt.Fprintf(b, "data = %s.encode(\"utf-8\")\n\n", pyQuote(text))
	case bodyBinary:
		fmt.Fprintf(b, "with open(%s, \"rb\") as f:\n    data = f.read()\n\n", pyFileVar(fileVars[snap.Body.File]))
	case bodyURLEncoded:
		switch {
		case snap.Body.HasFile:
			fmt.Fprintf(b, "with open(%s, \"rb\") as f:\n    data = f.read()\n\n", pyFileVar(fileVars[snap.Body.File]))
		case snap.Body.HasLineup:
			fmt.Fprintf(b, "data = %s.encode(\"utf-8\")\n\n", pyQuote(snap.Body.Lineup))
		case runtimeSecret(snap):
			b.WriteString("form = {\n")
			for _, f := range snap.Body.Fields {
				fmt.Fprintf(b, "    %s: %s,\n", pyQuote(f.Name), pySegExpr(f.Value))
			}
			b.WriteString("}\n")
			b.WriteString("data = urllib.parse.urlencode(form).encode(\"utf-8\")\n\n")
		default:
			var parts []string
			for _, f := range snap.Body.Fields {
				parts = append(parts, url.QueryEscape(f.Name)+"="+url.QueryEscape(literal(f.Value)))
			}
			fmt.Fprintf(b, "data = %s.encode(\"utf-8\")\n\n", pyQuote(strings.Join(parts, "&")))
		}
	case bodyMultipart:
		fmt.Fprintf(b, "boundary = %s.encode(\"ascii\")\n", pyQuote(multipartBoundary))
		b.WriteString("body = b\"\"\n")
		for _, part := range snap.Body.Parts {
			b.WriteString("body += b\"--\" + boundary + b\"\\r\\n\"\n")
			if part.IsFile {
				disposition := fmt.Sprintf("form-data; name=%s", pyBytesField(part.Name))
				disposition += fmt.Sprintf("; filename=%s", pyBytesField(effectiveMultipartFileName(part)))
				fmt.Fprintf(b, "body += %s + b\"\\r\\n\"\n", pyWrappedBytes(disposition))
				ct := effectiveMultipartCT(part)
				fmt.Fprintf(b, "body += %s + b\"\\r\\n\"\n", pyWrappedBytes("Content-Type: "+ct))
				b.WriteString("body += b\"\\r\\n\"\n")
				fmt.Fprintf(b, "body += open(%s, \"rb\").read() + b\"\\r\\n\"\n", pyFileVar(fileVars[part.File]))
			} else {
				fmt.Fprintf(b, "body += %s + b\"\\r\\n\"\n", pyWrappedBytes(fmt.Sprintf("form-data; name=%s", pyBytesField(part.Name))))
				if part.ContentType != "" {
					fmt.Fprintf(b, "body += %s + b\"\\r\\n\"\n", pyWrappedBytes("Content-Type: "+part.ContentType))
				}
				b.WriteString("body += b\"\\r\\n\"\n")
				fmt.Fprintf(b, "body += (%s).encode(\"utf-8\") + b\"\\r\\n\"\n", pySegExpr(part.Text))
			}
		}
		b.WriteString("body += b\"--\" + boundary + b\"--\\r\\n\"\n")
		b.WriteString("data = body\n\n")
	}
}

// runtimeSecret reports whether any urlencoded field needs a runtime secret lookup.
func runtimeSecret(snap *snapshot) bool {
	if snap.Body.Kind != bodyURLEncoded {
		return false
	}
	for _, f := range snap.Body.Fields {
		if hasSecret(f.Value) {
			return true
		}
	}
	return false
}

// pyBytesField renders a quoted multipart name piece for a b"..." literal.
func pyBytesField(name string) string {
	expr := strings.ReplaceAll(name, `\`, `\\`)
	expr = strings.ReplaceAll(expr, `"`, `\"`)
	return `"` + expr + `"`
}

// pyWrappedBytes wraps a static string into a Python bytes-literal line part.
func pyWrappedBytes(text string) string {
	expr := strings.ReplaceAll(text, `\`, `\\`)
	expr = strings.ReplaceAll(expr, `"`, `\"`)
	return "b\"" + expr + "\""
}

// hasDuplicateEnabledHeaders reports any header name (case-insensitive)
// appearing twice in the plan header list; those force the http.client path.
func hasDuplicateEnabledHeaders(snap *snapshot) bool {
	seen := map[string]bool{}
	for _, h := range snap.Headers {
		upper := strings.ToUpper(strings.TrimSpace(h.Name))
		if upper == "Content-Type" {
			continue
		}
		if seen[upper] {
			return true
		}
		seen[upper] = true
	}
	return false
}

func pyFileVar(varName string) string {
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
	if strings.HasPrefix(name, "_") {
		name = strings.TrimPrefix(name, "_")
	}
	if name == "" {
		name = "input_path"
	}
	return name
}

// writePyURLAndHeaders writes the url plus header structure — a dict for the
// urllib path, a list of pairs for http.client (to preserve duplicates).
func writePyURLAndHeaders(b *strings.Builder, snap *snapshot, dupHeaders bool) {
	plain, contentType, hasCT := headerPairs(snap)
	fmt.Fprintf(b, "url = %s\n", pyQuote(literal(snap.URL)))

	if dupHeaders {
		b.WriteString("headers = [\n")
		for _, h := range plain {
			fmt.Fprintf(b, "    (%s, %s),\n", pyQuote(h.Name), pySegExpr(h.Value))
		}
		switch snap.Auth.Carrier {
		case authBearer, authOAuth:
			fmt.Fprintf(b, "    (%s, %s),\n", pyQuote("Authorization"), "\"Bearer \" + "+pySegExpr(snap.Auth.Token))
		case authBasic:
			fmt.Fprintf(b, "    (%s, %s),\n", pyQuote("Authorization"),
				"\"Basic \" + base64.b64encode(("+pySegExpr(snap.Auth.User)+" + \":\" + "+pySegExpr(snap.Auth.Pass)+").encode(\"utf-8\")).decode(\"ascii\")")
		}
		if hasCT {
			fmt.Fprintf(b, "    (%s, %s),\n", pyQuote(contentType.Name), pySegExpr(contentType.Value))
		}
		b.WriteString("]\n\n")
		return
	}

	b.WriteString("headers = {\n")
	for _, h := range plain {
		fmt.Fprintf(b, "    %s: %s,\n", pyQuote(h.Name), pySegExpr(h.Value))
	}
	switch snap.Auth.Carrier {
	case authBearer, authOAuth:
		fmt.Fprintf(b, "    %s: %s,\n", pyQuote("Authorization"), "\"Bearer \" + "+pySegExpr(snap.Auth.Token))
	case authBasic:
		fmt.Fprintf(b, "    %s: %s,\n", pyQuote("Authorization"),
			"\"Basic \" + base64.b64encode(("+pySegExpr(snap.Auth.User)+" + \":\" + "+pySegExpr(snap.Auth.Pass)+").encode(\"utf-8\")).decode(\"ascii\")")
	}
	if hasCT {
		fmt.Fprintf(b, "    %s: %s,\n", pyQuote(contentType.Name), pySegExpr(contentType.Value))
	}
	b.WriteString("}\n\n")
}

func writePyUrllibSend(b *strings.Builder, snap *snapshot) {
	timeout := secondsString(snap.Timeout)
	fmt.Fprintf(b, "request = urllib.request.Request(url, headers=headers, method=%s", pyQuote(snap.Method))
	if snap.Body.HasBody {
		b.WriteString(", data=data")
	}
	b.WriteString(")\n\n")
	fmt.Fprintf(b, "try:\n    with urllib.request.urlopen(request, timeout=%s) as response:\n", timeout)
	b.WriteString("        print(response.status)\n")
	b.WriteString("        sys.stdout.buffer.write(response.read())\n")
	b.WriteString("except urllib.error.HTTPError as error:\n")
	b.WriteString("    print(error.code)\n")
	b.WriteString("    sys.stdout.buffer.write(error.read())\n")
}

func writePyHTTPClientSend(b *strings.Builder, snap *snapshot) {
	timeout := secondsString(snap.Timeout)
	b.WriteString("split = urllib.parse.urlsplit(url)\n")
	b.WriteString("if split.scheme == \"https\":\n")
	b.WriteString("    conn = http.client.HTTPSConnection(split.netloc, timeout=" + timeout + ")\n")
	b.WriteString("else:\n")
	b.WriteString("    conn = http.client.HTTPConnection(split.netloc, timeout=" + timeout + ")\n")
	b.WriteString("path = split.path or \"/\"\n")
	b.WriteString("if split.query:\n    path += \"?\" + split.query\n")
	fmt.Fprintf(b, "conn.putrequest(%s, path)\n", pyQuote(snap.Method))
	b.WriteString("for name, value in headers:\n    conn.putheader(name, value)\n")
	if snap.Body.HasBody {
		b.WriteString("conn.endheaders(data)\n")
	} else {
		b.WriteString("conn.endheaders()\n")
	}
	b.WriteString("response = conn.getresponse()\n")
	b.WriteString("print(response.status)\n")
	b.WriteString("sys.stdout.buffer.write(response.read())\n")
	b.WriteString("conn.close()\n")
}
