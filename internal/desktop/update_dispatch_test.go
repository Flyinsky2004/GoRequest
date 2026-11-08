package desktop

import (
	"testing"
	"time"
)

// The network result must wait for UI dispatch before reading the Manager's
// window list or updating dialog state. A closed original host must not cause
// the result to run inline on the background goroutine or disappear.
func TestUpdateDispatchSelectsReplacementHostOnUI(t *testing.T) {
	checker := &fakeUpdateChecker{}
	installed := "0.0.1-dev"
	m, u, _ := newUpdateTestManager(t, checker, nil, fixedClock().Now, &installed, nil)
	if err := m.openProject(writeProject(t, "First update host")); err != nil {
		t.Fatal(err)
	}
	first := m.workspaces[0]
	if err := m.openProject(writeProject(t, "Second update host")); err != nil {
		t.Fatal(err)
	}
	second := m.workspaces[1]
	u.setHost(first.win)

	queue := make(chan func(), 1)
	u.runOnUI = func(fn func()) { queue <- fn }
	done := make(chan struct{})
	delivered := false
	go func() {
		u.post(func() { delivered = true })
		close(done)
	}()
	guard := time.NewTimer(time.Minute)
	defer guard.Stop()
	var callback func()
	select {
	case callback = <-queue:
	case <-guard.C:
		t.Fatal("update callback did not reach the application dispatcher")
	}
	select {
	case <-done:
	case <-guard.C:
		t.Fatal("posting through the test dispatcher did not return")
	}
	if delivered {
		t.Fatal("update result ran before UI dispatch")
	}
	if u.getHost() != first.win {
		t.Fatal("host selection ran before UI dispatch")
	}

	// The first host disappears while the network completion is queued.
	first.win.Close()
	callback()
	if !delivered {
		t.Fatal("queued update result was lost with its old host")
	}
	if u.getHost() != second.win {
		t.Fatal("UI dispatch did not choose the surviving window")
	}
}

func TestRealUpdateControllerUsesApplicationDispatcher(t *testing.T) {
	m, _ := newTestManager(t)
	if newRealUpdateController(m).runOnUI == nil {
		t.Fatal("production updater has no application UI dispatcher")
	}
}
