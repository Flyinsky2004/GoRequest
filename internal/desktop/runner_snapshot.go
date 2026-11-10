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
	"gorequest/internal/protocolrun/runneradapter"
	"gorequest/internal/runner"
)

// Preparation runs on the initiating UI thread. Providers are sealed before
// launching workers: no worker can call back into UI/device settings objects.
type runnerSecrets struct {
	source core.SecretProvider
	values core.MapSecrets
}

func (s *runnerSecrets) Get(name string) (string, bool) {
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
func (s *runnerSecrets) SecretNames() []string {
	names := make([]string, 0, len(s.values))
	for n := range s.values {
		names = append(names, n)
	}
	return names
}

type runnerFile struct {
	bytes []byte
	name  string
}
type runnerFiles struct {
	source core.ProjectFileOpener
	files  map[string]runnerFile
	total  int64
}

func (s *runnerFiles) OpenProjectFile(ctx context.Context, rel string) (*core.OpenedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, ok := s.files[rel]
	if !ok {
		if s.source == nil {
			return nil, &core.ConfigError{Err: errors.New("file was not included in saved runner snapshot")}
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
			return nil, &core.ConfigError{Err: errors.New("runner file snapshot exceeds 64 MiB")}
		}
		f = runnerFile{b, opened.Name}
		s.files[rel] = f
	}
	return &core.OpenedFile{Reader: io.NopCloser(bytes.NewReader(f.bytes)), Name: f.name, Size: int64(len(f.bytes))}, nil
}

// freezeRunnerTLS preserves B04's reference boundary while loading fixed PEM
// bytes. mTLS keys must still be explicit local slots. No path goes in reports.
func freezeRunnerTLS(ctx context.Context, opts *core.ExecuteOptions, files *runnerFiles) error {
	load := func(ref string, key bool) ([]byte, error) {
		parsed, err := core.ParseFileRef(ref)
		if err != nil {
			return nil, err
		}
		if key && parsed.Kind != core.FileRefLocal {
			return nil, errors.New("runner mTLS key must use a local slot")
		}
		var opened *core.OpenedFile
		if parsed.Kind == core.FileRefProject {
			opened, err = files.OpenProjectFile(ctx, parsed.Path)
		} else if parsed.Kind == core.FileRefLocal && opts.LocalFiles != nil {
			name, ok := opts.LocalFiles.ResolveLocalFile(parsed.Slot)
			if !ok {
				return nil, errors.New("runner TLS slot is not bound")
			}
			f, e := os.Open(name)
			if e != nil {
				return nil, errors.New("runner TLS slot is unreadable")
			}
			info, e := f.Stat()
			if e != nil || !info.Mode().IsRegular() {
				f.Close()
				return nil, errors.New("runner TLS slot must be a regular file")
			}
			opened = &core.OpenedFile{Reader: f, Name: filepath.Base(name)}
		} else {
			return nil, errors.New("runner TLS reference is unavailable")
		}
		if err != nil {
			return nil, err
		}
		defer opened.Reader.Close()
		b, err := io.ReadAll(io.LimitReader(opened.Reader, 8<<20+1))
		if err != nil || len(b) > 8<<20 {
			return nil, errors.New("runner TLS material exceeds limit")
		}
		return b, nil
	}
	for _, item := range []struct {
		ref *string
		pem *[]byte
		key bool
	}{{&opts.TLS.CustomCARef, &opts.TLS.CustomCAPEM, false}, {&opts.TLS.ClientCertRef, &opts.TLS.ClientCertPEM, false}, {&opts.TLS.ClientKeyRef, &opts.TLS.ClientKeyPEM, true}} {
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

// desktopRunnerOptions carries the frozen per-run knobs decided by the UI
// thread. OAuth is the project-isolated service; the adapter seals it into a
// non-interactive runner.AuthProvider.
type desktopRunnerOptions struct {
	StepConcurrency int
	RowConcurrency  int
	OAuth           *core.OAuthService
}

// prepareDesktopRunner accepts only saved objects owned by this run. The
// returned Runner and all its options are independent of App/projectState/Tab.
func prepareDesktopRunner(ctx context.Context, p *core.Project, suite *runner.Suite, env string, network core.ExecuteOptions, source core.SecretProvider, emit runner.Emitter, ro desktopRunnerOptions) (*runner.Runner, error) {
	secrets := &runnerSecrets{source: source, values: core.MapSecrets{}}
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
	// Fetch all declared/template names before preflight can fail, so even config
	// reports get the same secret masking as successful runs.
	b, err := yaml.Marshal(struct {
		Requests map[string]*core.RequestEntry
		Envs     map[string]*core.Environment
		Suite    *runner.Suite
		Auth     map[string]*core.AuthScheme
	}{p.Requests, p.Environments, suite, p.AuthSchemes})
	if err != nil {
		return nil, errors.New("cannot capture runner definitions")
	}
	for _, name := range core.TemplateNames(string(b)) {
		if strings.HasPrefix(name, "secret.") {
			secrets.Get(strings.TrimPrefix(name, "secret."))
		}
	}
	for _, e := range p.Environments {
		for _, name := range e.Secrets {
			secrets.Get(name)
		}
	}
	if network.Proxy.PasswordSecret != "" {
		secrets.Get(network.Proxy.PasswordSecret)
	}
	files := &runnerFiles{source: core.NewDirFileOpener(p.Dir), files: map[string]runnerFile{}}
	if err := freezeRunnerTLS(ctx, &network, files); err != nil {
		return nil, errors.New("runner TLS snapshot failed: check configured CA/certificate/key slots")
	}
	// File-backed local uploads have no immutable byte port in Core. Reject them
	// in this first UI phase instead of following mutable device mappings later.
	for _, step := range suite.Steps {
		var entry *core.RequestEntry
		for _, e := range p.Requests {
			if e.RelPath == step.Request || e.Request.ID == step.Request {
				entry = e
				break
			}
		}
		if entry == nil || entry.Request.HTTP == nil || entry.Request.HTTP.Body == nil {
			continue
		}
		body := entry.Request.HTTP.Body
		tb, e := core.InspectBody(body)
		if e != nil {
			return nil, errors.New("invalid saved runner body")
		}
		if tb.LocalFile != "" || strings.HasPrefix(tb.File, "local:") {
			return nil, errors.New("runner local file uploads are not supported in this phase")
		}
		if len(core.TemplateNames(tb.File)) > 0 {
			return nil, errors.New("runner file names must be fixed in the saved snapshot")
		}
		for _, part := range tb.MultipartParts {
			if part.LocalFile != "" || strings.HasPrefix(part.File, "local:") || len(core.TemplateNames(part.File)) > 0 {
				return nil, errors.New("runner multipart file names must use fixed project files")
			}
		}
	}
	network.CookieJar = nil
	network.LocalFiles = nil
	network.Secrets = secrets
	network.Interactive = false
	network.Confirmed = false
	network.ProjectFiles = files
	// The desktop keeps the historical sequential default when the caller does
	// not pass an explicit value. ValidateConcurrency rejects out-of-range input
	// supplied by the UI instead of silently clamping it.
	if ro.StepConcurrency == 0 {
		ro.StepConcurrency = 1
	}
	if ro.RowConcurrency == 0 {
		ro.RowConcurrency = 1
	}
	if err := runner.ValidateConcurrency(ro.StepConcurrency, ro.RowConcurrency); err != nil {
		return nil, err
	}
	opts := runner.Options{Protocol: runneradapter.New(), Environment: env, Secrets: secrets, ExecuteOptions: network, PlanOptions: core.PlanOptions{ProjectRoot: p.Dir, ProjectFiles: files, MaxUploadBytes: core.DefaultMaxUploadBytes, AllowGraphQL: true}, Schema: runner.NewProjectSchemaAdapter(p), Emit: emit, StepConcurrency: ro.StepConcurrency, RowConcurrency: ro.RowConcurrency}
	if ro.OAuth != nil {
		slug := ""
		if p.File != nil {
			slug = p.File.ID
		}
		opts.Auth = newRunnerOAuthProvider(ro.OAuth, p.Dir, slug, env, secrets)
	}
	r := runner.New(p, suite, opts)
	// Run will return the cached, reportable configuration error. Sealing after
	// Prepare also prevents unknown/dynamic file references from reading disk.
	_ = r.Prepare(ctx)
	secrets.source = nil
	files.source = nil
	return r, nil
}
