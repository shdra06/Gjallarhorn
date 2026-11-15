package engine

import (
	"sort"
	"sync"
	"time"
)

// latencyRing keeps the most recent decision times, so the real cost of a decision, as the
// gateway pays it, can be read from the control API. It measures the request path only:
// resolving identity and the chain, deciding, and queueing the event for the recorder.
type latencyRing struct {
	mu    sync.Mutex
	buf   []int64 // nanoseconds
	next  int
	full  bool
	total uint64
}

func newLatencyRing(n int) *latencyRing { return &latencyRing{buf: make([]int64, n)} }

func (l *latencyRing) add(d time.Duration) {
	l.mu.Lock()
	l.buf[l.next] = d.Nanoseconds()
	l.next++
	if l.next == len(l.buf) {
		l.next, l.full = 0, true
	}
	l.total++
	l.mu.Unlock()
}

// LatencyStats summarises recent decision times in microseconds.
type LatencyStats struct {
	// Count is the number of decisions since the engine started.
	Count uint64 `json:"count"`
	// Window is how many recent decisions the percentiles cover.
	Window int     `json:"window"`
	P50    float64 `json:"p50_us"`
	P90    float64 `json:"p90_us"`
	P99    float64 `json:"p99_us"`
	Max    float64 `json:"max_us"`
}

func (l *latencyRing) stats() LatencyStats {
	l.mu.Lock()
	n := l.next
	if l.full {
		n = len(l.buf)
	}
	s := append([]int64(nil), l.buf[:n]...)
	total := l.total
	l.mu.Unlock()
	out := LatencyStats{Count: total, Window: n}
	if n == 0 {
		return out
	}
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(p float64) float64 { return float64(s[int(p*float64(n-1))]) / 1000 }
	out.P50, out.P90, out.P99, out.Max = at(0.5), at(0.9), at(0.99), float64(s[n-1])/1000
	return out
}

// DecisionLatency reports how long recent decisions took on the request path.
func (e *Engine) DecisionLatency() LatencyStats { return e.lat.stats() }
