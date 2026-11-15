package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/copilot"
	"github.com/shdra06/Gjallarhorn/engine/policy"
)

// Every endpoint refuses bad input with a clear status, never a 500 or a panic.
func TestBadRequests(t *testing.T) {
	srv, e := copilotServer(t, nil)
	seq := refunds(e)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"PUT", "/v1/policy", `{not json`, 400},
		{"POST", "/v1/simulate", `{not json`, 400},
		{"POST", "/v1/simulate", `{"policy":{"version":9}}`, 422},
		{"POST", "/v1/grants", `{not json`, 400},
		{"POST", "/v1/grants", `{"actor":"agent:billing","plane":"mcp","target":"x","duration":"soon","reason":"r"}`, 400},
		{"POST", "/v1/grants", `{"actor":"not an id","plane":"mcp","target":"x","duration":"5m","reason":"r"}`, 400},
		{"PUT", "/v1/mode", `{not json`, 400},
		{"PUT", "/v1/mode", `{}`, 400},
		{"GET", "/v1/events?after=x", ``, 400},
		{"GET", "/v1/events?limit=0", ``, 400},
		{"GET", "/v1/events?limit=99999", ``, 400},
		{"GET", "/v1/stream?after=x", ``, 400},
		{"GET", "/v1/incidents?limit=0", ``, 400},
		{"GET", "/v1/incidents?limit=abc", ``, 400},
		{"POST", "/v1/copilot/explain", `{not json`, 400},
		{"POST", "/v1/copilot/explain", `{"seq":99999}`, 404},
		{"POST", "/v1/copilot/suggest", `{not json`, 400},
		{"POST", "/v1/copilot/suggest", `{"seq":99999}`, 404},
		{"POST", "/v1/copilot/apply", `{not json`, 400},
		{"POST", "/v1/lockdowns", `{"principal":"agent:x","duration":"soon"}`, 400},
	}
	for _, c := range cases {
		resp, body := do(t, srv, c.method, c.path, c.body)
		if resp.StatusCode != c.want {
			t.Errorf("%s %s %s: got %d, want %d (%s)", c.method, c.path, c.body, resp.StatusCode, c.want, body)
		}
		if !json.Valid(body) {
			t.Errorf("%s %s: errors are JSON: %s", c.method, c.path, body)
		}
	}
	etag := engine.PolicyHash(e.Policy())
	if r, _ := do(t, srv, "POST", "/v1/copilot/apply", `{"seq":99999,"rule":{"id":"copilot-x","effect":"deny","match":{}}}`, "If-Match", etag); r.StatusCode != 404 {
		t.Fatalf("apply to an unknown call: %d", r.StatusCode)
	}
	if r, _ := do(t, srv, "GET", fmt.Sprintf("/v1/events?limit=10&after=%d", seq-1), ""); r.StatusCode != 200 {
		t.Fatal("valid events query")
	}
}

func TestApplyNeedsReviewConsent(t *testing.T) {
	srv, e := copilotServer(t, nil)
	seq := refunds(e)
	// An unlabelled call by billing that a broad rule would also block.
	e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-billing"})
	// Stops the attack (started by support) and also billing's own unlabelled refund.
	rule := policy.Rule{ID: "copilot-support-chains", Effect: policy.Deny,
		Match: policy.Match{Targets: []string{"billing.refund"}, Origins: []string{"agent:support", "agent:billing"}}}
	body, _ := json.Marshal(applyRequest{Seq: seq, Rule: rule})
	etag := engine.PolicyHash(e.Policy())
	resp, b := do(t, srv, "POST", "/v1/copilot/apply", string(body), "If-Match", `"`+etag+`"`)
	var out applyResponse
	_ = json.Unmarshal(b, &out)
	if resp.StatusCode != http.StatusConflict || out.Applied || out.Suggestion.Status != copilot.StatusReview {
		t.Fatalf("a rule that blocks unlabelled calls needs consent: %d %s", resp.StatusCode, b)
	}
	body, _ = json.Marshal(applyRequest{Seq: seq, Rule: rule, AcceptReview: true})
	resp, b = do(t, srv, "POST", "/v1/copilot/apply", string(body), "If-Match", `"`+etag+`"`)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"applied": true`) {
		t.Fatalf("with consent it is applied: %d %s", resp.StatusCode, b)
	}
	last := e.Recorder().Recent(1)[0]
	if last.Note["by"] != "operator via co-pilot" {
		t.Fatalf("the audit trail names the co-pilot: %v", last.Note)
	}
}

func TestOperatorHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if operator(r, "") != "operator" || operator(r, "fallback") != "fallback" {
		t.Fatal("defaults")
	}
	r.Header.Set("X-Operator", "  human:priya ")
	if operator(r, "x") != "human:priya" {
		t.Fatal("header wins, trimmed")
	}
	r.Header.Set("X-Operator", strings.Repeat("a", 200))
	if operator(r, "x") != "x" {
		t.Fatal("over-long names are ignored")
	}
}

type noFlush struct{ http.ResponseWriter }

func TestStreamNeedsFlushing(t *testing.T) {
	srv, _ := newServer(t)
	_ = srv
	s := &server{}
	rec := httptest.NewRecorder()
	s.stream(noFlush{rec}, httptest.NewRequest("GET", "/v1/stream", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a writer that cannot flush: %d", rec.Code)
	}
}

func TestConsoleNotBuiltAndHead(t *testing.T) {
	dir := t.TempDir()
	srv := consoleServer(t, Options{ConsoleDir: dir})
	if resp, body := get(t, srv.URL+"/", false); resp.StatusCode != 404 || !strings.Contains(body, "not built") {
		t.Fatalf("an empty console dir: %d %q", resp.StatusCode, body)
	}
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<title>c</title>"), 0o644)
	req, _ := http.NewRequest("HEAD", srv.URL+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("HEAD: %v %v", resp, err)
	}
	if _, err := NewHandler(nil, Options{AdminToken: token, DemoDriver: "http://[::1"}); err == nil {
		t.Fatal("a bad demo driver URL is a config error")
	}
}

func TestHealthHidesCountsWithoutTheToken(t *testing.T) {
	srv, e := newServer(t)
	e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-priya"})
	_, public := get(t, srv.URL+"/v1/health", false)
	if !strings.Contains(public, `"ok"`) || strings.Contains(public, "events") || strings.Contains(public, "lockdowns") {
		t.Fatalf("public health: %s", public)
	}
	_, private := get(t, srv.URL+"/v1/health", true)
	if !strings.Contains(private, `"events"`) || !strings.Contains(private, `"lockdowns"`) {
		t.Fatalf("health with the token: %s", private)
	}
	_, stats := get(t, srv.URL+"/v1/stats", true)
	if !strings.Contains(stats, `"decision_latency"`) || !strings.Contains(stats, `"last_seq"`) {
		t.Fatalf("stats: %s", stats)
	}
}

// Every route except the health probe and the console's static files needs the admin token.
// A new endpoint added without auth fails this test.
func TestEveryEndpointNeedsTheToken(t *testing.T) {
	driver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) }))
	defer driver.Close()
	srv := consoleServer(t, Options{DemoDriver: driver.URL})
	routes := []string{
		"GET /v1/principals", "GET /v1/lockdowns", "POST /v1/lockdowns", "DELETE /v1/lockdowns/agent:x",
		"GET /v1/policy", "PUT /v1/policy", "POST /v1/policy/validate", "POST /v1/simulate",
		"GET /v1/grants", "POST /v1/grants", "DELETE /v1/grants/g1",
		"GET /v1/events", "GET /v1/stream", "GET /v1/stats", "GET /v1/mode", "PUT /v1/mode",
		"GET /v1/incidents", "GET /v1/incidents/1", "GET /v1/copilot",
		"POST /v1/copilot/explain", "POST /v1/copilot/suggest", "POST /v1/copilot/apply",
		"GET /demo/scenarios", "POST /demo/scenarios/x/run", "PUT /demo/traffic",
	}
	for _, rt := range routes {
		method, path, _ := strings.Cut(rt, " ")
		for _, auth := range []string{"", "Bearer wrong-token", "Bearer sk-bf-some-gateway-key"} {
			req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader("{}"))
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s with %q: got %d, want 401", rt, auth, resp.StatusCode)
			}
		}
	}
}
