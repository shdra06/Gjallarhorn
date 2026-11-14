// Package gateway is a small client for the three things demo agents and the attack lab do
// through Bifrost: call a model, run a tool, and ask another agent.
//
// Every call carries the caller's virtual key and, when there is one, the Gjallarhorn chain
// ticket and the A2A correlation headers Bifrost documents for agent traffic.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
)

// Header names.
const (
	HeaderVK          = "x-bf-vk"
	HeaderChain       = "x-gjallarhorn-chain"
	HeaderLabel       = "x-gjallarhorn-label"
	HeaderSession     = "x-bf-session-id"
	HeaderCorrelation = "x-bf-agent-correlation-id"
)

// Identity is who a call is made as, and on behalf of which chain.
type Identity struct {
	VirtualKey  string
	Chain       string // Gjallarhorn chain ticket, if any
	Label       string // attack-lab label ("attack" or "benign"), if any
	SessionID   string
	Correlation string
}

func (id Identity) headers() map[string]string {
	h := map[string]string{HeaderVK: id.VirtualKey}
	if id.Chain != "" {
		h[HeaderChain] = id.Chain
	}
	if id.Label != "" {
		h[HeaderLabel] = id.Label
	}
	if id.SessionID != "" {
		h[HeaderSession] = id.SessionID
	}
	if id.Correlation != "" {
		h[HeaderCorrelation] = id.Correlation
	}
	return h
}

// Result is the outcome of one gateway call.
type Result struct {
	Status  int    // HTTP status (0 if the request could not be made)
	Blocked bool   // the gateway refused the call (401, 403 or a Gjallarhorn denial)
	By      string // "gjallarhorn", "bifrost" or "" when allowed
	Text    string // model reply, tool output, agent reply, or error message
}

func (r Result) String() string {
	if r.Blocked {
		return fmt.Sprintf("BLOCKED by %s (%d): %s", r.By, r.Status, oneLine(r.Text))
	}
	if r.Status >= 400 || r.Status == 0 {
		return fmt.Sprintf("FAILED (%d): %s", r.Status, oneLine(r.Text))
	}
	return "OK: " + oneLine(r.Text)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

// Client talks to one Bifrost gateway.
type Client struct {
	Base string // e.g. http://127.0.0.1:8080
	HTTP *http.Client
}

// New returns a client for the gateway at base.
func New(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) post(ctx context.Context, path string, id Identity, body any) Result {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+path, bytes.NewReader(b))
	if err != nil {
		return Result{Text: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range id.headers() {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{Text: err.Error()}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return classify(resp.StatusCode, data)
}

// classify turns a gateway response into a Result, telling Gjallarhorn denials apart from
// Bifrost's own refusals (unknown key, missing grant, budget).
func classify(status int, data []byte) Result {
	r := Result{Status: status, Text: string(data)}
	if status < 400 {
		return r
	}
	lower := strings.ToLower(string(data))
	switch {
	case strings.Contains(lower, "gjallarhorn"):
		r.Blocked, r.By = true, "gjallarhorn"
	case status == 401 || status == 403 || status == 402 || status == 429:
		r.Blocked, r.By = true, "bifrost"
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &env) == nil && env.Error.Message != "" {
		r.Text = env.Error.Message
	}
	return r
}

// Chat calls a model through Bifrost and returns its reply.
func (c *Client) Chat(ctx context.Context, id Identity, model, prompt string) Result {
	r := c.post(ctx, "/v1/chat/completions", id, map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	if r.Status == http.StatusOK {
		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(r.Text), &out) == nil && len(out.Choices) > 0 {
			r.Text = out.Choices[0].Message.Content
		}
	}
	return r
}

// Tool runs an MCP tool through Bifrost's tool gateway. name is Bifrost's registered tool name
// ("<client>-<tool>") and args is a JSON object.
func (c *Client) Tool(ctx context.Context, id Identity, name, args string) Result {
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	r := c.post(ctx, "/v1/mcp/tool/execute?format=chat", id, map[string]any{
		"id":       fmt.Sprintf("call-%d", time.Now().UnixNano()),
		"type":     "function",
		"function": map[string]string{"name": name, "arguments": args},
	})
	if r.Status == http.StatusOK {
		var out struct {
			Content any `json:"content"`
		}
		if json.Unmarshal([]byte(r.Text), &out) == nil {
			switch v := out.Content.(type) {
			case string:
				r.Text = v
			case nil:
			default:
				b, _ := json.Marshal(v)
				r.Text = string(b)
			}
		}
	}
	return r
}

// Ask sends a message to an agent registered in Bifrost's Agent Gateway and returns the
// agent's reply text.
func (c *Client) Ask(ctx context.Context, id Identity, agent, text string) Result {
	url := fmt.Sprintf("%s/agents/a2a/%s/json-rpc", c.Base, agent)
	client, err := a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{
		a2a.NewAgentInterface(url, a2a.TransportProtocolJSONRPC),
	})
	if err != nil {
		return Result{Text: err.Error()}
	}
	params := a2aclient.ServiceParams{}
	for k, v := range id.headers() {
		params[k] = []string{v}
	}
	ctx = a2aclient.AttachServiceParams(ctx, params)
	resp, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text)),
	})
	if err != nil {
		return classifyA2AError(err)
	}
	return Result{Status: http.StatusOK, Text: replyText(resp)}
}

func classifyA2AError(err error) Result {
	msg := err.Error()
	lower := strings.ToLower(msg)
	r := Result{Status: 0, Text: msg}
	switch {
	case strings.Contains(lower, "gjallarhorn"):
		r.Status, r.Blocked, r.By = http.StatusForbidden, true, "gjallarhorn"
	case strings.Contains(lower, "403") || strings.Contains(lower, "401") || strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "unauthorized") || strings.Contains(lower, "not allowed") || strings.Contains(lower, "access"):
		r.Status, r.Blocked, r.By = http.StatusForbidden, true, "bifrost"
	}
	return r
}

// replyText extracts the text of an agent's reply (a message, or a task's latest message).
func replyText(resp a2a.SendMessageResult) string {
	var parts []string
	collect := func(msg *a2a.Message) {
		if msg == nil {
			return
		}
		for _, p := range msg.Parts {
			if t := p.Text(); t != "" {
				parts = append(parts, t)
			}
		}
	}
	switch v := resp.(type) {
	case *a2a.Message:
		collect(v)
	case *a2a.Task:
		if v.Status.Message != nil {
			collect(v.Status.Message)
		} else if len(v.History) > 0 {
			collect(v.History[len(v.History)-1])
		}
		for _, a := range v.Artifacts {
			for _, p := range a.Parts {
				if t := p.Text(); t != "" {
					parts = append(parts, t)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}
