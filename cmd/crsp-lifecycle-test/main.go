// Command crsp-lifecycle-test serves the updater test interface over HTTP for the lifecycle
// conformance runner (scripts/delivery/lifecycle_conformance.py --url http://127.0.0.1:7391).
// It is a test tool: the release workflow does not build or ship it.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/lifecycletest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7391", "listen address (loopback only)")
	flag.Parse()
	srv := &http.Server{Addr: *addr, Handler: lifecycletest.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("crsp-lifecycle-test on %s", *addr)
	log.Fatal(srv.ListenAndServe())
}
