// Package gateway holds the gateway-facing glue that doesn't depend on Bifrost's types: plugin
// configuration, request-path parsing, credential extraction, target naming and the JSON body
// returned when a call is blocked. The Bifrost plugin (package main) stays a thin adapter over
// this package, so this logic is tested on every OS.
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/copilot"
	"github.com/shdra06/Gjallarhorn/engine/policy"
)

// DefaultA2APrefix is where Bifrost serves Agent Gateway routes.
const DefaultA2APrefix = "/agents/a2a/"

// DefaultControlListen keeps the control API on localhost unless configured otherwise.
const DefaultControlListen = "127.0.0.1:8787"

// LabelHeader lets the attack lab tag its traffic ("attack" or "benign"). It is only honoured
// when AcceptLabels is true, so production callers can't skew replay statistics.
const LabelHeader = "x-gjallarhorn-label"

// Config is the full plugin configuration: the engine configuration plus gateway options.
type Config struct {
	engine.Config
	Control ControlConfig `json:"control"`
	// A2APrefix is the Agent Gateway route prefix (default /agents/a2a/). Include Bifrost's
	// base path if it is served under one.
	A2APrefix string `json:"a2a_prefix,omitempty"`
	// AcceptLabels honours the x-gjallarhorn-label header (demo and test environments only).
	AcceptLabels bool `json:"accept_labels,omitempty"`
	// MonitorOnly records every decision but never blocks. Useful for a first rollout.
	MonitorOnly bool `json:"monitor_only,omitempty"`
	// Copilot configures the model the AI co-pilot uses. Point it at this Bifrost with a
	// virtual key of its own, so the co-pilot's model calls are governed and recorded too.
	// Leave it out to run the co-pilot without a model.
	Copilot copilot.Config `json:"copilot,omitempty"`
}

// ControlConfig configures the operator API the plugin serves on its own listener.
type ControlConfig struct {
	// Listen is the address for the control API (default 127.0.0.1:8787). Empty string with
	// Disabled=true turns it off.
	Listen string `json:"listen,omitempty"`
	// AdminToken may be "env:VAR" so the secret stays out of config files.
	AdminToken     string   `json:"admin_token,omitempty"`
	AllowedOrigins []string `json:"allowed_origins,omitempty"`
	Disabled       bool     `json:"disabled,omitempty"`
	// ConsoleDir serves the operator console from this directory at the control API's root.
	ConsoleDir string `json:"console_dir,omitempty"`
	// DemoDriver forwards /demo/ to a demo traffic driver (demo environments only).
	DemoDriver string `json:"demo_driver,omitempty"`
}

// ParseConfig converts the untyped plugin config Bifrost passes to Init.
func ParseConfig(v any) (Config, error) {
	if v == nil {
		return Config{}, errors.New("gjallarhorn: plugin config is empty")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return Config{}, fmt.Errorf("gjallarhorn: config: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("gjallarhorn: config: %w", err)
	}
	if c.A2APrefix == "" {
		c.A2APrefix = DefaultA2APrefix
	}
	if !strings.HasSuffix(c.A2APrefix, "/") {
		c.A2APrefix += "/"
	}
	if !c.Control.Disabled {
		if c.Control.Listen == "" {
			c.Control.Listen = DefaultControlListen
		}
		tok := c.Control.AdminToken
		if strings.HasPrefix(tok, "env:") {
			tok = os.Getenv(strings.TrimPrefix(tok, "env:"))
		}
		if tok == "" {
			return Config{}, errors.New("gjallarhorn: control.admin_token is required (use \"env:VAR\"), or set control.disabled")
		}
		c.Control.AdminToken = tok
	}
	if (c.Copilot.Endpoint == "") != (c.Copilot.Model == "") {
		return Config{}, errors.New("gjallarhorn: copilot needs both endpoint and model (or neither)")
	}
	return c, nil
}

// A2AAgent extracts the agent registration name from an Agent Gateway path such as
// "/agents/a2a/billing/json-rpc" or "/bifrost/agents/a2a/billing/rest/v1/message:send".
func A2AAgent(path, prefix string) (string, bool) {
	if prefix == "" {
		prefix = DefaultA2APrefix
	}
	i := strings.Index(path, prefix)
	if i < 0 {
		return "", false
	}
	rest := path[i+len(prefix):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}

// Credential returns the caller's Bifrost virtual key from request headers: x-bf-vk, or an
// "Authorization: Bearer sk-bf-..." header. get must look headers up case-insensitively.
func Credential(get func(string) string) string {
	if v := strings.TrimSpace(get("x-bf-vk")); v != "" {
		return v
	}
	auth := strings.TrimSpace(get("authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
		tok := strings.TrimSpace(auth[7:])
		if strings.HasPrefix(tok, "sk-bf-") {
			return tok
		}
	}
	return ""
}

// LLMTarget names a model call target as "provider/model".
func LLMTarget(provider, model string) string {
	switch {
	case provider == "":
		return model
	case strings.HasPrefix(model, provider+"/"):
		return model
	default:
		return provider + "/" + model
	}
}

// MCPTarget names a tool call target as "client.tool". Bifrost prefixes tool names with
// "<client>-"; that prefix is removed. Non-tool MCP requests become "client.<request type>".
func MCPTarget(client, toolName, requestType string) string {
	name := toolName
	if name != "" && client != "" {
		name = strings.TrimPrefix(name, client+"-")
	}
	if name == "" {
		name = requestType
	}
	if client == "" {
		return name
	}
	return client + "." + name
}

// DenyBody is the JSON returned to a caller whose call was blocked. It explains what
// happened without leaking policy internals beyond the rule ID and reason.
func DenyBody(d policy.Decision, seq uint64) []byte {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "gjallarhorn_denied",
			"code":    d.Code,
			"rule_id": d.RuleID,
			"message": "Blocked by Gjallarhorn: " + d.Reason,
			"event":   seq,
		},
	})
	return b
}

// DenyMessage is the short message used inside gateway error envelopes.
func DenyMessage(d policy.Decision) string { return "Blocked by Gjallarhorn: " + d.Reason }
