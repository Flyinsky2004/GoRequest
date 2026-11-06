package gitservice

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// RemoteTarget describes the configured upstream of the current branch.
type RemoteTarget struct {
	Branch    string
	Remote    string
	RemoteRef string // for example refs/heads/main
	URL       string // credential-free display URL
}

// PullResult reports a fast-forward-only pull.
type PullResult struct {
	Target   RemoteTarget
	Updated  bool
	OldHead  string
	NewHead  string
	Ahead    int
	Behind   int
	UpToDate bool
}

// PushResult reports a push. Output is the porcelain summary, already free of
// environment data.
type PushResult struct {
	Target  RemoteTarget
	Output  string
	Updated bool
}

// PullTarget resolves the current branch's upstream without touching the
// network.
func (s *Service) PullTarget(ctx context.Context, repoRoot string) (*RemoteTarget, error) {
	return s.remoteTarget(ctx, repoRoot)
}

// PushTarget resolves the current branch's upstream without touching the
// network.
func (s *Service) PushTarget(ctx context.Context, repoRoot string) (*RemoteTarget, error) {
	return s.remoteTarget(ctx, repoRoot)
}

func (s *Service) remoteTarget(ctx context.Context, repoRoot string) (*RemoteTarget, error) {
	if s == nil {
		return nil, ErrGitUnavailable
	}
	root, err := s.requireRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	branch, err := s.currentBranch(ctx, root)
	if err != nil {
		return nil, err
	}

	remote, err := s.configValue(ctx, root, "branch."+branch+".remote")
	if err != nil {
		return nil, err
	}
	mergeRef, err := s.configValue(ctx, root, "branch."+branch+".merge")
	if err != nil {
		return nil, err
	}
	if remote == "" || mergeRef == "" {
		return nil, &NoUpstreamError{Branch: branch}
	}
	if !validRemoteName(remote) {
		return nil, fmt.Errorf("unsupported remote name %q", remote)
	}
	if remote == "." {
		return nil, fmt.Errorf("local (.) upstream is not supported by pull/push")
	}
	if !validRefName(mergeRef) || !strings.HasPrefix(mergeRef, "refs/heads/") {
		return nil, fmt.Errorf("unsupported upstream ref %q", mergeRef)
	}
	target := &RemoteTarget{Branch: branch, Remote: remote, RemoteRef: mergeRef}
	if u, err := s.remoteURL(ctx, root, remote); err == nil {
		target.URL = sanitizeRemoteURL(u)
	}
	return target, nil
}

func (s *Service) configValue(ctx context.Context, root, key string) (string, error) {
	res, err := s.run(ctx, root, runOpts{
		op: "config", repoRoot: root,
		args: []string{"config", "--get", key},
	})
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) && ce.ExitCode == 1 && strings.TrimSpace(ce.Stderr) == "" {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(res.stdout)), nil
}

func (s *Service) remoteURL(ctx context.Context, root, remote string) (string, error) {
	res, err := s.run(ctx, root, runOpts{
		op: "remote", repoRoot: root,
		args: []string{"remote", "get-url", "--push", remote},
	})
	if err != nil {
		return "", err
	}
	lines := splitNonEmptyLines(string(res.stdout))
	if len(lines) == 0 {
		return "", nil
	}
	return strings.TrimSpace(lines[0]), nil
}

// sanitizeRemoteURL removes credentials embedded in http(s) URLs so the panel
// and diagnostics never echo a token.
func sanitizeRemoteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw
	}
	rest := raw[i+3:]
	end := strings.IndexAny(rest, "/?#")
	authority := rest
	tail := ""
	if end >= 0 {
		authority, tail = rest[:end], rest[end:]
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = "***" + authority[at:]
	}
	return raw[:i+3] + authority + tail
}

// Pull fetches the upstream and fast-forwards only. It refuses when tracked
// changes exist or when the branches diverged; it never stashes, resets,
// rebases or resolves conflicts.
func (s *Service) Pull(ctx context.Context, repoRoot string) (*PullResult, error) {
	target, err := s.remoteTarget(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	root, _ := s.requireRoot(repoRoot)

	st, err := s.Status(ctx, root)
	if err != nil {
		return nil, err
	}
	if st.Dirty() {
		return nil, &DirtyTreeError{Path: st.FirstDirtyPath()}
	}
	if st.Detached {
		return nil, ErrDetachedHead
	}

	oldHead, hasHead, err := s.revParseVerify(ctx, root, "HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	if !hasHead {
		return nil, fmt.Errorf("cannot pull into a branch without commits")
	}

	if _, err := s.run(ctx, root, runOpts{
		op: "fetch", repoRoot: root, withCred: true,
		args: append(remoteSafetyArgs(target.Remote),
			"fetch", "--no-tags", "--no-prune", target.Remote),
	}); err != nil {
		return nil, err
	}

	remoteCommit, ok, err := s.revParseVerify(ctx, root, "@{upstream}^{commit}")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("upstream %s not found after fetch", target.Remote+"/"+strings.TrimPrefix(target.RemoteRef, "refs/heads/"))
	}

	ahead, behind, err := s.aheadBehind(ctx, root, remoteCommit)
	if err != nil {
		return nil, err
	}
	res := &PullResult{Target: *target, OldHead: oldHead, NewHead: oldHead, Ahead: ahead, Behind: behind}
	switch {
	case behind == 0:
		res.UpToDate = true
		return res, nil
	case ahead > 0:
		return res, ErrDiverged
	}

	if _, err := s.run(ctx, root, runOpts{
		op: "merge", repoRoot: root,
		args: []string{"-c", "merge.autoStash=false", "merge", "--ff-only", remoteCommit},
	}); err != nil {
		return res, err
	}
	newHead, _, err := s.revParseVerify(ctx, root, "HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	res.NewHead = newHead
	res.Updated = true
	return res, nil
}

// Push pushes the current branch to its upstream with an explicit refspec and
// never passes a force option. Repository hooks are disabled by the fixed
// command policy.
func (s *Service) Push(ctx context.Context, repoRoot string) (*PushResult, error) {
	target, err := s.remoteTarget(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	root, _ := s.requireRoot(repoRoot)
	res, err := s.run(ctx, root, runOpts{
		op: "push", repoRoot: root, withCred: true,
		args: append(remoteSafetyArgs(target.Remote),
			"-c", "push.followTags=false",
			"-c", "push.gpgSign=false",
			"push", "--porcelain", "--no-follow-tags",
			target.Remote, "HEAD:"+target.RemoteRef),
	})
	out := strings.TrimSpace(string(res.stdout))
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) && out != "" {
			ce.Stderr = strings.TrimSpace(strings.Join([]string{ce.Stderr, out}, "\n"))
		}
		return nil, err
	}
	updated := false
	for _, line := range splitNonEmptyLines(out) {
		if strings.HasPrefix(line, "To ") || strings.TrimSpace(line) == "Done" ||
			strings.Contains(line, "Everything up-to-date") {
			continue
		}
		if strings.Contains(line, "refs/heads/") || strings.Contains(line, "HEAD:") ||
			strings.Contains(line, " -> ") || strings.Contains(line, " [new") {
			updated = true
		}
	}
	return &PushResult{Target: *target, Output: out, Updated: updated}, nil
}

// remoteSafetyArgs overrides repository-configurable programs for one remote.
// The standard git-upload-pack/git-receive-pack are used for local transports;
// ext:: URLs are denied by the global protocol policy.
func remoteSafetyArgs(remote string) []string {
	prefix := "remote." + remote + "."
	return []string{
		"-c", prefix + "uploadpack=git-upload-pack",
		"-c", prefix + "receivepack=git-receive-pack",
		"-c", prefix + "proxy=",
		"-c", "submodule.recurse=false",
	}
}

func (s *Service) aheadBehind(ctx context.Context, root, remoteCommit string) (ahead, behind int, err error) {
	res, err := s.run(ctx, root, runOpts{
		op: "rev-list", repoRoot: root,
		args: []string{"rev-list", "--left-right", "--count", "HEAD..." + remoteCommit},
	})
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(string(res.stdout))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("parse git rev-list counts %q", strings.TrimSpace(string(res.stdout)))
	}
	ahead, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parse ahead count: %w", err)
	}
	behind, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse behind count: %w", err)
	}
	return ahead, behind, nil
}
