package stubmodel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func complete(t *testing.T, srv *httptest.Server, model, system, user string, jsonMode bool) string {
	t.Helper()
	body := map[string]any{"model": model, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}}}
	if jsonMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(string(b)))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", err, resp)
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) != 1 {
		t.Fatalf("decode: %v", err)
	}
	return out.Choices[0].Message.Content
}

func TestAgentsGetTheFixedReply(t *testing.T) {
	srv := httptest.NewServer(Handler(t.Logf))
	defer srv.Close()
	if got := complete(t, srv, "standard-model", "", "hi", false); got != AgentReply {
		t.Fatalf("got %q", got)
	}
}

func TestScriptedCopilot(t *testing.T) {
	srv := httptest.NewServer(Handler(t.Logf))
	defer srv.Close()

	facts := `{"call_to_stop":{"plane":"mcp","target":"email.send","actor":{"id":"agent:research"},"origin":{"id":"service:customer-chat","labels":{"trust":"external"}},"depth":2}}`
	var rules struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal([]byte(complete(t, srv, "mock/"+CopilotModel, "You are Gjallarhorn's policy assistant.", facts, true)), &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules.Rules) != 3 {
		t.Fatalf("want a precise, a broad and a malformed rule, got %v", rules.Rules)
	}
	precise := rules.Rules[0]["match"].(map[string]any)
	if precise["delegated"] != true || precise["actors"].([]any)[0] != "agent:research" {
		t.Fatalf("precise rule: %v", precise)
	}
	if rules.Rules[1]["match"].(map[string]any)["targets"].([]any)[0] != "email.*" {
		t.Fatalf("broad rule: %v", rules.Rules[1])
	}
	if _, ok := rules.Rules[2]["severity"]; !ok {
		t.Fatal("the malformed rule must carry an unknown field")
	}

	incident := `{"origin":"service:customer-chat","agents":["agent:research","agent:support"],"why":"No rule applies.",
		"steps":[{"actor":"service:customer-chat","target":"agent:support"},{"actor":"agent:research","target":"email.send","focus":true,"executed":true,"decision":{"effect":"allow"}}]}`
	got := complete(t, srv, CopilotModel, "You are Gjallarhorn's incident analyst.", incident, false)
	for _, want := range []string{"Scripted stand-in", "agent:research using email.send", "was allowed and ran"} {
		if !strings.Contains(got, want) {
			t.Fatalf("explanation %q lacks %q", got, want)
		}
	}
}
