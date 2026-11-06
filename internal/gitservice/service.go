package gitservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultMaxOutputBytes    = 32 << 20 // 32 MiB of status/diff/command output
	defaultMaxDiffBytes      = 2 << 20  // 2 MiB per side shown in the native diff
	defaultMaxCommitFileSize = 64 << 20 // 64 MiB per file staged for a commit
	defaultMaxMessageBytes   = 100 << 10
	maxStderrBytes           = 256 << 10
)

// Options configures a Service. Zero values select safe defaults.
type Options struct {
	// GitPath overrides the git executable. Empty means "git" from PATH.
	GitPath string
	// MaxOutputBytes caps stdout per command. Truncation is an error, never a
	// silently partial parse.
	MaxOutputBytes int64
	// MaxDiffBytes caps each side of a native diff. Larger files report
	// TooLarge instead of being loaded.
	MaxDiffBytes int64
	// MaxCommitFileSize caps a file staged for commit.
	MaxCommitFileSize int64
}

// Repository is a discovered Git work tree.
type Repository struct {
	Root     string // absolute work-tree top level
	GitDir   string // absolute git directory
	Branch   string // current branch short name, empty when detached
	Detached bool
}

// Service runs Git operations for one application process. It creates an empty
// hooks directory that is injected through core.hooksPath for every command, so
// no repository hook can run. Call Close to remove it.
type Service struct {
	gitPath    string
	maxOutput  int64
	maxDiff    int64
	maxCommit  int64
	hooksDir   string
	closeOnce  sync.Once
	closeErr   error
	credOnce   sync.Once
	credHelper []string
}

// New resolves the git executable and prepares an isolated, empty hooks
// directory. It performs no repository access.
func New(opts Options) (*Service, error) {
	gitPath := strings.TrimSpace(opts.GitPath)
	if gitPath == "" {
		p, err := exec.LookPath("git")
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrGitUnavailable, err)
		}
		gitPath = p
	} else if !filepath.IsAbs(gitPath) && !strings.ContainsRune(gitPath, filepath.Separator) {
		if p, err := exec.LookPath(gitPath); err == nil {
			gitPath = p
		}
	}
	if _, err := os.Stat(gitPath); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGitUnavailable, err)
	}
	hooks, err := os.MkdirTemp("", "gorequest-git-hooks-*")
	if err != nil {
		// Fail closed: without a guaranteed-empty hooksPath we cannot promise
		// that opening a repository never executes its hooks.
		return nil, fmt.Errorf("prepare isolated git hooks directory: %w", err)
	}
	if opts.MaxOutputBytes <= 0 {
		opts.MaxOutputBytes = defaultMaxOutputBytes
	}
	if opts.MaxDiffBytes <= 0 {
		opts.MaxDiffBytes = defaultMaxDiffBytes
	}
	if opts.MaxCommitFileSize <= 0 {
		opts.MaxCommitFileSize = defaultMaxCommitFileSize
	}
	return &Service{
		gitPath:   gitPath,
		maxOutput: opts.MaxOutputBytes,
		maxDiff:   opts.MaxDiffBytes,
		maxCommit: opts.MaxCommitFileSize,
		hooksDir:  hooks,
	}, nil
}

// Close releases the temporary hooks directory. It is safe to call twice.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.hooksDir != "" {
			s.closeErr = os.RemoveAll(s.hooksDir)
		}
	})
	return s.closeErr
}

// GitPath returns the resolved executable path (useful for diagnostics).
func (s *Service) GitPath() string {
	if s == nil {
		return ""
	}
	return s.gitPath
}

// Discover resolves the work tree containing startDir. It only reads
// repository metadata and never runs hooks or configured commands.
func (s *Service) Discover(ctx context.Context, startDir string) (*Repository, error) {
	if s == nil {
		return nil, ErrGitUnavailable
	}
	if startDir == "" {
		startDir = "."
	}
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return nil, fmt.Errorf("resolve project directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve project directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrNotRepository, abs)
	}
	res, err := s.run(ctx, abs, runOpts{
		op:   "rev-parse",
		args: []string{"rev-parse", "--show-toplevel", "--absolute-git-dir", "--is-inside-work-tree"},
	})
	if err != nil {
		if isCanceled(ctx, err) {
			return nil, canceledErr(ctx, err)
		}
		var ce *CommandError
		if errors.As(err, &ce) {
			lower := strings.ToLower(ce.Stderr)
			if strings.Contains(lower, "not a git repository") ||
				strings.Contains(lower, "not a git repo") ||
				strings.Contains(lower, "outside repository") {
				return nil, fmt.Errorf("%w: %s", ErrNotRepository, abs)
			}
		}
		return nil, err
	}
	lines := splitNonEmptyLines(string(res.stdout))
	if len(lines) < 3 {
		return nil, fmt.Errorf("%w: unexpected rev-parse output", ErrNotRepository)
	}
	if strings.TrimSpace(lines[2]) != "true" {
		return nil, fmt.Errorf("%w: %s", ErrNotWorkTree, abs)
	}
	root := canonicalDir(filepath.Clean(strings.TrimSpace(lines[0])))
	gitDir := canonicalDir(filepath.Clean(strings.TrimSpace(lines[1])))
	repo := &Repository{Root: root, GitDir: gitDir}
	if name, err := s.currentBranch(ctx, root); err == nil && name != "" {
		repo.Branch = name
	} else if errors.Is(err, ErrDetachedHead) {
		repo.Detached = true
	} else if err != nil && !isExitStatus(err) {
		return nil, err
	}
	return repo, nil
}

// CurrentBranch returns the current branch short name or ErrDetachedHead.
func (s *Service) CurrentBranch(ctx context.Context, repoRoot string) (string, error) {
	return s.currentBranch(ctx, repoRoot)
}

func (s *Service) currentBranch(ctx context.Context, repoRoot string) (string, error) {
	res, err := s.run(ctx, repoRoot, runOpts{
		op:   "symbolic-ref",
		args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"},
	})
	if err != nil {
		if isCanceled(ctx, err) {
			return "", canceledErr(ctx, err)
		}
		var ce *CommandError
		if errors.As(err, &ce) && ce.ExitCode == 1 && strings.TrimSpace(ce.Stderr) == "" {
			return "", ErrDetachedHead
		}
		return "", err
	}
	name := strings.TrimSpace(string(res.stdout))
	if name == "" {
		return "", ErrDetachedHead
	}
	return name, nil
}

// runOpts describes one bounded git invocation.
type runOpts struct {
	op       string
	args     []string
	env      []string // extra environment (for example GIT_INDEX_FILE)
	stdin    io.Reader
	maxOut   int64
	maxErr   int64
	repoRoot string // enables the core.worktree override
	withCred bool   // network operations may load the user's global credential helpers
	noCred   bool   // config probe: do not load credential helpers recursively
}

type runResult struct {
	stdout    []byte
	stderr    []byte
	exitCode  int
	truncated bool
}

// run executes git with the fixed safety policy. The command's working
// directory is dir, all arguments are passed verbatim, and the process is
// killed as a group on cancellation.
func (s *Service) run(ctx context.Context, dir string, o runOpts) (runResult, error) {
	if s == nil {
		return runResult{}, ErrGitUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if o.maxOut <= 0 {
		o.maxOut = s.maxOutput
	}
	if o.maxErr <= 0 {
		o.maxErr = maxStderrBytes
	}
	outBuf := &limitedBuffer{limit: o.maxOut}
	errBuf := &limitedBuffer{limit: o.maxErr}
	args := s.globalArgs(o.repoRoot, o.withCred && !o.noCred)
	args = append(args, o.args...)
	cmd := exec.CommandContext(ctx, s.gitPath, args...)
	cmd.Dir = dir
	cmd.Env = s.env(o.env...)
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	cmd.Stdin = o.stdin
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	setProcessGroup(cmd)
	err := cmd.Run()
	res := runResult{
		stdout:    outBuf.Bytes(),
		stderr:    errBuf.Bytes(),
		truncated: outBuf.truncated || errBuf.truncated,
	}
	if cmd.ProcessState != nil {
		res.exitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return res, canceledErr(ctx, err)
	}
	if err != nil {
		if outBuf.truncated || errBuf.truncated {
			return res, ErrOutputTooLarge
		}
		exitCode := res.exitCode
		if exitCode == 0 {
			exitCode = -1
		}
		return res, &CommandError{Op: o.op, ExitCode: exitCode, Stderr: diagnosticText(res.stderr), Err: err}
	}
	if outBuf.truncated || errBuf.truncated {
		return res, ErrOutputTooLarge
	}
	return res, nil
}

// globalArgs is the fixed command-line policy. Command-line -c values take
// priority over repository config, so a repository cannot re-enable hooks,
// fsmonitor commands, signing programs, remote pack programs or its own
// credential helpers. Global credential helpers are re-added explicitly after
// the reset value (see credentialHelpers).
func (s *Service) globalArgs(repoRoot string, withCredentialHelpers bool) []string {
	args := []string{
		"--no-optional-locks",
		"--literal-pathspecs",
		"-c", "core.hooksPath=" + s.hooksDir,
		"-c", "core.fsmonitor=false",
		"-c", "core.sshCommand=ssh",
		"-c", "commit.gpgSign=false",
		"-c", "protocol.ext.allow=never",
		"-c", "credential.helper=",
	}
	if withCredentialHelpers {
		for _, h := range s.credentialHelpers() {
			args = append(args, "-c", "credential.helper="+h)
		}
	}
	if repoRoot != "" {
		// Repository config cannot redirect file operations to another work tree.
		args = append(args, "-c", "core.worktree="+repoRoot)
	}
	return args
}

var droppedGitEnv = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_INDEX_FILE":                   true,
	"GIT_PREFIX":                       true,
	"GIT_COMMON_DIR":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_CEILING_DIRECTORIES":          true,
	"GIT_CONFIG":                       true,
	"GIT_CONFIG_PARAMETERS":            true,
	"GIT_CONFIG_COUNT":                 true,
	"GIT_SSH":                          true,
	"GIT_SSH_COMMAND":                  true,
	"GIT_ASKPASS":                      true,
	"SSH_ASKPASS":                      true,
	"GIT_ALLOW_PROTOCOL":               true,
	"GIT_TERMINAL_PROMPT":              true,
	"GIT_OPTIONAL_LOCKS":               true,
}

// env builds the child environment: inherited user settings (identity,
// HTTP(S) proxy, ssh-agent socket) are kept, while repository/worktree
// injection and interactive helper overrides are replaced.
func (s *Service) env(extra ...string) []string {
	base := make([]string, 0, len(os.Environ())+len(extra)+8)
	for _, entry := range os.Environ() {
		key := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			key = entry[:i]
		}
		if droppedGitEnv[key] || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		base = append(base, entry)
	}
	base = append(base,
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"PAGER=cat",
		// BatchMode prevents ssh from prompting for passwords or host keys on a
		// GUI process that has no terminal to answer them.
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes",
		// Only encrypted, agent-authenticated or local transports are allowed;
		// ext:: (arbitrary command execution) is always denied.
		"GIT_ALLOW_PROTOCOL=https:ssh:file",
	)
	base = append(base, extra...)
	return base
}

// credentialHelpers returns helpers configured in the user's global Git
// config. Repository-local helpers are deliberately ignored: a repository
// cannot choose a command for this process to run. Reading the global config
// does not execute anything.
func (s *Service) credentialHelpers() []string {
	s.credOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := s.run(ctx, "", runOpts{
			op:     "config",
			args:   []string{"config", "--global", "--get-all", "credential.helper"},
			noCred: true,
		})
		if err != nil {
			return
		}
		for _, line := range splitNonEmptyLines(string(res.stdout)) {
			line = strings.TrimSpace(line)
			if line != "" {
				s.credHelper = append(s.credHelper, line)
			}
		}
	})
	return s.credHelper
}

// limitedBuffer keeps at most limit bytes and records that it truncated.
type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remain := b.limit - int64(b.buf.Len())
	if remain <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remain {
		b.buf.Write(p[:remain])
		b.truncated = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

func diagnosticText(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 4000 {
		s = s[:4000] + "…"
	}
	return s
}

func splitNonEmptyLines(s string) []string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func canceledErr(ctx context.Context, cause error) error {
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: deadline exceeded", ErrCanceled)
	}
	if cause != nil {
		return fmt.Errorf("%w: %v", ErrCanceled, cause)
	}
	return ErrCanceled
}

func isCanceled(ctx context.Context, err error) bool {
	return errors.Is(err, ErrCanceled) || (ctx != nil && ctx.Err() != nil)
}

func isExitStatus(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

var remoteNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validRemoteName accepts the remote names Git itself creates and the special
// "." remote, which refers to the local repository.
func validRemoteName(name string) bool {
	if name == "." {
		return true
	}
	return remoteNameRE.MatchString(name)
}

var refNameRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// validRefName rejects leading dashes and path tricks before a ref becomes an
// argument to push/fetch.
func validRefName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.Contains(name, "..") ||
		strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return false
	}
	return refNameRE.MatchString(name)
}

// CleanPath validates a repository-relative path and returns its slash form.
// Absolute paths, parent escapes, NUL bytes and .git internals are rejected.
func CleanPath(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: empty path", ErrUnsupportedPath)
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: absolute path %q", ErrUnsupportedPath, p)
	}
	slashed := filepath.ToSlash(p)
	clean := path.Clean(slashed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("%w: escapes repository %q", ErrUnsupportedPath, p)
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", fmt.Errorf("%w: .git internals %q", ErrUnsupportedPath, p)
	}
	return clean, nil
}

// cleanPaths normalizes and de-duplicates selected paths while preserving the
// caller's order.
func cleanPaths(paths []string) ([]string, error) {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		clean, err := CleanPath(p)
		if err != nil {
			return nil, err
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out, nil
}

// canonicalDir resolves macOS /var -> /private/var style aliases so status,
// discovered repository roots and the desktop project root compare equal.
func canonicalDir(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func worktreePath(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}

// isBinary marks content the native text diff cannot meaningfully render.
func isBinary(b []byte) bool {
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	for _, c := range head {
		if c == 0 {
			return true
		}
	}
	return !utf8.Valid(head)
}
