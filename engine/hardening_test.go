package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/policy"
)

func TestDecisionLatencyIsMeasured(t *testing.T) {
	e := newTestEngine(t, &fakeClock{t: time.Now()})
	if s := e.DecisionLatency(); s.Count != 0 || s.Window != 0 {
		t.Fatalf("no decisions yet: %+v", s)
	}
	for i := 0; i < 50; i++ {
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-priya"})
	}
	d := e.Delegate(Request{Target: "support", VirtualKey: "vk-chat"})
	_ = d
	s := e.DecisionLatency()
	// Windows' clock ticks coarsely, so individual decisions may read as 0 µs there.
	if s.Count != 51 || s.Window != 51 || s.P50 < 0 || s.P99 < s.P50 || s.Max < s.P99 {
		t.Fatalf("latency stats: %+v", s)
	}
}

func TestLatencyRingWraps(t *testing.T) {
	l := newLatencyRing(4)
	for i := 1; i <= 10; i++ {
		l.add(time.Duration(i) * time.Microsecond)
	}
	s := l.stats()
	if s.Count != 10 || s.Window != 4 || s.Max != 10 || s.P50 != 8 {
		t.Fatalf("only the last 4 count: %+v", s)
	}
}

func TestReplayReadsOnlyTheRecentTail(t *testing.T) {
	cfg := testConfig()
	cfg.Recorder.Path = filepath.Join(t.TempDir(), "flight.jsonl")
	cfg.Recorder.ReplayLimit = 5
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		e.Check(Request{Plane: policy.PlaneLLM, Target: "m", VirtualKey: "vk-priya"})
	}
	e.Close() // flush to disk
	e, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	h, err := e.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 5 || h[len(h)-1].Seq != 20 {
		t.Fatalf("history keeps the last 5 events of the file: %d, last %d", len(h), h[len(h)-1].Seq)
	}
}
