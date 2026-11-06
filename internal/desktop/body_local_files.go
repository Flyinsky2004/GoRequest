package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"gorequest/internal/core"
)

const localFilesFileName = "local-files.json"

type localFilesDisk struct {
	Projects map[string]map[string]string `json:"projects,omitempty"`
}

// localFileStore persists explicit local-machine file mappings ("local:<slot>"
// -> absolute local path) in the OS config directory alongside config.json.
// When path is empty (in-memory test configs), nothing touches disk.
// Shared .gorequest/ files never receive machine paths.
type localFileStore struct {
	mu       sync.RWMutex
	path     string
	projects map[string]map[string]string
}

var (
	localStoresMu sync.Mutex
	localStores   = map[*configStore]*localFileStore{}
)

func localFileStoreFor(cfg *configStore) *localFileStore {
	if cfg == nil {
		return &localFileStore{projects: map[string]map[string]string{}}
	}
	localStoresMu.Lock()
	defer localStoresMu.Unlock()
	if s, ok := localStores[cfg]; ok {
		return s
	}
	diskPath := ""
	if cfg.path != "" {
		diskPath = filepath.Join(filepath.Dir(cfg.path), localFilesFileName)
	}
	s := &localFileStore{
		path:     diskPath,
		projects: map[string]map[string]string{},
	}
	s.load()
	localStores[cfg] = s
	return s
}

func projectBindingKey(ps *projectState) string {
	if ps == nil {
		return ""
	}
	id := ""
	if ps.project != nil && ps.project.File != nil {
		id = strings.TrimSpace(ps.project.File.ID)
	}
	return id + "|" + filepath.Clean(ps.root)
}

func (s *localFileStore) load() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var disk localFilesDisk
	if json.Unmarshal(data, &disk) == nil && disk.Projects != nil {
		s.projects = disk.Projects
	}
}

func (s *localFileStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(localFilesDisk{Projects: s.projects}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".local-files-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

func (s *localFileStore) get(projKey, slot string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.projects[projKey]
	if m == nil {
		return "", false
	}
	v, ok := m[strings.TrimSpace(slot)]
	return v, ok && v != ""
}

func (s *localFileStore) set(projKey, slot, absPath string) error {
	slot = strings.TrimSpace(slot)
	absPath = strings.TrimSpace(absPath)
	if err := core.ValidateLocalSlot(slot); err != nil {
		return err
	}
	if absPath == "" {
		return s.delete(projKey, slot)
	}
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projects[projKey] == nil {
		s.projects[projKey] = map[string]string{}
	}
	s.projects[projKey][slot] = abs
	return s.saveLocked()
}

func (s *localFileStore) delete(projKey, slot string) error {
	slot = strings.TrimSpace(slot)
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.projects[projKey]
	if m == nil {
		return nil
	}
	delete(m, slot)
	if len(m) == 0 {
		delete(s.projects, projKey)
	}
	return s.saveLocked()
}

type projectLocalFileProvider struct {
	store   *localFileStore
	projKey string
}

func (p projectLocalFileProvider) ResolveLocalFile(slot string) (string, bool) {
	if p.store == nil || p.projKey == "" {
		return "", false
	}
	return p.store.get(p.projKey, slot)
}

func (a *App) localFilesFor(ps *projectState) projectLocalFileProvider {
	var cfg *configStore
	if a != nil && a.mgr != nil {
		cfg = a.mgr.cfg
	}
	return projectLocalFileProvider{
		store:   localFileStoreFor(cfg),
		projKey: projectBindingKey(ps),
	}
}

// BindLocalFile binds a logical slot name (for "local:<slot>" or localFile) to
// an absolute path on the current machine for ps, persisting only to local
// device state.
func (a *App) BindLocalFile(ps *projectState, slot, absPath string) error {
	lf := a.localFilesFor(ps)
	return lf.store.set(lf.projKey, slot, absPath)
}

// UnbindLocalFile removes the local machine binding for slot on ps.
func (a *App) UnbindLocalFile(ps *projectState, slot string) error {
	lf := a.localFilesFor(ps)
	return lf.store.delete(lf.projKey, slot)
}

// BoundLocalFile returns the currently bound local file path for slot on ps.
func (a *App) BoundLocalFile(ps *projectState, slot string) (string, bool) {
	return a.localFilesFor(ps).ResolveLocalFile(slot)
}

// pickLocalFileDialog opens the native OS file picker (for regular files, never
// copying into .gorequest/) and delivers the chosen path back on the owning
// window's UI thread.
func (a *App) pickLocalFileDialog(onChosen func(absPath string)) {
	if a == nil || a.win == nil {
		return
	}
	var pw *mygo.Window
	if nw, ok := a.win.(nativeWindower); ok {
		pw = nw.nativeWindow()
	}
	bm := a.bodyMsg()
	m := a.msg()
	if pw == nil {
		a.status = m.WindowNotReady
		return
	}
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      pw,
			Title:       bm.ChooseFile,
			ButtonLabel: m.Open,
			Directory:   false,
		})
		if err != nil {
			win.Update(func() { a.status = translate(m.OpenFailed, err.Error()) })
			return
		}
		if len(paths) == 0 {
			return
		}
		chosen := paths[0]
		win.Update(func() {
			if a.closed || win.IsDestroyed() {
				return
			}
			onChosen(chosen)
		})
	}()
}

// projectFilesSuggestions lists existing regular files inside .gorequest/
// (excluding *.request.yaml, *.auth.yaml, environments/*.yaml and project.yaml)
// as relative '/' suggestions for body.file inputs.
func projectFilesSuggestions(p *core.Project) []string {
	if p == nil || p.Store == nil {
		return nil
	}
	all, err := p.Store.List(".", func(name string) bool {
		if strings.HasPrefix(name, ".") {
			return false
		}
		if name == "project.yaml" || name == "collection.yaml" {
			return false
		}
		if strings.HasSuffix(name, ".request.yaml") || strings.HasSuffix(name, ".auth.yaml") {
			return false
		}
		return true
	})
	if err != nil {
		return nil
	}
	var out []string
	for _, rel := range all {
		if strings.HasPrefix(rel, "environments/") {
			continue
		}
		out = append(out, rel)
	}
	return out
}
