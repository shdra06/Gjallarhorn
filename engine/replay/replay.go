// Package replay re-runs recorded traffic under a different policy and reports what would
// have changed. It is how an operator proves a new rule blocks an attack without blocking
// normal work, before switching the rule on.
//
// Replay works on the resolved facts stored in the flight recorder, not on raw requests, so
// it is deterministic: expired tickets and rotated keys don't change the result, and nothing
// is sent to any model, tool or agent.
package replay

import (
	"sort"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
)

// Options controls a replay.
type Options struct {
	// Locks, if set, are applied during the replay (for "what if X had been locked at T").
	Locks policy.LockChecker
	// Grants are applied during the replay.
	Grants []policy.Grant
	// ReevaluateLockdowns re-decides calls that were originally blocked by a lockdown. By
	// default those stay blocked, because a lockdown is an operator action, not a rule.
	ReevaluateLockdowns bool
	// MaxChanges caps how many changed calls are listed in each direction (0 = 500).
	MaxChanges int
	// From and To limit the time window (zero = unbounded).
	From, To time.Time
}

// Change is one call whose outcome differs under the new policy.
type Change struct {
	Seq    uint64          `json:"seq"`
	Time   time.Time       `json:"time"`
	Label  string          `json:"label,omitempty"`
	Facts  policy.Facts    `json:"facts"`
	Before policy.Decision `json:"before"`
	After  policy.Decision `json:"after"`
}

// LabelStats summarises calls that carry a label (for example "attack" or "benign"), which
// the attack lab and the benign traffic set attach to their calls.
type LabelStats struct {
	Total         int `json:"total"`
	BlockedBefore int `json:"blocked_before"`
	BlockedAfter  int `json:"blocked_after"`
}

// Report is the result of a replay.
type Report struct {
	Evaluated     int      `json:"evaluated"`
	Skipped       int      `json:"skipped"`
	Unchanged     int      `json:"unchanged"`
	ReasonChanged int      `json:"reason_changed"`
	BlockedBefore int      `json:"blocked_before"`
	BlockedAfter  int      `json:"blocked_after"`
	NewlyBlocked  []Change `json:"newly_blocked"`
	NewlyAllowed  []Change `json:"newly_allowed"`
	// Truncated is true when more changes happened than MaxChanges allows to list.
	Truncated bool `json:"truncated"`
	// DeniedBy counts denials under the new policy by rule ID, or by code for guards.
	DeniedBy map[string]int `json:"denied_by"`
	// Monitored counts monitor-mode rule hits under the new policy.
	Monitored map[string]int        `json:"monitored"`
	Labels    map[string]LabelStats `json:"labels,omitempty"`
}

// Run replays events under p.
func Run(events []recorder.Event, p *policy.Policy, opts Options) Report {
	limit := opts.MaxChanges
	if limit <= 0 {
		limit = 500
	}
	rep := Report{DeniedBy: map[string]int{}, Monitored: map[string]int{}, Labels: map[string]LabelStats{}}
	for _, e := range events {
		if e.Type != recorder.TypeDecision || e.Facts == nil || e.Decision == nil {
			continue
		}
		f := *e.Facts
		if (!opts.From.IsZero() && f.Time.Before(opts.From)) || (!opts.To.IsZero() && f.Time.After(opts.To)) {
			continue
		}
		before := *e.Decision
		var after policy.Decision
		if before.Code == policy.CodeLockdown && !opts.ReevaluateLockdowns {
			after = before
			rep.Skipped++
		} else {
			after = policy.Evaluate(p, f, policy.Input{Locks: opts.Locks, Grants: opts.Grants, Now: f.Time})
		}
		rep.Evaluated++
		label := e.Note["label"]
		if label != "" {
			ls := rep.Labels[label]
			ls.Total++
			if !before.Allowed() {
				ls.BlockedBefore++
			}
			if !after.Allowed() {
				ls.BlockedAfter++
			}
			rep.Labels[label] = ls
		}
		if !before.Allowed() {
			rep.BlockedBefore++
		}
		if !after.Allowed() {
			rep.BlockedAfter++
			key := after.RuleID
			if key == "" {
				key = after.Code
			}
			rep.DeniedBy[key]++
		}
		for _, m := range after.Monitored {
			rep.Monitored[m]++
		}
		ch := Change{Seq: e.Seq, Time: f.Time, Label: label, Facts: f, Before: before, After: after}
		switch {
		case before.Allowed() && !after.Allowed():
			if len(rep.NewlyBlocked) < limit {
				rep.NewlyBlocked = append(rep.NewlyBlocked, ch)
			} else {
				rep.Truncated = true
			}
		case !before.Allowed() && after.Allowed():
			if len(rep.NewlyAllowed) < limit {
				rep.NewlyAllowed = append(rep.NewlyAllowed, ch)
			} else {
				rep.Truncated = true
			}
		case before.Code != after.Code || before.RuleID != after.RuleID:
			rep.ReasonChanged++
		default:
			rep.Unchanged++
		}
	}
	sort.Slice(rep.NewlyBlocked, func(i, j int) bool { return rep.NewlyBlocked[i].Seq < rep.NewlyBlocked[j].Seq })
	sort.Slice(rep.NewlyAllowed, func(i, j int) bool { return rep.NewlyAllowed[i].Seq < rep.NewlyAllowed[j].Seq })
	if len(rep.Labels) == 0 {
		rep.Labels = nil
	}
	return rep
}

// Safe reports whether a candidate policy, judged by its replay, blocks every labelled attack
// it blocked before and blocks no more benign calls than before. Labels are "attack" and
// "benign". It returns false when there is no labelled traffic to judge by.
func (r Report) Safe() bool {
	benign, okB := r.Labels["benign"]
	attack, okA := r.Labels["attack"]
	if !okB && !okA {
		return false
	}
	if okB && benign.BlockedAfter > benign.BlockedBefore {
		return false
	}
	if okA && attack.BlockedAfter < attack.BlockedBefore {
		return false
	}
	return true
}
