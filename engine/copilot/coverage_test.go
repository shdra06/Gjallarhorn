package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/identity"
	"github.com/shdra06/Gjallarhorn/engine/incident"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
)

func newEngine(t *testing.T, pol *policy.Policy) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.Config{
		Principals: []identity.PrincipalConfig{
			{ID: "human:priya", Kind: identity.KindHuman, Labels: map[string]string{"role": "operator"}, VirtualKeys: []string{"vk-priya"}},
			{ID: "agent:support", Kind: identity.KindAgent, VirtualKeys: []string{"vk-support"}, AgentNames: []string{"support"}},
			{ID: "agent:research", Kind: identity.KindAgent, VirtualKeys: []string{"vk-research"}, AgentNames: []string{"research"}},
		},
		Tickets: engine.TicketConfig{ActiveKey: "k", Keys: map[string]string{"k": strings.Repeat("k", 32)}},
		Policy:  pol,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func build(t *testing.T, e *engine.Engine, seq uint64) (*incident.Incident, []recorder.Event) {
	t.Helper()
	h, _ := e.History()
	in, err := incident.Build(h, seq)
	if err != nil {
		t.Fatal(err)
	}
	return in, h
}

var shellRule = policy.Rule{ID: "no-shell", Effect: policy.Deny, Lockdown: true,
	Match: policy.Match{Planes: []policy.Plane{policy.PlaneMCP}, Targets: []string{"shell.*"}, ActorKinds: []identity.Kind{identity.KindAgent}}}

func TestGroundedCoversEveryOutcome(t *testing.T) {
	e := newEngine(t, &policy.Policy{Version: 1, Guards: policy.Guards{MaxDepth: 1}, Rules: []policy.Rule{shellRule}})

	// A rule block that also locks the agent.
	r := e.Delegate(engine.Request{Target: "research", VirtualKey: "vk-priya"})
	e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "mock/model", VirtualKey: "vk-research", Ticket: r.Ticket})
	shell := e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "shell.exec", VirtualKey: "vk-research", Ticket: r.Ticket})
	in, _ := build(t, e, shell.Seq)
	g := Grounded(in, e.Policy())
	joined := strings.Join(g.Story, "\n")
	if !strings.Contains(joined, "called the model mock/model") || !strings.Contains(joined, "was locked down") {
		t.Fatalf("story: %s", joined)
	}
	for _, s := range g.NextSteps {
		if strings.HasPrefix(s, "Lock agent:research") {
			t.Fatal("no need to suggest locking an agent that is already locked")
		}
	}
	if !strings.Contains(g.NextSteps[len(g.NextSteps)-1], "human:priya asked for in step 1") {
		t.Fatalf("next steps: %v", g.NextSteps)
	}

	// A guard (not a rule) stops a chain that is too deep.
	_, _ = e.Unlock("agent:research", "t")
	s1 := e.Delegate(engine.Request{Target: "support", VirtualKey: "vk-priya"})
	deep := e.Delegate(engine.Request{Target: "research", VirtualKey: "vk-support", Ticket: s1.Ticket})
	in, _ = build(t, e, deep.Seq)
	g = Grounded(in, e.Policy())
	if !strings.Contains(g.Title, "BLOCKED (max_depth)") || !strings.Contains(g.Why, "built-in guard (max_depth)") {
		t.Fatalf("guard: %s / %s", g.Title, g.Why)
	}

	// In monitor-only mode a flagged call ran: high risk, switch to enforcing.
	e.SetEnforcing(false, "t")
	flag := e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "shell.exec", VirtualKey: "vk-support"})
	in, _ = build(t, e, flag.Seq)
	g = Grounded(in, e.Policy())
	if !strings.Contains(g.Title, "monitor-only mode, but flagged by no-shell") || !strings.HasPrefix(g.Risk, "High") ||
		!strings.Contains(strings.Join(g.NextSteps, " "), "Switch Gjallarhorn to enforcing") {
		t.Fatalf("monitor: %+v", g)
	}

	// An allowed call names the default effect.
	ok := e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "mock/model", VirtualKey: "vk-priya"})
	in, _ = build(t, e, ok.Seq)
	if g := Grounded(in, &policy.Policy{Version: 1, DefaultEffect: policy.Deny}); !strings.Contains(g.Why, "the default (deny)") {
		t.Fatalf("default: %s", g.Why)
	}
	if defaultEffect(nil) != policy.Allow {
		t.Fatal("no policy means allow")
	}
}

func TestExplainRejectsUselessModelAnswers(t *testing.T) {
	e := newEngine(t, &policy.Policy{Version: 1})
	ok := e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "mock/model", VirtualKey: "vk-priya"})
	in, _ := build(t, e, ok.Seq)
	for _, reply := range []string{"ok", strings.Repeat("long ", 500)} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + reply + `"}}]}`))
		}))
		x := Explain(context.Background(), NewLLM(Config{Endpoint: srv.URL, Model: "m"}), in, e.Policy())
		srv.Close()
		if x.Source != "grounded" || !strings.Contains(x.ModelError, "empty or too long") {
			t.Fatalf("%d chars: %+v", len(reply), x)
		}
	}
}

func TestLLMErrors(t *testing.T) {
	var l *LLM
	if _, err := l.Complete(context.Background(), "s", "u", false); err != ErrNoModel {
		t.Fatal("a nil client has no model")
	}
	cases := map[string]string{
		`not json`:         "unexpected model response",
		`{"choices":[]}`:   "unexpected model response",
		`{"error":"oops"}`: "unexpected model response",
	}
	for body, want := range cases {
		var auth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(body))
		}))
		t.Setenv("CP_KEY", "sk-test")
		_, err := NewLLM(Config{Endpoint: srv.URL + "/", Model: "m", APIKey: "env:CP_KEY", TimeoutSeconds: 5}).Complete(context.Background(), "s", "u", true)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", body, err)
		}
		if auth != "Bearer sk-test" {
			t.Fatalf("api keys go in a bearer header: %q", auth)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	if _, err := NewLLM(Config{Endpoint: url, Model: "m"}).Complete(context.Background(), "s", "u", false); err == nil || !strings.Contains(err.Error(), "model call failed") {
		t.Fatalf("unreachable model: %v", err)
	}
	if _, err := NewLLM(Config{Endpoint: "http://bad host", Model: "m"}).Complete(context.Background(), "s", "u", false); err == nil {
		t.Fatal("a bad URL is an error")
	}
	if truncate("abcdef", 3) != "abc…" || truncate("ab", 3) != "ab" {
		t.Fatal("truncate")
	}
	if extractJSON("no json here") != "" || extractJSON(`x {"a":1} y`) != `{"a":1}` {
		t.Fatal("extractJSON")
	}
}

func TestHelpers(t *testing.T) {
	if got := slug(strings.Repeat("Agent Name! ", 10)); len(got) > 40 || strings.HasSuffix(got, "-") {
		t.Fatalf("slug: %q", got)
	}
	if family("billing.refund") != "billing.*" || family("mock/model") != "mock/*" || family("plain") != "" {
		t.Fatal("family")
	}
	if labelOr("") != "unlabelled" || labelOr("attack") != "attack" {
		t.Fatal("labelOr")
	}
	if reasonKey(policy.Decision{Code: "loop"}) != "loop" || reasonKey(policy.Decision{Code: "rule", RuleID: "r"}) != "rule r" {
		t.Fatal("reasonKey")
	}
	one, two := 1, 2
	r := policy.Rule{Match: policy.Match{Targets: []string{"a"}, MinDepth: &one, MaxDepth: &two, Delegated: boolPtr(true)},
		Unless: &policy.Match{OriginKinds: []identity.Kind{identity.KindHuman}}}
	if conditions(r) != 5 {
		t.Fatalf("conditions: %d", conditions(r))
	}
}

func TestProveAgainAtApplyTime(t *testing.T) {
	e := newEngine(t, &policy.Policy{Version: 1, Rules: []policy.Rule{{ID: "taken", Effect: policy.Deny, Match: policy.Match{Targets: []string{"nothing.matches"}}}}})
	// Normal work: priya's research emails a report. Then research emails on its own.
	r := e.Delegate(engine.Request{Target: "research", VirtualKey: "vk-priya", Label: "benign"})
	e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "email.send", VirtualKey: "vk-research", Ticket: r.Ticket, Label: "benign"})
	bad := e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "email.send", VirtualKey: "vk-research", Label: "attack"})
	in, h := build(t, e, bad.Seq)

	direct := policy.Rule{ID: "taken", Effect: policy.Deny, Match: policy.Match{Actors: []string{"agent:research"}, Targets: []string{"email.send"}, Delegated: boolPtr(false)}}
	s := Prove(in, e.Policy(), h, direct)
	if s.Status != StatusProven || s.Rule.ID != "taken-2" || s.Policy == nil {
		t.Fatalf("a proven rule gets a free id: %+v", s)
	}
	broad := policy.Rule{ID: "copilot-all-email", Effect: policy.Deny, Match: policy.Match{Targets: []string{"email.*"}}}
	if s := Prove(in, e.Policy(), h, broad); s.Status != StatusRejected || !strings.Contains(s.Verdict, "normal work") || s.Policy != nil {
		t.Fatalf("broad: %+v", s)
	}
	invalid := policy.Rule{ID: "BAD ID", Effect: "maybe"}
	if s := Prove(in, e.Policy(), h, invalid); s.Status != StatusRejected || !strings.HasPrefix(s.Verdict, "invalid rule") {
		t.Fatalf("invalid: %+v", s)
	}
	miss := policy.Rule{ID: "copilot-miss", Effect: policy.Deny, Match: policy.Match{Targets: []string{"web.fetch"}}}
	if s := Prove(in, e.Policy(), h, miss); s.Status != StatusRejected || !strings.Contains(s.Verdict, "does not stop") {
		t.Fatalf("miss: %+v", s)
	}
	_ = e.SetPolicy(s.Policy, "t")
	applied := Prove(in, e.Policy(), h, direct)
	if applied.Status != StatusRejected {
		t.Fatal("the same rule cannot be proven twice")
	}
	_ = e.SetPolicy(&policy.Policy{Version: 1, Rules: []policy.Rule{broad}}, "t")
	if s := Prove(in, e.Policy(), h, direct); s.Status != StatusRejected || !strings.Contains(s.Verdict, "already blocks") {
		t.Fatalf("already blocked: %+v", s)
	}
}

func TestSuggestEdges(t *testing.T) {
	e := newEngine(t, &policy.Policy{Version: 1})
	ok := e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "plain", VirtualKey: "vk-research", Label: "attack"})
	in, h := build(t, e, ok.Seq)
	// The focus is missing from the history given.
	if res := Suggest(context.Background(), nil, in, e.Policy(), h[:0]); !strings.Contains(res.Message, "not in the recorded history") {
		t.Fatalf("missing: %+v", res)
	}
	// A direct call with no family: one actor-only rule, proven.
	res := Suggest(context.Background(), nil, in, e.Policy(), h)
	if len(res.Suggestions) != 1 || res.Suggestions[0].Status != StatusProven {
		t.Fatalf("direct: %+v", res)
	}

	// The model returns more than three rules, one without the copilot- prefix, then garbage.
	reply := `{"rules":[` + strings.Repeat(`{"id":"x","effect":"deny","match":{"targets":["plain"]}},`, 4) + `{"id":"y","effect":"deny","match":{"targets":["plain"]}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + quote(reply) + `}}]}`))
	}))
	defer srv.Close()
	llm := NewLLM(Config{Endpoint: srv.URL, Model: "m", TimeoutSeconds: 5})
	got, err := llmCandidates(context.Background(), llm, in, *h[len(h)-1].Facts, e.Policy())
	if err != nil || len(got) != 3 || got[0].Rule.ID != "copilot-x" {
		t.Fatalf("model rules: %+v %v", got, err)
	}
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"sorry, no"}}]}`))
	}))
	defer garbage.Close()
	if _, err := llmCandidates(context.Background(), NewLLM(Config{Endpoint: garbage.URL, Model: "m"}), in, *h[len(h)-1].Facts, e.Policy()); err == nil {
		t.Fatal("a reply without JSON is an error")
	}
	_ = time.Second
}

func quote(s string) string {
	b := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
	return `"` + b + `"`
}
