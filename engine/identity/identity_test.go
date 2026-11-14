package identity

import (
	"strings"
	"testing"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewRegistry([]PrincipalConfig{
		{ID: "human:priya", Kind: KindHuman, Labels: map[string]string{"role": "operator"}, VirtualKeys: []string{"vk-priya"}},
		{ID: "agent:support", Kind: KindAgent, VirtualKeys: []string{Fingerprint("vk-support")}, AgentNames: []string{"support"}},
		{ID: "agent:billing", Kind: KindAgent, VirtualKeys: []string{"vk-billing"}, AgentNames: []string{"billing", "billing-v2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestByVirtualKey(t *testing.T) {
	r := testRegistry(t)
	cases := []struct {
		key      string
		wantID   string
		wantKind Kind
		known    bool
	}{
		{"vk-priya", "human:priya", KindHuman, true},
		{"vk-support", "agent:support", KindAgent, true}, // configured by fingerprint
		{"vk-billing", "agent:billing", KindAgent, true},
		{"", "unknown:anonymous", KindUnknown, false},
	}
	for _, c := range cases {
		p, ok := r.ByVirtualKey(c.key)
		if p.ID != c.wantID || p.Kind != c.wantKind || ok != c.known {
			t.Errorf("ByVirtualKey(%q) = %+v, %v; want %s %s %v", c.key, p, ok, c.wantID, c.wantKind, c.known)
		}
	}
	a, _ := r.ByVirtualKey("stranger-1")
	b, _ := r.ByVirtualKey("stranger-2")
	if a.Kind != KindUnknown || !strings.HasPrefix(a.ID, "unknown:") || a.ID == b.ID {
		t.Errorf("unknown keys must map to distinct unknown principals, got %s and %s", a.ID, b.ID)
	}
	if !ValidID(a.ID) {
		t.Errorf("unknown principal id %q must be a valid id", a.ID)
	}
}

func TestLabelsAreCopied(t *testing.T) {
	r := testRegistry(t)
	p, _ := r.ByVirtualKey("vk-priya")
	p.Labels["role"] = "attacker"
	again, _ := r.ByVirtualKey("vk-priya")
	if again.Label("role") != "operator" {
		t.Fatal("mutating a returned principal changed the registry")
	}
}

func TestByAgentName(t *testing.T) {
	r := testRegistry(t)
	if p, ok := r.ByAgentName("billing-v2"); !ok || p.ID != "agent:billing" {
		t.Errorf("billing-v2 resolved to %+v, %v", p, ok)
	}
	p, ok := r.ByAgentName("research bot!")
	if ok || p.ID != "agent:research_bot_" || p.Kind != KindAgent {
		t.Errorf("unregistered agent resolved to %+v, %v", p, ok)
	}
}

func TestLoadValidation(t *testing.T) {
	bad := map[string][]PrincipalConfig{
		"bad id":             {{ID: "Support", Kind: KindAgent}},
		"bad kind":           {{ID: "agent:x", Kind: "robot"}},
		"unknown kind":       {{ID: "agent:x", Kind: KindUnknown}},
		"duplicate id":       {{ID: "agent:x", Kind: KindAgent}, {ID: "agent:x", Kind: KindAgent}},
		"shared key":         {{ID: "agent:x", Kind: KindAgent, VirtualKeys: []string{"k"}}, {ID: "agent:y", Kind: KindAgent, VirtualKeys: []string{"k"}}},
		"bad fingerprint":    {{ID: "agent:x", Kind: KindAgent, VirtualKeys: []string{"sha256:abc"}}},
		"agent name on user": {{ID: "human:x", Kind: KindHuman, AgentNames: []string{"x"}}},
		"shared agent name":  {{ID: "agent:x", Kind: KindAgent, AgentNames: []string{"n"}}, {ID: "agent:y", Kind: KindAgent, AgentNames: []string{"n"}}},
		"empty key":          {{ID: "agent:x", Kind: KindAgent, VirtualKeys: []string{" "}}},
	}
	for name, cfg := range bad {
		if _, err := NewRegistry(cfg); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLoadKeepsOldStateOnError(t *testing.T) {
	r := testRegistry(t)
	if err := r.Load([]PrincipalConfig{{ID: "bad", Kind: KindAgent}}); err == nil {
		t.Fatal("expected error")
	}
	if p, ok := r.ByVirtualKey("vk-priya"); !ok || p.ID != "human:priya" {
		t.Fatal("registry lost its state after a failed load")
	}
}

func TestFingerprints(t *testing.T) {
	fp := Fingerprint("secret")
	if !strings.HasPrefix(fp, "sha256:") || len(fp) != 7+64 {
		t.Fatalf("bad fingerprint %q", fp)
	}
	if s := ShortFingerprint("secret"); s != fp[:19] {
		t.Fatalf("short fingerprint %q", s)
	}
	if ShortFingerprint("") != "" {
		t.Fatal("empty key must have no fingerprint")
	}
	if strings.Contains(fp, "secret") {
		t.Fatal("fingerprint leaks the key")
	}
}

func TestAllSorted(t *testing.T) {
	all := testRegistry(t).All()
	if len(all) != 3 || all[0].ID != "agent:billing" || all[2].ID != "human:priya" {
		t.Fatalf("unexpected order: %+v", all)
	}
}
