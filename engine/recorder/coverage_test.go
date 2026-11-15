package recorder

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubscribeEdges(t *testing.T) {
	r := NewMemory()
	ch, cancel := r.Subscribe(0) // default buffer
	if cap(ch) != 256 {
		t.Fatalf("default buffer: %d", cap(ch))
	}
	cancel()
	cancel() // safe to call twice
	if _, ok := <-ch; ok {
		t.Fatal("cancel closes the channel")
	}
	r.Close()
	late, cancelLate := r.Subscribe(4)
	if _, ok := <-late; ok {
		t.Fatal("subscribing to a closed recorder gives a closed channel")
	}
	cancelLate()
}

func TestWriteErrorsAreCountedNotFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flight.jsonl")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Append(Event{Type: TypePolicy}); err != nil {
		t.Fatal(err)
	}
	r.waitAll(t)
	// The disk goes away underneath the recorder: writes fail, decisions keep flowing.
	_ = r.file.Close()
	for i := 0; i < 3; i++ {
		if _, err := r.Append(Event{Type: TypePolicy}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for r.Stats().WriteErrors == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	st := r.Stats()
	if st.WriteErrors == 0 || st.LastWriteError == "" {
		t.Fatalf("write errors must be visible in stats: %+v", st)
	}
	if st.Events != 4 {
		t.Fatalf("events still reach memory and subscribers: %+v", st)
	}
	r.Close()
}

// waitAll waits until everything appended so far is sealed.
func (r *Recorder) waitAll(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	r.waitSealedLocked()
	r.mu.Unlock()
}

func TestReadTailVerifiesEverythingKeepsTheEnd(t *testing.T) {
	r := NewMemory()
	for i := 0; i < 10; i++ {
		_, _ = r.Append(Event{Type: TypePolicy})
	}
	r.Close()
	var buf strings.Builder
	for _, e := range r.Recent(0) {
		b, _ := json.Marshal(e)
		buf.WriteString(string(b) + "\n")
	}
	log := buf.String()
	tail, err := ReadTail(strings.NewReader(log), 3)
	if err != nil || len(tail) != 3 || tail[0].Seq != 8 || tail[2].Seq != 10 {
		t.Fatalf("tail: %v %+v", err, tail)
	}
	all, err := ReadTail(strings.NewReader(log), 0)
	if err != nil || len(all) != 10 {
		t.Fatal("n <= 0 reads everything")
	}
	// Tampering early in the log is still caught, even though only the tail is kept.
	broken := strings.Replace(log, `"seq":2,`, `"seq":2,"note":{"x":"y"},`, 1)
	if _, err := ReadTail(strings.NewReader(broken), 3); err == nil {
		t.Fatal("the whole log must be verified")
	}
}
