package gitservice

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// StatusCode is one porcelain-v2 status character ('.', 'M', 'A', 'D', 'R',
// 'C', 'U', '?'). It is kept as a rune so unknown future codes still round-trip
// into diagnostics instead of being silently dropped.
type StatusCode rune

// Entry is one changed path reported by `git status --porcelain=v2`.
type Entry struct {
	Path      string
	OrigPath  string // rename/copy source when known
	Index     StatusCode
	Worktree  StatusCode
	Untracked bool
	Unmerged  bool
}

// Staged reports whether the index differs from HEAD for this path.
func (e Entry) Staged() bool {
	return !e.Untracked && !e.Unmerged && e.Index != '.' && e.Index != 0
}

// HasWorktreeChange reports whether the working tree differs from the index (or
// the path is untracked/unmerged).
func (e Entry) HasWorktreeChange() bool {
	if e.Untracked || e.Unmerged {
		return true
	}
	return e.Worktree != '.' && e.Worktree != 0
}

// Marker returns the single-letter badge shown in the Git panel.
func (e Entry) Marker() string {
	switch {
	case e.Unmerged:
		return "U"
	case e.Untracked:
		return "?"
	case e.Worktree != '.' && e.Worktree != 0:
		return string(e.Worktree)
	case e.Index != '.' && e.Index != 0:
		return string(e.Index)
	default:
		return " "
	}
}

// Status is one bounded snapshot of repository state.
type Status struct {
	Branch   string
	Upstream string
	HeadSHA  string
	Ahead    int
	Behind   int
	Detached bool
	Unborn   bool
	Entries  []Entry
}

// StagedCount counts entries whose index differs from HEAD.
func (s *Status) StagedCount() int {
	if s == nil {
		return 0
	}
	n := 0
	for _, e := range s.Entries {
		if e.Staged() || e.Unmerged {
			n++
		}
	}
	return n
}

// Dirty reports whether a pull/push must refuse because tracked changes exist.
// Untracked files do not by themselves block a fetch or fast-forward, but the
// UI still lists them.
func (s *Status) Dirty() bool {
	if s == nil {
		return false
	}
	for _, e := range s.Entries {
		if !e.Untracked {
			return true
		}
	}
	return false
}

// FirstDirtyPath names the first non-untracked change for diagnostics.
func (s *Status) FirstDirtyPath() string {
	if s == nil {
		return ""
	}
	for _, e := range s.Entries {
		if !e.Untracked {
			return e.Path
		}
	}
	return ""
}

// Status reads the repository state. Untracked files are included; ignored
// files are not. The command only reads state and never runs hooks.
func (s *Service) Status(ctx context.Context, repoRoot string) (*Status, error) {
	if s == nil {
		return nil, ErrGitUnavailable
	}
	root, err := s.requireRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	res, err := s.run(ctx, root, runOpts{
		op:       "status",
		repoRoot: root,
		args: []string{
			"status", "--porcelain=v2", "--branch", "-z",
			"--untracked-files=all", "--ignore-submodules=all",
		},
	})
	if err != nil {
		return nil, err
	}
	st, err := parsePorcelainV2(res.stdout)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(st.Entries, func(i, j int) bool { return st.Entries[i].Path < st.Entries[j].Path })
	return st, nil
}

func parsePorcelainV2(out []byte) (*Status, error) {
	st := &Status{}
	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens); i++ {
		rec := tokens[i]
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '#':
			parseBranchHeader(st, rec)
		case '1':
			fields := strings.SplitN(rec, " ", 9)
			if len(fields) != 9 {
				return nil, fmt.Errorf("parse git status record %q", rec)
			}
			xy, err := parseXY(fields[1])
			if err != nil {
				return nil, err
			}
			st.Entries = append(st.Entries, Entry{Path: fields[8], Index: xy[0], Worktree: xy[1]})
		case '2':
			fields := strings.SplitN(rec, " ", 10)
			if len(fields) != 10 {
				return nil, fmt.Errorf("parse git status rename record %q", rec)
			}
			xy, err := parseXY(fields[1])
			if err != nil {
				return nil, err
			}
			orig := ""
			if i+1 < len(tokens) {
				orig = tokens[i+1]
				i++
			}
			st.Entries = append(st.Entries, Entry{Path: fields[9], OrigPath: orig, Index: xy[0], Worktree: xy[1]})
		case 'u':
			fields := strings.SplitN(rec, " ", 11)
			if len(fields) != 11 {
				return nil, fmt.Errorf("parse git status unmerged record %q", rec)
			}
			xy, err := parseXY(fields[1])
			if err != nil {
				return nil, err
			}
			st.Entries = append(st.Entries, Entry{Path: fields[10], Index: xy[0], Worktree: xy[1], Unmerged: true})
		case '?':
			path := strings.TrimPrefix(rec, "? ")
			if path != "" {
				st.Entries = append(st.Entries, Entry{Path: path, Untracked: true, Index: '?', Worktree: '?'})
			}
		case '!':
			// Ignored entries are not requested; tolerate them if present.
		default:
			// Unknown record kinds are rejected rather than guessed.
			return nil, fmt.Errorf("unknown git status record %q", rec)
		}
	}
	return st, nil
}

func parseBranchHeader(st *Status, rec string) {
	fields := strings.Fields(rec)
	if len(fields) < 3 {
		return
	}
	key, value := fields[1], fields[2]
	switch key {
	case "branch.oid":
		if value == "(initial)" {
			st.Unborn = true
			return
		}
		st.HeadSHA = value
	case "branch.head":
		if value == "(detached)" {
			st.Detached = true
			return
		}
		st.Branch = value
	case "branch.upstream":
		st.Upstream = value
	case "branch.ab":
		for _, f := range fields[2:] {
			if strings.HasPrefix(f, "+") {
				if n, err := strconv.Atoi(strings.TrimPrefix(f, "+")); err == nil {
					st.Ahead = n
				}
			}
			if strings.HasPrefix(f, "-") {
				if n, err := strconv.Atoi(strings.TrimPrefix(f, "-")); err == nil {
					st.Behind = n
				}
			}
		}
	}
}

func parseXY(xy string) ([2]StatusCode, error) {
	if len(xy) != 2 {
		return [2]StatusCode{}, fmt.Errorf("invalid git status XY %q", xy)
	}
	return [2]StatusCode{StatusCode(xy[0]), StatusCode(xy[1])}, nil
}

func (s *Service) requireRoot(repoRoot string) (string, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return "", fmt.Errorf("%w: empty repository root", ErrNotRepository)
	}
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	return canonicalDir(abs), nil
}
