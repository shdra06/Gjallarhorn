// Package stubmodel is the demo's OpenAI-compatible model server. Agents get a fixed reply
// (their actions come from the task text, so the demo is repeatable). The model named
// "copilot-stub" is a scripted stand-in for the co-pilot's LLM, so the co-pilot fire drill
// runs offline and gives the same result every time:
//
//   - asked to explain an incident, it writes a short summary from the facts it is sent;
//   - asked for rules, it proposes three on purpose: a precise one, a dangerously broad one
//     (block the whole tool family for everyone) and a malformed one (a field Gjallarhorn does
//     not know). Gjallarhorn must prove the first and reject the other two by itself.
//
// Point the co-pilot at a real model to get real answers; nothing else changes.
package stubmodel

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// CopilotModel is the model name that selects the scripted co-pilot.
const CopilotModel = "copilot-stub"

// AgentReply is what every other model answers.
const AgentReply = "hello from the mock model"

type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format"`
}

// Handler serves /v1/chat/completions and /v1/models.
func Handler(logf func(string, ...any)) http.Handler {
	if logf == nil {
		logf = log.Printf
	}
	var calls atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"object":"list","data":[{"id":"standard-model","object":"model"},{"id":"premium-model","object":"model"},{"id":%q,"object":"model"}]}`, CopilotModel)
			return
		}
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var req chatRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
			http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
			return
		}
		n := calls.Add(1)
		logf("MOCK call=%d model=%s", n, req.Model)
		reply := AgentReply
		if strings.HasSuffix(req.Model, CopilotModel) {
			reply = copilot(req)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": fmt.Sprintf("chatcmpl-mock-%d", n), "object": "chat.completion", "created": time.Now().Unix(), "model": req.Model,
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": reply}}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 5, "total_tokens": 10},
		})
	})
}

func copilot(req chatRequest) string {
	var system, user string
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			system = m.Content
		case "user":
			user = m.Content
		}
	}
	if (req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object") || strings.Contains(system, "policy assistant") {
		return suggest(user)
	}
	return explain(user)
}

type principal struct {
	ID     string            `json:"id"`
	Labels map[string]string `json:"labels"`
}

type step struct {
	Actor    string `json:"actor"`
	Target   string `json:"target"`
	Focus    bool   `json:"focus"`
	Executed bool   `json:"executed"`
	Decision struct {
		Effect string `json:"effect"`
		RuleID string `json:"rule_id"`
	} `json:"decision"`
}

func explain(user string) string {
	var in struct {
		Origin string   `json:"origin"`
		Agents []string `json:"agents"`
		Steps  []step   `json:"steps"`
		Why    string   `json:"why"`
	}
	if err := json.Unmarshal([]byte(user), &in); err != nil {
		return "I could not read the incident."
	}
	var focus step
	for _, s := range in.Steps {
		if s.Focus {
			focus = s
		}
	}
	what := "was blocked before it ran"
	switch {
	case focus.Decision.Effect == "allow":
		what = "was allowed and ran, because no rule covers it"
	case focus.Executed:
		what = "ran, because Gjallarhorn was only monitoring, although rule " + focus.Decision.RuleID + " flags it"
	}
	agents := "no agents"
	if len(in.Agents) > 0 {
		agents = strings.Join(in.Agents, ", ")
	}
	return fmt.Sprintf("[Scripted stand-in model, not a real LLM] %s started this chain, which involved %s over %d call(s). The call in question, %s using %s, %s. %s",
		in.Origin, agents, len(in.Steps), focus.Actor, focus.Target, what, in.Why)
}

func suggest(user string) string {
	var in struct {
		Call struct {
			Plane  string    `json:"plane"`
			Target string    `json:"target"`
			Actor  principal `json:"actor"`
			Origin principal `json:"origin"`
			Depth  int       `json:"depth"`
		} `json:"call_to_stop"`
	}
	if err := json.Unmarshal([]byte(user), &in); err != nil || in.Call.Target == "" {
		return `{"rules":[]}`
	}
	c := in.Call
	precise := map[string]any{"planes": []string{c.Plane}, "targets": []string{c.Target}, "actors": []string{c.Actor.ID}, "origins": []string{c.Origin.ID}}
	if c.Depth > 0 {
		precise["delegated"] = true
	}
	family := c.Target
	if i := strings.Index(family, "."); i > 0 {
		family = family[:i]
	}
	rules := []map[string]any{
		{"id": "copilot-stub-precise", "description": fmt.Sprintf("%s may not use %s in chains started by %s", c.Actor.ID, c.Target, c.Origin.ID),
			"effect": "deny", "match": precise},
		{"id": "copilot-stub-block-all-" + family, "description": "Block every " + family + " tool for everyone (too broad on purpose)",
			"effect": "deny", "match": map[string]any{"targets": []string{family + ".*"}}},
		{"id": "copilot-stub-malformed", "description": "Uses a field Gjallarhorn does not know (malformed on purpose)",
			"effect": "deny", "severity": "high", "match": map[string]any{"targets": []string{c.Target}}},
	}
	b, _ := json.Marshal(map[string]any{"rules": rules})
	return string(b)
}
