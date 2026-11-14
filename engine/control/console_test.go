package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/identity"
)

func consoleServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	e, err := engine.New(engine.Config{
		Principals: []identity.PrincipalConfig{{ID: "agent:support", Kind: identity.KindAgent, VirtualKeys: []string{"vk-support"}}},
		Tickets:    engine.TicketConfig{ActiveKey: "k1", Keys: map[string]string{"k1": strings.Repeat("k", 32)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	opts.AdminToken = token
	h, err := NewHandler(e, opts)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() { srv.Close(); e.Close() })
	return srv
}

func get(t *testing.T, url string, auth bool) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if auth {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestConsoleIsServed(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>console</title>"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "assets", "app-1234.js"), []byte("console.log(1)"), 0o644)
	_ = os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("secret"), 0o644)
	srv := consoleServer(t, Options{ConsoleDir: dir})

	resp, body := get(t, srv.URL+"/", false)
	if resp.StatusCode != 200 || !strings.Contains(body, "<title>console</title>") {
		t.Fatalf("index: %d %q", resp.StatusCode, body)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("csp %q", csp)
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatal("index.html must be revalidated")
	}
	resp, body = get(t, srv.URL+"/assets/app-1234.js", false)
	if resp.StatusCode != 200 || body != "console.log(1)" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %q %q", resp.StatusCode, body, resp.Header.Get("Cache-Control"))
	}
	// The console's own routes fall back to index.html.
	if _, body := get(t, srv.URL+"/incidents/42", false); !strings.Contains(body, "console") {
		t.Fatalf("spa fallback: %q", body)
	}
	// No way out of the directory, and no directory listings.
	for _, p := range []string{"/../secret.txt", "/%2e%2e/secret.txt", "/assets/"} {
		if _, body := get(t, srv.URL+p, false); strings.Contains(body, "secret") || strings.Contains(body, "app-1234") {
			t.Fatalf("%s leaked: %q", p, body)
		}
	}
	// The API still needs the token, and unknown API paths are JSON 404s, not the console.
	if resp, _ := get(t, srv.URL+"/v1/lockdowns", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("api auth: %d", resp.StatusCode)
	}
	if resp, body := get(t, srv.URL+"/v1/nope", true); resp.StatusCode != 404 || !strings.Contains(body, "no such endpoint") {
		t.Fatalf("unknown api path: %d %q", resp.StatusCode, body)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/", nil)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST to the console: %d", r.StatusCode)
	}
}

func TestDemoDriverProxy(t *testing.T) {
	var gotPath, gotAuth string
	driver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer driver.Close()
	srv := consoleServer(t, Options{DemoDriver: driver.URL})

	if resp, _ := get(t, srv.URL+"/demo/scenarios", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the demo driver needs the admin token: %d", resp.StatusCode)
	}
	resp, body := get(t, srv.URL+"/demo/scenarios", true)
	if resp.StatusCode != 200 || body != `{"ok":true}` || gotPath != "/scenarios" {
		t.Fatalf("proxy: %d %q path=%q", resp.StatusCode, body, gotPath)
	}
	if gotAuth != "" {
		t.Fatal("the admin token must not be passed on to the driver")
	}
	// Without a console dir, "/" is not served.
	if resp, _ := get(t, srv.URL+"/", false); resp.StatusCode != 404 {
		t.Fatalf("no console configured: %d", resp.StatusCode)
	}
}
