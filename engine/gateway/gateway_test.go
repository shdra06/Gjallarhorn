package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shdra06/Gjallarhorn/engine/policy"
)

func TestA2AAgent(t *testing.T) {
	cases := []struct {
		path, prefix, want string
		ok                 bool
	}{
		{"/agents/a2a/billing/json-rpc", "", "billing", true},
		{"/agents/a2a/billing", "", "billing", true},
		{"/agents/a2a/billing/rest/v1/message:send", "", "billing", true},
		{"/bifrost/agents/a2a/support/json-rpc", "", "support", true},
		{"/agents/a2a/", "", "", false},
		{"/v1/chat/completions", "", "", false},
		{"/custom/agents/x/y", "/custom/agents/", "x", true},
	}
	for _, c := range cases {
		got, ok := A2AAgent(c.path, c.prefix)
		if got != c.want || ok != c.ok {
			t.Errorf("A2AAgent(%q) = %q, %v; want %q, %v", c.path, got, ok, c.want, c.ok)
		}
	}
}

func TestCredential(t *testing.T) {
	h := func(m map[string]string) func(string) string {
		return func(k string) string { return m[strings.ToLower(k)] }
	}
	if got := Credential(h(map[string]string{"x-bf-vk": " sk-bf-1 "})); got != "sk-bf-1" {
		t.Errorf("x-bf-vk: %q", got)
	}
	if got := Credential(h(map[string]string{"authorization": "Bearer sk-bf-2"})); got != "sk-bf-2" {
		t.Errorf("bearer: %q", got)
	}
	if got := Credential(h(map[string]string{"authorization": "Bearer sk-openai-provider-key"})); got != "" {
		t.Errorf("a provider key is not a virtual key: %q", got)
	}
	if got := Credential(h(map[string]string{})); got != "" {
		t.Errorf("none: %q", got)
	}
}

func TestTargets(t *testing.T) {
	if got := LLMTarget("openai", "gpt-4o-mini"); got != "openai/gpt-4o-mini" {
		t.Error(got)
	}
	if got := LLMTarget("openai", "openai/gpt-4o-mini"); got != "openai/gpt-4o-mini" {
		t.Error(got)
	}
	if got := LLMTarget("", "m"); got != "m" {
		t.Error(got)
	}
	if got := MCPTarget("billing", "billing-refund", "chat_tool_call"); got != "billing.refund" {
		t.Error(got)
	}
	if got := MCPTarget("billing", "", "list_tools"); got != "billing.list_tools" {
		t.Error(got)
	}
	if got := MCPTarget("", "refund", "execute_tool"); got != "refund" {
		t.Error(got)
	}
}

func TestParseConfig(t *testing.T) {
	t.Setenv("GJ_ADMIN", "an-admin-token-that-is-long-enough")
	raw := map[string]any{
		"tickets": map[string]any{"active_key": "k", "keys": map[string]any{"k": strings.Repeat("x", 32)}},
		"control": map[string]any{"admin_token": "env:GJ_ADMIN"},
	}
	c, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Control.Listen != DefaultControlListen || c.Control.AdminToken != "an-admin-token-that-is-long-enough" || c.A2APrefix != DefaultA2APrefix {
		t.Fatalf("defaults: %+v", c.Control)
	}
	if c.Tickets.ActiveKey != "k" {
		t.Fatal("embedded engine config not decoded")
	}
	if _, err := ParseConfig(map[string]any{"control": map[string]any{}}); err == nil {
		t.Fatal("a missing admin token must be an error")
	}
	if _, err := ParseConfig(map[string]any{"control": map[string]any{"disabled": true}, "typo": 1}); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if c, err := ParseConfig(map[string]any{"control": map[string]any{"disabled": true}, "a2a_prefix": "/x/agents"}); err != nil || c.A2APrefix != "/x/agents/" {
		t.Fatalf("prefix normalisation: %+v %v", c.A2APrefix, err)
	}
	if _, err := ParseConfig(nil); err == nil {
		t.Fatal("nil config")
	}
	cp := map[string]any{"endpoint": "http://127.0.0.1:8080", "model": "openai/gpt-4o-mini", "virtual_key": "env:CP_VK"}
	if c, err := ParseConfig(map[string]any{"control": map[string]any{"disabled": true}, "copilot": cp}); err != nil || !c.Copilot.Enabled() {
		t.Fatalf("copilot config: %+v %v", c.Copilot, err)
	}
	if _, err := ParseConfig(map[string]any{"control": map[string]any{"disabled": true}, "copilot": map[string]any{"endpoint": "http://x"}}); err == nil {
		t.Fatal("a co-pilot endpoint without a model is a mistake")
	}
}

func TestDenyBody(t *testing.T) {
	b := DenyBody(policy.Decision{Effect: policy.Deny, Code: "rule", RuleID: "r1", Reason: "no refunds"}, 42)
	var v map[string]map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v["error"]["type"] != "gjallarhorn_denied" || v["error"]["rule_id"] != "r1" || v["error"]["event"].(float64) != 42 {
		t.Fatalf("body %s", b)
	}
	if !strings.Contains(DenyMessage(policy.Decision{Reason: "x"}), "Gjallarhorn") {
		t.Fatal("message")
	}
}
