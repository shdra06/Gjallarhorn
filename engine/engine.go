// Package engine wires Gjallarhorn's parts together: identity, chain tickets, policy,
// lockdowns, grants, the flight recorder and replay.
//
// The engine has no dependency on Bifrost. A gateway adapter (the Bifrost plugin) turns each
// hook call into a Request, asks the engine for a decision, and blocks or forwards the call.
// Keeping the engine separate means every rule can be tested on any machine, quickly.
package engine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/identity"
	"github.com/shdra06/Gjallarhorn/engine/lockdown"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
	"github.com/shdra06/Gjallarhorn/engine/replay"
	"github.com/shdra06/Gjallarhorn/engine/ticket"
)

// DefaultTicketHeader is the HTTP header that carries chain tickets.
const DefaultTicketHeader = "x-gjallarhorn-chain"

// Request is one call as seen by the gateway adapter.
type Request struct {
	ID    string
	Time  time.Time
	Plane policy.Plane
	// Target is the model (llm), the tool (mcp) or the Agent Gateway registration name (a2a).
	Target string
	// VirtualKey is the raw credential presented by the caller. It is never stored.
	VirtualKey string
	// Ticket is the raw value of the chain ticket header, if the caller sent one.
	Ticket    string
	TaskID    string
	SessionID string
	// Label tags traffic from the attack lab or the benign set ("attack", "benign"). It is
	// recorded so replays can show attacks and normal work separately.
	Label string
}

// Result is the engine's answer for one call.
type Result struct {
	Decision policy.Decision `json:"decision"`
	Facts    policy.Facts    `json:"facts"`
	// Seq is the flight-recorder sequence number of the decision event.
	Seq uint64 `json:"seq"`
	// Ticket is set by Delegate when the call is allowed: the new chain ticket to forward to
	// the agent being called.
	Ticket   string `json:"-"`
	TicketID string `json:"ticket_id,omitempty"`
	// Locked is set when this decision locked the actor down (a rule with lockdown: true).
	Locked bool `json:"locked,omitempty"`
}

// Engine makes decisions. It is safe for concurrent use.
type Engine struct {
	cfg       Config
	registry  *identity.Registry
	issuer    *ticket.Issuer
	keys      *ticket.Keyring
	locks     *lockdown.Registry
	rec       *recorder.Recorder
	pol       atomic.Pointer[policy.Policy]
	grantsMu  sync.RWMutex
	grants    []policy.Grant
	now       func() time.Time
	closeOnce sync.Once
}

// Option configures an Engine.
type Option func(*Engine)

// WithClock replaces the time source for every component (tests and simulations).
func WithClock(now func() time.Time) Option { return func(e *Engine) { e.now = now } }

// New builds an engine from configuration.
func New(cfg Config, opts ...Option) (*Engine, error) {
	e := &Engine{now: time.Now}
	for _, o := range opts {
		o(e)
	}
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	e.cfg = cfg

	reg, err := identity.NewRegistry(cfg.Principals)
	if err != nil {
		return nil, fmt.Errorf("principals: %w", err)
	}
	e.registry = reg

	keys, err := ticket.NewKeyring(cfg.Tickets.ActiveKey, cfg.Tickets.Keys)
	if err != nil {
		return nil, fmt.Errorf("tickets: %w", err)
	}
	e.keys = keys
	e.issuer = ticket.NewIssuer(keys, ticket.WithTTL(cfg.Tickets.TTL.Duration), ticket.WithClock(e.now))

	lockOpts := []lockdown.Option{lockdown.WithClock(e.now)}
	if cfg.LockdownFile != "" {
		lockOpts = append(lockOpts, lockdown.WithFile(cfg.LockdownFile))
	}
	if e.locks, err = lockdown.New(lockOpts...); err != nil {
		return nil, err
	}

	recOpts := []recorder.Option{recorder.WithClock(e.now), recorder.WithRingSize(cfg.Recorder.RingSize)}
	if cfg.Recorder.Path != "" {
		if e.rec, err = recorder.Open(cfg.Recorder.Path, recOpts...); err != nil {
			return nil, err
		}
	} else {
		e.rec = recorder.NewMemory(recOpts...)
	}

	p := cfg.Policy
	if p == nil {
		p = policy.Default()
	}
	e.pol.Store(p)
	return e, nil
}

// Close flushes the flight recorder.
func (e *Engine) Close() error {
	var err error
	e.closeOnce.Do(func() { err = e.rec.Close() })
	return err
}

// TicketHeader is the header name agents must send back.
func (e *Engine) TicketHeader() string { return e.cfg.Tickets.Header }

// Registry exposes the principal registry (read-only use).
func (e *Engine) Registry() *identity.Registry { return e.registry }

// Recorder exposes the flight recorder (for the control API and live stream).
func (e *Engine) Recorder() *recorder.Recorder { return e.rec }

// Policy returns the active policy. Callers must not modify it.
func (e *Engine) Policy() *policy.Policy { return e.pol.Load() }

// resolved carries facts plus the verified ticket claims, if any.
type resolved struct {
	facts  policy.Facts
	claims *ticket.Claims
}

// Resolve turns a request into facts: who is calling, on whose behalf, through which chain.
func (e *Engine) Resolve(req Request) policy.Facts { return e.resolve(req).facts }

func (e *Engine) resolve(req Request) resolved {
	now := req.Time
	if now.IsZero() {
		now = e.now()
	}
	cred, _ := e.registry.ByVirtualKey(req.VirtualKey)
	f := policy.Facts{
		Time:       now.UTC(),
		Plane:      req.Plane,
		Target:     req.Target,
		Credential: cred,
		Actor:      cred,
		Origin:     cred,
		Chain:      []identity.Principal{cred},
		TaskID:     req.TaskID,
	}
	if req.Plane == policy.PlaneA2A {
		target, _ := e.registry.ByAgentName(req.Target)
		f.Target = target.ID
	}
	if req.Ticket == "" {
		return resolved{facts: f}
	}
	claims, err := e.issuer.Verify(req.Ticket)
	if err != nil {
		f.TicketProblem = ticket.Problem(err)
		return resolved{facts: f}
	}
	f.TicketID = claims.ID
	// Bind the ticket to the credential presented with it. An agent calling with its own key
	// must be the ticket holder; an agent calling with a forwarded key must be carrying the
	// key of the chain's origin. Anything else means the ticket was lifted from elsewhere.
	switch cred.Kind {
	case identity.KindAgent:
		if cred.ID != claims.Holder() {
			f.TicketProblem = "ticket_mismatch"
			return resolved{facts: f}
		}
	case identity.KindHuman, identity.KindService:
		if cred.ID != claims.Root {
			f.TicketProblem = "ticket_mismatch"
			return resolved{facts: f}
		}
	default:
		f.TicketProblem = "ticket_mismatch"
		return resolved{facts: f}
	}
	if claims.Context != "" && req.TaskID != "" && claims.Context != req.TaskID {
		f.TicketProblem = "ticket_mismatch"
		return resolved{facts: f}
	}
	chain := make([]identity.Principal, 0, len(claims.Hops)+1)
	chain = append(chain, e.principalFor(claims.Root, identity.Kind(claims.RootKind)))
	for _, h := range claims.Hops {
		chain = append(chain, e.principalFor(h, identity.KindAgent))
	}
	f.Origin = chain[0]
	f.Actor = chain[len(chain)-1]
	f.Chain = chain
	f.Depth = len(claims.Hops)
	return resolved{facts: f, claims: &claims}
}

func (e *Engine) principalFor(id string, kind identity.Kind) identity.Principal {
	if p, ok := e.registry.ByID(id); ok {
		return p
	}
	if !kind.Valid() || kind == "" {
		kind = identity.KindUnknown
	}
	return identity.Principal{ID: id, Kind: kind}
}

// Check decides a model or tool call (or an agent call when no ticket needs minting).
func (e *Engine) Check(req Request) Result {
	r := e.resolve(req)
	return e.decide(req, r, nil)
}

// Delegate decides an agent-to-agent call and, when it is allowed, mints the chain ticket to
// forward to the agent being called.
func (e *Engine) Delegate(req Request) Result {
	req.Plane = policy.PlaneA2A
	r := e.resolve(req)
	return e.decide(req, r, func(res *Result) {
		var (
			tok    string
			claims ticket.Claims
			err    error
		)
		if r.claims != nil {
			tok, claims, err = e.issuer.Extend(*r.claims, r.facts.Target, req.TaskID)
		} else {
			tok, claims, err = e.issuer.Start(r.facts.Origin.ID, string(r.facts.Origin.Kind), r.facts.Target, req.TaskID)
		}
		if err != nil {
			// Minting can only fail on a chain that is too long, which the depth guard
			// already blocks; fail closed if it ever happens anyway.
			res.Decision = policy.Decision{Effect: policy.Deny, Code: policy.CodeMaxDepth,
				Reason: "a chain ticket could not be issued: " + err.Error()}
			return
		}
		res.Ticket, res.TicketID = tok, claims.ID
	})
}

func (e *Engine) decide(req Request, r resolved, onAllow func(*Result)) Result {
	now := r.facts.Time
	d := policy.Evaluate(e.pol.Load(), r.facts, policy.Input{Locks: e.locks, Grants: e.activeGrants(now), Now: now})
	res := Result{Decision: d, Facts: r.facts}
	if d.Allowed() && onAllow != nil {
		onAllow(&res)
	}
	note := map[string]string{}
	if req.Label != "" {
		note["label"] = req.Label
	}
	if res.TicketID != "" {
		note["ticket_minted"] = res.TicketID
	}
	if fp := identity.ShortFingerprint(req.VirtualKey); fp != "" {
		note["key"] = fp
	}
	if len(note) == 0 {
		note = nil
	}
	ev, err := e.rec.Append(recorder.Event{Time: now, Type: recorder.TypeDecision, RequestID: req.ID,
		Facts: &res.Facts, Decision: &res.Decision, Note: note})
	if err == nil {
		res.Seq = ev.Seq
	}
	if !res.Decision.Allowed() && res.Decision.LockActor {
		if _, err := e.Lock(res.Facts.Actor.ID, res.Decision.Reason, "rule:"+res.Decision.RuleID, true, res.Decision.RuleID, time.Time{}); err == nil {
			res.Locked = true
		}
	}
	return res
}

// RecordOutcome stores what happened to an allowed call (status, error, latency, tokens).
func (e *Engine) RecordOutcome(requestID string, out recorder.Outcome) {
	_, _ = e.rec.Append(recorder.Event{Type: recorder.TypeOutcome, RequestID: requestID, Outcome: &out})
}

// Lock cuts a principal off from every plane, effective on its next call.
func (e *Engine) Lock(principal, reason, by string, auto bool, ruleID string, until time.Time) (lockdown.Entry, error) {
	if !identity.ValidID(principal) {
		return lockdown.Entry{}, fmt.Errorf("invalid principal id %q", principal)
	}
	entry, err := e.locks.Lock(lockdown.Entry{Principal: principal, Reason: reason, By: by, Since: e.now().UTC(),
		Auto: auto, RuleID: ruleID, Until: until})
	if err != nil {
		return lockdown.Entry{}, err
	}
	note := map[string]string{"principal": principal, "reason": entry.Reason, "by": by}
	if auto {
		note["auto"] = "true"
		note["rule_id"] = ruleID
	}
	if !until.IsZero() {
		note["until"] = until.UTC().Format(time.RFC3339)
	}
	_, _ = e.rec.Append(recorder.Event{Type: recorder.TypeLockdown, Note: note})
	return entry, nil
}

// Unlock lifts a lockdown. It reports whether one existed.
func (e *Engine) Unlock(principal, by string) (bool, error) {
	_, ok, err := e.locks.Unlock(principal)
	if err != nil || !ok {
		return ok, err
	}
	_, _ = e.rec.Append(recorder.Event{Type: recorder.TypeUnlock, Note: map[string]string{"principal": principal, "by": by}})
	return true, nil
}

// Lockdowns lists active lockdowns.
func (e *Engine) Lockdowns() []lockdown.Entry { return e.locks.List() }

// IsLocked reports whether a principal is locked right now.
func (e *Engine) IsLocked(principal string) bool { return e.locks.IsLocked(principal, e.now()) }

// SetPolicy validates and atomically replaces the policy.
func (e *Engine) SetPolicy(p *policy.Policy, by string) error {
	if p == nil {
		return errors.New("policy is nil")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	e.pol.Store(p)
	_, _ = e.rec.Append(recorder.Event{Type: recorder.TypePolicy, Note: map[string]string{
		"by": by, "policy_sha256": PolicyHash(p), "rules": fmt.Sprint(len(p.Rules))}})
	return nil
}

// PolicyHash returns a stable fingerprint of a policy, recorded on every change.
func PolicyHash(p *policy.Policy) string {
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// AddGrant stores a temporary grant. ID and CreatedAt are filled in when empty.
func (e *Engine) AddGrant(g policy.Grant) (policy.Grant, error) {
	if g.ID == "" {
		g.ID = "grant-" + randomID()
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = e.now().UTC()
	}
	if err := g.Validate(); err != nil {
		return policy.Grant{}, err
	}
	if !g.ExpiresAt.After(g.CreatedAt) {
		return policy.Grant{}, errors.New("grant expiry must be in the future")
	}
	e.grantsMu.Lock()
	for _, x := range e.grants {
		if x.ID == g.ID {
			e.grantsMu.Unlock()
			return policy.Grant{}, fmt.Errorf("grant %s already exists", g.ID)
		}
	}
	e.grants = append(e.grants, g)
	e.grantsMu.Unlock()
	_, _ = e.rec.Append(recorder.Event{Type: recorder.TypeGrant, Note: map[string]string{
		"action": "create", "id": g.ID, "actor": g.Actor, "plane": string(g.Plane), "target": g.Target,
		"expires_at": g.ExpiresAt.UTC().Format(time.RFC3339), "by": g.CreatedBy}})
	return g, nil
}

// RevokeGrant removes a grant. It reports whether it existed.
func (e *Engine) RevokeGrant(id, by string) bool {
	e.grantsMu.Lock()
	idx := -1
	for i, g := range e.grants {
		if g.ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		e.grants = append(e.grants[:idx], e.grants[idx+1:]...)
	}
	e.grantsMu.Unlock()
	if idx < 0 {
		return false
	}
	_, _ = e.rec.Append(recorder.Event{Type: recorder.TypeGrant, Note: map[string]string{"action": "revoke", "id": id, "by": by}})
	return true
}

// Grants returns unexpired grants, soonest expiry first.
func (e *Engine) Grants() []policy.Grant {
	out := e.activeGrants(e.now())
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	return out
}

func (e *Engine) activeGrants(now time.Time) []policy.Grant {
	e.grantsMu.RLock()
	defer e.grantsMu.RUnlock()
	if len(e.grants) == 0 {
		return nil
	}
	out := make([]policy.Grant, 0, len(e.grants))
	for _, g := range e.grants {
		if now.Before(g.ExpiresAt) {
			out = append(out, g)
		}
	}
	return out
}

// History returns recorded events for replay: the whole verified log when the recorder
// writes to a file, otherwise what is in memory.
func (e *Engine) History() ([]recorder.Event, error) {
	if e.cfg.Recorder.Path == "" {
		return e.rec.Recent(0), nil
	}
	f, err := os.Open(e.cfg.Recorder.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	events, err := recorder.ReadAll(f)
	if err != nil {
		return nil, err
	}
	// Events appended but not yet flushed are still in memory; include them.
	var last uint64
	if len(events) > 0 {
		last = events[len(events)-1].Seq
	}
	return append(events, e.rec.Since(last, 0)...), nil
}

// Simulate replays history under a candidate policy without changing anything.
func (e *Engine) Simulate(candidate *policy.Policy, opts replay.Options) (replay.Report, error) {
	if candidate == nil {
		return replay.Report{}, errors.New("candidate policy is nil")
	}
	if err := candidate.Validate(); err != nil {
		return replay.Report{}, err
	}
	events, err := e.History()
	if err != nil {
		return replay.Report{}, err
	}
	return replay.Run(events, candidate, opts), nil
}

func randomID() string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	return strings.ToLower(base64.RawURLEncoding.EncodeToString(b[:]))
}
