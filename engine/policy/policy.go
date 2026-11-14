// Package policy decides whether a call through the gateway may go ahead.
//
// A decision is made from Facts: who is calling (the actor), who started the chain (the
// origin), every principal in between, the plane (llm, mcp or a2a) and the target (a model,
// a tool or an agent). The evaluation order is fixed and documented on Evaluate, and deny
// always wins over allow.
package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/identity"
)

// Plane is the kind of traffic.
type Plane string

const (
	PlaneLLM Plane = "llm" // model calls
	PlaneMCP Plane = "mcp" // tool calls through the MCP gateway
	PlaneA2A Plane = "a2a" // agent-to-agent calls through the Agent Gateway
)

// Valid reports whether p is a known plane.
func (p Plane) Valid() bool { return p == PlaneLLM || p == PlaneMCP || p == PlaneA2A }

// Effect is the outcome of a rule or decision.
type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
)

// Valid reports whether e is a known effect.
func (e Effect) Valid() bool { return e == Allow || e == Deny }

// Mode controls whether a rule is enforced or only observed.
type Mode string

const (
	Enforce Mode = "enforce"
	Monitor Mode = "monitor"
)

// Decision codes. They are stable identifiers used in logs, metrics and the API.
const (
	CodeTicket        = "ticket"         // a presented chain ticket failed verification
	CodeLockdown      = "lockdown"       // a principal in the chain (or the target agent) is locked
	CodeUnknownCaller = "unknown_caller" // the credential is not registered and unknown callers are denied
	CodeLoop          = "loop"           // the chain visits the same agent twice
	CodeMaxDepth      = "max_depth"      // the chain is longer than allowed
	CodeGrant         = "grant"          // a temporary grant allowed the call
	CodeRule          = "rule"           // a policy rule decided
	CodeDefault       = "default"        // no rule matched; the default effect applied
)

// Facts describe one call. They are fully resolved, so a recorded Facts value can be
// re-evaluated later under a different policy (see package replay).
type Facts struct {
	Time   time.Time `json:"time"`
	Plane  Plane     `json:"plane"`
	Target string    `json:"target"`
	// Actor is the principal making this call. For a delegated call it is the agent holding
	// the chain ticket; otherwise it is whoever owns the credential.
	Actor identity.Principal `json:"actor"`
	// Origin started the chain. For a direct call Origin == Actor.
	Origin identity.Principal `json:"origin"`
	// Chain lists the origin first and the actor last. A direct call has a single element.
	Chain []identity.Principal `json:"chain"`
	// Depth is the number of agent hops in the chain (0 for a direct call).
	Depth int `json:"depth"`
	// Credential is the principal the virtual key resolved to. When an agent uses a forwarded
	// credential this is the human, not the agent.
	Credential    identity.Principal `json:"credential"`
	TicketID      string             `json:"ticket_id,omitempty"`
	TicketProblem string             `json:"ticket_problem,omitempty"`
	TaskID        string             `json:"task_id,omitempty"`
}

// Via returns the principals strictly between the origin and the actor.
func (f Facts) Via() []identity.Principal {
	if len(f.Chain) <= 2 {
		return nil
	}
	return f.Chain[1 : len(f.Chain)-1]
}

// Match selects calls. Every field that is set must match; empty fields match everything.
// Lists match when any element matches. Patterns are globs (see Glob).
type Match struct {
	Planes       []Plane           `json:"planes,omitempty"`
	Targets      []string          `json:"targets,omitempty"`
	Actors       []string          `json:"actors,omitempty"`
	ActorKinds   []identity.Kind   `json:"actor_kinds,omitempty"`
	ActorLabels  map[string]string `json:"actor_labels,omitempty"`
	Origins      []string          `json:"origins,omitempty"`
	OriginKinds  []identity.Kind   `json:"origin_kinds,omitempty"`
	OriginLabels map[string]string `json:"origin_labels,omitempty"`
	// Via matches when any principal strictly between the origin and the actor matches.
	Via []string `json:"via,omitempty"`
	// Delegated matches calls made inside a chain (true) or direct calls (false).
	Delegated *bool `json:"delegated,omitempty"`
	MinDepth  *int  `json:"min_depth,omitempty"`
	MaxDepth  *int  `json:"max_depth,omitempty"`
}

// Matches reports whether f is selected by m.
func (m Match) Matches(f Facts) bool {
	if len(m.Planes) > 0 && !containsPlane(m.Planes, f.Plane) {
		return false
	}
	if !AnyGlob(m.Targets, f.Target) {
		return false
	}
	if !AnyGlob(m.Actors, f.Actor.ID) || !kindIn(m.ActorKinds, f.Actor.Kind) || !labelsMatch(m.ActorLabels, f.Actor) {
		return false
	}
	if !AnyGlob(m.Origins, f.Origin.ID) || !kindIn(m.OriginKinds, f.Origin.Kind) || !labelsMatch(m.OriginLabels, f.Origin) {
		return false
	}
	if len(m.Via) > 0 {
		found := false
		for _, p := range f.Via() {
			if AnyGlob(m.Via, p.ID) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if m.Delegated != nil && *m.Delegated != (f.Depth > 0) {
		return false
	}
	if m.MinDepth != nil && f.Depth < *m.MinDepth {
		return false
	}
	if m.MaxDepth != nil && f.Depth > *m.MaxDepth {
		return false
	}
	return true
}

func containsPlane(list []Plane, p Plane) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}

func kindIn(list []identity.Kind, k identity.Kind) bool {
	if len(list) == 0 {
		return true
	}
	for _, x := range list {
		if x == k {
			return true
		}
	}
	return false
}

// labelsMatch requires every listed label to equal the given value. A value of "*" means
// "the label is present with any value"; a value of "!" means "the label is absent".
func labelsMatch(want map[string]string, p identity.Principal) bool {
	for k, v := range want {
		got, ok := p.Labels[k]
		switch v {
		case "*":
			if !ok {
				return false
			}
		case "!":
			if ok {
				return false
			}
		default:
			if !ok || got != v {
				return false
			}
		}
	}
	return true
}

// Rule is one policy statement.
type Rule struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Effect      Effect `json:"effect"`
	// Mode defaults to enforce. A monitor rule never blocks; its would-be denials are recorded.
	Mode   Mode   `json:"mode,omitempty"`
	Match  Match  `json:"match"`
	Unless *Match `json:"unless,omitempty"`
	// Lockdown locks the actor when this rule denies a call (enforce mode only).
	Lockdown bool `json:"lockdown,omitempty"`
	Disabled bool `json:"disabled,omitempty"`
}

func (r Rule) mode() Mode {
	if r.Mode == "" {
		return Enforce
	}
	return r.Mode
}

// Applies reports whether the rule selects f.
func (r Rule) Applies(f Facts) bool {
	if r.Disabled || !r.Match.Matches(f) {
		return false
	}
	if r.Unless != nil && r.Unless.Matches(f) {
		return false
	}
	return true
}

// Guards are structural protections that apply before any rule.
type Guards struct {
	// MaxDepth is the longest allowed delegation chain, counted in agent hops. Default 4.
	MaxDepth int `json:"max_depth,omitempty"`
	// AllowLoops permits a chain to visit the same agent twice. Default false.
	AllowLoops bool `json:"allow_loops,omitempty"`
	// AcceptBadTickets lets calls with an invalid chain ticket through (they are still
	// recorded). Default false: a forged, expired or mismatched ticket is denied.
	AcceptBadTickets bool `json:"accept_bad_tickets,omitempty"`
	// UnknownCallers is the effect for credentials that are not registered. Default allow,
	// so installing Gjallarhorn never breaks existing traffic.
	UnknownCallers Effect `json:"unknown_callers,omitempty"`
}

// DefaultMaxDepth is used when Guards.MaxDepth is zero.
const DefaultMaxDepth = 4

func (g Guards) maxDepth() int {
	if g.MaxDepth <= 0 {
		return DefaultMaxDepth
	}
	return g.MaxDepth
}

// Policy is a complete, versioned rule set.
type Policy struct {
	Version       int    `json:"version"`
	DefaultEffect Effect `json:"default_effect,omitempty"`
	Guards        Guards `json:"guards,omitempty"`
	Rules         []Rule `json:"rules,omitempty"`
}

// Default returns a permissive policy: guards on, no rules, default allow.
func Default() *Policy { return &Policy{Version: 1, DefaultEffect: Allow} }

func (p *Policy) defaultEffect() Effect {
	if p.DefaultEffect == "" {
		return Allow
	}
	return p.DefaultEffect
}

var ruleID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Validate checks the policy and returns every problem it finds, not just the first.
func (p *Policy) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if p.Version != 1 {
		add("version must be 1")
	}
	if p.DefaultEffect != "" && !p.DefaultEffect.Valid() {
		add("default_effect must be allow or deny")
	}
	if p.Guards.MaxDepth < 0 || p.Guards.MaxDepth > 32 {
		add("guards.max_depth must be between 1 and 32")
	}
	if p.Guards.UnknownCallers != "" && !p.Guards.UnknownCallers.Valid() {
		add("guards.unknown_callers must be allow or deny")
	}
	seen := map[string]bool{}
	for i, r := range p.Rules {
		where := fmt.Sprintf("rules[%d]", i)
		if !ruleID.MatchString(r.ID) {
			add("%s: id %q must be lowercase letters, digits, '.', '_' or '-'", where, r.ID)
		} else {
			where = fmt.Sprintf("rules[%d] (%s)", i, r.ID)
		}
		if seen[r.ID] {
			add("%s: duplicate id", where)
		}
		seen[r.ID] = true
		if !r.Effect.Valid() {
			add("%s: effect must be allow or deny", where)
		}
		if r.Mode != "" && r.Mode != Enforce && r.Mode != Monitor {
			add("%s: mode must be enforce or monitor", where)
		}
		if r.Lockdown && r.Effect != Deny {
			add("%s: lockdown only makes sense on a deny rule", where)
		}
		errs = append(errs, validateMatch(where+".match", r.Match)...)
		if r.Unless != nil {
			errs = append(errs, validateMatch(where+".unless", *r.Unless)...)
		}
	}
	return errors.Join(errs...)
}

func validateMatch(where string, m Match) []error {
	var errs []error
	for _, pl := range m.Planes {
		if !pl.Valid() {
			errs = append(errs, fmt.Errorf("%s.planes: unknown plane %q", where, pl))
		}
	}
	patterns := []struct {
		field string
		list  []string
	}{{"targets", m.Targets}, {"actors", m.Actors}, {"origins", m.Origins}, {"via", m.Via}}
	for _, f := range patterns {
		for _, pat := range f.list {
			if err := ValidatePattern(pat); err != nil {
				errs = append(errs, fmt.Errorf("%s.%s: %q: %w", where, f.field, pat, err))
			}
		}
	}
	kinds := []struct {
		field string
		list  []identity.Kind
	}{{"actor_kinds", m.ActorKinds}, {"origin_kinds", m.OriginKinds}}
	for _, f := range kinds {
		for _, k := range f.list {
			if !k.Valid() {
				errs = append(errs, fmt.Errorf("%s.%s: unknown kind %q", where, f.field, k))
			}
		}
	}
	if m.MinDepth != nil && *m.MinDepth < 0 {
		errs = append(errs, fmt.Errorf("%s.min_depth must not be negative", where))
	}
	if m.MaxDepth != nil && *m.MaxDepth < 0 {
		errs = append(errs, fmt.Errorf("%s.max_depth must not be negative", where))
	}
	if m.MinDepth != nil && m.MaxDepth != nil && *m.MinDepth > *m.MaxDepth {
		errs = append(errs, fmt.Errorf("%s: min_depth is greater than max_depth", where))
	}
	return errs
}

// Parse decodes and validates a policy document. Unknown fields are rejected so typos in a
// security policy are caught instead of silently ignored.
func Parse(data []byte) (*Policy, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}
