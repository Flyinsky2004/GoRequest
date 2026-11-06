// Package gitservice implements GoRequest's Git workflow over the system Git
// CLI. It is pure Go (no GUI, no MyGo) and is safe for Core-adjacent use: every
// command is invoked with an explicit argument vector (never a shell), accepts
// a cancellation context, runs with repository hooks and repo-configured
// helper commands disabled, and is bounded in output size.
//
// The service deliberately supports only the operations the B07 card needs:
// repository discovery, status, native diff inputs, selective commit, a
// fast-forward-only pull and a never-force push. It does not implement branch
// switching, merge conflict resolution, rebase, reset, force push or any
// command that would discard user work.
package gitservice

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrGitUnavailable means no usable git executable was found.
	ErrGitUnavailable = errors.New("git executable is not available")
	// ErrNotRepository means the target directory is not inside a Git work tree.
	ErrNotRepository = errors.New("not a git repository")
	// ErrNotWorkTree means a Git directory was found but it has no work tree
	// (a bare repository), which this service cannot operate on.
	ErrNotWorkTree = errors.New("git repository has no work tree")
	// ErrNoUpstream means the current branch has no configured upstream, so
	// pull/push cannot choose a remote without guessing.
	ErrNoUpstream = errors.New("current branch has no upstream")
	// ErrDetachedHead means the current checkout is detached.
	ErrDetachedHead = errors.New("HEAD is detached")
	// ErrDirtyWorkingTree means pull/push refused because tracked or staged
	// changes exist. The service never stashes, resets or discards them.
	ErrDirtyWorkingTree = errors.New("working tree has uncommitted changes")
	// ErrDiverged means local and remote branches both advanced, so a
	// fast-forward-only pull cannot continue without a manual merge.
	ErrDiverged = errors.New("local and remote branches have diverged")
	// ErrNoChanges means the selected files would produce the same tree as HEAD.
	ErrNoChanges = errors.New("selected files contain no changes")
	// ErrUnmerged means the selected path has unresolved merge conflict stages.
	ErrUnmerged = errors.New("path has unresolved merge conflicts")
	// ErrCanceled means the caller canceled the operation.
	ErrCanceled = errors.New("git operation canceled")
	// ErrOutputTooLarge means a command produced more output than the configured
	// cap; the result is rejected instead of silently parsed from a truncation.
	ErrOutputTooLarge = errors.New("git command output exceeds the configured limit")
	// ErrUnsupportedPath means a path failed repository-relative validation.
	ErrUnsupportedPath = errors.New("unsupported repository path")
)

// CommandError reports a failed git invocation with its safe diagnostic
// context. Arguments and environment are intentionally not included so tokens
// embedded in remote URLs or environment values never reach logs or the UI.
type CommandError struct {
	Op       string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *CommandError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = "git command failed"
	}
	if e.Op != "" {
		return fmt.Sprintf("git %s: %s", e.Op, msg)
	}
	return "git: " + msg
}

func (e *CommandError) Unwrap() error { return e.Err }

// DirtyTreeError carries the first path that made a pull/push refuse, so the
// UI can tell the user what to save, commit or discard themselves.
type DirtyTreeError struct {
	Path string
}

func (e *DirtyTreeError) Error() string {
	if e.Path == "" {
		return ErrDirtyWorkingTree.Error()
	}
	return fmt.Sprintf("%s: %s", ErrDirtyWorkingTree.Error(), e.Path)
}

func (e *DirtyTreeError) Unwrap() error { return ErrDirtyWorkingTree }

// NoUpstreamError includes the current branch name when one is known.
type NoUpstreamError struct {
	Branch string
}

func (e *NoUpstreamError) Error() string {
	if e.Branch == "" {
		return ErrNoUpstream.Error()
	}
	return fmt.Sprintf("%s: %s", ErrNoUpstream.Error(), e.Branch)
}

func (e *NoUpstreamError) Unwrap() error { return ErrNoUpstream }

// NoChangesError lists the paths that were selected but would not change HEAD.
type NoChangesError struct {
	Paths []string
}

func (e *NoChangesError) Error() string {
	return fmt.Sprintf("%s (%s)", ErrNoChanges.Error(), strings.Join(e.Paths, ", "))
}

func (e *NoChangesError) Unwrap() error { return ErrNoChanges }
