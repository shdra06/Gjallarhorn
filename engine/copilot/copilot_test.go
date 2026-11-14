package copilot_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/copilot"
	"github.com/shdra06/Gjallarhorn/engine/identity"
	"github.com/shdra06/Gjallarhorn/engine/incident"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
)

// world is the demo company with no email rule yet, so the exfiltration attack gets through.
func world(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.Config{
		Principals: []identity.PrincipalConfig{
			{ID: "human:priya", Kind: identity.KindHuman, Labels: map[string]string{"role": "operator"}, VirtualKeys: []string{"vk-priya"}},
			{ID: "service:customer-chat", Kind: identity.KindService, Labels: map[string]string{"trust": "external"}, VirtualKeys: []string{"vk-chat"}},
			{ID: "agent:support", Kind: identity.KindAgent, VirtualKeys: []string{"vk-support"}, AgentNames: []string{"support"}},
			{ID: "agent:research", Kind: identity.KindAgent, VirtualKeys: []string{"vk-research"}, AgentNames: []string{"research"}},
		},
		Tickets: engine.TicketConfig{ActiveKey: "k", Keys: map[string]string{"k": strings.Repeat("k", 32)}},
		Policy:  &policy.Policy{Version: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

// traffic records normal work and one exfiltration; it returns the seq of the attack's email.
func traffic(e *engine.Engine) uint64 {
	// Normal: priya has research email an internal report.
	r := e.Delegate(engine.Request{Target: "research", VirtualKey: "vk-priya", Label: "benign"})
	e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "email.send", VirtualKey: "vk-priya", Ticket: r.Ticket, Label: "benign"})
	// Normal: a customer asks support, and support emails them an update.
	s := e.Delegate(engine.Request{Target: "support", VirtualKey: "vk-chat", Label: "benign"})
	e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "email.send", VirtualKey: "vk-support", Ticket: s.Ticket, Label: "benign"})
	// Normal: support asks research to read the news.
	s2 := e.Delegate(engine.Request{Target: "support", VirtualKey: "vk-chat", Label: "benign"})
	n := e.Delegate(engine.Request{Target: "research", VirtualKey: "vk-support", Ticket: s2.Ticket, Label: "benign"})
	e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "web.fetch", VirtualKey: "vk-support", Ticket: n.Ticket, Label: "benign"})
	// Attack: a forum post makes research email customer data out, via support.
	a := e.Delegate(engine.Request{Target: "support", VirtualKey: "vk-chat", Label: "attack"})
	b := e.Delegate(engine.Request{Target: "research", VirtualKey: "vk-support", Ticket: a.Ticket, Label: "attack"})
	e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "web.fetch", VirtualKey: "vk-support", Ticket: b.Ticket, Label: "attack"})
	return e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "email.send", VirtualKey: "vk-support", Ticket: b.Ticket, Label: "attack"}).Seq
}

func incidentFor(t *testing.T, e *engine.Engine, seq uint64) (*incident.Incident, []recorder.Event) {
	t.Helper()
	h, err := e.History()
	if err != nil {
		t.Fatal(err)
	}
	in, err := incident.Build(h, seq)
	if err != nil {
		t.Fatal(err)
	}
	return in, h
}

func TestSuggestProvesSafeRulesAndRejectsUnsafeOnes(t *testing.T) {
	e := world(t)
	focus := traffic(e)
	in, h := incidentFor(t, e, focus)
	res := copilot.Suggest(context.Background(), nil, in, e.Policy(), h)

	if len(res.Suggestions) < 4 {
		t.Fatalf("expected several candidates, got %+v", res)
	}
	best := res.Suggestions[0]
	if best.Status != copilot.StatusProven || !best.StopsFocus || best.Collateral != 0 || best.Policy == nil {
		t.Fatalf("the top suggestion must be proven safe: %+v", best)
	}
	rejected := 0
	for _, s := range res.Suggestions {
		if s.Status == copilot.StatusProven {
			if b := s.Labels["benign"]; b.BlockedAfter != 0 {
				t.Fatalf("a proven rule must not block normal work: %+v", s)
			}
		}
		if s.Status == copilot.StatusRejected {
			rejected++
			if s.Policy != nil {
				t.Fatal("a rejected rule must not come with an applicable policy")
			}
		}
	}
	if rejected == 0 {
		t.Fatal("broad candidates (for example every email from external chains) block normal work and must be rejected")
	}
	if !strings.Contains(res.Message, "proven safe") {
		t.Fatalf("message %q", res.Message)
	}

	// Applying the proven policy really stops the attack and keeps normal work going.
	if err := e.SetPolicy(best.Policy, "test"); err != nil {
		t.Fatal(err)
	}
	focus2 := traffic(e)
	h2, _ := e.History()
	var benignBlocked, attackBlocked int
	for _, ev := range h2 {
		if ev.Seq <= focus || ev.Type != recorder.TypeDecision || ev.Decision.Allowed() {
			continue
		}
		if ev.Note["label"] == "benign" {
			benignBlocked++
		} else {
			attackBlocked++
		}
	}
	if benignBlocked != 0 || attackBlocked == 0 {
		t.Fatalf("after applying: benign blocked %d, attack blocked %d", benignBlocked, attackBlocked)
	}
	_ = focus2
}

func TestSuggestWhenAlreadyBlocked(t *testing.T) {
	e := world(t)
	_ = e.SetPolicy(&policy.Policy{Version: 1, Rules: []policy.Rule{{ID: "no-email", Effect: policy.Deny,
		Match: policy.Match{Targets: []string{"email.*"}, Actors: []string{"agent:research"}, OriginLabels: map[string]string{"trust": "external"}}}}}, "t")
	focus := traffic(e)
	in, h := incidentFor(t, e, focus)
	res := copilot.Suggest(context.Background(), nil, in, e.Policy(), h)
	if len(res.Suggestions) != 0 || !strings.Contains(res.Message, "already blocked") {
		t.Fatalf("already blocked: %+v", res)
	}

	// In monitor-only mode the call ran, but the current policy covers it.
	e2 := world(t)
	_ = e2.SetPolicy(e.Policy(), "t")
	e2.SetEnforcing(false, "t")
	focus = traffic(e2)
	in, h = incidentFor(t, e2, focus)
	res = copilot.Suggest(context.Background(), nil, in, e2.Policy(), h)
	if len(res.Suggestions) != 0 || !strings.Contains(res.Message, "monitor-only") {
		t.Fatalf("monitor mode: %+v", res)
	}
}

// fakeModel is an OpenAI-compatible endpoint with a scripted reply.
type fakeModel struct {
	mu      sync.Mutex
	reply   string
	status  int
	prompts []string
	headers http.Header
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.prompts = append(f.prompts, string(b))
	f.headers = r.Header.Clone()
	reply, status := f.reply, f.status
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"model down"}}`))
		return
	}
	out, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": reply}}}})
	_, _ = w.Write(out)
}

func TestSuggestWithModel(t *testing.T) {
	e := world(t)
	focus := traffic(e)
	in, h := incidentFor(t, e, focus)
	fm := &fakeModel{reply: "Here you go:\n```json\n" + `{"rules":[
		{"id":"research-no-external-email","description":"Research may not email for outside chains","effect":"deny",
		 "match":{"planes":["mcp"],"targets":["email.send"],"actors":["agent:research"],"origin_labels":{"trust":"external"}}},
		{"id":"copilot-ban-all-email","effect":"deny","match":{"targets":["email.*"]}},
		{"id":"copilot-bad","effect":"deny","match":{"targetz":["email.*"]}}
	]}` + "\n```"}
	srv := httptest.NewServer(fm)
	defer srv.Close()
	t.Setenv("COPILOT_VK", "sk-bf-copilot")
	llm := copilot.NewLLM(copilot.Config{Endpoint: srv.URL, Model: "test/model", VirtualKey: "env:COPILOT_VK"})

	res := copilot.Suggest(context.Background(), llm, in, e.Policy(), h)
	byID := map[string]copilot.Suggestion{}
	var bad *copilot.Suggestion
	for i, s := range res.Suggestions {
		if s.Source == "llm" {
			byID[s.Rule.ID] = s
			if s.Rule.ID == "" {
				bad = &res.Suggestions[i]
			}
		}
	}
	if s := byID["copilot-research-no-external-email"]; s.Status != copilot.StatusProven {
		t.Fatalf("the model's precise rule should be proven (and its id prefixed): %+v", byID)
	}
	if s := byID["copilot-ban-all-email"]; s.Status != copilot.StatusRejected || !strings.Contains(s.Verdict, "normal work") {
		t.Fatalf("a rule that blocks normal work must be rejected: %+v", s)
	}
	if bad == nil || bad.Status != copilot.StatusRejected || !strings.Contains(bad.Verdict, "fields") {
		t.Fatal("a rule with unknown fields must be rejected")
	}
	if fm.headers.Get("x-bf-vk") != "sk-bf-copilot" {
		t.Fatal("the co-pilot must call Bifrost with its own virtual key")
	}
	if strings.Contains(fm.prompts[0], "vk-") {
		t.Fatal("raw keys must never reach the model")
	}

	fm.status = http.StatusServiceUnavailable
	res = copilot.Suggest(context.Background(), llm, in, e.Policy(), h)
	if res.ModelError == "" || len(res.Suggestions) == 0 || res.Suggestions[0].Status != copilot.StatusProven {
		t.Fatalf("when the model fails, synthesized suggestions still work: %+v", res)
	}
}

func TestExplain(t *testing.T) {
	e := world(t)
	_ = e.SetPolicy(&policy.Policy{Version: 1, Rules: []policy.Rule{{ID: "no-email", Effect: policy.Deny,
		Description: "Research may not email for outside chains",
		Match:       policy.Match{Targets: []string{"email.*"}, Actors: []string{"agent:research"}, OriginLabels: map[string]string{"trust": "external"}}}}}, "t")
	focus := traffic(e)
	in, _ := incidentFor(t, e, focus)

	g := copilot.Explain(context.Background(), nil, in, e.Policy())
	if g.Source != "grounded" || !strings.Contains(g.Title, "BLOCKED by rule no-email") || len(g.Story) != 4 {
		t.Fatalf("grounded explanation: %+v", g)
	}
	if !strings.Contains(g.Why, "service:customer-chat → agent:support → agent:research → email.send") {
		t.Fatalf("why must show the full chain: %s", g.Why)
	}
	if !strings.Contains(strings.Join(g.NextSteps, " "), "Lock agent:research") {
		t.Fatalf("next steps: %v", g.NextSteps)
	}

	fm := &fakeModel{reply: "A customer message reached the research agent through support, and research tried to email data outside. Gjallarhorn blocked it under rule no-email.\x07"}
	srv := httptest.NewServer(fm)
	defer srv.Close()
	llm := copilot.NewLLM(copilot.Config{Endpoint: srv.URL, Model: "test/model"})
	x := copilot.Explain(context.Background(), llm, in, e.Policy())
	if x.Source != "llm" || x.Model != "test/model" || strings.ContainsRune(x.Narrative, '\x07') || len(x.Story) != 4 {
		t.Fatalf("model explanation: %+v", x)
	}
	fm.status = http.StatusBadGateway
	x = copilot.Explain(context.Background(), llm, in, e.Policy())
	if x.Source != "grounded" || x.ModelError == "" {
		t.Fatalf("model failure falls back to grounded: %+v", x)
	}
	if copilot.NewLLM(copilot.Config{}) != nil {
		t.Fatal("no endpoint means no model")
	}
}
