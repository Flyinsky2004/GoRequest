package desktop

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// window is the small slice of a native MyGo window that the desktop manager
// needs. It exists so the window/lifecycle logic (open, activate, close, quit,
// async result routing) can be exercised with a fake window in headless tests,
// while the real application uses *mygo.Window through realWindow.
//
// The interface mirrors the real methods exactly, including that OnClose and
// OnClosed return an unsubscribe function and that Update runs its function on
// the UI thread. Nothing here is called from a background goroutine directly;
// background work always goes through Update.
type window interface {
	Show()
	Hide()
	Focus()
	Close()
	SetTitle(string)
	Update(func())
	OnClose(func(*mygo.CloseEvent)) func()
	OnClosed(func()) func()
	IsDestroyed() bool
	IsVisible() bool
}

// nativeWindower is implemented by windows that wrap a real *mygo.Window, so
// native dialogs can be parented correctly. Fake windows return nil.
type nativeWindower interface {
	nativeWindow() *mygo.Window
}

// windowSpec describes a window the manager wants to create. The content view
// is a plain function so the same spec builds real or fake windows.
type windowSpec struct {
	Title     string
	Width     int
	Height    int
	MinWidth  int
	MinHeight int
	// Chrome makes the window a modern AppKit-style workbench window: the title
	// bar is hidden (the view draws its own top bar under the traffic lights)
	// and the sidebar shows the system's sidebar vibrancy behind it. Off for the
	// lightweight launcher.
	Chrome bool
	View   func(*ui.Context)
}

// windowFactory creates a window for a spec. The default builds a MyGo native
// UI window; tests substitute a fake factory.
type windowFactory func(windowSpec) window

// realWindow adapts *mygo.Window to the window interface.
type realWindow struct{ w *mygo.Window }

func (r *realWindow) Show()             { r.w.Show() }
func (r *realWindow) Hide()             { r.w.Hide() }
func (r *realWindow) Focus()            { r.w.Focus() }
func (r *realWindow) Close()            { r.w.Close() }
func (r *realWindow) SetTitle(t string) { r.w.SetTitle(t) }
func (r *realWindow) Update(fn func())  { r.w.Update(fn) }

func (r *realWindow) OnClose(fn func(*mygo.CloseEvent)) func() { return r.w.OnClose(fn) }
func (r *realWindow) OnClosed(fn func()) func()                { return r.w.OnClosed(fn) }
func (r *realWindow) IsDestroyed() bool                        { return r.w.IsDestroyed() }
func (r *realWindow) IsVisible() bool                          { return r.w.IsVisible() }
func (r *realWindow) nativeWindow() *mygo.Window               { return r.w }

// Size and OnResize let the app persist the window's geometry. They are not on
// the window interface so fake windows in tests need not implement them.
func (r *realWindow) Size() (int, int)          { return r.w.Size() }
func (r *realWindow) OnResize(fn func()) func() { return r.w.OnResize(fn) }

// newRealWindow builds a real MyGo native UI window from a spec.
func newRealWindow(s windowSpec) window {
	opts := mygo.WindowOptions{
		Title:     s.Title,
		Width:     s.Width,
		Height:    s.Height,
		MinWidth:  s.MinWidth,
		MinHeight: s.MinHeight,
		Content:   ui.View(s.View),
	}
	if s.Chrome {
		opts.TitleBarStyle = mygo.TitleBarHidden
		opts.Vibrancy = mygo.VibrancySidebar
	}
	return &realWindow{w: mygo.NewWindow(opts)}
}
