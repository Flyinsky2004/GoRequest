package core

import (
	"context"
	"encoding/binary"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Default timing parameters for ProjectWatcher.
const (
	DefaultWatchPollInterval = 400 * time.Millisecond
	DefaultWatchDebounce     = 80 * time.Millisecond
	DefaultWatchMaxBatchWait = 500 * time.Millisecond
)

// WatchOptions configures polling, storm coalescing, and limits for a
// ProjectWatcher.
type WatchOptions struct {
	PollInterval   time.Duration
	DebounceWindow time.Duration
	MaxBatchWait   time.Duration
	Limits         StoreLimits
}

func (o WatchOptions) effective(store *Store) WatchOptions {
	out := o
	if out.PollInterval <= 0 {
		out.PollInterval = DefaultWatchPollInterval
	}
	if out.DebounceWindow <= 0 {
		out.DebounceWindow = DefaultWatchDebounce
	}
	if out.MaxBatchWait <= 0 {
		out.MaxBatchWait = DefaultWatchMaxBatchWait
	}
	if out.MaxBatchWait < out.DebounceWindow {
		out.MaxBatchWait = out.DebounceWindow * 2
	}
	if out.Limits == (StoreLimits{}) {
		out.Limits = store.EffectiveLimits()
	} else {
		out.Limits = out.Limits.Effective()
	}
	return out
}

// WatcherStats exposes counters for diagnostics and watch-storm verification.
type WatcherStats struct {
	Scans            uint64
	Triggers         uint64
	CoalescedBatches uint64
}

// ProjectWatcher monitors a .gorequest/ Store for external file modifications,
// creations, deletions, and renames. It accepts both periodic polling and
// explicit Trigger() hints (e.g. from OS watcher adapters), coalescing rapid
// write storms into settled notification batches without touching network or
// writing to disk.
type ProjectWatcher struct {
	store    *Store
	opts     WatchOptions
	onChange func()

	ctx       context.Context
	cancel    context.CancelFunc
	triggerCh chan struct{}
	doneCh    chan struct{}
	closeOnce sync.Once

	mu            sync.Mutex
	baselineToken string

	scans   atomic.Uint64
	trigs   atomic.Uint64
	batches atomic.Uint64
}

// StartWatcher creates and starts a background ProjectWatcher that calls
// onChange whenever a settled external filesystem change is detected under s.
// Call Close() to stop the background goroutine and release all resources.
func StartWatcher(parent context.Context, s *Store, opts WatchOptions, onChange func()) (*ProjectWatcher, error) {
	if s == nil {
		return nil, configErrorf("store is nil")
	}
	if parent == nil {
		parent = context.Background()
	}
	eff := opts.effective(s)
	initialToken, err := computeTreeToken(s, eff.Limits)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	w := &ProjectWatcher{
		store:         s,
		opts:          eff,
		onChange:      onChange,
		ctx:           ctx,
		cancel:        cancel,
		triggerCh:     make(chan struct{}, 1),
		doneCh:        make(chan struct{}),
		baselineToken: initialToken,
	}
	go w.loop()
	return w, nil
}

// Trigger nudges the watcher to inspect the filesystem immediately (coalesced
// through DebounceWindow). External OS event sources (FSEvents/inotify/ReadDirectoryChangesW)
// can call Trigger non-blockingly on raw events.
func (w *ProjectWatcher) Trigger() {
	if w == nil {
		return
	}
	w.trigs.Add(1)
	select {
	case w.triggerCh <- struct{}{}:
	default:
	}
}

// AcknowledgeCurrent updates the watcher's settled token to match the current
// on-disk tree so local saves recorded on the caller's thread do not fire a
// redundant notification.
func (w *ProjectWatcher) AcknowledgeCurrent() {
	if w == nil {
		return
	}
	tok, err := computeTreeToken(w.store, w.opts.Limits)
	if err != nil {
		return
	}
	w.mu.Lock()
	w.baselineToken = tok
	w.mu.Unlock()
}

// Stats returns a snapshot of the watcher's activity counters.
func (w *ProjectWatcher) Stats() WatcherStats {
	if w == nil {
		return WatcherStats{}
	}
	return WatcherStats{
		Scans:            w.scans.Load(),
		Triggers:         w.trigs.Load(),
		CoalescedBatches: w.batches.Load(),
	}
}

// Close stops the background watcher goroutine and waits for it to exit.
func (w *ProjectWatcher) Close() error {
	if w == nil {
		return nil
	}
	w.closeOnce.Do(func() {
		w.cancel()
		<-w.doneCh
	})
	return nil
}

func (w *ProjectWatcher) loop() {
	defer close(w.doneCh)

	pollTicker := time.NewTicker(w.opts.PollInterval)
	defer pollTicker.Stop()

	var (
		debounceTimer *time.Timer
		debounceCh    <-chan time.Time
		pendingToken  string
		batchStart    time.Time
	)
	stopDebounce := func() {
		if debounceTimer != nil {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceCh = nil
		}
	}
	defer stopDebounce()

	armDebounce := func(d time.Duration) {
		if debounceTimer == nil {
			debounceTimer = time.NewTimer(d)
		} else {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimer.Reset(d)
		}
		debounceCh = debounceTimer.C
	}

	inspect := func() {
		w.scans.Add(1)
		tok, err := computeTreeToken(w.store, w.opts.Limits)
		if err != nil {
			return
		}
		w.mu.Lock()
		baseTok := w.baselineToken
		w.mu.Unlock()

		if tok == baseTok {
			// Reverted back to baseline mid-burst (e.g. temp file cleaned up): cancel pending flush.
			pendingToken = ""
			batchStart = time.Time{}
			stopDebounce()
			return
		}
		now := time.Now()
		if batchStart.IsZero() {
			batchStart = now
			pendingToken = tok
			armDebounce(w.opts.DebounceWindow)
			return
		}
		if tok != pendingToken {
			pendingToken = tok
			// Keep extending quiet window while storm continues, bounded by MaxBatchWait.
			elapsed := now.Sub(batchStart)
			rem := w.opts.MaxBatchWait - elapsed
			if rem <= 0 {
				// Flush right away on MaxBatchWait ceiling.
				w.flushToken(tok)
				pendingToken = ""
				batchStart = time.Time{}
				stopDebounce()
				return
			}
			wait := w.opts.DebounceWindow
			if rem < wait {
				wait = rem
			}
			armDebounce(wait)
		}
	}

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-pollTicker.C:
			inspect()
		case <-w.triggerCh:
			inspect()
		case <-debounceCh:
			debounceCh = nil
			// Re-verify once on quiet timer expiration: if still changing and under
			// MaxBatchWait, wait one more short quiet window; otherwise flush.
			w.scans.Add(1)
			tok, err := computeTreeToken(w.store, w.opts.Limits)
			if err != nil {
				pendingToken = ""
				batchStart = time.Time{}
				continue
			}
			w.mu.Lock()
			baseTok := w.baselineToken
			w.mu.Unlock()
			if tok == baseTok {
				pendingToken = ""
				batchStart = time.Time{}
				continue
			}
			if tok != pendingToken && time.Since(batchStart) < w.opts.MaxBatchWait {
				pendingToken = tok
				wait := w.opts.DebounceWindow
				if rem := w.opts.MaxBatchWait - time.Since(batchStart); rem < wait {
					if rem <= 0 {
						w.flushToken(tok)
						pendingToken = ""
						batchStart = time.Time{}
						continue
					}
					wait = rem
				}
				armDebounce(wait)
				continue
			}
			w.flushToken(tok)
			pendingToken = ""
			batchStart = time.Time{}
		}
	}
}

func (w *ProjectWatcher) flushToken(tok string) {
	w.mu.Lock()
	if tok == w.baselineToken {
		w.mu.Unlock()
		return
	}
	w.baselineToken = tok
	w.mu.Unlock()

	w.batches.Add(1)
	if w.onChange != nil && w.ctx.Err() == nil {
		w.onChange()
	}
}

// computeTreeToken hashes the sorted paths and content digests of all tracked
// files under s.Root (respecting StoreLimits and ignoring temp/swap files).
// Reading bounded content digests directly avoids false negatives when multiple
// writes occur within coarse filesystem mtime granularity.
func computeTreeToken(s *Store, limits StoreLimits) (string, error) {
	lim := limits.Effective()
	type item struct {
		rel    string
		digest string
	}
	var items []item
	count := 0
	err := filepath.WalkDir(s.Root, func(absPath string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if absPath == s.Root {
			return nil
		}
		name := d.Name()
		if IsTempOrIgnoredName(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relOS, rerr := filepath.Rel(s.Root, absPath)
		if rerr != nil {
			return rerr
		}
		rel := filepath.ToSlash(relOS)
		if d.Type()&os.ModeSymlink != 0 {
			if _, serr := s.resolve(rel); serr != nil {
				items = append(items, item{rel: rel, digest: "symlink-err"})
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			if strings.HasPrefix(name, ".gorequest-") || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		kind, ok := classifyProjectFile(rel)
		if !ok {
			return nil
		}
		count++
		if count > lim.MaxTrackedFiles {
			items = append(items, item{rel: rel, digest: "limit-exceeded"})
			return filepath.SkipAll
		}
		maxB := lim.MaxYAMLBytes
		if kind == EntityDoc {
			maxB = lim.MaxDocBytes
		}
		_, dig, berr := s.ReadBounded(rel, maxB)
		if berr != nil {
			if os.IsNotExist(berr) {
				return nil
			}
			// Encode size/err state so oversized transitions are still detected.
			if fi, ierr := d.Info(); ierr == nil {
				var sz [8]byte
				binary.LittleEndian.PutUint64(sz[:], uint64(fi.Size()))
				items = append(items, item{rel: rel, digest: "oversize:" + hex.EncodeToString(sz[:])})
			} else {
				items = append(items, item{rel: rel, digest: "err"})
			}
			return nil
		}
		items = append(items, item{rel: rel, digest: dig})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].rel < items[j].rel })
	h := sha256.New()
	for _, it := range items {
		h.Write([]byte(it.rel))
		h.Write([]byte{0})
		h.Write([]byte(it.digest))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
