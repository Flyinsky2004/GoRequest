// Command gorequest-desktop runs the GoRequest desktop app using MyGo's native
// UI (GPU-drawn Go widgets, no web page and no webview). All request and file
// logic lives in the shared pure Go core.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop"
)

func main() {
	project := flag.String("project", "", "path to a project folder containing .gorequest/")
	flag.Parse()

	if *project == "" {
		if _, err := os.Stat(".gorequest"); err == nil {
			*project = "."
		}
	}

	app := desktop.New(*project)

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "GoRequest",
			Width:     1200,
			Height:    820,
			MinWidth:  820,
			MinHeight: 560,
			Content:   ui.View(app.View),
		})
		app.SetWindow(win)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
