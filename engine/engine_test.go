package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/identity"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
	"github.com/shdra06/Gjallarhorn/engine/replay"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func testConfig() Config {
	return Config{
		Principals: []identity.PrincipalConfig{
			{ID: "human:priya", Kind: identity.KindHuman, Labels: map[string]string{"role": "operator"}, VirtualKeys: []string{"vk-priya"}},
			{ID: "service:customer-chat", Kind: identity.KindService, Labels: map[string]string{"trust": "external"}, VirtualKeys: []string{"vk-chat"}},
			{ID: "agent:support", Kind: identity.KindAgent, VirtualKeys: []string{"vk-support"}, AgentNames: []string{"support"}},
			{ID: "agent:billing", Kind: identity.KindAgent, VirtualKeys: []string{"vk-billing"}, AgentNames: []string{"billing"}},
			{ID: "agent:research", Kind: identity.KindAgent, VirtualKeys: []string{"vk-research"}, AgentNames: []string{"research"}},
		},
		Tickets: TicketConfig{ActiveKey: "k1", Keys: map[string]string{"k1": strings.Repeat("s", 32)}},
		Policy: &policy.Policy{Version: 1, Guards: policy.Guards{MaxDepth: 3}, Rules: []policy.Rule{
			{
				ID: "refunds-need-an-operator", Effect: policy.Deny,
				Description: "Refunds only when a human operator started the chain",
				Match:       policy.Match{Planes: []policy.Plane{policy.PlaneMCP}, Targets: []string{"billing.refund*"}},
				Unless:      &policy.Match{OriginKinds: []identity.Kind{identity.KindHuman}, OriginLabels: map[string]string{"role": "operator"}},
			},
			{
				ID: "research-never-touches-shell", Effect: policy.Deny, Lockdown: true,
				Match: policy.Match{Actors: []string{"agent:research"}, Targets: []string{"shell.*"}},
			},
		}},
	}
}

func newTestEngine(t *testing.T, c *fakeClock) *Engine {
	t.Helper()
	e, err := New(testConfig(), WithClock(c.now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func mustAllow(t *testing.T, what string, r Result) Result {
	t.Helper()
	if !r.Decision.Allowed() {
		t.Fatalf("%s: expected allow, got %s (%s: %s)", what, r.Decision.Effect, r.Decision.Code, r.Decision.Reason)
	}
	return r
}

func mustDeny(t *testing.T, what, code string, r Result) Result {
	t.Helper()
	if r.Decision.Allowed() || r.Decision.Code != code {
		t.Fatalf("%s: expected deny with code %s, got %s %s (%s)", what, code, r.Decision.Effect, r.Decision.Code, r.Decision.Reason)
	}
	return r
}

func chainIDs(f policy.Facts) string {
	ids := make([]string, len(f.Chain))
	for i, p := range f.Chain {
		ids[i] = p.ID
	}
	return strings.Join(ids, " > ")
}

// TestBorrowedRefundIsBlocked is the headline scenario: a customer message tricks the support
// agent into asking the billing agent for a refund. Every single hop is "allowed" on its own;
// only the chain shows the refund was not started by an operator.
func TestBorrowedRefundIsBlocked(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e := newTestEngine(t, c)

	t1 := mustAllow(t, "chat app asks support", e.Delegate(Request{Target: "support", VirtualKey: "vk-chat"}))
	if t1.Ticket == "" || t1.Facts.Target != "agent:support" {
		t.Fatalf("delegation must mint a ticket for agent:support: %+v", t1)
	}
	t2 := mustAllow(t, "support asks billing", e.Delegate(Request{Target: "billing", VirtualKey: "vk-support", Ticket: t1.Ticket}))
	if t2.Facts.Depth != 1 || chainIDs(t2.Facts) != "service:customer-chat > agent:support" {
		t.Fatalf("support's call should carry the chain so far: depth %d, chain %s", t2.Facts.Depth, chainIDs(t2.Facts))
	}

	refund := mustDeny(t, "billing issues the refund", policy.CodeRule,
		e.Check(Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-billing", Ticket: t2.Ticket, Label: "attack"}))
	if refund.Decision.RuleID != "refunds-need-an-operator" {
		t.Fatalf("wrong rule: %+v", refund.Decision)
	}
	if got := chainIDs(refund.Facts); got != "service:customer-chat > agent:support > agent:billing" {
		t.Fatalf("chain %s", got)
	}
	if refund.Facts.Actor.ID != "agent:billing" || refund.Facts.Origin.ID != "service:customer-chat" || refund.Facts.Depth != 2 {
		t.Fatalf("facts %+v", refund.Facts)
	}

	// The same refund started by the operator goes through.
	t3 := mustAllow(t, "priya asks billing", e.Delegate(Request{Target: "billing", VirtualKey: "vk-priya"}))
	mustAllow(t, "operator refund", e.Check(Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-billing", Ticket: t3.Ticket}))
	// And with Bifrost's "forward authentication" the agent carries priya's key: still billing acting.
	fwd := mustAllow(t, "operator refund with a forwarded key",
		e.Check(Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-priya", Ticket: t3.Ticket}))
	if fwd.Facts.Actor.ID != "agent:billing" || fwd.Facts.Credential.ID != "human:priya" {
		t.Fatalf("with a forwarded key the actor is still the agent: %+v", fwd.Facts)
	}
	// Forwarding also happens between agents: support calls research with its own key, and
	// research reuses that forwarded key. The key belongs to the chain, so it is accepted, and
	// research is still the actor.
	t4 := mustAllow(t, "support asks research", e.Delegate(Request{Target: "research", VirtualKey: "vk-support", Ticket: t1.Ticket}))
	viaAgent := mustAllow(t, "research uses support's forwarded key",
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-support", Ticket: t4.Ticket}))
	if viaAgent.Facts.Actor.ID != "agent:research" || viaAgent.Facts.Credential.ID != "agent:support" {
		t.Fatalf("actor must be the ticket holder: %+v", viaAgent.Facts)
	}
}

func TestStolenOrForgedTickets(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e := newTestEngine(t, c)
	t1 := e.Delegate(Request{Target: "support", VirtualKey: "vk-chat"})
	t2 := e.Delegate(Request{Target: "billing", VirtualKey: "vk-support", Ticket: t1.Ticket})

	r := mustDeny(t, "research uses billing's ticket", policy.CodeTicket,
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-research", Ticket: t2.Ticket}))
	if r.Facts.TicketProblem != "ticket_mismatch" {
		t.Fatalf("problem %q", r.Facts.TicketProblem)
	}
	mustDeny(t, "ticket with someone else's human key", policy.CodeTicket,
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-priya", Ticket: t2.Ticket}))
	mustDeny(t, "ticket with an unregistered key", policy.CodeTicket,
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "who-knows", Ticket: t2.Ticket}))
	mustDeny(t, "garbage ticket", policy.CodeTicket,
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-billing", Ticket: "gh1.bad.bad"}))
	mustDeny(t, "ticket bound to another task", policy.CodeTicket,
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-billing", Ticket: e.Delegate(Request{Target: "billing", VirtualKey: "vk-priya", TaskID: "task-1"}).Ticket, TaskID: "task-2"}))

	c.t = c.t.Add(11 * time.Minute)
	r = mustDeny(t, "expired ticket", policy.CodeTicket,
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-billing", Ticket: t2.Ticket}))
	if r.Facts.TicketProblem != "ticket_expired" {
		t.Fatalf("problem %q", r.Facts.TicketProblem)
	}
}

func TestLockdownLocksTheAgentNotThePeople(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e := newTestEngine(t, c)
	t1 := e.Delegate(Request{Target: "support", VirtualKey: "vk-chat"})
	t2 := e.Delegate(Request{Target: "billing", VirtualKey: "vk-support", Ticket: t1.Ticket})
	direct := e.Delegate(Request{Target: "billing", VirtualKey: "vk-priya"})

	if _, err := e.Lock("agent:support", "prompt injection suspected", "human:priya", false, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if !e.IsLocked("agent:support") || len(e.Lockdowns()) != 1 {
		t.Fatal("lockdown not active")
	}
	mustDeny(t, "support's own model call", policy.CodeLockdown,
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-support"}))
	mustDeny(t, "support's model call with a forwarded human key", policy.CodeLockdown,
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-chat", Ticket: t1.Ticket}))
	mustDeny(t, "billing working for a chain that includes support", policy.CodeLockdown,
		e.Check(Request{Plane: policy.PlaneMCP, Target: "orders.lookup", VirtualKey: "vk-billing", Ticket: t2.Ticket}))
	mustDeny(t, "anyone calling support", policy.CodeLockdown,
		e.Delegate(Request{Target: "support", VirtualKey: "vk-priya"}))
	mustAllow(t, "priya herself", e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-priya"}))
	mustAllow(t, "billing working for priya", e.Check(Request{Plane: policy.PlaneMCP, Target: "orders.lookup", VirtualKey: "vk-billing", Ticket: direct.Ticket}))

	if ok, err := e.Unlock("agent:support", "human:priya"); !ok || err != nil {
		t.Fatalf("unlock: %v %v", ok, err)
	}
	mustAllow(t, "support after unlock", e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-support"}))
	if _, err := e.Lock("not a principal", "", "", false, "", time.Time{}); err == nil {
		t.Fatal("invalid principal must be rejected")
	}
}

func TestRuleCanTriggerLockdown(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e := newTestEngine(t, c)
	r := mustDeny(t, "research tries the shell", policy.CodeRule,
		e.Check(Request{Plane: policy.PlaneMCP, Target: "shell.exec", VirtualKey: "vk-research"}))
	if !r.Locked || !e.IsLocked("agent:research") {
		t.Fatal("the rule asks for a lockdown")
	}
	entry := e.Lockdowns()[0]
	if !entry.Auto || entry.RuleID != "research-never-touches-shell" || entry.By != "rule:research-never-touches-shell" {
		t.Fatalf("auto lockdown entry %+v", entry)
	}
	mustDeny(t, "research is now cut off everywhere", policy.CodeLockdown,
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-research"}))
}

func TestLoopAndDepthGuards(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e := newTestEngine(t, c)
	t1 := e.Delegate(Request{Target: "support", VirtualKey: "vk-priya"})
	t2 := e.Delegate(Request{Target: "billing", VirtualKey: "vk-support", Ticket: t1.Ticket})
	mustDeny(t, "billing asks support back", policy.CodeLoop,
		e.Delegate(Request{Target: "support", VirtualKey: "vk-billing", Ticket: t2.Ticket}))
	t3 := mustAllow(t, "billing asks research (depth 3)", e.Delegate(Request{Target: "research", VirtualKey: "vk-billing", Ticket: t2.Ticket}))
	mustDeny(t, "research asks a fourth agent", policy.CodeMaxDepth,
		e.Delegate(Request{Target: "lawyer", VirtualKey: "vk-research", Ticket: t3.Ticket}))
}

func TestGrants(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e := newTestEngine(t, c)
	t1 := e.Delegate(Request{Target: "billing", VirtualKey: "vk-chat"})
	req := Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-billing", Ticket: t1.Ticket}
	mustDeny(t, "before the grant", policy.CodeRule, e.Check(req))

	g, err := e.AddGrant(policy.Grant{Actor: "agent:billing", Plane: policy.PlaneMCP, Target: "billing.refund",
		ExpiresAt: c.t.Add(5 * time.Minute), CreatedBy: "human:priya", Reason: "approved goodwill refund"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(g.ID, "grant-") || len(e.Grants()) != 1 {
		t.Fatalf("grant %+v", g)
	}
	mustAllow(t, "with the grant", e.Check(req))
	c.t = c.t.Add(6 * time.Minute)
	if len(e.Grants()) != 0 {
		t.Fatal("expired grants must not be listed")
	}
	if _, err := e.AddGrant(policy.Grant{Actor: "agent:billing", Plane: policy.PlaneMCP, Target: "x", ExpiresAt: c.t.Add(-time.Minute)}); err == nil {
		t.Fatal("a grant that is already expired must be rejected")
	}
	g2, _ := e.AddGrant(policy.Grant{ID: "g2", Actor: "agent:billing", Plane: policy.PlaneMCP, Target: "x", ExpiresAt: c.t.Add(time.Hour)})
	if _, err := e.AddGrant(g2); err == nil {
		t.Fatal("duplicate grant id must be rejected")
	}
	if !e.RevokeGrant("g2", "human:priya") || e.RevokeGrant("g2", "human:priya") {
		t.Fatal("revoke should succeed once")
	}
}

func TestPolicyChangesAndSimulation(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e := newTestEngine(t, c)
	t1 := e.Delegate(Request{Target: "billing", VirtualKey: "vk-chat", Label: "attack"})
	e.Check(Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-billing", Ticket: t1.Ticket, Label: "attack"})
	e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-priya", Label: "benign"})

	// What if we dropped all rules?
	rep, err := e.Simulate(&policy.Policy{Version: 1}, replay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.NewlyAllowed) != 1 || rep.NewlyAllowed[0].Facts.Target != "billing.refund" {
		t.Fatalf("simulation %+v", rep)
	}
	if _, err := e.Simulate(&policy.Policy{Version: 9}, replay.Options{}); err == nil {
		t.Fatal("an invalid candidate must be rejected")
	}

	if err := e.SetPolicy(&policy.Policy{Version: 2}, "human:priya"); err == nil {
		t.Fatal("invalid policy must be rejected")
	}
	before := PolicyHash(e.Policy())
	if err := e.SetPolicy(&policy.Policy{Version: 1, DefaultEffect: policy.Deny}, "human:priya"); err != nil {
		t.Fatal(err)
	}
	if PolicyHash(e.Policy()) == before {
		t.Fatal("policy hash should change")
	}
	mustDeny(t, "default deny now applies", policy.CodeDefault, e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-priya"}))

	var types []string
	for _, ev := range e.Recorder().Recent(0) {
		types = append(types, ev.Type)
	}
	if !strings.Contains(strings.Join(types, ","), recorder.TypePolicy) {
		t.Fatalf("policy change must be recorded: %v", types)
	}
}

func TestFileBackedEngineSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig()
	cfg.Recorder.Path = filepath.Join(dir, "flight.jsonl")
	cfg.LockdownFile = filepath.Join(dir, "lockdowns.json")
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.Check(Request{ID: "r1", Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-support"})
	e.RecordOutcome("r1", recorder.Outcome{Status: "ok", LatencyMS: 120, Tokens: 300})
	if _, err := e.Lock("agent:support", "test", "human:priya", false, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	hist, err := e.History()
	if err != nil || len(hist) != 3 {
		t.Fatalf("history before close: %d %v", len(hist), err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if !e2.IsLocked("agent:support") {
		t.Fatal("lockdown must survive a restart")
	}
	mustDeny(t, "after restart", policy.CodeLockdown, e2.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-support"}))
	f, _ := os.Open(cfg.Recorder.Path)
	defer f.Close()
	if n, err := recorder.Verify(f); err != nil || n < 3 {
		t.Fatalf("log after restart: %d %v", n, err)
	}
}

func TestRawKeysAreNeverRecorded(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig()
	cfg.Recorder.Path = filepath.Join(dir, "flight.jsonl")
	e, _ := New(cfg)
	t1 := e.Delegate(Request{Target: "support", VirtualKey: "vk-chat"})
	e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-support", Ticket: t1.Ticket})
	e.Close()
	data, _ := os.ReadFile(cfg.Recorder.Path)
	for _, secret := range []string{"vk-chat", "vk-support", t1.Ticket} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("the flight recorder leaked %q", secret)
		}
	}
}

func TestConfig(t *testing.T) {
	t.Setenv("GJ_TICKET_KEY", strings.Repeat("e", 40))
	raw := `{
		"principals": [{"id": "agent:a", "kind": "agent", "virtual_keys": ["k"]}],
		"tickets": {"active_key": "k1", "keys": {"k1": "env:GJ_TICKET_KEY"}, "ttl": "5m", "header": "X-Chain"},
		"policy": {"version": 1}
	}`
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.TicketHeader() != "x-chain" {
		t.Fatalf("header %q", e.TicketHeader())
	}
	if _, err := ParseConfig([]byte(`{"principles": []}`)); err == nil {
		t.Fatal("unknown config fields must be rejected")
	}
	if _, err := ConfigFromAny(map[string]any{"tickets": map[string]any{"active_key": "k", "keys": map[string]any{"k": strings.Repeat("x", 32)}}}); err != nil {
		t.Fatalf("config from map: %v", err)
	}
	bad := []Config{
		{},
		{Tickets: TicketConfig{ActiveKey: "k1", Keys: map[string]string{"k1": "env:GJ_NOT_SET"}}},
		{Tickets: TicketConfig{ActiveKey: "k1", Keys: map[string]string{"k1": strings.Repeat("s", 32)}, TTL: Duration{48 * time.Hour}}},
		{Tickets: TicketConfig{ActiveKey: "k1", Keys: map[string]string{"k1": "short"}}},
		{Tickets: TicketConfig{ActiveKey: "k1", Keys: map[string]string{"k1": strings.Repeat("s", 32)}}, Policy: &policy.Policy{Version: 3}},
	}
	for i, b := range bad {
		if _, err := New(b); err == nil {
			t.Errorf("config %d should be rejected", i)
		}
	}
}

func BenchmarkCheckDelegatedCall(b *testing.B) {
	e, _ := New(testConfig())
	defer e.Close()
	t1 := e.Delegate(Request{Target: "support", VirtualKey: "vk-chat"})
	t2 := e.Delegate(Request{Target: "billing", VirtualKey: "vk-support", Ticket: t1.Ticket})
	req := Request{Plane: policy.PlaneMCP, Target: "orders.lookup", VirtualKey: "vk-billing", Ticket: t2.Ticket}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = e.Check(req)
	}
}

func BenchmarkCheckDirectCall(b *testing.B) {
	e, _ := New(testConfig())
	defer e.Close()
	req := Request{Plane: policy.PlaneLLM, Target: "openai/gpt-4o-mini", VirtualKey: "vk-support"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = e.Check(req)
	}
}

// BenchmarkCheckDelegatedLatency measures one decision as a request sees it under normal load:
// the flight recorder seals events in the background (off the timer here).
func BenchmarkCheckDelegatedLatency(b *testing.B) {
	e, _ := New(testConfig())
	defer e.Close()
	t1 := e.Delegate(Request{Target: "support", VirtualKey: "vk-chat"})
	t2 := e.Delegate(Request{Target: "billing", VirtualKey: "vk-support", Ticket: t1.Ticket})
	req := Request{Plane: policy.PlaneMCP, Target: "orders.lookup", VirtualKey: "vk-billing", Ticket: t2.Ticket}
	const burst = 512
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.Check(req)
		if i%burst == burst-1 {
			b.StopTimer()
			_ = e.Recorder().Stats()
			b.StartTimer()
		}
	}
}
