package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Driver is the demo traffic driver behind the console's demo panel. It runs scenarios on
// request and can keep a loop of normal work going so the airspace is never empty. Scenarios
// never overlap, because each one is judged by the change in the effects ledger.
//
// Unlike the attack lab, the driver leaves lockdowns alone: if a rule locks an agent, or an
// operator locks one from the console, it stays locked until someone lifts it.
type Driver struct {
	env       *Env
	scenarios []Scenario
	pause     time.Duration

	run sync.Mutex // one scenario at a time

	mu      sync.Mutex
	traffic context.CancelFunc
	runs    int
	last    *Result
}

// NewDriver returns a driver for the given scenarios (normally Scenarios()).
func NewDriver(env *Env, scenarios []Scenario) *Driver {
	env.KeepLockdowns = true
	return &Driver{env: env, scenarios: scenarios, pause: 1500 * time.Millisecond}
}

type scenarioInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Title string `json:"title"`
	Story string `json:"story"`
}

type trafficState struct {
	On   bool    `json:"on"`
	Runs int     `json:"runs"`
	Last *Result `json:"last,omitempty"`
}

// Handler serves the driver API:
//
//	GET  /scenarios              the catalogue
//	POST /scenarios/{id}/run     run one scenario and return its judged result
//	GET  /traffic                whether the normal-work loop is running
//	PUT  /traffic {"on": true}   start or stop it
func (d *Driver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /scenarios", func(w http.ResponseWriter, _ *http.Request) {
		out := make([]scenarioInfo, 0, len(d.scenarios))
		for _, s := range d.scenarios {
			out = append(out, scenarioInfo{ID: s.ID, Label: s.Label, Title: s.Title, Story: s.Story})
		}
		writeDriverJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /scenarios/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		for _, s := range d.scenarios {
			if s.ID == r.PathValue("id") {
				res := d.runOne(r.Context(), s)
				writeDriverJSON(w, http.StatusOK, res)
				return
			}
		}
		writeDriverJSON(w, http.StatusNotFound, map[string]string{"error": "no such scenario"})
	})
	mux.HandleFunc("GET /traffic", func(w http.ResponseWriter, _ *http.Request) {
		writeDriverJSON(w, http.StatusOK, d.state())
	})
	mux.HandleFunc("PUT /traffic", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			On *bool `json:"on"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.On == nil {
			writeDriverJSON(w, http.StatusBadRequest, map[string]string{"error": `send {"on": true} or {"on": false}`})
			return
		}
		d.SetTraffic(*body.On)
		writeDriverJSON(w, http.StatusOK, d.state())
	})
	return mux
}

func writeDriverJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (d *Driver) state() trafficState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return trafficState{On: d.traffic != nil, Runs: d.runs, Last: d.last}
}

// runOne runs a scenario, judged under the mode Gjallarhorn is in right now.
func (d *Driver) runOne(ctx context.Context, s Scenario) Result {
	d.run.Lock()
	defer d.run.Unlock()
	var mode struct {
		Enforcing *bool `json:"enforcing"`
	}
	_ = d.env.control(ctx, http.MethodGet, "/v1/mode", nil, &mode)
	enforcing := mode.Enforcing == nil || *mode.Enforcing
	res := RunOne(ctx, d.env, s, enforcing)
	d.mu.Lock()
	d.runs++
	d.last = &res
	d.mu.Unlock()
	return res
}

// SetTraffic starts or stops the loop of normal work.
func (d *Driver) SetTraffic(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !on {
		if d.traffic != nil {
			d.traffic()
			d.traffic = nil
		}
		return
	}
	if d.traffic != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.traffic = cancel
	var benign []Scenario
	for _, s := range d.scenarios {
		if s.Label == "benign" {
			benign = append(benign, s)
		}
	}
	if len(benign) == 0 {
		return
	}
	go func() {
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d.pause):
			}
			run, cancelRun := context.WithTimeout(ctx, time.Minute)
			d.runOne(run, benign[i%len(benign)])
			cancelRun()
		}
	}()
}
