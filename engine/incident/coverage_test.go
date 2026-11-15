package incident_test

import (
	"testing"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/incident"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
)

func TestSeqsFocusAndOutcomes(t *testing.T) {
	e := newEngine(t)
	t1 := e.Delegate(engine.Request{Target: "support", VirtualKey: "vk-chat"})
	r := e.Check(engine.Request{ID: "req-1", Plane: policy.PlaneLLM, Target: "mock/model", VirtualKey: "vk-support", Ticket: t1.Ticket})
	e.RecordOutcome("req-1", recorder.Outcome{Status: "ok", LatencyMS: 12})
	events, _ := e.History()
	in, err := incident.Build(events, r.Seq)
	if err != nil {
		t.Fatal(err)
	}
	seqs := in.Seqs()
	if len(seqs) != 2 || !seqs[t1.Seq] || !seqs[r.Seq] {
		t.Fatalf("seqs %v", seqs)
	}
	f := in.FocusStep()
	if f.Seq != r.Seq || f.Outcome == nil || f.Outcome.LatencyMS != 12 {
		t.Fatalf("the focus step carries its outcome: %+v", f)
	}
	empty := &incident.Incident{Focus: 99}
	if s := empty.FocusStep(); s.Seq != 0 {
		t.Fatal("no focus step")
	}
}

func TestNotableSkipsAllowedAndRespectsLimit(t *testing.T) {
	e := newEngine(t)
	e.Check(engine.Request{Plane: policy.PlaneLLM, Target: "mock/model", VirtualKey: "vk-priya"})
	for i := 0; i < 3; i++ {
		e.Check(engine.Request{Plane: policy.PlaneMCP, Target: "billing.refund", VirtualKey: "vk-billing"})
	}
	events, _ := e.History()
	if got := incident.Notable(events, 2); len(got) != 2 {
		t.Fatalf("limit: %d", len(got))
	}
	for _, s := range incident.Notable(events, 10) {
		if s.Decision.Allowed() {
			t.Fatal("only denials are notable")
		}
	}
	if got := incident.Notable(nil, 5); len(got) != 0 {
		t.Fatal("empty history")
	}
}
