package identity

import (
	"strings"
	"testing"
)

func TestPrincipalHelpers(t *testing.T) {
	var p Principal
	if !p.IsZero() || p.Label("role") != "" {
		t.Fatal("the empty principal has no id and no labels")
	}
	p = Principal{ID: "human:priya", Kind: KindHuman, Labels: map[string]string{"role": "operator"}}
	if p.IsZero() || p.Label("role") != "operator" || p.Label("team") != "" {
		t.Fatal("labels")
	}
}

func TestByIDReturnsACopy(t *testing.T) {
	r, err := NewRegistry([]PrincipalConfig{{ID: "human:priya", Kind: KindHuman, Labels: map[string]string{"role": "operator"}}})
	if err != nil {
		t.Fatal(err)
	}
	p, ok := r.ByID("human:priya")
	if !ok || p.Label("role") != "operator" {
		t.Fatal("lookup")
	}
	p.Labels["role"] = "attacker"
	again, _ := r.ByID("human:priya")
	if again.Label("role") != "operator" {
		t.Fatal("callers must not be able to change the registry through a returned principal")
	}
	if _, ok := r.ByID("human:nobody"); ok {
		t.Fatal("unknown id")
	}
}

func TestUnknownAgentNamesAreSanitised(t *testing.T) {
	r, _ := NewRegistry(nil)
	for name, want := range map[string]string{
		"":                       "agent:unnamed",
		"bad agent<script>":      "agent:bad_agent_script_",
		strings.Repeat("a", 300): "agent:" + strings.Repeat("a", 128),
	} {
		p, ok := r.ByAgentName(name)
		if ok || p.ID != want || p.Kind != KindAgent {
			t.Errorf("%q: got %+v %v", name, p, ok)
		}
	}
}
