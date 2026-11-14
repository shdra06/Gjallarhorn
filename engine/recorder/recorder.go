// Package recorder is Gjallarhorn's flight recorder: an append-only, tamper-evident log of
// every decision, outcome and operator action.
//
// Each event carries the hash of the previous event, and its own hash covers its content and
// that link. Changing, deleting or reordering any line breaks the chain, and Verify reports
// exactly where. Events are written as JSON Lines so they are easy to read, ship and replay.
//
// The request path never waits on disk under normal load: Append hashes the event (a few
// microseconds), keeps it in an in-memory ring for the live console, and hands the line to a
// background writer. If the writer falls far behind, Append blocks rather than drop an event,
// because a silently missing event would defeat the point of a flight recorder.
package recorder

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/gjallarhorn-dev/gjallarhorn/engine/policy"
)

// Event types.
const (
	TypeDecision = "decision" // a call was checked
	TypeOutcome  = "outcome"  // what happened after an allowed call
	TypeLockdown = "lockdown" // a principal was locked
	TypeUnlock   = "unlock"   // a lockdown was lifted
	TypePolicy   = "policy"   // the policy was replaced
	TypeGrant    = "grant"    // a temporary grant was created or revoked
	TypeTicket   = "ticket"   // a chain ticket was minted for a delegation
)

// GenesisHash is the "previous hash" of the first event in a log.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Outcome records what happened to an allowed call.
type Outcome struct {
	Status    string  `json:"status"` // "ok" or "error"
	Error     string  `json:"error,omitempty"`
	LatencyMS float64 `json:"latency_ms,omitempty"`
	Tokens    int     `json:"tokens,omitempty"`
}

// Event is one line in the flight recorder.
type Event struct {
	Seq       uint64            `json:"seq"`
	Time      time.Time         `json:"ts"`
	Type      string            `json:"type"`
	RequestID string            `json:"request_id,omitempty"`
	Facts     *policy.Facts     `json:"facts,omitempty"`
	Decision  *policy.Decision  `json:"decision,omitempty"`
	Outcome   *Outcome          `json:"outcome,omitempty"`
	Note      map[string]string `json:"note,omitempty"`
	// Capture holds request or response content when capture is enabled. It is empty by
	// default so prompts and tool results are not stored.
	Capture json.RawMessage `json:"capture,omitempty"`
	Prev    string          `json:"prev"`
	Hash    string          `json:"hash"`
}

// computeHash hashes the event with its Hash field cleared.
func computeHash(e Event) (string, error) {
	e.Hash = ""
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Stats describes the recorder's state.
type Stats struct {
	Events      uint64 `json:"events"`
	LastSeq     uint64 `json:"last_seq"`
	LastHash    string `json:"last_hash"`
	Subscribers int    `json:"subscribers"`
	// SlowSubscriberDrops counts live-stream messages skipped because a subscriber was full.
	// The log itself never drops events.
	SlowSubscriberDrops uint64 `json:"slow_subscriber_drops"`
	WriteErrors         uint64 `json:"write_errors"`
	LastWriteError      string `json:"last_write_error,omitempty"`
}

// Recorder appends events. Create one with Open or NewMemory.
type Recorder struct {
	mu       sync.Mutex
	seq      uint64
	prev     string
	count    uint64
	ring     []Event
	ringNext int
	ringFull bool
	subs     map[int]chan Event
	nextSub  int
	drops    uint64
	closed   bool

	// errMu guards write-error counters. It is separate from mu because the writer goroutine
	// must never need mu: Append may be blocked holding mu while waiting for the writer.
	errMu    sync.Mutex
	writeErr uint64
	lastErr  string

	file  *os.File
	lines chan []byte
	done  chan struct{}
	now   func() time.Time
}

// Option configures a Recorder.
type Option func(*Recorder)

// WithRingSize sets how many recent events are kept in memory for the live view (default 4096).
func WithRingSize(n int) Option {
	return func(r *Recorder) {
		if n > 0 {
			r.ring = make([]Event, n)
		}
	}
}

// WithClock replaces the time source.
func WithClock(now func() time.Time) Option { return func(r *Recorder) { r.now = now } }

func newRecorder(opts []Option) *Recorder {
	r := &Recorder{prev: GenesisHash, subs: map[int]chan Event{}, now: time.Now}
	for _, o := range opts {
		o(r)
	}
	if r.ring == nil {
		r.ring = make([]Event, 4096)
	}
	return r
}

// NewMemory returns a recorder that keeps events in memory only (tests, demos, replay).
func NewMemory(opts ...Option) *Recorder { return newRecorder(opts) }

// Open returns a recorder that appends to path, continuing the hash chain of any existing
// content. The existing content is verified first; a broken chain is an error, so tampering
// is never silently extended.
func Open(path string, opts ...Option) (*Recorder, error) {
	r := newRecorder(opts)
	if f, err := os.Open(path); err == nil {
		events, verr := verifyAndCollect(f, r.ring)
		f.Close()
		if verr != nil {
			return nil, fmt.Errorf("recorder: existing log %s failed verification: %w", path, verr)
		}
		if len(events) > 0 {
			last := events[len(events)-1]
			r.seq, r.prev, r.count = last.Seq, last.Hash, uint64(len(events))
			for _, e := range events {
				r.pushRing(e)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("recorder: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("recorder: %w", err)
	}
	r.file = f
	r.lines = make(chan []byte, 8192)
	r.done = make(chan struct{})
	go r.writeLoop()
	return r, nil
}

// verifyAndCollect verifies a log and keeps the last len(ring) events.
func verifyAndCollect(rd io.Reader, ring []Event) ([]Event, error) {
	keep := len(ring)
	var tail []Event
	err := walk(rd, func(e Event) {
		tail = append(tail, e)
		if len(tail) > keep {
			tail = tail[1:]
		}
	})
	return tail, err
}

// Append adds an event, filling in Seq, Time (if zero), Prev and Hash. It returns the stored
// event. Append is safe for concurrent use.
func (r *Recorder) Append(e Event) (Event, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return Event{}, errors.New("recorder is closed")
	}
	if e.Time.IsZero() {
		e.Time = r.now()
	}
	e.Time = e.Time.UTC()
	if e.Facts != nil {
		f := *e.Facts
		f.Time = f.Time.UTC()
		e.Facts = &f
	}
	e.Seq = r.seq + 1
	e.Prev = r.prev
	h, err := computeHash(e)
	if err != nil {
		r.mu.Unlock()
		return Event{}, fmt.Errorf("recorder: %w", err)
	}
	e.Hash = h
	var line []byte
	if r.lines != nil {
		line, err = json.Marshal(e)
		if err != nil {
			r.mu.Unlock()
			return Event{}, fmt.Errorf("recorder: %w", err)
		}
		line = append(line, '\n')
	}
	r.seq, r.prev = e.Seq, e.Hash
	r.count++
	r.pushRing(e)
	for _, ch := range r.subs {
		select {
		case ch <- e:
		default:
			r.drops++
		}
	}
	// Sending under the lock keeps file order identical to sequence order.
	if line != nil {
		r.lines <- line
	}
	r.mu.Unlock()
	return e, nil
}

func (r *Recorder) pushRing(e Event) {
	r.ring[r.ringNext] = e
	r.ringNext = (r.ringNext + 1) % len(r.ring)
	if r.ringNext == 0 {
		r.ringFull = true
	}
}

// Recent returns up to n of the most recent events, oldest first.
func (r *Recorder) Recent(n int) []Event {
	return r.Since(0, n)
}

// Since returns events with Seq > after that are still in memory, oldest first, at most limit
// (limit <= 0 means no limit).
func (r *Recorder) Since(after uint64, limit int) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	size := r.ringNext
	start := 0
	if r.ringFull {
		size = len(r.ring)
		start = r.ringNext
	}
	out := make([]Event, 0, size)
	for i := 0; i < size; i++ {
		e := r.ring[(start+i)%len(r.ring)]
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Subscribe returns a channel of new events and a function that ends the subscription.
// Slow subscribers miss live messages (counted in Stats) and can catch up with Since.
func (r *Recorder) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 256
	}
	ch := make(chan Event, buffer)
	r.mu.Lock()
	id := r.nextSub
	r.nextSub++
	r.subs[id] = ch
	r.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.subs, id)
			r.mu.Unlock()
			close(ch)
		})
	}
}

// Stats returns counters.
func (r *Recorder) Stats() Stats {
	r.mu.Lock()
	s := Stats{Events: r.count, LastSeq: r.seq, LastHash: r.prev, Subscribers: len(r.subs),
		SlowSubscriberDrops: r.drops}
	r.mu.Unlock()
	r.errMu.Lock()
	s.WriteErrors, s.LastWriteError = r.writeErr, r.lastErr
	r.errMu.Unlock()
	return s
}

func (r *Recorder) writeLoop() {
	defer close(r.done)
	w := bufio.NewWriterSize(r.file, 64*1024)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	flush := func() {
		if err := w.Flush(); err != nil {
			r.noteWriteError(err)
		}
	}
	for {
		select {
		case line, ok := <-r.lines:
			if !ok {
				flush()
				if err := r.file.Sync(); err != nil {
					r.noteWriteError(err)
				}
				return
			}
			if _, err := w.Write(line); err != nil {
				r.noteWriteError(err)
			}
			if len(r.lines) == 0 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (r *Recorder) noteWriteError(err error) {
	r.errMu.Lock()
	r.writeErr++
	r.lastErr = err.Error()
	r.errMu.Unlock()
}

// Close flushes pending events and closes the file. It is safe to call more than once.
func (r *Recorder) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	for id, ch := range r.subs {
		delete(r.subs, id)
		close(ch)
	}
	lines := r.lines
	r.mu.Unlock()
	if lines == nil {
		return nil
	}
	close(lines)
	<-r.done
	return r.file.Close()
}

// VerifyError reports where a log's chain broke.
type VerifyError struct {
	Line   int
	Seq    uint64
	Reason string
}

func (e *VerifyError) Error() string {
	return fmt.Sprintf("line %d (seq %d): %s", e.Line, e.Seq, e.Reason)
}

// Verify checks every line of a log and returns the number of events.
func Verify(rd io.Reader) (int, error) {
	n := 0
	err := walk(rd, func(Event) { n++ })
	return n, err
}

// ReadAll verifies a log and returns all events.
func ReadAll(rd io.Reader) ([]Event, error) {
	var out []Event
	err := walk(rd, func(e Event) { out = append(out, e) })
	return out, err
}

func walk(rd io.Reader, fn func(Event)) error {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	prev := GenesisHash
	var lastSeq uint64
	line := 0
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return &VerifyError{Line: line, Seq: lastSeq + 1, Reason: "not valid JSON: " + err.Error()}
		}
		if e.Seq != lastSeq+1 {
			return &VerifyError{Line: line, Seq: e.Seq, Reason: fmt.Sprintf("expected seq %d (an event is missing or out of order)", lastSeq+1)}
		}
		if e.Prev != prev {
			return &VerifyError{Line: line, Seq: e.Seq, Reason: "previous-hash link is broken"}
		}
		h, err := computeHash(e)
		if err != nil {
			return &VerifyError{Line: line, Seq: e.Seq, Reason: err.Error()}
		}
		if h != e.Hash {
			return &VerifyError{Line: line, Seq: e.Seq, Reason: "content does not match its hash (the event was modified)"}
		}
		prev, lastSeq = e.Hash, e.Seq
		fn(e)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read log: %w", err)
	}
	return nil
}
