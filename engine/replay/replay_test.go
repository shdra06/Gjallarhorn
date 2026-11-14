package replay

import (
	"testing"
	"time"

	"github.com/gjallarhorn-dev/gjallarhorn/engine/identity"
	"github.com/gjallarhorn-dev/gjallarhorn/engine/lockdown"
	"github.com/gjallarhorn-dev/gjallarhorn/engine/policy"
	"github.com/gjallarhorn-dev/gjallarhorn/engine/recorder"
)

var (
	t0      = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	priya   = identity.Principal{ID: "human:priya", Kind: identity.KindHuman, Labels: map[string]string{"role": "operator"}}
	chat    = identity.Principal{ID: "service:customer-chat", Kind: identity.KindService}
	support = identity.Principal{ID: "agent:support", Kind: identity.KindAgent}
	billing = identity.Principal{ID: "agent:billing", Kind: identity.KindAgent}
)

// history records calls under the permissive default policy, like a gateway that had no
// rules yet when the attack happened.
func history(t *testing.T) []recorder.Event {
	t.Helper()
	rec := recorder.NewMemory()
	add := func(i int, label string, plane policy.Plane, target string, chain ...identity.Principal) {
		f := policy.Facts{Time: t0.Add(time.Duration(i) * time.Minute), Plane: plane, Target: target,
			Actor: chain[len(chain)-1], Origin: chain[0], Chain: chain, Depth: len(chain) - 1}
		d := policy.Evaluate(policy.Default(), f, policy.Input{})
		if _, err := rec.Append(recorder.Event{Type: recorder.TypeDecision, Facts: &f, Decision: &d,
			Note: map[string]string{"label": label}}); err != nil {
			t.Fatal(err)
		}
	}
	add(1, "benign", policy.PlaneMCP, "billing.refund", priya, billing)         // operator refund
	add(2, "benign", policy.PlaneMCP, "orders.lookup", chat, support)           // normal support work
	add(3, "attack", policy.PlaneA2A, "agent:billing", chat, support)           // support asks billing
	add(4, "attack", policy.PlaneMCP, "billing.refund", chat, support, billing) // borrowed refund
	add(5, "benign", policy.PlaneLLM, "openai/gpt-4o-mini", chat, support)      // model call
	_, _ = rec.Append(recorder.Event{Type: recorder.TypeLockdown, Note: map[string]string{"principal": "agent:x"}})
	return rec.Recent(0)
}

func refundRule() *policy.Policy {
	return &policy.Policy{Version: 1, Rules: []policy.Rule{{
		ID: "refunds-need-an-operator", Effect: policy.Deny,
		Match:  policy.Match{Planes: []policy.Plane{policy.PlaneMCP}, Targets: []string{"billing.refund*"}},
		Unless: &policy.Match{OriginLabels: map[string]string{"role": "operator"}},
	}}}
}

func TestReplayShowsWhatANewRuleWouldChange(t *testing.T) {
	rep := Run(history(t), refundRule(), Options{})
	if rep.Evaluated != 5 {
		t.Fatalf("evaluated %d decision events, want 5 (other event types are ignored)", rep.Evaluated)
	}
	if len(rep.NewlyBlocked) != 1 || rep.NewlyBlocked[0].Facts.Target != "billing.refund" || rep.NewlyBlocked[0].Label != "attack" {
		t.Fatalf("newly blocked %+v", rep.NewlyBlocked)
	}
	if len(rep.NewlyAllowed) != 0 || rep.Unchanged != 4 {
		t.Fatalf("unexpected report %+v", rep)
	}
	if rep.DeniedBy["refunds-need-an-operator"] != 1 {
		t.Fatalf("denied by %v", rep.DeniedBy)
	}
	if rep.Labels["attack"].BlockedAfter != 1 || rep.Labels["benign"].BlockedAfter != 0 {
		t.Fatalf("labels %+v", rep.Labels)
	}
	if !rep.Safe() {
		t.Fatal("the rule blocks an attack and no benign calls, so it is safe")
	}
}

func TestOverBroadRuleIsUnsafe(t *testing.T) {
	broad := &policy.Policy{Version: 1, Rules: []policy.Rule{{ID: "no-refunds", Effect: policy.Deny,
		Match: policy.Match{Targets: []string{"billing.refund*"}}}}}
	rep := Run(history(t), broad, Options{})
	if rep.Labels["benign"].BlockedAfter != 1 || rep.Safe() {
		t.Fatalf("a rule that blocks the operator's refund must be reported unsafe: %+v", rep.Labels)
	}
}

func TestWhatIfLockdown(t *testing.T) {
	locks := lockdown.Snapshot{"agent:support": {Principal: "agent:support", Since: t0.Add(3*time.Minute + 30*time.Second)}}
	rep := Run(history(t), policy.Default(), Options{Locks: locks})
	// Calls at minutes 4 and 5 involve support after the lockdown time; minute 2 and 3 are before it.
	if len(rep.NewlyBlocked) != 2 || rep.NewlyBlocked[0].Seq != 4 || rep.NewlyBlocked[1].Seq != 5 {
		t.Fatalf("what-if lockdown blocked %v", changeSeqs(rep.NewlyBlocked))
	}
}

func TestRecordedLockdownsStayBlocked(t *testing.T) {
	rec := recorder.NewMemory()
	f := policy.Facts{Time: t0, Plane: policy.PlaneLLM, Target: "m", Actor: support, Origin: support, Chain: []identity.Principal{support}}
	d := policy.Decision{Effect: policy.Deny, Code: policy.CodeLockdown, Reason: "locked"}
	_, _ = rec.Append(recorder.Event{Type: recorder.TypeDecision, Facts: &f, Decision: &d})
	events := rec.Recent(0)

	rep := Run(events, policy.Default(), Options{})
	if rep.Skipped != 1 || len(rep.NewlyAllowed) != 0 {
		t.Fatalf("a lockdown decision must stay blocked by default: %+v", rep)
	}
	rep = Run(events, policy.Default(), Options{ReevaluateLockdowns: true})
	if len(rep.NewlyAllowed) != 1 {
		t.Fatalf("with ReevaluateLockdowns the call is re-decided: %+v", rep)
	}
}

func TestWindowAndTruncation(t *testing.T) {
	broad := &policy.Policy{Version: 1, DefaultEffect: policy.Deny}
	rep := Run(history(t), broad, Options{MaxChanges: 2})
	if len(rep.NewlyBlocked) != 2 || !rep.Truncated {
		t.Fatalf("truncation: %d %v", len(rep.NewlyBlocked), rep.Truncated)
	}
	rep = Run(history(t), broad, Options{From: t0.Add(4 * time.Minute), To: t0.Add(4 * time.Minute)})
	if rep.Evaluated != 1 {
		t.Fatalf("window should select one call, got %d", rep.Evaluated)
	}
}

func TestSafeNeedsLabels(t *testing.T) {
	if (Report{}).Safe() {
		t.Fatal("with no labelled traffic there is no evidence of safety")
	}
}

func changeSeqs(cs []Change) []uint64 {
	out := make([]uint64, len(cs))
	for i, c := range cs {
		out[i] = c.Seq
	}
	return out
}
