package lockdown

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockDefaultsAndGet(t *testing.T) {
	r, _ := New(WithClock(func() time.Time { return t0 }))
	if _, err := r.Lock(Entry{}); err == nil {
		t.Fatal("a lockdown without a principal must be refused")
	}
	e, err := r.Lock(Entry{Principal: "agent:support"})
	if err != nil {
		t.Fatal(err)
	}
	if !e.Since.Equal(t0) || e.Reason != "locked by operator" {
		t.Fatalf("defaults: %+v", e)
	}
	if got, ok := r.Get("agent:support"); !ok || got.Principal != "agent:support" {
		t.Fatalf("get: %+v %v", got, ok)
	}
	if _, ok := r.Get("agent:nobody"); ok {
		t.Fatal("get of an unknown principal")
	}
	if _, ok, err := r.Unlock("agent:nobody"); ok || err != nil {
		t.Fatal("unlocking someone who is not locked is a no-op")
	}
}

func TestListOrderAndActiveOnly(t *testing.T) {
	now := t0
	r, _ := New(WithClock(func() time.Time { return now }))
	_, _ = r.Lock(Entry{Principal: "agent:b", Since: t0})
	_, _ = r.Lock(Entry{Principal: "agent:a", Since: t0})
	_, _ = r.Lock(Entry{Principal: "agent:old", Since: t0.Add(-time.Hour)})
	_, _ = r.Lock(Entry{Principal: "agent:expired", Since: t0.Add(-time.Hour), Until: t0.Add(-time.Minute)})
	_, _ = r.Lock(Entry{Principal: "agent:later", Since: t0.Add(time.Hour)})
	var got []string
	for _, e := range r.List() {
		got = append(got, e.Principal)
	}
	if strings.Join(got, ",") != "agent:old,agent:a,agent:b" {
		t.Fatalf("oldest first, then by name, active only: %v", got)
	}
}

func TestSnapshotIsFrozen(t *testing.T) {
	r, _ := New()
	_, _ = r.Lock(Entry{Principal: "agent:x", Since: t0})
	s := r.Snapshot()
	_, _, _ = r.Unlock("agent:x")
	_, _ = r.Lock(Entry{Principal: "agent:y", Since: t0})
	if !s.IsLocked("agent:x", t0) || s.IsLocked("agent:y", t0) {
		t.Fatal("a snapshot must not change when the registry does")
	}
	if s.IsLocked("agent:x", t0.Add(-time.Second)) {
		t.Fatal("a snapshot honours the start time, for replay")
	}
}

func TestSaveFailuresRollBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lockdowns.json")
	r, err := New(WithFile(path))
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Lock(Entry{Principal: "agent:x", Reason: "first"})
	if err != nil {
		t.Fatal(err)
	}

	// A directory where the file should be: the atomic rename fails.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lock(Entry{Principal: "agent:x", Reason: "second"}); err == nil {
		t.Fatal("expected the save to fail")
	}
	if e, _ := r.Get("agent:x"); e.Reason != first.Reason {
		t.Fatalf("a failed replace must keep the previous lockdown, got %+v", e)
	}
	if _, err := r.Lock(Entry{Principal: "agent:new"}); err == nil {
		t.Fatal("expected the save to fail")
	}
	if _, ok := r.Get("agent:new"); ok {
		t.Fatal("a failed new lockdown must not stay in memory")
	}
	if _, ok, err := r.Unlock("agent:x"); err == nil || ok {
		t.Fatal("expected the unlock to fail")
	}
	if !r.IsLocked("agent:x", time.Now()) {
		t.Fatal("a failed unlock must keep the agent locked: failing open would be worse")
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".lockdowns-*"))
	if len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestMissingDirectoryIsASaveError(t *testing.T) {
	r, _ := New(WithFile(filepath.Join(t.TempDir(), "no", "such", "lockdowns.json")))
	if _, err := r.Lock(Entry{Principal: "agent:x"}); err == nil {
		t.Fatal("expected a save error")
	}
}

func TestUnreadableFileIsAnError(t *testing.T) {
	dir := t.TempDir() // a directory cannot be read as a file
	if _, err := New(WithFile(dir)); err == nil {
		t.Fatal("expected a read error")
	}
	// Entries without a principal in the file are ignored, not loaded as "".
	path := filepath.Join(t.TempDir(), "l.json")
	_ = os.WriteFile(path, []byte(`{"version":1,"lockdowns":[{"principal":""},{"principal":"agent:z"}]}`), 0o600)
	r, err := New(WithFile(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get(""); ok || !r.IsLocked("agent:z", time.Now()) {
		t.Fatal("load")
	}
}
