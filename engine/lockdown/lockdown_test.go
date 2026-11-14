package lockdown

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func TestLockUnlock(t *testing.T) {
	r, err := New(WithClock(func() time.Time { return t0 }))
	if err != nil {
		t.Fatal(err)
	}
	if r.IsLocked("agent:support", t0) {
		t.Fatal("nothing is locked yet")
	}
	e, err := r.Lock(Entry{Principal: "agent:support", By: "priya"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Since != t0 || e.Reason == "" {
		t.Fatalf("defaults not filled: %+v", e)
	}
	if !r.IsLocked("agent:support", t0) {
		t.Fatal("lock must take effect immediately")
	}
	if got := r.List(); len(got) != 1 || got[0].Principal != "agent:support" {
		t.Fatalf("list %+v", got)
	}
	if _, ok, _ := r.Unlock("agent:support"); !ok {
		t.Fatal("unlock should report the removed lockdown")
	}
	if r.IsLocked("agent:support", t0) {
		t.Fatal("still locked after unlock")
	}
	if _, ok, _ := r.Unlock("agent:support"); ok {
		t.Fatal("second unlock should report nothing removed")
	}
	if _, err := r.Lock(Entry{}); err == nil {
		t.Fatal("a lockdown needs a principal")
	}
}

func TestTimeWindow(t *testing.T) {
	e := Entry{Principal: "agent:x", Since: t0, Until: t0.Add(time.Hour)}
	if e.Active(t0.Add(-time.Second)) || !e.Active(t0) || !e.Active(t0.Add(59*time.Minute)) || e.Active(t0.Add(time.Hour)) {
		t.Fatal("active window wrong")
	}
	s := Snapshot{"agent:x": e}
	if !s.IsLocked("agent:x", t0.Add(time.Minute)) || s.IsLocked("agent:y", t0) {
		t.Fatal("snapshot")
	}
	r, _ := New(WithClock(func() time.Time { return t0.Add(2 * time.Hour) }))
	_, _ = r.Lock(e)
	if len(r.List()) != 0 {
		t.Fatal("expired lockdowns must not be listed")
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lockdowns.json")
	r, err := New(WithFile(path), WithClock(func() time.Time { return t0 }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lock(Entry{Principal: "agent:support", Reason: "prompt injection", By: "priya"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lock(Entry{Principal: "agent:billing", Auto: true, RuleID: "r1"}); err != nil {
		t.Fatal(err)
	}
	again, err := New(WithFile(path))
	if err != nil {
		t.Fatal(err)
	}
	if !again.IsLocked("agent:support", t0) || !again.IsLocked("agent:billing", t0) {
		t.Fatal("lockdowns must survive a restart")
	}
	got, _ := again.Get("agent:support")
	if got.Reason != "prompt injection" || got.By != "priya" {
		t.Fatalf("persisted entry %+v", got)
	}
	_, _, _ = again.Unlock("agent:billing")
	third, _ := New(WithFile(path))
	if third.IsLocked("agent:billing", t0) {
		t.Fatal("unlock must be persisted")
	}
}

func TestCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lockdowns.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(WithFile(path)); err == nil {
		t.Fatal("a corrupt lockdown file must be an error, not silently ignored")
	}
}

func TestSaveFailureRollsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions behave differently on Windows")
	}
	dir := t.TempDir()
	r, _ := New(WithFile(filepath.Join(dir, "sub", "lockdowns.json")))
	if _, err := r.Lock(Entry{Principal: "agent:x"}); err == nil {
		t.Fatal("expected a save error for a missing directory")
	}
	if r.IsLocked("agent:x", time.Now()) {
		t.Fatal("memory must not keep a lockdown that could not be saved")
	}
}

func TestConcurrentUse(t *testing.T) {
	r, _ := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_, _ = r.Lock(Entry{Principal: "agent:x"})
				_, _, _ = r.Unlock("agent:x")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = r.IsLocked("agent:x", time.Now())
				_ = r.List()
			}
		}()
	}
	wg.Wait()
}

func BenchmarkIsLocked(b *testing.B) {
	r, _ := New()
	for i := 0; i < 100; i++ {
		_, _ = r.Lock(Entry{Principal: "agent:a" + string(rune('a'+i%26))})
	}
	now := time.Now()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.IsLocked("agent:support", now)
	}
}
