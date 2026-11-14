package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStack answers the control API and ledger calls RunOne makes.
func fakeStack(t *testing.T, unlocks *atomic.Int32) *Env {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/mode":
			_, _ = w.Write([]byte(`{"enforcing":true}`))
		case r.URL.Path == "/v1/stats":
			_, _ = w.Write([]byte(`{"last_seq":0}`))
		case r.URL.Path == "/v1/events":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/v1/lockdowns" && r.Method == http.MethodGet:
			unlocks.Add(1)
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/ledger":
			_, _ = w.Write([]byte(`{"effects":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Env{Control: srv.URL, Token: "t", Ledger: srv.URL + "/ledger", HTTP: srv.Client()}
}

func TestDriver(t *testing.T) {
	var unlocks atomic.Int32
	env := fakeStack(t, &unlocks)
	var ran atomic.Int32
	run := func(context.Context, *Env) (string, error) { ran.Add(1); return "ok", nil }
	d := NewDriver(env, []Scenario{
		{ID: "a1", Label: "attack", Title: "An attack", Harm: map[string]int{}, Run: run},
		{ID: "b1", Label: "benign", Title: "Normal work", Harm: map[string]int{}, Run: run},
	})
	d.pause = 10 * time.Millisecond
	srv := httptest.NewServer(d.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/scenarios")
	if err != nil {
		t.Fatal(err)
	}
	var list []scenarioInfo
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 2 || list[0].ID != "a1" {
		t.Fatalf("catalogue: %+v", list)
	}

	resp, _ = http.Post(srv.URL+"/scenarios/a1/run", "application/json", nil)
	var res Result
	_ = json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()
	if res.ID != "a1" || !res.Enforcing || !res.Passed {
		t.Fatalf("run: %+v", res)
	}
	if unlocks.Load() != 0 {
		t.Fatal("the driver must leave lockdowns alone")
	}
	if resp, _ := http.Post(srv.URL+"/scenarios/nope/run", "application/json", nil); resp.StatusCode != 404 {
		t.Fatalf("unknown scenario: %d", resp.StatusCode)
	}

	put := func(body string) int {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/traffic", strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if put(`{}`) != http.StatusBadRequest {
		t.Fatal("on is required")
	}
	before := ran.Load()
	if put(`{"on":true}`) != 200 {
		t.Fatal("start traffic")
	}
	deadline := time.Now().Add(3 * time.Second)
	for ran.Load() < before+3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ran.Load() < before+3 || !d.state().On {
		t.Fatalf("traffic loop did not run: %d", ran.Load()-before)
	}
	put(`{"on":false}`)
	time.Sleep(50 * time.Millisecond)
	stopped := ran.Load()
	time.Sleep(100 * time.Millisecond)
	if ran.Load() > stopped+1 || d.state().On {
		t.Fatal("traffic loop did not stop")
	}
}
