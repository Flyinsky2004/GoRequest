package snippet

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// snapshotUsesSecret reports whether any composable field of the snapshot
// carries a runtime secret lookup (headers, credentials, form or multipart
// text values). JSON/text-embedded secrets stay textual placeholders and do
// not need runtime lookups.
func snapshotUsesSecret(snap *snapshot) bool {
	plain, _, _ := headerPairs(snap)
	for _, p := range plain {
		if hasSecret(p.Value) {
			return true
		}
	}
	switch snap.Auth.Carrier {
	case authBearer, authOAuth:
		if hasSecret(snap.Auth.Token) {
			return true
		}
	case authBasic:
		if hasSecret(snap.Auth.User) || hasSecret(snap.Auth.Pass) {
			return true
		}
	}
	if snap.Body.Kind == bodyURLEncoded {
		for _, f := range snap.Body.Fields {
			if hasSecret(f.Value) {
				return true
			}
		}
	}
	if snap.Body.Kind == bodyMultipart {
		for _, p := range snap.Body.Parts {
			if !p.IsFile && hasSecret(p.Text) {
				return true
			}
		}
	}
	if snap.Body.Kind == bodyText || snap.Body.Kind == bodyXML {
		return hasSecret(snap.Body.Text)
	}
	return false
}

// renderGo builds a complete, runnable Go program using only the standard
// library. GoRequest's transport facts (body bytes, header set, timeout) are
// frozen into the snapshot exactly as the runner would send them.
func renderGo(snap *snapshot, res *Result) string {
	usesMultipart := snap.Body.Kind == bodyMultipart
	readsFile := len(collectFileInputs(snap)) > 0
	needsMustEnv := snapshotUsesSecret(snap)
	usesBase64 := snap.Auth.Carrier == authBasic
	usesURLValues := false
	if snap.Body.Kind == bodyURLEncoded && !snap.Body.HasFile && !snap.Body.HasLineup {
		for _, f := range snap.Body.Fields {
			if hasSecret(f.Value) {
				usesURLValues = true
				break
			}
		}
	}
	usesLiteralBody := false
	switch snap.Body.Kind {
	case bodyJSON, bodyText, bodyXML:
		usesLiteralBody = snap.Body.HasBody && !snap.Body.HasFile
	case bodyURLEncoded:
		usesLiteralBody = snap.Body.HasLineup || (!usesURLValues && !snap.Body.HasFile)
	}

	pick := func(segs []segment) string { return goSegExpr(segs) }

	imports := map[string]bool{"io": true, "log": true, "net/http": true, "os": true, "time": true}
	if usesMultipart || readsFile {
		imports["bytes"] = true
	}
	if usesMultipart || usesLiteralBody {
		imports["strings"] = true
	}
	if usesBase64 {
		imports["encoding/base64"] = true
	}
	if usesURLValues {
		imports["net/url"] = true
	}
	if usesMultipart {
		imports["mime/multipart"] = true
		imports["net/textproto"] = true
	}

	var b strings.Builder
	writeReviewHeader(&b, snap, res, "//")
	b.WriteString("package main\n\nimport (\n")
	for _, imp := range sortedStrings(imports) {
		fmt.Fprintf(&b, "\t%q\n", imp)
	}
	b.WriteString(")\n\n")

	if needsMustEnv {
		b.WriteString(`func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("missing environment variable %s", name)
	}
	return v
}

`)
	}
	if usesMultipart {
		b.WriteString(`func partHeader(name, filename, contentType string) textproto.MIMEHeader {
	h := make(textproto.MIMEHeader)
	disposition := "form-data; name=" + quoteMultipartField(name)
	if filename != "" {
		disposition += "; filename=" + quoteMultipartField(filename)
	}
	h.Set("Content-Disposition", disposition)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return h
}

func quoteMultipartField(s string) string {
	escaped := strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + escaped + "\""
}

`)
	}

	fileVars := fileVarAssignments(collectFileInputs(snap))
	b.WriteString("func main() {\n")
	for _, ref := range collectFileInputs(snap) {
		if ref.IsLocal {
			fmt.Fprintf(&b, "\t// GoRequest local file slot %s — host paths never enter shared definitions.\n", ref.Suggested)
		} else {
			fmt.Fprintf(&b, "\t// GoRequest project file %s — point this at the local copy.\n", ref.Suggested)
		}
		fmt.Fprintf(&b, "\tconst %s = %s\n\n", fileVars[ref], goQuote(filePlaceholder(ref)))
	}

	// --- body ---------------------------------------------------------------
	hasBody := snap.Body.HasBody || usesMultipart
	switch snap.Body.Kind {
	case bodyJSON, bodyText, bodyXML:
		if snap.Body.HasFile {
			fmt.Fprintf(&b, "\tbodyBytes, err := os.ReadFile(%s)\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tbody := bytes.NewReader(bodyBytes)\n\n", fileVars[snap.Body.File])
		} else {
			text := snap.Body.JSONText
			if snap.Body.Kind != bodyJSON {
				text = literal(snap.Body.Text)
			}
			fmt.Fprintf(&b, "\tbody := strings.NewReader(%s)\n\n", goQuote(text))
		}
	case bodyBinary:
		fmt.Fprintf(&b, "\tbodyBytes, err := os.ReadFile(%s)\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tbody := bytes.NewReader(bodyBytes)\n\n", fileVars[snap.Body.File])
	case bodyURLEncoded:
		switch {
		case snap.Body.HasFile:
			fmt.Fprintf(&b, "\tbodyBytes, err := os.ReadFile(%s)\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tbody := bytes.NewReader(bodyBytes)\n\n", fileVars[snap.Body.File])
		case snap.Body.HasLineup:
			fmt.Fprintf(&b, "\tbody := strings.NewReader(%s)\n\n", goQuote(snap.Body.Lineup))
		case usesURLValues:
			b.WriteString("\tform := url.Values{}\n")
			for _, f := range snap.Body.Fields {
				fmt.Fprintf(&b, "\tform.Set(%s, %s)\n", goQuote(f.Name), pick(f.Value))
			}
			b.WriteString("\tbody := strings.NewReader(form.Encode())\n\n")
		default:
			var parts []string
			for _, f := range snap.Body.Fields {
				parts = append(parts, queryFormEncoded(f.Name, literal(f.Value)))
			}
			fmt.Fprintf(&b, "\tbody := strings.NewReader(%s)\n\n", goQuote(strings.Join(parts, "&")))
		}
	case bodyMultipart:
		b.WriteString("\tpayload := &bytes.Buffer{}\n")
		b.WriteString("\twriter := multipart.NewWriter(payload)\n")
		fmt.Fprintf(&b, "\tif err := writer.SetBoundary(%s); err != nil {\n\t\tlog.Fatal(err)\n\t}\n", goQuote(multipartBoundary))
		for _, part := range snap.Body.Parts {
			if part.IsFile {
				fmt.Fprintf(&b, "\tpw, err := writer.CreatePart(partHeader(%s, %s, %s))\n",
					goQuote(part.Name), goQuote(effectiveMultipartFileName(part)), goQuote(effectiveMultipartCT(part)))
				b.WriteString("\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n")
				fmt.Fprintf(&b, "\tdata, err := os.ReadFile(%s)\n", fileVars[part.File])
				b.WriteString("\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n")
				b.WriteString("\tif _, err := pw.Write(data); err != nil {\n\t\tlog.Fatal(err)\n\t}\n")
			} else {
				fmt.Fprintf(&b, "\tpw, err := writer.CreatePart(partHeader(%s, \"\", %s))\n",
					goQuote(part.Name), goQuote(part.ContentType))
				b.WriteString("\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n")
				fmt.Fprintf(&b, "\tif _, err := pw.Write([]byte(%s)); err != nil {\n\t\tlog.Fatal(err)\n\t}\n", pick(part.Text))
			}
		}
		b.WriteString("\tif err := writer.Close(); err != nil {\n\t\tlog.Fatal(err)\n\t}\n")
		b.WriteString("\tbody := bytes.NewReader(payload.Bytes())\n\n")
	}

	newBodyArg := "nil"
	if hasBody {
		newBodyArg = "body"
	}
	fmt.Fprintf(&b, "\treq, err := http.NewRequest(%s, %s, %s)\n", goQuote(snap.Method), goQuote(literal(snap.URL)), newBodyArg)
	b.WriteString("\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\n")

	// --- headers (plan order; Add preserves duplicates) --------------------
	plain, contentType, hasCT := headerPairs(snap)
	for _, h := range plain {
		fmt.Fprintf(&b, "\treq.Header.Add(%s, %s)\n", goQuote(h.Name), pick(h.Value))
	}
	switch snap.Auth.Carrier {
	case authBearer, authOAuth:
		fmt.Fprintf(&b, "\treq.Header.Add(%s, %s)\n", goQuote("Authorization"), "\"Bearer \" + "+pick(snap.Auth.Token))
	case authBasic:
		fmt.Fprintf(&b, "\tcredential := base64.StdEncoding.EncodeToString([]byte(%s + \":\" + %s))\n", pick(snap.Auth.User), pick(snap.Auth.Pass))
		fmt.Fprintf(&b, "\treq.Header.Add(%s, %s)\n", goQuote("Authorization"), "\"Basic \" + credential")
	}
	if usesMultipart {
		b.WriteString("\treq.Header.Set(\"Content-Type\", writer.FormDataContentType())\n")
	} else if hasCT {
		fmt.Fprintf(&b, "\treq.Header.Add(%s, %s)\n", goQuote(contentType.Name), pick(contentType.Value))
	}

	// --- send ----------------------------------------------------------------
	fmt.Fprintf(&b, "\n\tclient := &http.Client{Timeout: %s * time.Second}\n", secondsString(snap.Timeout))
	b.WriteString("\tresp, err := client.Do(req)\n")
	b.WriteString("\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n")
	b.WriteString("\tdefer resp.Body.Close()\n")
	b.WriteString("\tlog.Printf(\"status %d\", resp.StatusCode)\n")
	b.WriteString("\tif _, err := io.Copy(os.Stdout, resp.Body); err != nil {\n\t\tlog.Fatal(err)\n\t}\n")
	b.WriteString("}\n")
	return b.String()
}

func sortedStrings(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k, ok := range set {
		if ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// queryFormEncoded mirrors url.Values.Encode output for one field
// (Go-style QueryEscape) so pre-built literal bodies are byte-identical to
// what the runner would send.
func queryFormEncoded(name, value string) string {
	return url.QueryEscape(name) + "=" + url.QueryEscape(value)
}
