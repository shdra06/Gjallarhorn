// Command mockmodel is the demo's OpenAI-compatible model server: a fixed reply for agents and
// a scripted stand-in for the co-pilot's LLM (model "copilot-stub"). See package stubmodel.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/shdra06/Gjallarhorn/demo/internal/stubmodel"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9000", "listen address")
	flag.Parse()
	log.Printf("mock model server on %s", *addr)
	srv := &http.Server{Addr: *addr, Handler: stubmodel.Handler(nil), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
