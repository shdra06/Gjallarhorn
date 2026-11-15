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
	rule := policy.Rule{ID: "copilot-billing-no-refunds", Effect: policy.Deny,
		Match: policy.Match{Actors: []string{"agent:billing"}, Targets: []string{"billing.refund"}, Delegated: boolPtr(false)}}
	// Delegated=false does not stop the delegated attack, so try the delegated-only rule plus collateral instead.
	rule = policy.Rule{ID: "copilot-support-chains", Effect: policy.Deny,
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

func boolPtr(b bool) *bool { return &b }

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
