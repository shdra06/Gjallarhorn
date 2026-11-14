package control

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/identity"
	"github.com/shdra06/Gjallarhorn/engine/policy"
)

const token = "test-admin-token-0123456789abcdef"

func newServer(t *testing.T) (*httptest.Server, *engine.Engine) {
	t.Helper()
	e, err := engine.New(engine.Config{
		Principals: []identity.PrincipalConfig{
			{ID: "human:priya", Kind: identity.KindHuman, Labels: map[string]string{"role": "operator"}, VirtualKeys: []string{"vk-priya"}},
			{ID: "agent:support", Kind: identity.KindAgent, VirtualKeys: []string{"vk-support"}, AgentNames: []string{"support"}},
			{ID: "agent:billing", Kind: identity.KindAgent, VirtualKeys: []string{"vk-billing"}, AgentNames: []string{"billing"}},
		},
		Tickets: engine.TicketConfig{ActiveKey: "k1", Keys: map[string]string{"k1": strings.Repeat("k", 32)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(e, Options{AdminToken: token, AllowedOrigins: []string{"http://localhost:5173"}, Version: "test", Heartbeat: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() { srv.Close(); e.Close() })
	return srv, e
}

func do(t *testing.T, srv *httptest.Server, method, path, body string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestTokenRequired(t *testing.T) {
	if _, err := NewHandler(nil, Options{AdminToken: "short"}); err == nil {
		t.Fatal("short tokens must be rejected")
	}
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/v1/health")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health is public: %v %v", err, resp)
	}
	for _, auth := range []string{"", "Bearer wrong", "Bearer " + token + "x", token} {
		req, _ := http.NewRequest("GET", srv.URL+"/v1/lockdowns", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		r, _ := http.DefaultClient.Do(req)
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth %q: got %d", auth, r.StatusCode)
		}
	}
}

func TestLockdownLifecycle(t *testing.T) {
	srv, e := newServer(t)
	resp, body := do(t, srv, "POST", "/v1/lockdowns", `{"principal":"agent:support","reason":"injection"}`, "X-Operator", "human:priya")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lock: %d %s", resp.StatusCode, body)
	}
	if !e.IsLocked("agent:support") {
		t.Fatal("lockdown not applied")
	}
	if r := e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-support"}); r.Decision.Allowed() {
		t.Fatal("locked agent's next call must be denied")
	}
	_, body = do(t, srv, "GET", "/v1/lockdowns", "")
	var list []map[string]any
	_ = json.Unmarshal(body, &list)
	if len(list) != 1 || list[0]["by"] != "human:priya" {
		t.Fatalf("list: %s", body)
	}
	if r, _ := do(t, srv, "DELETE", "/v1/lockdowns/agent:support", ""); r.StatusCode != http.StatusNoContent {
		t.Fatalf("unlock: %d", r.StatusCode)
	}
	if r, _ := do(t, srv, "DELETE", "/v1/lockdowns/agent:support", ""); r.StatusCode != http.StatusNotFound {
		t.Fatalf("second unlock: %d", r.StatusCode)
	}
	if r, _ := do(t, srv, "POST", "/v1/lockdowns", `{"principal":"bad id"}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid principal: %d", r.StatusCode)
	}
	if r, _ := do(t, srv, "POST", "/v1/lockdowns", `{"principal":"agent:x","duration":"-5m"}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative duration: %d", r.StatusCode)
	}
	if r, _ := do(t, srv, "POST", "/v1/lockdowns", `{"principal":"agent:x","oops":1}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", r.StatusCode)
	}
}

func TestPolicyEndpoints(t *testing.T) {
	srv, e := newServer(t)
	resp, _ := do(t, srv, "GET", "/v1/policy", "")
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("policy must have an ETag")
	}
	newPolicy := `{"version":1,"rules":[{"id":"no-shell","effect":"deny","match":{"targets":["shell.*"]}}]}`
	if r, b := do(t, srv, "PUT", "/v1/policy", newPolicy, "If-Match", `"stale"`); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, srv, "PUT", "/v1/policy", newPolicy, "If-Match", etag); r.StatusCode != http.StatusOK {
		t.Fatalf("put: %d %s", r.StatusCode, b)
	}
	if len(e.Policy().Rules) != 1 {
		t.Fatal("policy not applied")
	}
	r, b := do(t, srv, "PUT", "/v1/policy", `{"version":2,"rules":[{"id":"x","effect":"nope"}]}`)
	var apiErr apiError
	_ = json.Unmarshal(b, &apiErr)
	if r.StatusCode != http.StatusUnprocessableEntity || len(apiErr.Details) < 2 {
		t.Fatalf("invalid policy should list every problem: %d %s", r.StatusCode, b)
	}
	_, b = do(t, srv, "POST", "/v1/policy/validate", `{"version":1}`)
	if !strings.Contains(string(b), `"valid": true`) {
		t.Fatalf("validate: %s", b)
	}
	_, b = do(t, srv, "POST", "/v1/policy/validate", `{"version":1,"bogus":true}`)
	if !strings.Contains(string(b), `"valid": false`) {
		t.Fatalf("validate unknown field: %s", b)
	}
}

func TestSimulate(t *testing.T) {
	srv, e := newServer(t)
	e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "shell.exec", VirtualKey: "vk-support", Label: "attack"})
	e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-priya", Label: "benign"})
	body := `{"policy":{"version":1,"rules":[{"id":"no-shell","effect":"deny","match":{"targets":["shell.*"]}}]}}`
	r, b := do(t, srv, "POST", "/v1/simulate", body)
	if r.StatusCode != 200 {
		t.Fatalf("simulate: %d %s", r.StatusCode, b)
	}
	var out struct {
		Safe         bool              `json:"safe"`
		NewlyBlocked []json.RawMessage `json:"newly_blocked"`
	}
	_ = json.Unmarshal(b, &out)
	if !out.Safe || len(out.NewlyBlocked) != 1 {
		t.Fatalf("simulate result: %s", b)
	}
	if r, _ := do(t, srv, "POST", "/v1/simulate", `{}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing policy: %d", r.StatusCode)
	}
}

func TestGrantEndpoints(t *testing.T) {
	srv, _ := newServer(t)
	r, b := do(t, srv, "POST", "/v1/grants", `{"actor":"agent:billing","plane":"mcp","target":"billing.refund","duration":"10m","reason":"approved refund"}`)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("grant: %d %s", r.StatusCode, b)
	}
	var g policy.Grant
	_ = json.Unmarshal(b, &g)
	if r, _ := do(t, srv, "POST", "/v1/grants", `{"actor":"agent:billing","plane":"mcp","target":"x","duration":"48h","reason":"r"}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("over-long grant: %d", r.StatusCode)
	}
	if r, _ := do(t, srv, "POST", "/v1/grants", `{"actor":"agent:billing","plane":"mcp","target":"x","duration":"5m"}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("grant without a reason: %d", r.StatusCode)
	}
	_, b = do(t, srv, "GET", "/v1/grants", "")
	if !strings.Contains(string(b), g.ID) {
		t.Fatalf("list grants: %s", b)
	}
	if r, _ := do(t, srv, "DELETE", "/v1/grants/"+g.ID, ""); r.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: %d", r.StatusCode)
	}
	if r, _ := do(t, srv, "DELETE", "/v1/grants/"+g.ID, ""); r.StatusCode != http.StatusNotFound {
		t.Fatalf("revoke twice: %d", r.StatusCode)
	}
}

func TestEventsAndStats(t *testing.T) {
	srv, e := newServer(t)
	for i := 0; i < 5; i++ {
		e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-priya"})
	}
	_, b := do(t, srv, "GET", "/v1/events?after=3&limit=10", "")
	var evs []map[string]any
	_ = json.Unmarshal(b, &evs)
	if len(evs) != 2 {
		t.Fatalf("events after 3: %s", b)
	}
	if r, _ := do(t, srv, "GET", "/v1/events?limit=0", ""); r.StatusCode != http.StatusBadRequest {
		t.Fatal("bad limit")
	}
	if r, _ := do(t, srv, "GET", "/v1/events?after=x", ""); r.StatusCode != http.StatusBadRequest {
		t.Fatal("bad after")
	}
	_, b = do(t, srv, "GET", "/v1/stats", "")
	if !strings.Contains(string(b), `"events": 5`) {
		t.Fatalf("stats: %s", b)
	}
	_, b = do(t, srv, "GET", "/v1/principals", "")
	if !strings.Contains(string(b), "agent:billing") {
		t.Fatalf("principals: %s", b)
	}
}

func TestStream(t *testing.T) {
	srv, e := newServer(t)
	e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "backlog", VirtualKey: "vk-priya"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/v1/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "live", VirtualKey: "vk-priya"})
	}()
	sc := bufio.NewScanner(resp.Body)
	var ids []string
	heartbeat := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "id: ") {
			ids = append(ids, strings.TrimPrefix(line, "id: "))
		}
		if strings.HasPrefix(line, ": keep-alive") {
			heartbeat = true
		}
		if len(ids) == 2 && heartbeat {
			break
		}
	}
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "2" {
		t.Fatalf("stream should send the backlog then live events in order, got ids %v", ids)
	}
}

func TestCORS(t *testing.T) {
	srv, _ := newServer(t)
	req, _ := http.NewRequest("OPTIONS", srv.URL+"/v1/lockdowns", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
	req.Header.Set("Origin", "https://evil.example")
	resp, _ = http.DefaultClient.Do(req)
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unknown origins must not be allowed")
	}
}
