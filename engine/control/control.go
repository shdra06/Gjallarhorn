// Package control is Gjallarhorn's operator API: lockdowns, policy, grants, simulations, events
// and a live event stream. The console and the AI co-pilot use it, and so can scripts.
//
// Every endpoint except /v1/health needs "Authorization: Bearer <admin token>". The token is
// compared in constant time. The live stream uses Server-Sent Events over a normal
// authenticated GET, so the token never has to appear in a URL.
package control

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/copilot"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
	"github.com/shdra06/Gjallarhorn/engine/replay"
)

// MinTokenLen is the minimum admin token length.
const MinTokenLen = 24

const maxBody = 1 << 20

// Options configures the API.
type Options struct {
	// AdminToken is required and must be at least MinTokenLen characters.
	AdminToken string
	// AllowedOrigins lists browser origins allowed to call the API (for the console).
	AllowedOrigins []string
	// Version is reported by /v1/health.
	Version string
	// Heartbeat is the SSE keep-alive interval (default 15s).
	Heartbeat time.Duration
	// Copilot is the model the AI co-pilot uses. Nil runs the co-pilot without a model:
	// explanations come from the recorded facts and rules are synthesised, then proven the
	// same way.
	Copilot *copilot.LLM
	// ConsoleDir, when set, serves the operator console (the built single-page app) at "/".
	ConsoleDir string
	// DemoDriver, when set, forwards /demo/... (admin token required) to a demo traffic driver
	// such as the attack lab's -serve mode, so the console can start demo traffic. Demo
	// environments only.
	DemoDriver string
}

// maxCopilotCalls bounds concurrent co-pilot requests, which can be slow and cost money.
const maxCopilotCalls = 2

type server struct {
	e       *engine.Engine
	opts    Options
	origins map[string]bool
	copilot chan struct{}
}

// NewHandler returns the API handler.
func NewHandler(e *engine.Engine, opts Options) (http.Handler, error) {
	if len(opts.AdminToken) < MinTokenLen {
		return nil, fmt.Errorf("control: admin token must be at least %d characters", MinTokenLen)
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 15 * time.Second
	}
	s := &server{e: e, opts: opts, origins: map[string]bool{}, copilot: make(chan struct{}, maxCopilotCalls)}
	for _, o := range opts.AllowedOrigins {
		s.origins[strings.TrimRight(o, "/")] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.Handle("GET /v1/principals", s.auth(s.principals))
	mux.Handle("GET /v1/lockdowns", s.auth(s.listLockdowns))
	mux.Handle("POST /v1/lockdowns", s.auth(s.lock))
	mux.Handle("DELETE /v1/lockdowns/{principal}", s.auth(s.unlock))
	mux.Handle("GET /v1/policy", s.auth(s.getPolicy))
	mux.Handle("PUT /v1/policy", s.auth(s.putPolicy))
	mux.Handle("POST /v1/policy/validate", s.auth(s.validatePolicy))
	mux.Handle("POST /v1/simulate", s.auth(s.simulate))
	mux.Handle("GET /v1/grants", s.auth(s.listGrants))
	mux.Handle("POST /v1/grants", s.auth(s.addGrant))
	mux.Handle("DELETE /v1/grants/{id}", s.auth(s.revokeGrant))
	mux.Handle("GET /v1/events", s.auth(s.events))
	mux.Handle("GET /v1/stream", s.auth(s.stream))
	mux.Handle("GET /v1/stats", s.auth(s.stats))
	mux.Handle("GET /v1/mode", s.auth(s.getMode))
	mux.Handle("PUT /v1/mode", s.auth(s.putMode))
	mux.Handle("GET /v1/incidents", s.auth(s.listIncidents))
	mux.Handle("GET /v1/incidents/{seq}", s.auth(s.getIncident))
	mux.Handle("GET /v1/copilot", s.auth(s.copilotInfo))
	mux.Handle("POST /v1/copilot/explain", s.auth(s.busy(s.explain)))
	mux.Handle("POST /v1/copilot/suggest", s.auth(s.busy(s.suggest)))
	mux.Handle("POST /v1/copilot/apply", s.auth(s.apply))
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	if opts.DemoDriver != "" {
		p, err := demoProxy(opts.DemoDriver)
		if err != nil {
			return nil, fmt.Errorf("control: demo driver: %w", err)
		}
		mux.Handle("/demo/", s.auth(p.ServeHTTP))
	}
	if opts.ConsoleDir != "" {
		mux.Handle("/", console(opts.ConsoleDir))
	}
	return s.cors(mux), nil
}

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && s.origins[origin] {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match, Last-Event-ID, X-Operator")
			h.Set("Access-Control-Expose-Headers", "ETag")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) auth(fn http.HandlerFunc) http.Handler {
	want := []byte("Bearer " + s.opts.AdminToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or invalid admin token")
			return
		}
		fn(w, r)
	})
}

// operator is who is acting, for the audit trail. Scripts and the console send X-Operator.
func operator(r *http.Request, fallback string) string {
	if v := strings.TrimSpace(r.Header.Get("X-Operator")); v != "" && len(v) <= 128 {
		return v
	}
	if fallback != "" {
		return fallback
	}
	return "operator"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

type apiError struct {
	Error   string   `json:"error"`
	Details []string `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string, details ...string) {
	writeJSON(w, status, apiError{Error: msg, Details: details})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func splitErrors(err error) []string {
	if err == nil {
		return nil
	}
	return strings.Split(err.Error(), "\n")
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	st := s.e.Recorder().Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"version":   s.opts.Version,
		"events":    st.Events,
		"lockdowns": len(s.e.Lockdowns()),
	})
}

func (s *server) principals(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.e.Registry().All())
}

func (s *server) listLockdowns(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.e.Lockdowns())
}

type lockRequest struct {
	Principal string `json:"principal"`
	Reason    string `json:"reason"`
	By        string `json:"by,omitempty"`
	// Duration optionally limits the lockdown, e.g. "30m".
	Duration string `json:"duration,omitempty"`
}

func (s *server) lock(w http.ResponseWriter, r *http.Request) {
	var req lockRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var until time.Time
	if req.Duration != "" {
		d, err := time.ParseDuration(req.Duration)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "duration must be positive, like \"30m\"")
			return
		}
		until = time.Now().Add(d)
	}
	entry, err := s.e.Lock(req.Principal, req.Reason, operator(r, req.By), false, "", until)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (s *server) unlock(w http.ResponseWriter, r *http.Request) {
	ok, err := s.e.Unlock(r.PathValue("principal"), operator(r, ""))
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	case !ok:
		writeError(w, http.StatusNotFound, "no lockdown for that principal")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) getPolicy(w http.ResponseWriter, _ *http.Request) {
	p := s.e.Policy()
	w.Header().Set("ETag", `"`+engine.PolicyHash(p)+`"`)
	writeJSON(w, http.StatusOK, p)
}

func (s *server) putPolicy(w http.ResponseWriter, r *http.Request) {
	var p policy.Policy
	if err := decode(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Optimistic concurrency: if the client sends If-Match, it must match the current policy.
	cur := s.e.Policy()
	want := strings.Trim(r.Header.Get("If-Match"), `"`)
	if want != "" && want != engine.PolicyHash(cur) {
		writeError(w, http.StatusPreconditionFailed, "the policy changed since you loaded it; reload and try again")
		return
	}
	var err error
	if want != "" {
		err = s.e.ReplacePolicy(cur, &p, operator(r, ""))
	} else {
		err = s.e.SetPolicy(&p, operator(r, ""))
	}
	if errors.Is(err, engine.ErrPolicyChanged) {
		writeError(w, http.StatusPreconditionFailed, "the policy changed since you loaded it; reload and try again")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "policy is invalid", splitErrors(err)...)
		return
	}
	w.Header().Set("ETag", `"`+engine.PolicyHash(&p)+`"`)
	writeJSON(w, http.StatusOK, &p)
}

func (s *server) validatePolicy(w http.ResponseWriter, r *http.Request) {
	var p policy.Policy
	if err := decode(r, &p); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "errors": []string{err.Error()}})
		return
	}
	err := p.Validate()
	writeJSON(w, http.StatusOK, map[string]any{"valid": err == nil, "errors": splitErrors(err)})
}

type simulateRequest struct {
	Policy              *policy.Policy `json:"policy"`
	From                time.Time      `json:"from,omitempty"`
	To                  time.Time      `json:"to,omitempty"`
	ReevaluateLockdowns bool           `json:"reevaluate_lockdowns,omitempty"`
	MaxChanges          int            `json:"max_changes,omitempty"`
}

type simulateResponse struct {
	Safe bool `json:"safe"`
	replay.Report
}

func (s *server) simulate(w http.ResponseWriter, r *http.Request) {
	var req simulateRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Policy == nil {
		writeError(w, http.StatusBadRequest, "policy is required")
		return
	}
	rep, err := s.e.Simulate(req.Policy, replay.Options{From: req.From, To: req.To,
		ReevaluateLockdowns: req.ReevaluateLockdowns, MaxChanges: req.MaxChanges})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "simulation failed", splitErrors(err)...)
		return
	}
	writeJSON(w, http.StatusOK, simulateResponse{Safe: rep.Safe(), Report: rep})
}

func (s *server) listGrants(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.e.Grants())
}

type grantRequest struct {
	Actor    string       `json:"actor"`
	Plane    policy.Plane `json:"plane"`
	Target   string       `json:"target"`
	TaskID   string       `json:"task_id,omitempty"`
	Duration string       `json:"duration"`
	Reason   string       `json:"reason"`
}

// MaxGrantDuration bounds how long a grant can last.
const MaxGrantDuration = 24 * time.Hour

func (s *server) addGrant(w http.ResponseWriter, r *http.Request) {
	var req grantRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	d, err := time.ParseDuration(req.Duration)
	if err != nil || d <= 0 || d > MaxGrantDuration {
		writeError(w, http.StatusBadRequest, "duration must be between 1s and 24h, like \"30m\"")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, "a reason is required: grants are audited exceptions")
		return
	}
	g, err := s.e.AddGrant(policy.Grant{Actor: req.Actor, Plane: req.Plane, Target: req.Target, TaskID: req.TaskID,
		ExpiresAt: time.Now().Add(d), Reason: req.Reason, CreatedBy: operator(r, "")})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *server) revokeGrant(w http.ResponseWriter, r *http.Request) {
	if !s.e.RevokeGrant(r.PathValue("id"), operator(r, "")) {
		writeError(w, http.StatusNotFound, "no such grant")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseAfter(r *http.Request) (uint64, error) {
	v := r.URL.Query().Get("after")
	if v == "" {
		v = r.Header.Get("Last-Event-ID")
	}
	if v == "" {
		return 0, nil
	}
	return strconv.ParseUint(v, 10, 64)
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	after, err := parseAfter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "after must be a sequence number")
		return
	}
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 5000 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 5000")
			return
		}
		limit = n
	}
	writeJSON(w, http.StatusOK, s.e.Recorder().Since(after, limit))
}

type modeBody struct {
	// Enforcing is true when decisions block calls, false in monitor-only mode.
	Enforcing *bool `json:"enforcing"`
}

func (s *server) getMode(w http.ResponseWriter, _ *http.Request) {
	on := s.e.Enforcing()
	writeJSON(w, http.StatusOK, modeBody{Enforcing: &on})
}

func (s *server) putMode(w http.ResponseWriter, r *http.Request) {
	var body modeBody
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Enforcing == nil {
		writeError(w, http.StatusBadRequest, "enforcing (true or false) is required")
		return
	}
	s.e.SetEnforcing(*body.Enforcing, operator(r, ""))
	on := s.e.Enforcing()
	writeJSON(w, http.StatusOK, modeBody{Enforcing: &on})
}

func (s *server) stats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.e.Recorder().Stats())
}

// stream sends recorded events as Server-Sent Events: first the backlog after ?after= (or
// Last-Event-ID), then live events, with a heartbeat comment to keep proxies from closing it.
func (s *server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}
	after, err := parseAfter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "after must be a sequence number")
		return
	}
	// Subscribe before reading the backlog so nothing falls in the gap; duplicates are skipped
	// by sequence number.
	ch, cancel := s.e.Recorder().Subscribe(1024)
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// A comment right away, so proxies pass the response on before the first event.
	if _, err := io.WriteString(w, ": connected\n\n"); err != nil {
		return
	}

	last := after
	send := func(e recorder.Event) error {
		if e.Seq <= last {
			return nil
		}
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, b); err != nil {
			return err
		}
		last = e.Seq
		return nil
	}
	for _, e := range s.e.Recorder().Since(after, 0) {
		if err := send(e); err != nil {
			return
		}
	}
	flusher.Flush()

	tick := time.NewTicker(s.opts.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if err := send(e); err != nil {
				return
			}
			flusher.Flush()
		case <-tick.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
