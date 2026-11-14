package policy

import (
	"fmt"
	"strings"
	"time"
)

// Decision is the outcome for one call.
type Decision struct {
	Effect Effect `json:"effect"`
	Code   string `json:"code"`
	RuleID string `json:"rule_id,omitempty"`
	// Reason is a short sentence a human operator can read.
	Reason string `json:"reason"`
	// Monitored lists monitor-mode rules that would have denied this call.
	Monitored []string `json:"monitored,omitempty"`
	// LockActor asks the caller of Evaluate to lock the actor (a deny rule with lockdown set).
	LockActor bool `json:"lock_actor,omitempty"`
	// Blocking is the principal whose lockdown caused a deny, when Code is CodeLockdown.
	Blocking string `json:"blocking,omitempty"`
}

// Allowed reports whether the call may proceed.
func (d Decision) Allowed() bool { return d.Effect == Allow }

// Grant temporarily allows an actor to reach targets that rules would otherwise deny.
// Grants are explicit, time-boxed, audited exceptions ("break glass").
type Grant struct {
	ID        string    `json:"id"`
	Actor     string    `json:"actor"` // exact principal ID
	Plane     Plane     `json:"plane"`
	Target    string    `json:"target"` // glob
	TaskID    string    `json:"task_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	Reason    string    `json:"reason,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks a grant before it is stored.
func (g Grant) Validate() error {
	if g.Actor == "" {
		return fmt.Errorf("grant needs an actor")
	}
	if !g.Plane.Valid() {
		return fmt.Errorf("grant plane must be llm, mcp or a2a")
	}
	if err := ValidatePattern(g.Target); err != nil {
		return fmt.Errorf("grant target: %w", err)
	}
	if g.ExpiresAt.IsZero() {
		return fmt.Errorf("grant needs an expiry")
	}
	return nil
}

// Covers reports whether the grant allows f at time now.
func (g Grant) Covers(f Facts, now time.Time) bool {
	if !now.Before(g.ExpiresAt) {
		return false
	}
	if g.Actor != f.Actor.ID || g.Plane != f.Plane || !Glob(g.Target, f.Target) {
		return false
	}
	if g.TaskID != "" && g.TaskID != f.TaskID {
		return false
	}
	return true
}

// LockChecker tells the evaluator whether a principal is locked down.
type LockChecker interface {
	IsLocked(principalID string, now time.Time) bool
}

// noLocks is used when no checker is supplied.
type noLocks struct{}

func (noLocks) IsLocked(string, time.Time) bool { return false }

// Input bundles what Evaluate needs besides the facts.
type Input struct {
	Locks  LockChecker
	Grants []Grant
	// Now is the evaluation time. Zero means "use Facts.Time".
	Now time.Time
}

// Evaluate decides one call. The order is fixed:
//
//  1. Chain ticket problems (a forged, expired or mismatched ticket) deny, unless the
//     policy explicitly accepts bad tickets.
//  2. Lockdown: if any principal in the chain, or the agent being called, is locked, deny.
//  3. Unknown callers are denied when guards.unknown_callers is deny.
//  4. Loops: a chain that visits the same agent twice is denied (unless allowed).
//  5. Depth: a chain longer than guards.max_depth is denied.
//  6. A matching, unexpired grant allows the call.
//  7. Rules: any enforced deny rule that applies denies (the first one is reported);
//     otherwise an allow rule that applies allows. Monitor rules never block but are
//     reported in Decision.Monitored.
//  8. Otherwise the default effect applies.
func Evaluate(p *Policy, f Facts, in Input) Decision {
	if p == nil {
		p = Default()
	}
	locks := in.Locks
	if locks == nil {
		locks = noLocks{}
	}
	now := in.Now
	if now.IsZero() {
		now = f.Time
	}

	// 1. Ticket problems.
	if f.TicketProblem != "" && !p.Guards.AcceptBadTickets {
		return Decision{Effect: Deny, Code: CodeTicket,
			Reason: fmt.Sprintf("the chain ticket was rejected (%s)", strings.ReplaceAll(f.TicketProblem, "_", " "))}
	}

	// 2. Lockdown, checked on every principal in the chain and on the agent being called.
	for _, pr := range f.Chain {
		if locks.IsLocked(pr.ID, now) {
			return lockedDecision(pr.ID, f)
		}
	}
	if locks.IsLocked(f.Actor.ID, now) {
		return lockedDecision(f.Actor.ID, f)
	}
	if f.Plane == PlaneA2A && locks.IsLocked(agentTargetID(f.Target), now) {
		return Decision{Effect: Deny, Code: CodeLockdown, Blocking: agentTargetID(f.Target),
			Reason: fmt.Sprintf("agent %s is locked down", f.Target)}
	}

	// 3. Unknown callers.
	if f.Actor.Kind == "unknown" && p.Guards.UnknownCallers == Deny {
		return Decision{Effect: Deny, Code: CodeUnknownCaller, Reason: "the caller's key is not registered"}
	}

	// 4. Loops.
	if !p.Guards.AllowLoops {
		if who, ok := loopIn(f); ok {
			return Decision{Effect: Deny, Code: CodeLoop,
				Reason: fmt.Sprintf("the chain would visit %s twice", who)}
		}
	}

	// 5. Depth. Calling another agent adds a hop.
	depth := f.Depth
	if f.Plane == PlaneA2A {
		depth++
	}
	if max := p.Guards.maxDepth(); depth > max {
		return Decision{Effect: Deny, Code: CodeMaxDepth,
			Reason: fmt.Sprintf("the chain would be %d hops long; the limit is %d", depth, max)}
	}

	// 6. Grants.
	for _, g := range in.Grants {
		if g.Covers(f, now) {
			return Decision{Effect: Allow, Code: CodeGrant, RuleID: g.ID,
				Reason: fmt.Sprintf("temporary grant %s allows %s to use %s", g.ID, f.Actor.ID, f.Target)}
		}
	}

	// 7. Rules: deny overrides allow.
	var (
		monitored []string
		allowRule *Rule
		denyRule  *Rule
	)
	for i := range p.Rules {
		r := &p.Rules[i]
		if !r.Applies(f) {
			continue
		}
		switch {
		case r.Effect == Deny && r.mode() == Monitor:
			monitored = append(monitored, r.ID)
		case r.Effect == Deny && denyRule == nil:
			denyRule = r
		case r.Effect == Allow && r.mode() == Enforce && allowRule == nil:
			allowRule = r
		}
	}
	if denyRule != nil {
		return Decision{Effect: Deny, Code: CodeRule, RuleID: denyRule.ID, Monitored: monitored,
			LockActor: denyRule.Lockdown, Reason: ruleReason(denyRule, f)}
	}
	if allowRule != nil {
		return Decision{Effect: Allow, Code: CodeRule, RuleID: allowRule.ID, Monitored: monitored,
			Reason: ruleReason(allowRule, f)}
	}

	// 8. Default.
	eff := p.defaultEffect()
	return Decision{Effect: eff, Code: CodeDefault, Monitored: monitored,
		Reason: fmt.Sprintf("no rule matched; the default is %s", eff)}
}

func lockedDecision(id string, f Facts) Decision {
	if id == f.Actor.ID {
		return Decision{Effect: Deny, Code: CodeLockdown, Blocking: id,
			Reason: fmt.Sprintf("%s is locked down", id)}
	}
	return Decision{Effect: Deny, Code: CodeLockdown, Blocking: id,
		Reason: fmt.Sprintf("%s is locked down, and this call is part of its chain", id)}
}

func ruleReason(r *Rule, f Facts) string {
	if r.Description != "" {
		return r.Description
	}
	verb := "allows"
	if r.Effect == Deny {
		verb = "blocks"
	}
	return fmt.Sprintf("rule %s %s %s calling %s", r.ID, verb, f.Actor.ID, f.Target)
}

// agentTargetID turns an Agent Gateway target name into the principal ID used for lockdowns.
// Engines that resolve registered names to custom principal IDs pass that ID as the target.
func agentTargetID(target string) string {
	if strings.Contains(target, ":") {
		return target
	}
	return "agent:" + target
}

// loopIn finds an agent that appears twice in the chain, counting the agent being called on
// the a2a plane.
func loopIn(f Facts) (string, bool) {
	seen := make(map[string]bool, len(f.Chain)+1)
	for _, pr := range f.Chain {
		// Only agents can loop: humans and services start chains but are never "asked".
		if pr.Kind != "agent" {
			continue
		}
		if seen[pr.ID] {
			return pr.ID, true
		}
		seen[pr.ID] = true
	}
	if f.Plane == PlaneA2A {
		if t := agentTargetID(f.Target); seen[t] {
			return t, true
		}
	}
	return "", false
}
