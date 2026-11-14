package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/identity"
)

var (
	priya    = identity.Principal{ID: "human:priya", Kind: identity.KindHuman, Labels: map[string]string{"role": "operator"}}
	chatApp  = identity.Principal{ID: "service:customer-chat", Kind: identity.KindService, Labels: map[string]string{"trust": "external"}}
	support  = identity.Principal{ID: "agent:support", Kind: identity.KindAgent}
	billing  = identity.Principal{ID: "agent:billing", Kind: identity.KindAgent}
	stranger = identity.Principal{ID: "unknown:abc123abc123", Kind: identity.KindUnknown}
	t0       = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
)

// call builds facts for a call made by the last principal in chain.
func call(plane Plane, target string, chain ...identity.Principal) Facts {
	return Facts{Time: t0, Plane: plane, Target: target, Actor: chain[len(chain)-1], Origin: chain[0],
		Chain: chain, Depth: len(chain) - 1, Credential: chain[len(chain)-1]}
}

func intp(i int) *int    { return &i }
func boolp(b bool) *bool { return &b }

func TestGlob(t *testing.T) {
	cases := []struct {
		p, s string
		want bool
	}{
		{"*", "", true},
		{"*", "anything/at:all", true},
		{"billing.*", "billing.refund", true},
		{"billing.*", "billing", false},
		{"billing.refund*", "billing.refund_order", true},
		{"agent:*", "agent:support", true},
		{"agent:*", "human:priya", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"*refund*", "payments.refund.create", true},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
		{"exact", "exact", true},
		{"exact", "exactly", false},
		{"*.*.*", "a.b", false},
		{"**", "x", true},
	}
	for _, c := range cases {
		if got := Glob(c.p, c.s); got != c.want {
			t.Errorf("Glob(%q, %q) = %v, want %v", c.p, c.s, got, c.want)
		}
	}
	if !AnyGlob(nil, "x") || AnyGlob([]string{"a", "b"}, "c") || !AnyGlob([]string{"a", "c*"}, "cat") {
		t.Error("AnyGlob")
	}
}

func TestGlobHostilePatternIsFast(t *testing.T) {
	p := strings.Repeat("*a", 100) + "b"
	s := strings.Repeat("a", 5000)
	start := time.Now()
	if Glob(p, s) {
		t.Fatal("should not match")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("hostile glob took %v", d)
	}
}

func TestMatch(t *testing.T) {
	refund := call(PlaneMCP, "billing.refund", chatApp, support, billing)
	cases := []struct {
		name string
		m    Match
		f    Facts
		want bool
	}{
		{"empty matches all", Match{}, refund, true},
		{"plane", Match{Planes: []Plane{PlaneLLM}}, refund, false},
		{"target glob", Match{Targets: []string{"billing.*"}}, refund, true},
		{"actor", Match{Actors: []string{"agent:billing"}}, refund, true},
		{"actor kind", Match{ActorKinds: []identity.Kind{identity.KindHuman}}, refund, false},
		{"origin kind", Match{OriginKinds: []identity.Kind{identity.KindService}}, refund, true},
		{"origin label", Match{OriginLabels: map[string]string{"trust": "external"}}, refund, true},
		{"origin label any", Match{OriginLabels: map[string]string{"trust": "*"}}, refund, true},
		{"origin label absent", Match{OriginLabels: map[string]string{"role": "!"}}, refund, true},
		{"origin label wrong", Match{OriginLabels: map[string]string{"role": "operator"}}, refund, false},
		{"via", Match{Via: []string{"agent:support"}}, refund, true},
		{"via excludes actor", Match{Via: []string{"agent:billing"}}, refund, false},
		{"via direct call", Match{Via: []string{"*"}}, call(PlaneLLM, "m", support), false},
		{"delegated", Match{Delegated: boolp(true)}, refund, true},
		{"not delegated", Match{Delegated: boolp(false)}, refund, false},
		{"min depth", Match{MinDepth: intp(3)}, refund, false},
		{"max depth", Match{MaxDepth: intp(2)}, refund, true},
	}
	for _, c := range cases {
		if got := c.m.Matches(c.f); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

type locks map[string]bool

func (l locks) IsLocked(id string, _ time.Time) bool { return l[id] }

func demoPolicy() *Policy {
	return &Policy{
		Version:       1,
		DefaultEffect: Allow,
		Guards:        Guards{MaxDepth: 3},
		Rules: []Rule{
			{
				ID:          "refunds-need-an-operator",
				Description: "Refunds only when a human operator started the chain",
				Effect:      Deny,
				Match:       Match{Planes: []Plane{PlaneMCP}, Targets: []string{"billing.refund*"}},
				Unless:      &Match{OriginKinds: []identity.Kind{identity.KindHuman}, OriginLabels: map[string]string{"role": "operator"}},
			},
			{
				ID:     "watch-external-delegation",
				Effect: Deny,
				Mode:   Monitor,
				Match:  Match{OriginLabels: map[string]string{"trust": "external"}, Delegated: boolp(true)},
			},
			{
				ID:       "no-shell-for-support",
				Effect:   Deny,
				Lockdown: true,
				Match:    Match{Actors: []string{"agent:support"}, Targets: []string{"shell.*"}},
			},
			{
				ID:     "allow-billing-reads",
				Effect: Allow,
				Match:  Match{Targets: []string{"billing.read*"}},
			},
		},
	}
}

func TestEvaluate(t *testing.T) {
	p := demoPolicy()
	cases := []struct {
		name      string
		f         Facts
		in        Input
		effect    Effect
		code      string
		rule      string
		monitored []string
		lock      bool
	}{
		{name: "borrowed refund is blocked",
			f: call(PlaneMCP, "billing.refund", chatApp, support, billing), effect: Deny, code: CodeRule,
			rule: "refunds-need-an-operator", monitored: []string{"watch-external-delegation"}},
		{name: "operator refund is allowed",
			f: call(PlaneMCP, "billing.refund", priya, billing), effect: Allow, code: CodeDefault},
		{name: "allow rule",
			f: call(PlaneMCP, "billing.read_invoice", priya, billing), effect: Allow, code: CodeRule, rule: "allow-billing-reads"},
		{name: "deny wins over allow and asks for lockdown",
			f: call(PlaneMCP, "shell.exec", support), effect: Deny, code: CodeRule, rule: "no-shell-for-support", lock: true},
		{name: "locked actor",
			f: call(PlaneLLM, "openai/gpt-4o", support), in: Input{Locks: locks{"agent:support": true}}, effect: Deny, code: CodeLockdown},
		{name: "locked agent upstream in chain",
			f: call(PlaneMCP, "billing.read_invoice", priya, support, billing), in: Input{Locks: locks{"agent:support": true}}, effect: Deny, code: CodeLockdown},
		{name: "calling a locked agent",
			f: call(PlaneA2A, "agent:billing", priya, support), in: Input{Locks: locks{"agent:billing": true}}, effect: Deny, code: CodeLockdown},
		{name: "locked target given as bare name",
			f: call(PlaneA2A, "billing", priya, support), in: Input{Locks: locks{"agent:billing": true}}, effect: Deny, code: CodeLockdown},
		{name: "bad ticket",
			f:      func() Facts { f := call(PlaneLLM, "m", support); f.TicketProblem = "ticket_bad_signature"; return f }(),
			effect: Deny, code: CodeTicket},
		{name: "loop back to an agent in the chain",
			f: call(PlaneA2A, "agent:support", priya, support, billing), effect: Deny, code: CodeLoop},
		{name: "self call is a loop",
			f: call(PlaneA2A, "agent:support", support), effect: Deny, code: CodeLoop},
		{name: "too deep",
			f: call(PlaneA2A, "agent:d", priya, support, billing, identity.Principal{ID: "agent:c", Kind: identity.KindAgent}), effect: Deny, code: CodeMaxDepth},
		{name: "at the depth limit is fine",
			f: call(PlaneMCP, "x.y", priya, support, billing, identity.Principal{ID: "agent:c", Kind: identity.KindAgent}), effect: Allow},
		{name: "grant overrides a rule",
			f:      call(PlaneMCP, "billing.refund", chatApp, support, billing),
			in:     Input{Grants: []Grant{{ID: "g1", Actor: "agent:billing", Plane: PlaneMCP, Target: "billing.refund", ExpiresAt: t0.Add(time.Minute)}}},
			effect: Allow, code: CodeGrant, rule: "g1"},
		{name: "expired grant does nothing",
			f:      call(PlaneMCP, "billing.refund", chatApp, support, billing),
			in:     Input{Grants: []Grant{{ID: "g1", Actor: "agent:billing", Plane: PlaneMCP, Target: "billing.refund", ExpiresAt: t0}}},
			effect: Deny, code: CodeRule},
		{name: "grant never beats a lockdown",
			f:      call(PlaneMCP, "billing.refund", priya, billing),
			in:     Input{Locks: locks{"agent:billing": true}, Grants: []Grant{{ID: "g1", Actor: "agent:billing", Plane: PlaneMCP, Target: "*", ExpiresAt: t0.Add(time.Hour)}}},
			effect: Deny, code: CodeLockdown},
	}
	for _, c := range cases {
		d := Evaluate(p, c.f, c.in)
		if d.Effect != c.effect {
			t.Errorf("%s: effect %s, want %s (%+v)", c.name, d.Effect, c.effect, d)
			continue
		}
		if c.code != "" && d.Code != c.code {
			t.Errorf("%s: code %s, want %s (%s)", c.name, d.Code, c.code, d.Reason)
		}
		if d.RuleID != c.rule && c.rule != "" {
			t.Errorf("%s: rule %q, want %q", c.name, d.RuleID, c.rule)
		}
		if strings.Join(d.Monitored, ",") != strings.Join(c.monitored, ",") && c.monitored != nil {
			t.Errorf("%s: monitored %v, want %v", c.name, d.Monitored, c.monitored)
		}
		if d.LockActor != c.lock {
			t.Errorf("%s: lock actor %v, want %v", c.name, d.LockActor, c.lock)
		}
		if d.Reason == "" {
			t.Errorf("%s: decision has no reason", c.name)
		}
	}
}

func TestGuards(t *testing.T) {
	strict := &Policy{Version: 1, Guards: Guards{UnknownCallers: Deny}}
	if d := Evaluate(strict, call(PlaneLLM, "m", stranger), Input{}); d.Code != CodeUnknownCaller {
		t.Errorf("unknown caller: %+v", d)
	}
	if d := Evaluate(Default(), call(PlaneLLM, "m", stranger), Input{}); !d.Allowed() {
		t.Errorf("unknown callers are allowed by default: %+v", d)
	}
	loose := &Policy{Version: 1, Guards: Guards{AllowLoops: true, AcceptBadTickets: true}}
	if d := Evaluate(loose, call(PlaneA2A, "agent:support", priya, support, billing), Input{}); !d.Allowed() {
		t.Errorf("loops allowed: %+v", d)
	}
	f := call(PlaneLLM, "m", support)
	f.TicketProblem = "ticket_expired"
	if d := Evaluate(loose, f, Input{}); !d.Allowed() {
		t.Errorf("bad tickets accepted: %+v", d)
	}
	deny := &Policy{Version: 1, DefaultEffect: Deny}
	if d := Evaluate(deny, call(PlaneLLM, "m", priya), Input{}); d.Allowed() || d.Code != CodeDefault {
		t.Errorf("default deny: %+v", d)
	}
	if d := Evaluate(nil, call(PlaneLLM, "m", priya), Input{}); !d.Allowed() {
		t.Errorf("nil policy behaves as default: %+v", d)
	}
}

func TestDisabledAndMonitorRules(t *testing.T) {
	p := &Policy{Version: 1, Rules: []Rule{
		{ID: "off", Effect: Deny, Disabled: true},
		{ID: "watch", Effect: Deny, Mode: Monitor, Match: Match{Targets: []string{"x"}}},
	}}
	d := Evaluate(p, call(PlaneLLM, "x", priya), Input{})
	if !d.Allowed() || len(d.Monitored) != 1 || d.Monitored[0] != "watch" {
		t.Fatalf("got %+v", d)
	}
}

func TestValidate(t *testing.T) {
	if err := demoPolicy().Validate(); err != nil {
		t.Fatalf("demo policy should be valid: %v", err)
	}
	bad := map[string]*Policy{
		"version":       {Version: 2},
		"default":       {Version: 1, DefaultEffect: "maybe"},
		"depth":         {Version: 1, Guards: Guards{MaxDepth: 99}},
		"unknown guard": {Version: 1, Guards: Guards{UnknownCallers: "perhaps"}},
		"rule id":       {Version: 1, Rules: []Rule{{ID: "Bad ID", Effect: Deny}}},
		"dup id":        {Version: 1, Rules: []Rule{{ID: "a", Effect: Deny}, {ID: "a", Effect: Deny}}},
		"effect":        {Version: 1, Rules: []Rule{{ID: "a", Effect: "block"}}},
		"mode":          {Version: 1, Rules: []Rule{{ID: "a", Effect: Deny, Mode: "loud"}}},
		"lockdown":      {Version: 1, Rules: []Rule{{ID: "a", Effect: Allow, Lockdown: true}}},
		"plane":         {Version: 1, Rules: []Rule{{ID: "a", Effect: Deny, Match: Match{Planes: []Plane{"email"}}}}},
		"pattern":       {Version: 1, Rules: []Rule{{ID: "a", Effect: Deny, Match: Match{Targets: []string{""}}}}},
		"kind":          {Version: 1, Rules: []Rule{{ID: "a", Effect: Deny, Match: Match{ActorKinds: []identity.Kind{"robot"}}}}},
		"depth range":   {Version: 1, Rules: []Rule{{ID: "a", Effect: Deny, Match: Match{MinDepth: intp(3), MaxDepth: intp(1)}}}},
		"neg depth":     {Version: 1, Rules: []Rule{{ID: "a", Effect: Deny, Match: Match{MinDepth: intp(-1)}}}},
		"unless":        {Version: 1, Rules: []Rule{{ID: "a", Effect: Deny, Unless: &Match{Via: []string{""}}}}},
	}
	for name, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	multi := &Policy{Version: 2, Rules: []Rule{{ID: "a", Effect: "x"}}}
	if err := multi.Validate(); err == nil || strings.Count(err.Error(), "\n") < 1 {
		t.Errorf("validation should report every problem, got %v", err)
	}
}

func TestParse(t *testing.T) {
	ok := `{"version":1,"rules":[{"id":"r1","effect":"deny","match":{"targets":["shell.*"]}}]}`
	if _, err := Parse([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	typo := `{"version":1,"rules":[{"id":"r1","effect":"deny","mtach":{}}]}`
	if _, err := Parse([]byte(typo)); err == nil {
		t.Fatal("a typo in a field name must be rejected")
	}
	if _, err := Parse([]byte(`{"version":1,"rules":[{"id":"r1","effect":"nope"}]}`)); err == nil {
		t.Fatal("invalid policy must be rejected")
	}
}

func TestGrantValidateAndCovers(t *testing.T) {
	g := Grant{ID: "g", Actor: "agent:research", Plane: PlaneMCP, Target: "email.*", TaskID: "t1", ExpiresAt: t0.Add(time.Hour)}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	f := call(PlaneMCP, "email.send", priya, identity.Principal{ID: "agent:research", Kind: identity.KindAgent})
	if g.Covers(f, t0) {
		t.Error("grant bound to a task must not cover calls without that task")
	}
	f.TaskID = "t1"
	if !g.Covers(f, t0) {
		t.Error("grant should cover the task")
	}
	for _, bad := range []Grant{{}, {Actor: "a", Plane: "x", Target: "t", ExpiresAt: t0}, {Actor: "a", Plane: PlaneMCP, ExpiresAt: t0}, {Actor: "a", Plane: PlaneMCP, Target: "t"}} {
		if bad.Validate() == nil {
			t.Errorf("grant %+v should be invalid", bad)
		}
	}
}

func BenchmarkEvaluate(b *testing.B) {
	p := demoPolicy()
	f := call(PlaneMCP, "billing.refund", chatApp, support, billing)
	in := Input{Locks: locks{}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Evaluate(p, f, in)
	}
}
