package desktop

import (
	"io"
	"net"
	"testing"
	"time"
)

// A tab can be closed while TLS is still negotiating, before Connect has a
// Session to return. All user-facing teardown paths must cancel that attempt.
func TestWSWorkspaceCancelsPendingHandshake(t *testing.T) {
	for _, action := range []string{"close-tab", "close-window", "disconnect", "clear"} {
		t.Run(action, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			accepted := make(chan net.Conn, 1)
			closed := make(chan struct{})
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				accepted <- conn
				// Read the ClientHello without ever returning a ServerHello.
				_, _ = io.Copy(io.Discard, conn)
				close(closed)
			}()

			a, ps, w := openWSProject(t, "Pending Handshake")
			if _, err := ps.createWebSocketIn("", "Pending"); err != nil {
				t.Fatal(err)
			}
			tab := ps.activeTab()
			tab.URL = "wss://" + listener.Addr().String() + "/socket"
			t.Cleanup(func() { w.Update(func() { wsTeardownTab(tab) }) })
			a.wsTabConnect(tab)
			guard := time.NewTimer(time.Minute)
			defer guard.Stop()
			select {
			case conn := <-accepted:
				t.Cleanup(func() { _ = conn.Close() })
			case <-guard.C:
				t.Fatal("handshake did not reach the server")
			}

			w.Update(func() {
				if !tab.ws.busy || tab.ws.session != nil {
					t.Fatal("expected a pending attempt with no established session")
				}
				switch action {
				case "close-tab":
					ps.removeTab(tab)
				case "close-window":
					a.cancelAllRequests()
				case "disconnect":
					a.wsTabDisconnect(tab)
				case "clear":
					a.wsTabClear(tab)
				}
				if tab.ws.busy || tab.ws.connectCancel != nil {
					t.Error("teardown left the attempt busy or retained its cancel handle")
				}
			})
			select {
			case <-closed:
			case <-guard.C:
				t.Fatal("teardown did not cancel the pending TLS connection")
			}
		})
	}
}
