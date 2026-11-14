package control

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/copilot"
	"github.com/shdra06/Gjallarhorn/engine/incident"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
)

// busy limits how many co-pilot requests run at once.
func (s *server) busy(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.copilot <- struct{}{}:
			defer func() { <-s.copilot }()
			fn(w, r)
		default:
			writeError(w, http.StatusTooManyRequests, "the co-pilot is busy; try again in a moment")
		}
	}
}

func (s *server) listIncidents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = n
	}
	h, err := s.e.History()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	notes := incident.Notable(h, limit)
	if notes == nil {
		notes = []incident.Step{}
	}
	writeJSON(w, http.StatusOK, notes)
}

// load builds the incident around seq along with the history it came from.
func (s *server) load(w http.ResponseWriter, seq uint64) (*incident.Incident, []recorder.Event, bool) {
	h, err := s.e.History()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, nil, false
	}
	in, err := incident.Build(h, seq)
	if errors.Is(err, incident.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no recorded call with that sequence number")
		return nil, nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, nil, false
	}
	return in, h, true
}

func (s *server) getIncident(w http.ResponseWriter, r *http.Request) {
	seq, err := strconv.ParseUint(r.PathValue("seq"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "seq must be a sequence number")
		return
	}
	if in, _, ok := s.load(w, seq); ok {
		writeJSON(w, http.StatusOK, in)
	}
}

type copilotInfo struct {
	// Model is the model the co-pilot uses, empty when it runs without one.
	Model string `json:"model,omitempty"`
	// Mode is "llm" with a model and "grounded" without.
	Mode string `json:"mode"`
}

func (s *server) copilotInfo(w http.ResponseWriter, _ *http.Request) {
	info := copilotInfo{Mode: "grounded"}
	if s.opts.Copilot != nil {
		info.Model, info.Mode = s.opts.Copilot.Model(), "llm"
	}
	writeJSON(w, http.StatusOK, info)
}

type seqRequest struct {
	Seq uint64 `json:"seq"`
}

func (s *server) explain(w http.ResponseWriter, r *http.Request) {
	var req seqRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in, _, ok := s.load(w, req.Seq)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, copilot.Explain(r.Context(), s.opts.Copilot, in, s.e.Policy()))
}

type suggestResponse struct {
	copilot.Suggestions
	// PolicyETag identifies the policy the suggestions were proven against. Send it as
	// If-Match when applying one.
	PolicyETag string `json:"policy_etag"`
}

func (s *server) suggest(w http.ResponseWriter, r *http.Request) {
	var req seqRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in, h, ok := s.load(w, req.Seq)
	if !ok {
		return
	}
	pol := s.e.Policy()
	res := copilot.Suggest(r.Context(), s.opts.Copilot, in, pol, h)
	writeJSON(w, http.StatusOK, suggestResponse{Suggestions: res, PolicyETag: engine.PolicyHash(pol)})
}

type applyRequest struct {
	// Seq is the call the rule must stop.
	Seq  uint64      `json:"seq"`
	Rule policy.Rule `json:"rule"`
	// AcceptReview applies a rule that blocks other, unlabelled calls. Rules that block normal
	// (benign) work are never applied.
	AcceptReview bool `json:"accept_review,omitempty"`
}

type applyResponse struct {
	Applied    bool               `json:"applied"`
	Suggestion copilot.Suggestion `json:"suggestion"`
	PolicyETag string             `json:"policy_etag"`
}

// apply adds a co-pilot rule to the live policy, but only after proving it again against the
// current policy and everything recorded so far. A proof from minutes ago is not enough: new
// traffic may have arrived that the rule would break.
func (s *server) apply(w http.ResponseWriter, r *http.Request) {
	var req applyRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	want := strings.Trim(r.Header.Get("If-Match"), `"`)
	if want == "" {
		writeError(w, http.StatusPreconditionRequired, "send the policy_etag from the suggestion as If-Match")
		return
	}
	in, h, ok := s.load(w, req.Seq)
	if !ok {
		return
	}
	pol := s.e.Policy()
	if want != engine.PolicyHash(pol) {
		writeError(w, http.StatusPreconditionFailed, "the policy changed since the suggestion was made; ask for new suggestions")
		return
	}
	sg := copilot.Prove(in, pol, h, req.Rule)
	res := applyResponse{Suggestion: sg, PolicyETag: want}
	switch {
	case sg.Status == copilot.StatusRejected:
		writeJSON(w, http.StatusConflict, res)
		return
	case sg.Status == copilot.StatusReview && !req.AcceptReview:
		writeJSON(w, http.StatusConflict, res)
		return
	}
	err := s.e.ReplacePolicy(pol, sg.Policy, operator(r, "")+" via co-pilot")
	if errors.Is(err, engine.ErrPolicyChanged) {
		writeError(w, http.StatusPreconditionFailed, "the policy changed while the rule was being proven; ask for new suggestions")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "policy is invalid", splitErrors(err)...)
		return
	}
	res.Applied, res.PolicyETag = true, engine.PolicyHash(sg.Policy)
	res.Suggestion.Policy = nil
	w.Header().Set("ETag", `"`+res.PolicyETag+`"`)
	writeJSON(w, http.StatusOK, res)
}
