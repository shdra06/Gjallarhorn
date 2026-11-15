// Command measure records the numbers Gjallarhorn publishes, against a running demo stack, with
// every raw sample saved:
//
//	measure -mode lockdown   how long a lockdown takes to stop an agent that is busy calling,
//	                         and whether any call started after the lock was let through
//	measure -mode overhead   latency of direct model and tool calls through Bifrost (run it once
//	                         with the plugin on and once with it off)
//	measure -mode compare    compare two overhead files
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

func main() {
	gw := flag.String("gateway", "http://127.0.0.1:8080", "Bifrost base URL")
	ctrl := flag.String("control", "http://127.0.0.1:8787", "Gjallarhorn control API URL")
	mode := flag.String("mode", "lockdown", "lockdown, overhead or compare")
	n := flag.Int("n", 0, "samples (lockdown trials, or calls per plane)")
	label := flag.String("label", "", "label stored in the report, e.g. plugin-on")
	out := flag.String("out", "", "write the JSON report here")
	a := flag.String("a", "", "compare: baseline overhead reports (plugin off), comma-separated")
	b := flag.String("b", "", "compare: overhead reports to compare (plugin on), comma-separated")
	flag.Parse()

	var report any
	switch *mode {
	case "lockdown":
		report = lockdown(*gw, *ctrl, or(*n, 40))
	case "overhead":
		report = overhead(*gw, *ctrl, or(*n, 2000), *label)
	case "compare":
		report = compare(*a, *b)
	default:
		log.Fatalf("unknown mode %q", *mode)
	}
	b2, _ := json.MarshalIndent(report, "", "  ")
	if *out != "" {
		_ = os.MkdirAll(filepath.Dir(*out), 0o755)
		if err := os.WriteFile(*out, b2, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("report written to", *out)
	}
}

func or(v, d int) int {
	if v > 0 {
		return v
	}
	return d
}

var client = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 64}}

func post(ctx context.Context, url string, headers map[string]string, body any) (int, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

func chat(ctx context.Context, gw, vk string) (int, error) {
	return post(ctx, gw+"/v1/chat/completions", map[string]string{"x-bf-vk": vk},
		map[string]any{"model": "mock/standard-model", "messages": []map[string]string{{"role": "user", "content": "ping"}}})
}

func tool(ctx context.Context, gw, vk string) (int, error) {
	return post(ctx, gw+"/v1/mcp/tool/execute?format=chat", map[string]string{"x-bf-vk": vk}, map[string]any{
		"id": "m", "type": "function", "function": map[string]string{"name": "orders-lookup", "arguments": `{"order_id":"ORD-1001"}`}})
}

func admin(method, url string, body any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("GJ_ADMIN_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Operator", "measure")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

// Stats summarises samples in microseconds.
type Stats struct {
	N    int     `json:"n"`
	Min  float64 `json:"min_us"`
	P50  float64 `json:"p50_us"`
	P90  float64 `json:"p90_us"`
	P99  float64 `json:"p99_us"`
	Max  float64 `json:"max_us"`
	Mean float64 `json:"mean_us"`
}

func summarise(us []float64) Stats {
	if len(us) == 0 {
		return Stats{}
	}
	s := append([]float64(nil), us...)
	sort.Float64s(s)
	at := func(p float64) float64 { return s[int(p*float64(len(s)-1))] }
	var sum float64
	for _, v := range s {
		sum += v
	}
	return Stats{N: len(s), Min: s[0], P50: at(0.5), P90: at(0.9), P99: at(0.99), Max: s[len(s)-1], Mean: sum / float64(len(s))}
}

// LockTrial is one lockdown while the agent is busy.
type LockTrial struct {
	// LockAPIUS is how long the lock request itself took.
	LockAPIUS float64 `json:"lock_api_us"`
	// FirstBlockedUS is the time from sending the lock request to the first refused response.
	FirstBlockedUS float64 `json:"first_blocked_us"`
	// AllowedAfterLock counts calls that started after the lock request returned and were still
	// allowed. Anything above zero would break the promise "effective on the next call".
	AllowedAfterLock int `json:"allowed_after_lock"`
	// InFlight counts calls that started before the lock returned and finished allowed.
	InFlight int `json:"in_flight_allowed"`
	Calls    int `json:"calls"`
}

type lockReport struct {
	Kind        string      `json:"kind"`
	When        time.Time   `json:"when"`
	Workers     int         `json:"workers"`
	Trials      []LockTrial `json:"trials"`
	FirstBlock  Stats       `json:"first_blocked"`
	LockAPI     Stats       `json:"lock_api"`
	AllowedLate int         `json:"allowed_after_lock_total"`
	Calls       int         `json:"calls_total"`
}

type callRec struct {
	start, end time.Time
	status     int
}

// lockdown locks a busy agent again and again. Four workers keep sending model calls as the
// research agent; each trial locks it, waits for the calls to be refused, then unlocks.
func lockdown(gw, ctrl string, trials int) lockReport {
	const workers = 4
	vk := "sk-bf-research-demo"
	rep := lockReport{Kind: "lockdown-latency", When: time.Now().UTC(), Workers: workers}
	_, _ = admin(http.MethodDelete, ctrl+"/v1/lockdowns/agent:research", nil)
	for i := 0; i < trials; i++ {
		var mu sync.Mutex
		var recs []callRec
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ctx.Err() == nil {
					s := time.Now()
					st, err := chat(ctx, gw, vk)
					if err != nil {
						continue
					}
					mu.Lock()
					recs = append(recs, callRec{s, time.Now(), st})
					mu.Unlock()
				}
			}()
		}
		time.Sleep(150 * time.Millisecond)
		t0 := time.Now()
		st, err := admin(http.MethodPost, ctrl+"/v1/lockdowns", map[string]string{"principal": "agent:research", "reason": "measurement"})
		tAck := time.Now()
		if err != nil || st != http.StatusCreated {
			log.Fatalf("lock failed: %d %v", st, err)
		}
		time.Sleep(150 * time.Millisecond)
		cancel()
		wg.Wait()
		_, _ = admin(http.MethodDelete, ctrl+"/v1/lockdowns/agent:research", nil)

		tr := LockTrial{LockAPIUS: us(tAck.Sub(t0)), Calls: len(recs), FirstBlockedUS: -1}
		sort.Slice(recs, func(a, b int) bool { return recs[a].end.Before(recs[b].end) })
		for _, r := range recs {
			blocked := r.status >= 400
			if blocked && tr.FirstBlockedUS < 0 && r.end.After(t0) {
				tr.FirstBlockedUS = us(r.end.Sub(t0))
			}
			if !blocked && r.start.After(tAck) {
				tr.AllowedAfterLock++
			}
			if !blocked && r.start.Before(tAck) && r.end.After(t0) {
				tr.InFlight++
			}
		}
		rep.Trials = append(rep.Trials, tr)
		rep.AllowedLate += tr.AllowedAfterLock
		rep.Calls += tr.Calls
		time.Sleep(50 * time.Millisecond)
	}
	var first, api []float64
	for _, t := range rep.Trials {
		if t.FirstBlockedUS >= 0 {
			first = append(first, t.FirstBlockedUS)
		}
		api = append(api, t.LockAPIUS)
	}
	rep.FirstBlock, rep.LockAPI = summarise(first), summarise(api)
	fmt.Printf("lockdown: %d trials, %d calls; first refused response after the lock request: p50 %.0f µs, p90 %.0f µs, max %.0f µs; calls started after the lock returned and still allowed: %d\n",
		trials, rep.Calls, rep.FirstBlock.P50, rep.FirstBlock.P90, rep.FirstBlock.Max, rep.AllowedLate)
	return rep
}

func us(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1000 }

type overheadReport struct {
	Kind    string               `json:"kind"`
	Label   string               `json:"label"`
	When    time.Time            `json:"when"`
	Warmup  int                  `json:"warmup"`
	Planes  map[string]Stats     `json:"planes"`
	Samples map[string][]float64 `json:"samples_us"`
	Errors  map[string]int       `json:"errors"`
	// Decision is Gjallarhorn's own timing of its decisions inside the gateway, read from
	// /v1/stats after the run (plugin on only): the cost Gjallarhorn adds to each call,
	// without the noise of the network and the containers.
	Decision map[string]float64 `json:"gjallarhorn_decision_us,omitempty"`
}

// overhead times direct calls, one at a time, after a warm-up.
func overhead(gw, ctrl string, n int, label string) overheadReport {
	vk := "sk-bf-priya-demo"
	rep := overheadReport{Kind: "overhead", Label: label, When: time.Now().UTC(), Warmup: 300,
		Planes: map[string]Stats{}, Samples: map[string][]float64{}, Errors: map[string]int{}}
	calls := map[string]func(context.Context, string, string) (int, error){"llm": chat, "mcp": tool}
	for _, plane := range []string{"llm", "mcp"} {
		call := calls[plane]
		for i := 0; i < rep.Warmup; i++ {
			_, _ = call(context.Background(), gw, vk)
		}
		var samples []float64
		for i := 0; i < n; i++ {
			s := time.Now()
			st, err := call(context.Background(), gw, vk)
			d := time.Since(s)
			if err != nil || st != http.StatusOK {
				rep.Errors[plane]++
				continue
			}
			samples = append(samples, us(d))
		}
		rep.Samples[plane] = samples
		rep.Planes[plane] = summarise(samples)
		st := rep.Planes[plane]
		fmt.Printf("%s %s: n=%d p50 %.0f µs p90 %.0f µs p99 %.0f µs (errors %d)\n", label, plane, st.N, st.P50, st.P90, st.P99, rep.Errors[plane])
	}
	if d := decisionLatency(ctrl); d != nil {
		rep.Decision = d
		fmt.Printf("%s Gjallarhorn decision (in gateway, last %.0f decisions): p50 %.1f µs p90 %.1f µs p99 %.1f µs max %.0f µs\n",
			label, d["window"], d["p50_us"], d["p90_us"], d["p99_us"], d["max_us"])
	}
	return rep
}

// decisionLatency reads Gjallarhorn's own decision timing, or nil when the plugin is off.
func decisionLatency(ctrl string) map[string]float64 {
	req, _ := http.NewRequest(http.MethodGet, ctrl+"/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("GJ_ADMIN_TOKEN"))
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var st struct {
		Decision map[string]float64 `json:"decision_latency"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&st) != nil {
		return nil
	}
	return st.Decision
}

type compareReport struct {
	Kind     string                          `json:"kind"`
	Baseline []string                        `json:"baseline"`
	Compared []string                        `json:"compared"`
	Rounds   map[string][]map[string]float64 `json:"rounds"`
	Planes   map[string]map[string]float64   `json:"planes"`
}

// compare pools the samples of several runs per side, so one slow or fast container start
// doesn't decide the result, and also reports each round's medians.
func compare(a, b string) compareReport {
	load := func(list string) ([]overheadReport, []string) {
		var out []overheadReport
		var names []string
		for _, p := range strings.Split(list, ",") {
			data, err := os.ReadFile(strings.TrimSpace(p))
			if err != nil {
				log.Fatal(err)
			}
			var r overheadReport
			if err := json.Unmarshal(data, &r); err != nil {
				log.Fatal(err)
			}
			out = append(out, r)
			names = append(names, filepath.Base(p))
		}
		return out, names
	}
	offs, offNames := load(a)
	ons, onNames := load(b)
	pool := func(rs []overheadReport, plane string) Stats {
		var all []float64
		for _, r := range rs {
			all = append(all, r.Samples[plane]...)
		}
		return summarise(all)
	}
	rep := compareReport{Kind: "overhead-comparison", Baseline: offNames, Compared: onNames,
		Rounds: map[string][]map[string]float64{}, Planes: map[string]map[string]float64{}}
	var lines []string
	for _, plane := range []string{"llm", "mcp"} {
		for i := range offs {
			if i < len(ons) {
				rep.Rounds[plane] = append(rep.Rounds[plane], map[string]float64{
					"p50_off_us": offs[i].Planes[plane].P50, "p50_on_us": ons[i].Planes[plane].P50})
			}
		}
		x, y := pool(offs, plane), pool(ons, plane)
		rep.Planes[plane] = map[string]float64{
			"p50_off_us": x.P50, "p50_on_us": y.P50, "p50_added_us": y.P50 - x.P50,
			"p90_off_us": x.P90, "p90_on_us": y.P90, "p90_added_us": y.P90 - x.P90,
			"mean_off_us": x.Mean, "mean_on_us": y.Mean, "mean_added_us": y.Mean - x.Mean,
		}
		lines = append(lines, fmt.Sprintf("%s: p50 %.0f → %.0f µs (%+.0f), p90 %.0f → %.0f µs (%+.0f)", plane, x.P50, y.P50, y.P50-x.P50, x.P90, y.P90, y.P90-x.P90))
	}
	fmt.Println(strings.Join(lines, "\n"))
	return rep
}
