package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/copilot"
	"github.com/shdra06/Gjallarhorn/engine/identity"
	"github.com/shdra06/Gjallarhorn/engine/incident"
	"github.com/shdra06/Gjallarhorn/engine/policy"
)

func copilotServer(t *testing.T, llm *copilot.LLM) (*httptest.Server, *engine.Engine) {
	t.Helper()
	e, err := engine.New(engine.Config{
		Principals: []identity.PrincipalConfig{
			{ID: "human:priya", Kind: identity.KindHuman, Labels: map[string]string{"role": "operator"}, VirtualKeys: []string{"vk-priya"}},
			{ID: "agent:support", Kind: identity.KindAgent, VirtualKeys: []string{"vk-support"}, AgentNames: []string{"support"}},
			{ID: "agent:billing", Kind: identity.KindAgent, VirtualKeys: []string{"vk-billing"}, AgentNames: []string{"billing"}},
		},
		Tickets: engine.TicketConfig{ActiveKey: "k1", Keys: map[string]string{"k1": strings.Repeat("k", 32)}},
		Policy:  &policy.Policy{Version: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(e, Options{AdminToken: token, Copilot: llm})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() { srv.Close(); e.Close() })
	return srv, e
}

// refunds records a normal refund (priya asks billing) and a harmful one (support, talked into
// it, asks billing); it returns the seq of the harmful refund.
func refunds(e *engine.Engine) uint64 {
	p := e.Delegate(engine.Request{Target: "billing", VirtualKey: "vk-priya", Label: "benign"})
	e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-billing", Ticket: p.Ticket, Label: "benign"})
	a := e.Delegate(engine.Request{Target: "billing", VirtualKey: "vk-support", Label: "attack"})
	return e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-billing", Ticket: a.Ticket, Label: "attack"}).Seq
}

func TestIncidentEndpoints(t *testing.T) {
	srv, e := copilotServer(t, nil)
	seq := refunds(e)

	resp, body := do(t, srv, "GET", fmt.Sprintf("/v1/incidents/%d", seq), "")
	var in incident.Incident
	if resp.StatusCode != 200 || json.Unmarshal(body, &in) != nil || in.Origin != "agent:support" || len(in.Steps) != 2 {
		t.Fatalf("incident: %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, srv, "GET", "/v1/incidents/99999", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown seq: %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "GET", "/v1/incidents/abc", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad seq: %d", resp.StatusCode)
	}
	resp, body = do(t, srv, "GET", "/v1/incidents", "")
	if resp.StatusCode != 200 || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("nothing was blocked yet, so no incidents: %s", body)
	}
}

func TestCopilotSuggestAndApply(t *testing.T) {
	srv, e := copilotServer(t, nil)
	seq := refunds(e)

	resp, body := do(t, srv, "GET", "/v1/copilot", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"grounded"`) {
		t.Fatalf("info: %s", body)
	}
	resp, body = do(t, srv, "POST", "/v1/copilot/explain", fmt.Sprintf(`{"seq":%d}`, seq))
	var ex copilot.Explanation
	if resp.StatusCode != 200 || json.Unmarshal(body, &ex) != nil || ex.Source != "grounded" || len(ex.Story) != 2 {
		t.Fatalf("explain: %d %s", resp.StatusCode, body)
	}

	resp, body = do(t, srv, "POST", "/v1/copilot/suggest", fmt.Sprintf(`{"seq":%d}`, seq))
	var sug suggestResponse
	if resp.StatusCode != 200 || json.Unmarshal(body, &sug) != nil || len(sug.Suggestions.Suggestions) == 0 {
		t.Fatalf("suggest: %d %s", resp.StatusCode, body)
	}
	if sug.PolicyETag != engine.PolicyHash(e.Policy()) {
		t.Fatal("suggestions must say which policy they were proven against")
	}
	var proven, rejected *copilot.Suggestion
	for i, s := range sug.Suggestions.Suggestions {
		switch {
		case s.Status == copilot.StatusProven && proven == nil:
			proven = &sug.Suggestions.Suggestions[i]
		case s.Status == copilot.StatusRejected && rejected == nil:
			rejected = &sug.Suggestions.Suggestions[i]
		}
	}
	if proven == nil || rejected == nil {
		t.Fatalf("expected a proven and a rejected suggestion: %s", body)
	}

	apply := func(rule policy.Rule, etag string) (*http.Response, applyResponse) {
		b, _ := json.Marshal(applyRequest{Seq: seq, Rule: rule})
		var h []string
		if etag != "" {
			h = []string{"If-Match", `"` + etag + `"`}
		}
		resp, body := do(t, srv, "POST", "/v1/copilot/apply", string(b), h...)
		var out applyResponse
		_ = json.Unmarshal(body, &out)
		return resp, out
	}
	if resp, _ := apply(proven.Rule, ""); resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("apply needs If-Match: %d", resp.StatusCode)
	}
	if resp, _ := apply(proven.Rule, "stale"); resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale etag: %d", resp.StatusCode)
	}
	// The server proves again: a rule that blocks normal work is refused even if a client sends it.
	resp, out := apply(rejected.Rule, sug.PolicyETag)
	if resp.StatusCode != http.StatusConflict || out.Applied || out.Suggestion.Status != copilot.StatusRejected {
		t.Fatalf("unsafe rule applied: %d %+v", resp.StatusCode, out)
	}
	if len(e.Policy().Rules) != 0 {
		t.Fatal("the policy must not change")
	}

	resp, out = apply(proven.Rule, sug.PolicyETag)
	if resp.StatusCode != 200 || !out.Applied || out.PolicyETag != engine.PolicyHash(e.Policy()) || len(e.Policy().Rules) != 1 {
		t.Fatalf("apply proven: %d %+v", resp.StatusCode, out)
	}
	// Applying again: the policy already blocks it.
	if resp, out := apply(proven.Rule, out.PolicyETag); resp.StatusCode != http.StatusConflict || !strings.Contains(out.Suggestion.Verdict, "already blocks") {
		t.Fatalf("second apply: %d %+v", resp.StatusCode, out)
	}

	// The same attack is now blocked, and normal refunds still work.
	again := refunds(e)
	h, _ := e.History()
	for _, ev := range h {
		if ev.Seq > seq && ev.Decision != nil {
			if blocked := !ev.Decision.Allowed(); blocked != (ev.Seq == again) {
				t.Fatalf("after applying, only the harmful refund is blocked; seq %d blocked=%v (%s)", ev.Seq, blocked, ev.Note["label"])
			}
		}
	}
	resp, body = do(t, srv, "GET", "/v1/incidents", "")
	var notes []incident.Step
	if json.Unmarshal(body, &notes) != nil || len(notes) != 1 || notes[0].Seq != again {
		t.Fatalf("the blocked attack is now an incident: %s", body)
	}
}

func TestCopilotIsRateLimited(t *testing.T) {
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(maxCopilotCalls)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started.Done()
		<-release
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Support asked billing for a refund and it went through."}}]}`))
	}))
	defer model.Close()
	srv, e := copilotServer(t, copilot.NewLLM(copilot.Config{Endpoint: model.URL, Model: "test/model", TimeoutSeconds: 10}))
	seq := refunds(e)
	body := fmt.Sprintf(`{"seq":%d}`, seq)

	results := make(chan copilot.Explanation, maxCopilotCalls)
	for i := 0; i < maxCopilotCalls; i++ {
		go func() {
			_, b := do(t, srv, "POST", "/v1/copilot/explain", body)
			var ex copilot.Explanation
			_ = json.Unmarshal(b, &ex)
			results <- ex
		}()
	}
	started.Wait()
	if resp, _ := do(t, srv, "POST", "/v1/copilot/explain", body); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a third concurrent call must wait its turn: %d", resp.StatusCode)
	}
	close(release)
	for i := 0; i < maxCopilotCalls; i++ {
		select {
		case ex := <-results:
			if ex.Source != "llm" || ex.Model != "test/model" {
				t.Fatalf("explanation: %+v", ex)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out")
		}
	}
	if resp, body := do(t, srv, "GET", "/v1/copilot", ""); resp.StatusCode != 200 || !strings.Contains(string(body), "test/model") {
		t.Fatalf("info: %s", body)
	}
}
