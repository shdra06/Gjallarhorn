package engine

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/identity"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
	"github.com/shdra06/Gjallarhorn/engine/replay"
)

func TestDurationJSON(t *testing.T) {
	b, err := json.Marshal(Duration{90 * time.Second})
	if err != nil || string(b) != `"1m30s"` {
		t.Fatalf("marshal: %s %v", b, err)
	}
	for in, want := range map[string]time.Duration{`"10m"`: 10 * time.Minute, `30`: 30 * time.Second, `1.5`: 1500 * time.Millisecond} {
		var d Duration
		if err := json.Unmarshal([]byte(in), &d); err != nil || d.Duration != want {
			t.Fatalf("%s: %v %v", in, d.Duration, err)
		}
	}
	for _, in := range []string{`"ten minutes"`, `true`, `{}`} {
		var d Duration
		if err := json.Unmarshal([]byte(in), &d); err == nil {
			t.Fatalf("%s must be rejected", in)
		}
	}
}

func TestConfigFromAny(t *testing.T) {
	if _, err := ConfigFromAny(nil); err == nil {
		t.Fatal("nil config")
	}
	c := testConfig()
	got, err := ConfigFromAny(c)
	if err != nil || got.Tickets.ActiveKey != c.Tickets.ActiveKey {
		t.Fatal("a typed Config passes straight through")
	}
	if _, err := ConfigFromAny(map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("values that cannot be encoded are an error")
	}
	if _, err := ConfigFromAny(map[string]any{"principles": 1}); err == nil {
		t.Fatal("unknown fields are an error")
	}
}

func TestMonitorModeRecordsButDoesNotBlock(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e := newTestEngine(t, c)
	if !e.Enforcing() {
		t.Fatal("engines enforce by default")
	}
	before := e.Recorder().Stats().Events
	e.SetEnforcing(false, "test")
	e.SetEnforcing(false, "test") // no change, no second event
	if e.Enforcing() || e.Recorder().Stats().Events != before+1 {
		t.Fatal("switching to monitor-only records exactly one event")
	}

	// The shell rule would lock research; in monitor mode it is recorded, not enforced.
	r := e.Check(Request{Plane: policy.PlaneMCP, Target: "shell.exec", VirtualKey: "vk-research"})
	if r.Decision.Allowed() || r.Enforced || r.Block() || r.Locked || e.IsLocked("agent:research") {
		t.Fatalf("monitor mode: %+v", r)
	}
	evs := e.Recorder().Recent(1)
	if evs[0].Note["mode"] != "monitor" {
		t.Fatalf("the decision is marked as monitor-only: %v", evs[0].Note)
	}
	// Delegation still works, so chains can be followed while monitoring.
	d := e.Delegate(Request{Target: "support", VirtualKey: "vk-chat"})
	if d.Ticket == "" {
		t.Fatal("monitor mode still mints tickets")
	}

	e.SetEnforcing(true, "test")
	r = e.Check(Request{Plane: policy.PlaneMCP, Target: "shell.exec", VirtualKey: "vk-research"})
	if !r.Block() || !r.Locked {
		t.Fatalf("enforcing again: %+v", r)
	}
}

func TestReplacePolicy(t *testing.T) {
	c := &fakeClock{t: time.Now()}
	e := newTestEngine(t, c)
	cur := e.Policy()
	next := &policy.Policy{Version: 1}
	if err := e.ReplacePolicy(cur, nil, "t"); err == nil {
		t.Fatal("nil policy")
	}
	if err := e.ReplacePolicy(cur, &policy.Policy{Version: 9}, "t"); err == nil {
		t.Fatal("invalid policy")
	}
	if err := e.ReplacePolicy(cur, next, "t"); err != nil {
		t.Fatal(err)
	}
	if err := e.ReplacePolicy(cur, &policy.Policy{Version: 1}, "t"); !errors.Is(err, ErrPolicyChanged) {
		t.Fatalf("a stale base must not overwrite a newer policy: %v", err)
	}
	if e.Policy() != next {
		t.Fatal("the newer policy stays")
	}
}

func TestChainFromAnotherGatewayKeepsUnknownPrincipals(t *testing.T) {
	// Engine A knows the customer chat; engine B (same ticket keys) does not.
	c := &fakeClock{t: time.Now()}
	a := newTestEngine(t, c)
	cfg := testConfig()
	var kept []identity.PrincipalConfig
	for _, p := range cfg.Principals {
		if p.ID != "service:customer-chat" {
			kept = append(kept, p)
		}
	}
	cfg.Principals = kept
	b, err := New(cfg, WithClock(c.now))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, ok := b.Registry().ByID("service:customer-chat"); ok {
		t.Fatal("setup")
	}
	d := a.Delegate(Request{Target: "support", VirtualKey: "vk-chat"})
	f := b.Resolve(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-support", Ticket: d.Ticket})
	if f.TicketProblem != "" || f.Origin.ID != "service:customer-chat" || f.Origin.Kind != identity.KindService {
		t.Fatalf("the origin comes from the signed ticket even if this gateway does not know it: %+v", f)
	}
}

func TestLockUnlockAndGrantEdges(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	e := newTestEngine(t, c)
	if _, err := e.Lock("not a principal id", "r", "t", false, "", time.Time{}); err == nil {
		t.Fatal("invalid principal ids are refused")
	}
	if ok, err := e.Unlock("agent:support", "t"); ok || err != nil {
		t.Fatal("unlocking someone who is not locked")
	}
	until := c.t.Add(time.Hour)
	if _, err := e.Lock("agent:support", "", "t", false, "", until); err != nil {
		t.Fatal(err)
	}
	last := e.Recorder().Recent(1)[0]
	if last.Type != recorder.TypeLockdown || last.Note["until"] == "" {
		t.Fatalf("a timed lockdown records when it ends: %+v", last.Note)
	}

	add := func(id string, in time.Duration) {
		t.Helper()
		if _, err := e.AddGrant(policy.Grant{ID: id, Actor: "agent:support", Plane: policy.PlaneMCP, Target: "billing.refund",
			ExpiresAt: c.t.Add(in), Reason: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	add("late", 2*time.Hour)
	add("soon", time.Minute)
	if _, err := e.AddGrant(policy.Grant{ID: "soon", Actor: "agent:support", Plane: policy.PlaneMCP, Target: "x",
		ExpiresAt: c.t.Add(time.Hour), Reason: "dup"}); err == nil {
		t.Fatal("duplicate grant ids are refused")
	}
	if _, err := e.AddGrant(policy.Grant{Actor: "agent:support", Plane: policy.PlaneMCP, Target: "x", ExpiresAt: c.t.Add(-time.Minute), Reason: "past"}); err == nil {
		t.Fatal("grants that already expired are refused")
	}
	g := e.Grants()
	if len(g) != 2 || g[0].ID != "soon" {
		t.Fatalf("soonest expiry first: %+v", g)
	}
	c.t = c.t.Add(10 * time.Minute)
	if g := e.Grants(); len(g) != 1 || g[0].ID != "late" {
		t.Fatalf("expired grants drop out: %+v", g)
	}
	if e.RevokeGrant("nope", "t") {
		t.Fatal("revoking an unknown grant")
	}
}

func TestSimulateRefusesBadPolicies(t *testing.T) {
	e := newTestEngine(t, &fakeClock{t: time.Now()})
	if _, err := e.Simulate(nil, replay.Options{}); err == nil {
		t.Fatal("nil")
	}
	if _, err := e.Simulate(&policy.Policy{Version: 7}, replay.Options{}); err == nil {
		t.Fatal("invalid")
	}
}

func TestResultBlock(t *testing.T) {
	deny := policy.Decision{Effect: policy.Deny}
	allow := policy.Decision{Effect: policy.Allow}
	cases := []struct {
		r    Result
		want bool
	}{
		{Result{Decision: deny, Enforced: true}, true},
		{Result{Decision: deny, Enforced: false}, false},
		{Result{Decision: allow, Enforced: true}, false},
	}
	for i, c := range cases {
		if c.r.Block() != c.want {
			t.Errorf("case %d", i)
		}
	}
	if !strings.Contains(ErrPolicyChanged.Error(), "changed") {
		t.Fatal("error text")
	}
}
