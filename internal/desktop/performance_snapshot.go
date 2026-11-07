package desktop

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
	"gorequest/internal/diagnostics"
)

// perfSecrets snapshots bound secrets on the UI thread and seals its backing
// provider before background diagnostic workers start.
type perfSecrets struct {
	source core.SecretProvider
	values core.MapSecrets
}

func (s *perfSecrets) Get(name string) (string, bool) {
	if v, ok := s.values[name]; ok {
		return v, true
	}
	if s.source != nil {
		if v, ok := s.source.Get(name); ok {
			s.values[name] = v
			return v, true
		}
	}
	return "", false
}

func (s *perfSecrets) SecretNames() []string {
	names := make([]string, 0, len(s.values))
	for n := range s.values {
		names = append(names, n)
	}
	return names
}

type perfFile struct {
	bytes []byte
	name  string
}

type perfFiles struct {
	source core.ProjectFileOpener
	files  map[string]perfFile
	total  int64
}

func (s *perfFiles) OpenProjectFile(ctx context.Context, rel string) (*core.OpenedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, ok := s.files[rel]
	if !ok {
		if s.source == nil {
			return nil, &core.ConfigError{Err: errors.New("file was not included in saved performance diagnostics snapshot")}
		}
		opened, err := s.source.OpenProjectFile(ctx, rel)
		if err != nil {
			return nil, err
		}
		defer opened.Reader.Close()
		b, err := io.ReadAll(io.LimitReader(opened.Reader, core.DefaultMaxUploadBytes-s.total+1))
		if err != nil {
			return nil, err
		}
		s.total += int64(len(b))
		if s.total > core.DefaultMaxUploadBytes {
			return nil, &core.ConfigError{Err: errors.New("performance diagnostics file snapshot exceeds 64 MiB")}
		}
		f = perfFile{bytes: b, name: opened.Name}
		s.files[rel] = f
	}
	return &core.OpenedFile{
		Reader: io.NopCloser(bytes.NewReader(f.bytes)),
		Name:   f.name,
		Size:   int64(len(f.bytes)),
	}, nil
}

// freezePerformanceTLS resolves project and local TLS references into in-memory
// PEM slices on the UI thread so background sampling workers never read local
// file slots directly and can use a run-scoped keep-alive transport pool.
func freezePerformanceTLS(ctx context.Context, opts *core.ExecuteOptions, files *perfFiles) error {
	load := func(ref string, key bool) ([]byte, error) {
		parsed, err := core.ParseFileRef(ref)
		if err != nil {
			return nil, err
		}
		if key && parsed.Kind != core.FileRefLocal {
			return nil, errors.New("mTLS private key must use a local slot")
		}
		var opened *core.OpenedFile
		if parsed.Kind == core.FileRefProject {
			opened, err = files.OpenProjectFile(ctx, parsed.Path)
		} else if parsed.Kind == core.FileRefLocal && opts.LocalFiles != nil {
			name, ok := opts.LocalFiles.ResolveLocalFile(parsed.Slot)
			if !ok {
				return nil, errors.New("TLS local slot is not bound")
			}
			f, e := os.Open(name)
			if e != nil {
				return nil, errors.New("TLS local slot is unreadable")
			}
			info, e := f.Stat()
			if e != nil || !info.Mode().IsRegular() {
				_ = f.Close()
				return nil, errors.New("TLS local slot must be a regular file")
			}
			opened = &core.OpenedFile{Reader: f, Name: filepath.Base(name)}
		} else {
			return nil, errors.New("TLS file reference is unavailable")
		}
		if err != nil {
			return nil, err
		}
		defer opened.Reader.Close()
		b, err := io.ReadAll(io.LimitReader(opened.Reader, 8<<20+1))
		if err != nil || len(b) > 8<<20 {
			return nil, errors.New("TLS certificate/key material exceeds 8 MiB limit")
		}
		return b, nil
	}

	for _, item := range []struct {
		ref *string
		pem *[]byte
		key bool
	}{
		{&opts.TLS.CustomCARef, &opts.TLS.CustomCAPEM, false},
		{&opts.TLS.ClientCertRef, &opts.TLS.ClientCertPEM, false},
		{&opts.TLS.ClientKeyRef, &opts.TLS.ClientKeyPEM, true},
	} {
		if *item.ref != "" {
			b, err := load(*item.ref, item.key)
			if err != nil {
				return err
			}
			*item.pem = b
			*item.ref = ""
		} else {
			*item.pem = append([]byte(nil), (*item.pem)...)
		}
	}
	if opts.TLS.UseSystemCA != nil {
		v := *opts.TLS.UseSystemCA
		opts.TLS.UseSystemCA = &v
	}
	return nil
}

// prepareDesktopPerformance snapshots secrets, TLS material, and project files
// on the initiating UI thread and returns a self-contained *diagnostics.Engine
// with zero references to App, projectState, or Tab.
func prepareDesktopPerformance(
	ctx context.Context,
	p *core.Project,
	requestRef string,
	env string,
	cfg diagnostics.SamplingConfig,
	network core.ExecuteOptions,
	source core.SecretProvider,
	executor diagnostics.Executor,
	emit diagnostics.Emitter,
) (*diagnostics.Engine, error) {
	if p == nil {
		return nil, errors.New("diagnostics requires a saved project snapshot")
	}
	secrets := &perfSecrets{source: source, values: core.MapSecrets{}}
	switch provider := source.(type) {
	case interface{ SecretNames() []string }:
		for _, name := range provider.SecretNames() {
			secrets.Get(name)
		}
	case core.MapSecrets:
		for name := range provider {
			secrets.Get(name)
		}
	}

	b, err := yaml.Marshal(struct {
		Requests map[string]*core.RequestEntry
		Envs     map[string]*core.Environment
		Auth     map[string]*core.AuthScheme
	}{p.Requests, p.Environments, p.AuthSchemes})
	if err != nil {
		return nil, errors.New("cannot capture performance diagnostics definitions")
	}
	for _, name := range core.TemplateNames(string(b)) {
		if after, ok := strings.CutPrefix(name, "secret."); ok {
			secrets.Get(strings.TrimSpace(after))
		}
	}
	for _, e := range p.Environments {
		if e == nil {
			continue
		}
		for _, name := range e.Secrets {
			secrets.Get(strings.TrimSpace(name))
		}
	}
	if network.Proxy.PasswordSecret != "" {
		secrets.Get(strings.TrimSpace(network.Proxy.PasswordSecret))
	}

	files := &perfFiles{source: core.NewDirFileOpener(p.Dir), files: map[string]perfFile{}}
	if err := freezePerformanceTLS(ctx, &network, files); err != nil {
		return nil, errors.New("performance diagnostics TLS snapshot failed: check configured CA/certificate/key slots")
	}

	// Verify target request body does not rely on mutable local file slots.
	normRef := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(requestRef), "\\", "/"), "./")
	var targetEntry *core.RequestEntry
	for _, e := range p.Requests {
		if e == nil || e.Request == nil {
			continue
		}
		if e.RelPath == normRef || e.Request.ID == requestRef {
			targetEntry = e
			break
		}
	}
	if targetEntry != nil && targetEntry.Request.HTTP != nil && targetEntry.Request.HTTP.Body != nil {
		tb, e := core.InspectBody(targetEntry.Request.HTTP.Body)
		if e != nil {
			return nil, errors.New("invalid saved request body in snapshot")
		}
		if tb.LocalFile != "" || strings.HasPrefix(tb.File, "local:") {
			return nil, errors.New("local file uploads are not supported in batch performance diagnostics")
		}
		if len(core.TemplateNames(tb.File)) > 0 {
			return nil, errors.New("request body file path must be fixed in the saved snapshot")
		}
		for _, part := range tb.MultipartParts {
			if part.LocalFile != "" || strings.HasPrefix(part.File, "local:") || len(core.TemplateNames(part.File)) > 0 {
				return nil, errors.New("multipart file uploads must use fixed project files in performance diagnostics")
			}
		}
	}

	network.CookieJar = nil
	network.LocalFiles = nil
	network.Secrets = secrets
	network.Interactive = false
	network.Confirmed = false
	network.ProjectFiles = files

	eng := diagnostics.New(p, diagnostics.Options{
		RequestRef:     requestRef,
		Environment:    env,
		Config:         cfg,
		Secrets:        secrets,
		ExecuteOptions: network,
		PlanOptions: core.PlanOptions{
			ProjectRoot:    p.Dir,
			ProjectFiles:   files,
			MaxUploadBytes: core.DefaultMaxUploadBytes,
		},
		Executor: executor,
		Emit:     emit,
	})
	_ = eng.Prepare(ctx)
	secrets.source = nil
	files.source = nil
	return eng, nil
}
