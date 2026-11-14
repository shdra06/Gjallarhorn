// Package incident rebuilds the whole story around one recorded call: who started it, which
// agents passed it along, and everything each of them did.
//
// The flight recorder links calls through chain tickets. A delegation decision records the ID
// of the ticket it minted (note "ticket_minted"); every call the receiving agent then makes
// carries that ticket's ID (facts.ticket_id). Following those links up gives the root of the
// chain, and following them down gives the full tree, so an operator who clicks on one
// suspicious tool call sees the customer message, the hand-offs and every side effect.
package incident

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
)

// ErrNotFound is returned when the focus event is not in the history.
var ErrNotFound = errors.New("incident: event not found")

// Step is one call in the incident, in time order.
type Step struct {
	Seq       uint64            `json:"seq"`
	Time      time.Time         `json:"time"`
	Depth     int               `json:"depth"` // nesting level in the tree (0 = root)
	Parent    uint64            `json:"parent,omitempty"`
	Plane     policy.Plane      `json:"plane"`
	Actor     string            `json:"actor"`
	Target    string            `json:"target"`
	Chain     []string          `json:"chain"`
	Decision  policy.Decision   `json:"decision"`
	Monitored bool              `json:"monitored,omitempty"` // decided in monitor-only mode
	Outcome   *recorder.Outcome `json:"outcome,omitempty"`
	Label     string            `json:"label,omitempty"`
	Focus     bool              `json:"focus,omitempty"`
	// Executed is true when the call actually went ahead: it was allowed, or it was denied
	// only in monitor-only mode.
	Executed bool `json:"executed"`
}

// Lockdown is a lockdown that happened during the incident.
type Lockdown struct {
	Seq       uint64    `json:"seq"`
	Time      time.Time `json:"time"`
	Principal string    `json:"principal"`
	Reason    string    `json:"reason"`
	By        string    `json:"by"`
	RuleID    string    `json:"rule_id,omitempty"`
}

// Incident is the reconstructed story.
type Incident struct {
	Focus     uint64     `json:"focus"`
	Root      uint64     `json:"root"`
	Origin    string     `json:"origin"`
	Agents    []string   `json:"agents"`
	Steps     []Step     `json:"steps"`
	Lockdowns []Lockdown `json:"lockdowns,omitempty"`
	Start     time.Time  `json:"start"`
	End       time.Time  `json:"end"`
	// Blocked counts calls that were stopped; Executed counts calls that went ahead.
	Blocked  int `json:"blocked"`
	Executed int `json:"executed"`
}

// Seqs returns the sequence numbers of every decision in the incident.
func (in *Incident) Seqs() map[uint64]bool {
	out := make(map[uint64]bool, len(in.Steps))
	for _, s := range in.Steps {
		out[s.Seq] = true
	}
	return out
}

// FocusStep returns the step the incident was built around.
func (in *Incident) FocusStep() Step {
	for _, s := range in.Steps {
		if s.Seq == in.Focus {
			return s
		}
	}
	return Step{}
}

// Build reconstructs the incident around the decision event with sequence number focus.
func Build(events []recorder.Event, focus uint64) (*Incident, error) {
	decisions := make(map[uint64]recorder.Event)
	mintedBy := make(map[string]uint64)           // ticket ID -> delegation decision seq
	childrenOf := make(map[string][]uint64)       // ticket ID -> decisions that presented it
	outcomes := make(map[string]recorder.Outcome) // request ID -> outcome
	var lockdowns []recorder.Event
	for _, e := range events {
		switch e.Type {
		case recorder.TypeDecision:
			if e.Facts == nil || e.Decision == nil {
				continue
			}
			decisions[e.Seq] = e
			if t := e.Note["ticket_minted"]; t != "" {
				mintedBy[t] = e.Seq
			}
			if e.Facts.TicketID != "" {
				childrenOf[e.Facts.TicketID] = append(childrenOf[e.Facts.TicketID], e.Seq)
			}
		case recorder.TypeOutcome:
			if e.RequestID != "" && e.Outcome != nil {
				outcomes[e.RequestID] = *e.Outcome
			}
		case recorder.TypeLockdown:
			lockdowns = append(lockdowns, e)
		}
	}
	if _, ok := decisions[focus]; !ok {
		return nil, fmt.Errorf("%w: seq %d is not a recorded decision", ErrNotFound, focus)
	}

	// Walk up to the root of the chain.
	root := focus
	for guard := 0; guard < 64; guard++ {
		t := decisions[root].Facts.TicketID
		parent, ok := mintedBy[t]
		if t == "" || !ok || parent == root {
			break
		}
		root = parent
	}

	// Walk down from the root, collecting every call in the tree.
	in := &Incident{Focus: focus, Root: root}
	type item struct {
		seq, parent uint64
		depth       int
	}
	queue := []item{{seq: root}}
	seen := map[uint64]bool{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if seen[it.seq] {
			continue
		}
		seen[it.seq] = true
		e := decisions[it.seq]
		in.Steps = append(in.Steps, toStep(e, it.depth, it.parent, focus, outcomes))
		if t := e.Note["ticket_minted"]; t != "" {
			for _, child := range childrenOf[t] {
				queue = append(queue, item{seq: child, parent: it.seq, depth: it.depth + 1})
			}
		}
	}
	sort.Slice(in.Steps, func(i, j int) bool { return in.Steps[i].Seq < in.Steps[j].Seq })

	agents := map[string]bool{}
	for i, s := range in.Steps {
		if i == 0 || s.Time.Before(in.Start) {
			in.Start = s.Time
		}
		if s.Time.After(in.End) {
			in.End = s.Time
		}
		if s.Executed {
			in.Executed++
		} else {
			in.Blocked++
		}
		for _, c := range s.Chain {
			if strings.HasPrefix(c, "agent:") {
				agents[c] = true
			}
		}
		if s.Plane == policy.PlaneA2A && strings.HasPrefix(s.Target, "agent:") {
			agents[s.Target] = true
		}
	}
	if len(in.Steps) > 0 && len(in.Steps[0].Chain) > 0 {
		in.Origin = in.Steps[0].Chain[0]
	}
	for a := range agents {
		in.Agents = append(in.Agents, a)
	}
	sort.Strings(in.Agents)

	// Lockdowns of anyone involved, from the start of the incident to shortly after its end.
	involved := map[string]bool{in.Origin: true}
	for _, a := range in.Agents {
		involved[a] = true
	}
	for _, l := range lockdowns {
		p := l.Note["principal"]
		if !involved[p] || l.Time.Before(in.Start) || l.Time.After(in.End.Add(5*time.Second)) {
			continue
		}
		in.Lockdowns = append(in.Lockdowns, Lockdown{Seq: l.Seq, Time: l.Time, Principal: p,
			Reason: l.Note["reason"], By: l.Note["by"], RuleID: l.Note["rule_id"]})
	}
	return in, nil
}

func toStep(e recorder.Event, depth int, parent, focus uint64, outcomes map[string]recorder.Outcome) Step {
	f, d := e.Facts, e.Decision
	chain := make([]string, len(f.Chain))
	for i, p := range f.Chain {
		chain[i] = p.ID
	}
	monitored := e.Note["mode"] == "monitor"
	s := Step{Seq: e.Seq, Time: f.Time, Depth: depth, Parent: parent, Plane: f.Plane, Actor: f.Actor.ID,
		Target: f.Target, Chain: chain, Decision: *d, Monitored: monitored, Label: e.Note["label"],
		Focus: e.Seq == focus, Executed: d.Allowed() || monitored}
	if o, ok := outcomes[e.RequestID]; ok && e.RequestID != "" {
		oc := o
		s.Outcome = &oc
	}
	return s
}

// Notable lists recent decision events worth an operator's attention, newest first: denials
// (enforced or flagged in monitor mode), each reported once per incident root.
func Notable(events []recorder.Event, limit int) []Step {
	var out []Step
	seenRoot := map[uint64]bool{}
	for i := len(events) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		e := events[i]
		if e.Type != recorder.TypeDecision || e.Decision == nil || e.Facts == nil || e.Decision.Allowed() {
			continue
		}
		in, err := Build(events, e.Seq)
		if err != nil || seenRoot[in.Root] {
			continue
		}
		seenRoot[in.Root] = true
		out = append(out, in.FocusStep())
	}
	return out
}
