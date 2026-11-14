// Command mock is a tiny OpenAI-compatible model server for tests. It answers any POST to a
// path ending in /chat/completions with a fixed reply and logs each request.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	flag.Parse()
	var calls atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			if strings.HasSuffix(r.URL.Path, "/models") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"allowed-model","object":"model"},{"id":"blocked-model","object":"model"}]}`))
				return
			}
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		n := calls.Add(1)
		log.Printf("MOCK call=%d path=%s model=%s", n, r.URL.Path, body.Model)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   body.Model,
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": "hello from the mock model"},
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 5, "total_tokens": 10},
		})
	})
	log.Printf("mock model server on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
