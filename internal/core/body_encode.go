package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PlanOptions supplies file-resolution ports and upload bounds when building a
// Plan or encoding a Body. Zero values remain safe: MaxUploadBytes defaults to
// DefaultMaxUploadBytes, and unconfigured file ports fail before network with a
// ConfigError if a request references files.
type PlanOptions struct {
	// ProjectRoot is the absolute path to .gorequest/ (used when ProjectFiles is nil).
	ProjectRoot string
	// ProjectFiles opens relative files strictly confined to .gorequest/.
	ProjectFiles ProjectFileOpener
	// LocalFiles resolves explicit "local:<slot>" / localFile mappings on this device.
	LocalFiles LocalFileProvider
	// MaxUploadBytes caps total encoded request body bytes (0 => DefaultMaxUploadBytes).
	MaxUploadBytes int64
	// AllowGraphQL opts this execution path into GraphQL Query/Mutation
	// planning. Interactive request sending sets it; Runner, diagnostics and
	// other automation leave it false so a GraphQL request is rejected rather
	// than silently executed as ordinary HTTP with HTTP-only assertions.
	AllowGraphQL bool
}

// EncodedBody is the result of encoding a Request's HTTP Body.
type EncodedBody struct {
	Bytes            []byte
	HasBody          bool
	ContentType      string
	ForceContentType bool
}

var multipartQuoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeMultipartQuotes(s string) string {
	return multipartQuoteEscaper.Replace(s)
}

// BuildBody resolves templates, validates attachments, and encodes b into bytes
// plus its media type. Missing files, path escapes, unknown fields and size
// overruns fail with ConfigError before any network call. Context cancellation
// aborts chunked file reads immediately.
func BuildBody(ctx context.Context, b *Body, r *Resolver, opts PlanOptions) (*EncodedBody, error) {
	if err := checkUploadContext(ctx); err != nil {
		return nil, err
	}
	if b == nil {
		return &EncodedBody{HasBody: false}, nil
	}
	if r == nil {
		r = NewResolver(nil, nil)
	}
	if err := ValidateBody(b); err != nil {
		return nil, err
	}
	tb, err := InspectBody(b)
	if err != nil {
		return nil, err
	}

	maxBytes := opts.MaxUploadBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxUploadBytes
	}
	var used int64
	checkBytes := func(n int) error {
		used += int64(n)
		if used > maxBytes {
			return configErrorf("request body exceeds maximum upload size of %d bytes", maxBytes)
		}
		return nil
	}

	fileRefOf := func() (FileRef, bool) {
		if tb.LocalFile != "" {
			return FileRef{Kind: FileRefLocal, Slot: tb.LocalFile}, true
		}
		if tb.File != "" {
			return FileRef{Kind: FileRefProject, Path: tb.File}, true
		}
		return FileRef{}, false
	}

	switch tb.Kind {
	case BodyNone:
		return &EncodedBody{HasBody: false}, nil

	case BodyJSON:
		if ref, hasFile := fileRefOf(); hasFile {
			raw, _, err := readFileRefBounded(ctx, ref, opts, &used, maxBytes)
			if err != nil {
				return nil, err
			}
			encoded, err := encodeJSONFileBytes(raw, r, maxBytes)
			if err != nil {
				return nil, err
			}
			return &EncodedBody{
				Bytes:       encoded,
				HasBody:     true,
				ContentType: "application/json",
			}, nil
		}
		v, err := renderNode(tb.JSONNode, r)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(v)
		if err != nil {
			return nil, configErrorf("cannot encode json body: %v", err)
		}
		if err := checkBytes(len(out)); err != nil {
			return nil, err
		}
		return &EncodedBody{
			Bytes:       out,
			HasBody:     true,
			ContentType: "application/json",
		}, nil

	case BodyText, BodyXML:
		ct := "text/plain; charset=utf-8"
		if tb.Kind == BodyXML {
			ct = "application/xml"
		}
		if ref, hasFile := fileRefOf(); hasFile {
			raw, _, err := readFileRefBounded(ctx, ref, opts, &used, maxBytes)
			if err != nil {
				return nil, err
			}
			return &EncodedBody{
				Bytes:       raw,
				HasBody:     true,
				ContentType: ct,
			}, nil
		}
		text, err := r.Resolve(tb.Text)
		if err != nil {
			return nil, err
		}
		out := []byte(text)
		if err := checkBytes(len(out)); err != nil {
			return nil, err
		}
		return &EncodedBody{
			Bytes:       out,
			HasBody:     true,
			ContentType: ct,
		}, nil

	case BodyURLEncoded:
		if ref, hasFile := fileRefOf(); hasFile {
			raw, _, err := readFileRefBounded(ctx, ref, opts, &used, maxBytes)
			if err != nil {
				return nil, err
			}
			return &EncodedBody{
				Bytes:       raw,
				HasBody:     true,
				ContentType: "application/x-www-form-urlencoded",
			}, nil
		}
		encoded, err := EncodeURLEncodedFields(tb.FormFields, r)
		if err != nil {
			return nil, err
		}
		out := []byte(encoded)
		if err := checkBytes(len(out)); err != nil {
			return nil, err
		}
		return &EncodedBody{
			Bytes:       out,
			HasBody:     true,
			ContentType: "application/x-www-form-urlencoded",
		}, nil

	case BodyFormData:
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for idx, p := range tb.MultipartParts {
			if !p.IsEnabled() {
				continue
			}
			if err := checkUploadContext(ctx); err != nil {
				return nil, err
			}
			name, err := r.Resolve(p.Name)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(name) == "" {
				return nil, configErrorf("form-data part %d: enabled part has an empty name", idx+1)
			}
			if strings.ContainsAny(name, "\r\n") {
				return nil, configErrorf("form-data part %q contains a newline; refusing header injection", name)
			}

			var partCT string
			if p.ContentType != "" {
				partCT, err = r.Resolve(p.ContentType)
				if err != nil {
					return nil, err
				}
				if err := validateMediaType(partCT); err != nil {
					return nil, configErrorf("form-data part %q contentType: %v", name, err)
				}
			}

			switch p.EffectiveKind() {
			case "text":
				val, err := r.Resolve(p.Value)
				if err != nil {
					return nil, err
				}
				if err := checkBytes(len(val)); err != nil {
					return nil, err
				}
				hdr := make(textproto.MIMEHeader)
				hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, escapeMultipartQuotes(name)))
				if partCT != "" {
					hdr.Set("Content-Type", partCT)
				}
				pw, err := mw.CreatePart(hdr)
				if err != nil {
					return nil, configErrorf("creating multipart field %q: %v", name, err)
				}
				if _, err := pw.Write([]byte(val)); err != nil {
					return nil, configErrorf("writing multipart field %q: %v", name, err)
				}

			case "file":
				ref, err := p.ResolveFileRef()
				if err != nil {
					return nil, err
				}
				fileBytes, diskName, err := readFileRefBounded(ctx, ref, opts, &used, maxBytes)
				if err != nil {
					return nil, err
				}
				fileName := diskName
				if p.FileName != "" {
					fileName, err = r.Resolve(p.FileName)
					if err != nil {
						return nil, err
					}
					if strings.ContainsAny(fileName, "\r\n") {
						return nil, configErrorf("form-data part %q fileName contains a newline", name)
					}
				}
				if partCT == "" {
					if guessed := mime.TypeByExtension(filepath.Ext(fileName)); guessed != "" {
						partCT = guessed
					} else {
						partCT = "application/octet-stream"
					}
				}
				hdr := make(textproto.MIMEHeader)
				hdr.Set("Content-Disposition", fmt.Sprintf(
					`form-data; name="%s"; filename="%s"`,
					escapeMultipartQuotes(name),
					escapeMultipartQuotes(fileName),
				))
				hdr.Set("Content-Type", partCT)
				pw, err := mw.CreatePart(hdr)
				if err != nil {
					return nil, configErrorf("creating multipart file part %q: %v", name, err)
				}
				if _, err := pw.Write(fileBytes); err != nil {
					return nil, configErrorf("writing multipart file part %q: %v", name, err)
				}
			}

			if int64(buf.Len()) > maxBytes {
				return nil, configErrorf("request body exceeds maximum upload size of %d bytes", maxBytes)
			}
		}
		if err := mw.Close(); err != nil {
			return nil, configErrorf("closing multipart writer: %v", err)
		}
		if int64(buf.Len()) > maxBytes {
			return nil, configErrorf("request body exceeds maximum upload size of %d bytes", maxBytes)
		}
		return &EncodedBody{
			Bytes:            buf.Bytes(),
			HasBody:          true,
			ContentType:      mw.FormDataContentType(),
			ForceContentType: true,
		}, nil

	case BodyBinary:
		ref, hasFile := fileRefOf()
		if !hasFile {
			return nil, configErrorf("binary body requires file or local file mapping")
		}
		raw, _, err := readFileRefBounded(ctx, ref, opts, &used, maxBytes)
		if err != nil {
			return nil, err
		}
		ct := "application/octet-stream"
		if tb.ContentType != "" {
			resolvedCT, err := r.Resolve(tb.ContentType)
			if err != nil {
				return nil, err
			}
			if err := validateMediaType(resolvedCT); err != nil {
				return nil, configErrorf("binary body contentType: %v", err)
			}
			ct = resolvedCT
		}
		return &EncodedBody{
			Bytes:       raw,
			HasBody:     true,
			ContentType: ct,
		}, nil

	default:
		return nil, configErrorf("unsupported body type %q", b.Type)
	}
}

func encodeJSONFileBytes(raw []byte, r *Resolver, maxBytes int64) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, configErrorf("json body file is empty")
	}
	if !json.Valid(raw) {
		return nil, configErrorf("invalid JSON in body file")
	}
	if len(TemplateNames(string(raw))) == 0 {
		return raw, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, configErrorf("invalid JSON in body file: %v", err)
	}
	if err := checkNoDuplicateMappingKeys(&doc); err != nil {
		return nil, err
	}
	v, err := renderNode(&doc, r)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, configErrorf("cannot encode json body file: %v", err)
	}
	if int64(len(out)) > maxBytes {
		return nil, configErrorf("request body exceeds maximum upload size of %d bytes", maxBytes)
	}
	return out, nil
}

// EncodeURLEncodedFields encodes enabled FormFields in exact slice order,
// preserving duplicate keys and empty values while preventing double encoding.
func EncodeURLEncodedFields(fields []FormField, r *Resolver) (string, error) {
	if r == nil {
		r = NewResolver(nil, nil)
	}
	var parts []string
	for idx, f := range fields {
		if !f.IsEnabled() {
			continue
		}
		var encName, encVal string
		var err error
		if f.Encoded {
			encName, err = resolveTemplatesIntoPercentEncoded(f.Name, r)
			if err != nil {
				return "", configErrorf("urlencoded item %d name: %w", idx+1, err)
			}
			if strings.TrimSpace(encName) == "" {
				return "", configErrorf("urlencoded item %d: enabled field has an empty name", idx+1)
			}
			encVal, err = resolveTemplatesIntoPercentEncoded(f.Value, r)
			if err != nil {
				return "", configErrorf("urlencoded item %d value: %w", idx+1, err)
			}
		} else {
			name, err := r.Resolve(f.Name)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(name) == "" {
				return "", configErrorf("urlencoded item %d: enabled field has an empty name", idx+1)
			}
			val, err := r.Resolve(f.Value)
			if err != nil {
				return "", err
			}
			encName = url.QueryEscape(name)
			encVal = url.QueryEscape(val)
		}
		parts = append(parts, encName+"="+encVal)
	}
	return strings.Join(parts, "&"), nil
}

// OpenBodyStream opens a bounded, context-cancelable upload stream for b.
// Binary bodies stream directly from the opened file descriptor after path,
// symlink and size checks; structured bodies stream from bounded encoded bytes.
func OpenBodyStream(ctx context.Context, b *Body, r *Resolver, opts PlanOptions) (*EncodedBodyStream, error) {
	if err := checkUploadContext(ctx); err != nil {
		return nil, err
	}
	if b == nil {
		return &EncodedBodyStream{HasBody: false}, nil
	}
	if err := ValidateBody(b); err != nil {
		return nil, err
	}
	tb, err := InspectBody(b)
	if err != nil {
		return nil, err
	}
	maxBytes := opts.MaxUploadBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxUploadBytes
	}

	if tb.Kind == BodyBinary {
		var ref FileRef
		switch {
		case tb.LocalFile != "":
			ref = FileRef{Kind: FileRefLocal, Slot: tb.LocalFile}
		case tb.File != "":
			ref = FileRef{Kind: FileRefProject, Path: tb.File}
		default:
			return nil, configErrorf("binary body requires file or local file mapping")
		}
		opened, err := openBodyFileRef(ctx, ref, opts)
		if err != nil {
			return nil, err
		}
		if opened.Size >= 0 && opened.Size > maxBytes {
			_ = opened.Reader.Close()
			return nil, configErrorf("request body exceeds maximum upload size of %d bytes", maxBytes)
		}
		ct := "application/octet-stream"
		if tb.ContentType != "" {
			if r == nil {
				r = NewResolver(nil, nil)
			}
			resolvedCT, err := r.Resolve(tb.ContentType)
			if err != nil {
				_ = opened.Reader.Close()
				return nil, err
			}
			if err := validateMediaType(resolvedCT); err != nil {
				_ = opened.Reader.Close()
				return nil, configErrorf("binary body contentType: %v", err)
			}
			ct = resolvedCT
		}
		return &EncodedBodyStream{
			Reader:        &boundedCancelReadCloser{ctx: ctx, rc: opened.Reader, maxBytes: maxBytes},
			ContentType:   ct,
			ContentLength: opened.Size,
			HasBody:       true,
		}, nil
	}

	enc, err := BuildBody(ctx, b, r, opts)
	if err != nil {
		return nil, err
	}
	if !enc.HasBody {
		return &EncodedBodyStream{HasBody: false}, nil
	}
	return &EncodedBodyStream{
		Reader:           &boundedCancelReadCloser{ctx: ctx, rc: io.NopCloser(bytes.NewReader(enc.Bytes)), maxBytes: maxBytes},
		ContentType:      enc.ContentType,
		ContentLength:    int64(len(enc.Bytes)),
		ForceContentType: enc.ForceContentType,
		HasBody:          true,
	}, nil
}

// BuildPlanWithOptions resolves a request into an executable Plan with full
// support for custom HTTP methods, project-relative body.file references,
// explicit local-machine file mappings, upload size caps, and cancellation.
func BuildPlanWithOptions(ctx context.Context, req *Request, vars map[string]string, secrets SecretProvider, authScheme *AuthScheme, opts PlanOptions) (*Plan, error) {
	if req == nil || req.HTTP == nil {
		return nil, configErrorf("request has no http definition")
	}
	if isGraphQLRequest(req) {
		// GraphQL needs tracked variable resolution and a JSON envelope; the
		// plain builder must not silently send it as an ordinary HTTP request.
		return nil, configErrorf("GraphQL requests must be built through BuildTrackedPlan so variables and secrets are tracked")
	}
	if strings.EqualFold(strings.TrimSpace(req.Protocol), ProtocolWebSocket) {
		// WebSocket requests share the request target fields but are driven by
		// internal/websocket.Session; they must never be sent as HTTP.
		return nil, configErrorf("request %q is a WebSocket request and cannot be executed as HTTP", req.Name)
	}
	if err := checkUploadContext(ctx); err != nil {
		return nil, err
	}
	r := NewResolver(vars, secrets)

	method := strings.ToUpper(strings.TrimSpace(req.HTTP.Method))
	if method == "" {
		method = http.MethodGet
	}
	if err := ValidateHTTPMethod(method); err != nil {
		return nil, err
	}

	rawURL, err := r.Resolve(req.HTTP.URL)
	if err != nil {
		return nil, err
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, configErrorf("request url is empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, configErrorf("invalid url %q: %v", rawURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, configErrorf("url %q must be absolute (scheme and host)", rawURL)
	}

	var headers [][2]string
	for _, kv := range req.HTTP.Headers {
		if !kv.IsEnabled() {
			continue
		}
		name, err := r.Resolve(kv.Name)
		if err != nil {
			return nil, err
		}
		val, err := r.Resolve(kv.Value)
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(val, "\r\n") {
			return nil, configErrorf("header %q contains a newline; refusing header injection", name)
		}
		if strings.TrimSpace(name) == "" {
			return nil, configErrorf("header name is empty")
		}
		headers = append(headers, [2]string{name, val})
	}

	var query [][2]string
	for _, kv := range req.HTTP.Query {
		if !kv.IsEnabled() {
			continue
		}
		name, err := r.Resolve(kv.Name)
		if err != nil {
			return nil, err
		}
		val, err := r.Resolve(kv.Value)
		if err != nil {
			return nil, err
		}
		query = append(query, [2]string{name, val})
	}

	if err := applyAuth(authScheme, r, &headers, &query); err != nil {
		return nil, err
	}

	encBody, err := BuildBody(ctx, req.HTTP.Body, r, opts)
	if err != nil {
		return nil, err
	}
	if encBody.HasBody && encBody.ContentType != "" {
		headers = applyBodyContentType(headers, encBody.ContentType, encBody.ForceContentType)
	}

	timeout := DefaultTimeout
	timeoutExplicit := false
	if req.Timeout != "" {
		d, err := time.ParseDuration(req.Timeout)
		if err != nil {
			return nil, configErrorf("invalid timeout %q: %v", req.Timeout, err)
		}
		if d > 0 {
			timeout = d
			timeoutExplicit = true
		}
	}

	return &Plan{
		Method:          method,
		URL:             rawURL,
		Headers:         headers,
		Query:           query,
		Body:            encBody.Bytes,
		Timeout:         timeout,
		TimeoutExplicit: timeoutExplicit,
		HasBody:         encBody.HasBody,
	}, nil
}

func applyBodyContentType(headers [][2]string, ct string, force bool) [][2]string {
	if !force {
		return ensureContentType(headers, ct)
	}
	out := make([][2]string, 0, len(headers)+1)
	replaced := false
	for _, h := range headers {
		if strings.EqualFold(h[0], "Content-Type") {
			if !replaced {
				out = append(out, [2]string{"Content-Type", ct})
				replaced = true
			}
			continue
		}
		out = append(out, h)
	}
	if !replaced {
		out = append(out, [2]string{"Content-Type", ct})
	}
	return out
}
