package recorder

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gjallarhorn-dev/gjallarhorn/engine/identity"
	"github.com/gjallarhorn-dev/gjallarhorn/engine/policy"
)

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func sampleEvent(i int) Event {
	p := identity.Principal{ID: "agent:support", Kind: identity.KindAgent, Labels: map[string]string{"team": "cx", "env": "prod"}}
	f := policy.Facts{Time: t0.Add(time.Duration(i) * time.Second), Plane: policy.PlaneMCP, Target: "billing.refund",
		Actor: p, Origin: p, Chain: []identity.Principal{p}, Credential: p}
	d := policy.Decision{Effect: policy.Deny, Code: policy.CodeRule, RuleID: "r1", Reason: "blocked <html> & co"}
	return Event{Type: TypeDecision, RequestID: "req-" + string(rune('a'+i%26)), Facts: &f, Decision: &d,
		Note: map[string]string{"label": "attack"}}
}

func TestMemoryRecorderChainAndRing(t *testing.T) {
	r := NewMemory(WithRingSize(3), WithClock(func() time.Time { return t0 }))
	defer r.Close()
	for i := 0; i < 5; i++ {
		e, err := r.Append(sampleEvent(i))
		if err != nil {
			t.Fatal(err)
		}
		if e.Seq != uint64(i+1) {
			t.Fatalf("seq %d, want %d", e.Seq, i+1)
		}
	}
	recent := r.Recent(0)
	if len(recent) != 3 || recent[0].Seq != 3 || recent[2].Seq != 5 {
		t.Fatalf("ring should keep the last 3 events in order, got %v", seqs(recent))
	}
	for i := 1; i < len(recent); i++ {
		if recent[i].Prev != recent[i-1].Hash {
			t.Fatalf("event %d does not link to the previous hash", recent[i].Seq)
		}
		if h, _ := computeHash(recent[i]); h != recent[i].Hash {
			t.Fatalf("event %d hash does not match its content", recent[i].Seq)
		}
	}
	if got := r.Since(4, 0); len(got) != 1 || got[0].Seq != 5 {
		t.Fatalf("since 4: %v", seqs(got))
	}
	if got := r.Recent(2); len(got) != 2 || got[0].Seq != 4 {
		t.Fatalf("recent 2: %v", seqs(got))
	}
	st := r.Stats()
	if st.Events != 5 || st.LastSeq != 5 || st.LastHash != recent[2].Hash {
		t.Fatalf("stats %+v", st)
	}
}

func TestFirstEventLinksToGenesis(t *testing.T) {
	r := NewMemory()
	defer r.Close()
	_, _ = r.Append(sampleEvent(0))
	if got := r.Recent(1); len(got) != 1 || got[0].Prev != GenesisHash {
		t.Fatalf("first event must link to the genesis hash: %+v", got)
	}
}

func TestAppendCopiesCallerData(t *testing.T) {
	r := NewMemory()
	defer r.Close()
	e := sampleEvent(0)
	_, _ = r.Append(e)
	e.Decision.Reason = "changed after append"
	e.Note["label"] = "changed"
	got := r.Recent(1)[0]
	if got.Decision.Reason == "changed after append" || got.Note["label"] == "changed" {
		t.Fatal("the recorder must keep what was appended, not what the caller changed later")
	}
}

func TestBackpressureDoesNotDeadlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flight.jsonl")
	r, err := Open(path, WithQueueSize(1), WithRingSize(16))
	if err != nil {
		t.Fatal(err)
	}
	ch, cancel := r.Subscribe(1)
	defer cancel()
	go func() {
		for range ch {
		}
	}()
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 300; i++ {
					_, _ = r.Append(sampleEvent(i))
					if i%50 == 0 {
						_ = r.Stats()
						_ = r.Recent(5)
					}
				}
			}()
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("appends with a full queue deadlocked")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(path)
	defer f.Close()
	if n, err := Verify(f); err != nil || n != 2400 {
		t.Fatalf("log after backpressure: n=%d err=%v", n, err)
	}
}

func seqs(es []Event) []uint64 {
	out := make([]uint64, len(es))
	for i, e := range es {
		out[i] = e.Seq
	}
	return out
}

func TestFileRecorderVerifyAndResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flight.jsonl")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := r.Append(sampleEvent(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal("Close must be idempotent")
	}
	f, _ := os.Open(path)
	n, err := Verify(f)
	f.Close()
	if err != nil || n != 50 {
		t.Fatalf("verify: n=%d err=%v", n, err)
	}

	// Reopening continues the same chain.
	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := r2.Recent(1); len(got) != 1 || got[0].Seq != 50 {
		t.Fatal("reopened recorder should load recent events")
	}
	e, _ := r2.Append(sampleEvent(99))
	if e.Seq != 51 {
		t.Fatalf("seq after reopen %d, want 51", e.Seq)
	}
	r2.Close()
	data, _ := os.ReadFile(path)
	events, err := ReadAll(bytes.NewReader(data))
	if err != nil || len(events) != 51 {
		t.Fatalf("read all: %d %v", len(events), err)
	}
	if events[0].Facts.Actor.Label("team") != "cx" || events[0].Decision.Reason != "blocked <html> & co" {
		t.Fatalf("content did not round-trip: %+v", events[0])
	}
}

func TestTamperDetection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flight.jsonl")
	r, _ := Open(path)
	for i := 0; i < 10; i++ {
		_, _ = r.Append(sampleEvent(i))
	}
	r.Close()
	orig, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(orig), "\n"), "\n")

	modify := func(i int, fn func(*Event)) string {
		var e Event
		_ = json.Unmarshal([]byte(lines[i]), &e)
		fn(&e)
		b, _ := json.Marshal(e)
		cp := append([]string(nil), lines...)
		cp[i] = string(b)
		return strings.Join(cp, "\n") + "\n"
	}
	cases := map[string]struct {
		content string
		line    int
	}{
		"edited decision":  {modify(4, func(e *Event) { e.Decision.Effect = policy.Allow }), 5},
		"deleted event":    {strings.Join(append(append([]string(nil), lines[:3]...), lines[4:]...), "\n"), 4},
		"swapped events":   {strings.Join([]string{lines[0], lines[2], lines[1]}, "\n"), 2},
		"rehashed edit":    {modify(4, func(e *Event) { e.Decision.Effect = policy.Allow; e.Hash, _ = computeHash(*e) }), 6},
		"garbage line":     {lines[0] + "\n{oops\n", 2},
		"wrong first link": {modify(0, func(e *Event) { e.Prev = strings.Repeat("1", 64) }), 1},
	}
	for name, c := range cases {
		_, err := Verify(strings.NewReader(c.content))
		var ve *VerifyError
		if !errors.As(err, &ve) {
			t.Errorf("%s: expected a VerifyError, got %v", name, err)
			continue
		}
		if ve.Line != c.line {
			t.Errorf("%s: reported line %d, want %d (%v)", name, ve.Line, c.line, ve)
		}
	}

	// A tampered log must not be extended.
	if err := os.WriteFile(path, []byte(cases["edited decision"].content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open must refuse to append to a tampered log")
	}
}

func TestSubscribe(t *testing.T) {
	r := NewMemory()
	defer r.Close()
	ch, cancel := r.Subscribe(1)
	_, _ = r.Append(sampleEvent(0))
	_, _ = r.Append(sampleEvent(1)) // subscriber buffer is full: dropped, not blocking
	_ = r.Stats()                   // wait until both are sealed
	if e := <-ch; e.Seq != 1 {
		t.Fatalf("got seq %d", e.Seq)
	}
	if r.Stats().SlowSubscriberDrops != 1 {
		t.Fatal("a slow subscriber drop should be counted")
	}
	cancel()
	cancel() // idempotent
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
	if r.Stats().Subscribers != 0 {
		t.Fatal("subscriber not removed")
	}
}

func TestCloseEndsSubscriptionsAndRejectsAppends(t *testing.T) {
	r := NewMemory()
	ch, _ := r.Subscribe(4)
	_ = r.Close()
	if _, ok := <-ch; ok {
		t.Fatal("subscriptions must end on close")
	}
	if _, err := r.Append(sampleEvent(0)); err == nil {
		t.Fatal("append after close must fail")
	}
}

func TestConcurrentAppendsKeepOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flight.jsonl")
	r, _ := Open(path, WithRingSize(64))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				_, _ = r.Append(sampleEvent(g*1000 + i))
			}
		}(g)
	}
	wg.Wait()
	r.Close()
	f, _ := os.Open(path)
	defer f.Close()
	n, err := Verify(f)
	if err != nil || n != 2000 {
		t.Fatalf("concurrent appends: n=%d err=%v", n, err)
	}
}

func BenchmarkAppendMemory(b *testing.B) {
	r := NewMemory()
	defer r.Close()
	e := sampleEvent(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = r.Append(e)
	}
}

func BenchmarkAppendFile(b *testing.B) {
	r, _ := Open(filepath.Join(b.TempDir(), "flight.jsonl"))
	defer r.Close()
	e := sampleEvent(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = r.Append(e)
	}
}

// BenchmarkAppendLatency measures the time a caller spends in Append when the sealer has room,
// which is what a request sees under normal load. Sealing happens off the timer.
func BenchmarkAppendLatency(b *testing.B) {
	r, _ := Open(filepath.Join(b.TempDir(), "flight.jsonl"))
	defer r.Close()
	e := sampleEvent(1)
	const burst = 512
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Append(e)
		if i%burst == burst-1 {
			b.StopTimer()
			_ = r.Stats() // let the sealer catch up
			b.StartTimer()
		}
	}
}
