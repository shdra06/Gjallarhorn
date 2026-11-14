package incident_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/identity"
	"github.com/shdra06/Gjallarhorn/engine/incident"
	"github.com/shdra06/Gjallarhorn/engine/policy"
)

func newEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.Config{
		Principals: []identity.PrincipalConfig{
			{ID: "human:priya", Kind: identity.KindHuman, Labels: map[string]string{"role": "operator"}, VirtualKeys: []string{"vk-priya"}},
			{ID: "service:customer-chat", Kind: identity.KindService, Labels: map[string]string{"trust": "external"}, VirtualKeys: []string{"vk-chat"}},
			{ID: "agent:support", Kind: identity.KindAgent, VirtualKeys: []string{"vk-support"}, AgentNames: []string{"support"}},
			{ID: "agent:billing", Kind: identity.KindAgent, VirtualKeys: []string{"vk-billing"}, AgentNames: []string{"billing"}},
		},
		Tickets: engine.TicketConfig{ActiveKey: "k", Keys: map[string]string{"k": strings.Repeat("k", 32)}},
		Policy: &policy.Policy{Version: 1, Rules: []policy.Rule{{
			ID: "refunds-need-an-operator", Effect: policy.Deny, Lockdown: true,
			Match:  policy.Match{Planes: []policy.Plane{policy.PlaneMCP}, Targets: []string{"billing.refund*"}},
			Unless: &policy.Match{OriginLabels: map[string]string{"role": "operator"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

// borrowedRefund replays the headline attack and returns the seq of the refund decision.
func borrowedRefund(e *engine.Engine) uint64 {
	t1 := e.Delegate(engine.Request{Target: "support", VirtualKey: "vk-chat", Label: "attack"})
	e.Check(engine.Request{ID: "r-think", Plane: policy.PlaneLLM, Target: "mock/model", VirtualKey: "vk-support", Ticket: t1.Ticket})
	t2 := e.Delegate(engine.Request{Target: "billing", VirtualKey: "vk-support", Ticket: t1.Ticket})
	if t2.Block() {
		// Like a real agent: if the hand-off is refused, billing never gets the task.
		return t2.Seq
	}
	e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "mock/model", VirtualKey: "vk-billing", Ticket: t2.Ticket})
	return e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-billing", Ticket: t2.Ticket}).Seq
}

func TestBuildFollowsTheChainBothWays(t *testing.T) {
	e := newEngine(t)
	// Unrelated traffic before and after must not leak into the incident.
	e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "mock/model", VirtualKey: "vk-priya"})
	refund := borrowedRefund(e)
	e.Delegate(engine.Request{Target: "billing", VirtualKey: "vk-priya"})

	events, _ := e.History()
	in, err := incident.Build(events, refund)
	if err != nil {
		t.Fatal(err)
	}
	if in.Origin != "service:customer-chat" {
		t.Fatalf("origin %q", in.Origin)
	}
	if len(in.Steps) != 5 {
		var got []string
		for _, s := range in.Steps {
			got = append(got, s.Actor+"→"+s.Target)
		}
		t.Fatalf("expected the 5 calls of the chain, got %d: %v", len(in.Steps), got)
	}
	if in.Steps[0].Seq != in.Root || in.Steps[0].Depth != 0 || in.Steps[0].Target != "agent:support" {
		t.Fatalf("root step %+v", in.Steps[0])
	}
	focus := in.FocusStep()
	if !focus.Focus || focus.Depth != 2 || focus.Decision.Allowed() || focus.Executed {
		t.Fatalf("focus step %+v", focus)
	}
	if strings.Join(in.Agents, ",") != "agent:billing,agent:support" {
		t.Fatalf("agents %v", in.Agents)
	}
	if in.Blocked != 1 || in.Executed != 4 {
		t.Fatalf("blocked %d executed %d", in.Blocked, in.Executed)
	}
	if len(in.Lockdowns) != 1 || in.Lockdowns[0].Principal != "agent:billing" || in.Lockdowns[0].RuleID != "refunds-need-an-operator" {
		t.Fatalf("the rule's automatic lockdown belongs to the incident: %+v", in.Lockdowns)
	}

	// Building from any step of the chain gives the same incident.
	again, _ := incident.Build(events, in.Steps[1].Seq)
	if again.Root != in.Root || len(again.Steps) != len(in.Steps) {
		t.Fatal("any step should lead to the same incident")
	}
}

func TestDirectCallIsItsOwnIncident(t *testing.T) {
	e := newEngine(t)
	seq := e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "mock/model", VirtualKey: "vk-priya"}).Seq
	events, _ := e.History()
	in, err := incident.Build(events, seq)
	if err != nil || len(in.Steps) != 1 || in.Root != seq || in.Origin != "human:priya" {
		t.Fatalf("direct call: %+v %v", in, err)
	}
	if _, err := incident.Build(events, 9999); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("unknown seq: %v", err)
	}
}

func TestMonitorModeStepsCountAsExecuted(t *testing.T) {
	e := newEngine(t)
	e.SetEnforcing(false, "test")
	refund := borrowedRefund(e)
	events, _ := e.History()
	in, _ := incident.Build(events, refund)
	f := in.FocusStep()
	if f.Decision.Allowed() || !f.Monitored || !f.Executed {
		t.Fatalf("in monitor mode a flagged call still ran: %+v", f)
	}
	if len(in.Lockdowns) != 0 {
		t.Fatal("monitor mode never locks")
	}
}

func TestNotable(t *testing.T) {
	e := newEngine(t)
	borrowedRefund(e)
	time.Sleep(time.Millisecond)
	borrowedRefund(e) // billing is locked now, so this chain has several denials
	events, _ := e.History()
	notes := incident.Notable(events, 10)
	if len(notes) != 2 {
		t.Fatalf("one entry per incident, newest first; got %d", len(notes))
	}
	if notes[0].Seq < notes[1].Seq {
		t.Fatal("newest first")
	}
}
