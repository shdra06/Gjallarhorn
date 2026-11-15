package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/identity"
)

func TestGrantEdges(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	ok := Grant{Actor: "agent:billing", Plane: PlaneMCP, Target: "billing.refund", TaskID: "t1", ExpiresAt: now.Add(time.Hour)}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Grant{
		"actor":     {Plane: PlaneMCP, Target: "x", ExpiresAt: now},
		"principal": {Actor: "billing agent", Plane: PlaneMCP, Target: "x", ExpiresAt: now},
		"plane":     {Actor: "agent:billing", Plane: "smtp", Target: "x", ExpiresAt: now},
		"target":    {Actor: "agent:billing", Plane: PlaneMCP, Target: "", ExpiresAt: now},
		"expiry":    {Actor: "agent:billing", Plane: PlaneMCP, Target: "x"},
	}
	for want, g := range bad {
		if err := g.Validate(); err == nil || !strings.Contains(err.Error(), want) && want != "principal" {
			t.Errorf("%s: %v", want, err)
		}
	}
	f := Facts{Actor: identity.Principal{ID: "agent:billing"}, Plane: PlaneMCP, Target: "billing.refund", TaskID: "t1"}
	if !ok.Covers(f, now) {
		t.Fatal("covers")
	}
	if ok.Covers(f, now.Add(2*time.Hour)) {
		t.Fatal("expired grants cover nothing")
	}
	other := f
	other.TaskID = "t2"
	if ok.Covers(other, now) {
		t.Fatal("a task-bound grant covers only that task")
	}
}

func TestPatternLimits(t *testing.T) {
	for _, p := range []string{"", strings.Repeat("a", 300), "bad\npattern"} {
		if ValidatePattern(p) == nil {
			t.Errorf("%q must be rejected", p)
		}
	}
}

func TestLabelWildcards(t *testing.T) {
	ext := identity.Principal{ID: "service:chat", Labels: map[string]string{"trust": "external"}}
	plain := identity.Principal{ID: "human:priya"}
	if !labelsMatch(map[string]string{"trust": "*"}, ext) || labelsMatch(map[string]string{"trust": "*"}, plain) {
		t.Fatal("* means present with any value")
	}
	if labelsMatch(map[string]string{"trust": "!"}, ext) || !labelsMatch(map[string]string{"trust": "!"}, plain) {
		t.Fatal("! means absent")
	}
	if labelsMatch(map[string]string{"trust": "internal"}, ext) {
		t.Fatal("exact values must match")
	}
}
