// Command gorequest-desktop runs the GoRequest desktop app using MyGo's native
// UI (GPU-drawn Go widgets, no web page and no webview). All request and file
// logic lives in the shared pure Go core.
package main

import (
	"flag"
	"log"

	"github.com/egoist/mygo"

	"gorequest/internal/desktop"
)

func main() {
	project := flag.String("project", "", "path to a project folder containing .gorequest/")
	flag.Parse()

	// The desktop manager owns the startup decision: an explicit -project path
	// wins, then the recorded last project (when restore is on), otherwise the
	// small welcome/project-chooser window. The working directory is never
	// probed implicitly, so a .gorequest folder here cannot override the last
	// project the user opened.
	mgr := desktop.NewManager(*project)

	mygo.App.WhenReady(func() {
		mgr.WireApp()
		mgr.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
