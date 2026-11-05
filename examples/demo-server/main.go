// Command demo-server is a small local HTTP service for exercising the sample
// project in examples/sample-project. It exists only for local development and
// manual debugging of the GoRequest desktop app; it is not production code.
//
//	go run ./examples/demo-server -addr 127.0.0.1:4010
//
// The server binds to the loopback interface by default and uses only the Go
// standard library. See examples/demo-server/README.md for the endpoint list.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4010", "listen address (loopback by default)")
	flag.Parse()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("demo server listening on http://%s", *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
