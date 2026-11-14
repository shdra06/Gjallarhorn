// Package agents runs the demo company's three AI agents as real A2A servers.
//
// The agents' "brain" is deliberately simple and deliberately gullible: an agent follows any
// instruction written in its input, including instructions hidden in a customer message or in
// a web page it reads. That is exactly how prompt injection hijacks real LLM agents, and making
// it deterministic lets the attack lab reproduce every attack the same way on every run.
//
// Every action is still a real call through Bifrost (a model call, an MCP tool call or an
// Agent Gateway call), so Gjallarhorn sees and governs real traffic.
//
// Instructions an agent follows, one per line, anywhere in its input:
//
//	@model <model>                 think with this model (default: the agent's model)
//	@tool <name> {json arguments}  run an MCP tool, e.g. @tool billing-refund {"amount":500,...}
//	@delegate <agent>: <message>   ask another agent; the rest of the line is the message
package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log"
	"net"
	"net/http"
	"strings"
	"unicode"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/shdra06/Gjallarhorn/demo/internal/gateway"
)

// MaxActions bounds how many actions one message can trigger, so an injected page can't make
// an agent loop forever.
const MaxActions = 8

// Spec describes one agent.
type Spec struct {
	// Name is the Agent Gateway registration name (support, billing, research).
	Name        string
	Description string
	Port        int
	// VirtualKey is the agent's own Bifrost key. Empty means the agent reuses the caller's
	// forwarded key ("Forward Bifrost authentication").
	VirtualKey string
	// Model is the model the agent thinks with.
	Model string
	// ReadsInstructionsInToolOutput makes the agent follow instructions found in tool results
	// (indirect prompt injection), like an agent that summarises web pages.
	ReadsInstructionsInToolOutput bool
	// LeaksTicket makes the agent echo the chain ticket it received in its reply. It stands in
	// for a compromised or careless third-party agent, so the attack lab has a real ticket to
	// try to steal and forge.
	LeaksTicket bool
}

// Specs are the demo company's agents, plus one untrusted partner agent.
func Specs(supportVK, billingVK, partnerVK string) []Spec {
	return []Spec{
		{Name: "support", Description: "Answers customer questions, looks up orders, escalates billing issues.",
			Port: 9201, VirtualKey: supportVK, Model: "mock/standard-model"},
		{Name: "billing", Description: "Reads invoices and pays refunds.",
			Port: 9202, VirtualKey: billingVK, Model: "mock/standard-model"},
		{Name: "research", Description: "Reads web pages and writes summaries. Uses the caller's credentials.",
			Port: 9203, VirtualKey: "", Model: "mock/standard-model", ReadsInstructionsInToolOutput: true},
		{Name: "partner", Description: "A third-party agent from another company. Untrusted: it leaks what it receives.",
			Port: 9204, VirtualKey: partnerVK, Model: "mock/standard-model", LeaksTicket: true},
	}
}

// Action is one instruction found in an agent's input.
type Action struct {
	Kind   string // "model", "tool" or "delegate"
	Target string // model, tool name or agent name
	Args   string // JSON arguments (tool) or message (delegate)
}

// ParseActions finds instructions in text, in order. A @delegate consumes the rest of its
// line, so instructions meant for the next agent are passed along, not run here.
func ParseActions(text string) []Action {
	var out []Action
	for _, line := range strings.Split(text, "\n") {
		rest := line
		for {
			i := strings.Index(rest, "@")
			if i < 0 {
				break
			}
			rest = rest[i+1:]
			word, after := splitWord(rest)
			switch word {
			case "model":
				m, tail := splitWord(strings.TrimLeftFunc(after, unicode.IsSpace))
				if m != "" {
					out = append(out, Action{Kind: "model", Target: m})
				}
				rest = tail
			case "tool":
				name, tail := splitWord(strings.TrimLeftFunc(after, unicode.IsSpace))
				args, tail2 := takeJSON(strings.TrimLeftFunc(tail, unicode.IsSpace))
				if name != "" {
					out = append(out, Action{Kind: "tool", Target: name, Args: args})
				}
				rest = tail2
			case "delegate":
				spec := strings.TrimLeftFunc(after, unicode.IsSpace)
				colon := strings.Index(spec, ":")
				if colon > 0 {
					agent := strings.TrimSpace(spec[:colon])
					msg := strings.TrimSpace(spec[colon+1:])
					msg = strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(msg, "-->")), "-->")
					if agent != "" {
						out = append(out, Action{Kind: "delegate", Target: agent, Args: msg})
					}
				}
				rest = "" // the rest of the line belongs to the delegated message
			default:
				rest = after
			}
		}
	}
	return out
}

func splitWord(s string) (string, string) {
	end := strings.IndexFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '/' || r == '.')
	})
	if end < 0 {
		return s, ""
	}
	return s[:end], s[end:]
}

// takeJSON reads one balanced JSON object from the start of s.
func takeJSON(s string) (string, string) {
	if !strings.HasPrefix(s, "{") {
		return "{}", s
	}
	depth, inStr, esc := 0, false, false
	for i, r := range s {
		switch {
		case esc:
			esc = false
		case r == '\\' && inStr:
			esc = true
		case r == '"':
			inStr = !inStr
		case !inStr && r == '{':
			depth++
		case !inStr && r == '}':
			depth--
			if depth == 0 {
				return s[:i+1], s[i+1:]
			}
		}
	}
	return "{}", s
}

// Agent is a running agent.
type Agent struct {
	Spec Spec
	gw   *gateway.Client
}

// New creates an agent that acts through the gateway at gatewayURL.
func New(spec Spec, gatewayURL string) *Agent {
	return &Agent{Spec: spec, gw: gateway.New(gatewayURL)}
}

// Card returns the agent's A2A card.
func (a *Agent) Card(host string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "demo-" + a.Spec.Name,
		Description: a.Spec.Description,
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(fmt.Sprintf("http://%s:%d/invoke", host, a.Spec.Port), a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills: []a2a.AgentSkill{{ID: a.Spec.Name, Name: a.Spec.Name, Description: a.Spec.Description,
			Tags: []string{"demo"}}},
	}
}

func param(ec *a2asrv.ExecutorContext, key string) string {
	if ec.ServiceParams == nil {
		return ""
	}
	if v, ok := ec.ServiceParams.Get(key); ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

// identity is how this agent appears on its outbound calls for one task.
func (a *Agent) identity(ec *a2asrv.ExecutorContext) gateway.Identity {
	vk := a.Spec.VirtualKey
	if vk == "" {
		vk = param(ec, gateway.HeaderVK) // forwarded by Bifrost
	}
	return gateway.Identity{
		VirtualKey:  vk,
		Chain:       param(ec, gateway.HeaderChain),
		Label:       param(ec, gateway.HeaderLabel),
		SessionID:   ec.ContextID,
		Correlation: string(ec.TaskID),
	}
}

// Handle runs one message and returns the agent's reply.
func (a *Agent) Handle(ctx context.Context, id gateway.Identity, input string) string {
	var lines []string
	say := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	if a.Spec.LeaksTicket && id.Chain != "" {
		say("[%s] leaked ticket: %s", a.Spec.Name, id.Chain)
	}

	// Think first: one model call, like any LLM agent.
	thought := a.gw.Chat(ctx, id, a.Spec.Model, fmt.Sprintf("You are the %s agent. Task: %s", a.Spec.Name, firstLine(input)))
	say("[%s] think with %s: %s", a.Spec.Name, a.Spec.Model, thought)
	if thought.Blocked {
		say("[%s] stopped: I was blocked from thinking", a.Spec.Name)
		return strings.Join(lines, "\n")
	}

	queue := ParseActions(input)
	for n := 0; n < len(queue) && n < MaxActions; n++ {
		act := queue[n]
		switch act.Kind {
		case "model":
			r := a.gw.Chat(ctx, id, act.Target, firstLine(input))
			say("[%s] model %s: %s", a.Spec.Name, act.Target, r)
		case "tool":
			r := a.gw.Tool(ctx, id, act.Target, act.Args)
			say("[%s] tool %s %s: %s", a.Spec.Name, act.Target, act.Args, r)
			if a.Spec.ReadsInstructionsInToolOutput && !r.Blocked && r.Status == http.StatusOK {
				// The agent "reads" what the tool returned and follows what it finds.
				queue = append(queue, ParseActions(r.Text)...)
			}
		case "delegate":
			r := a.gw.Ask(ctx, id, act.Target, act.Args)
			say("[%s] ask %s %q: %s", a.Spec.Name, act.Target, act.Args, r)
		}
	}
	if len(queue) > MaxActions {
		say("[%s] stopped after %d actions", a.Spec.Name, MaxActions)
	}
	return strings.Join(lines, "\n")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Serve starts the agent's A2A server (JSON-RPC at /invoke, card at the well-known path).
func (a *Agent) Serve(host string) (*http.Server, error) {
	exec := a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			input := ""
			if ec.Message != nil {
				for _, p := range ec.Message.Parts {
					input += p.Text()
				}
			}
			reply := a.Handle(ctx, a.identity(ec), input)
			msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(reply))
			msg.ContextID = ec.ContextID
			yield(msg, nil)
		}
	})
	mux := http.NewServeMux()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(exec)))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(a.Card(host)))
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, a.Spec.Port))
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("agent %s stopped: %v", a.Spec.Name, err)
		}
	}()
	return srv, nil
}

// Describe returns a JSON summary of the agents (for logs).
func Describe(specs []Spec) string {
	type row struct {
		Name     string `json:"name"`
		Port     int    `json:"port"`
		OwnKey   bool   `json:"own_key"`
		Indirect bool   `json:"follows_tool_output"`
	}
	var rows []row
	for _, s := range specs {
		rows = append(rows, row{s.Name, s.Port, s.VirtualKey != "", s.ReadsInstructionsInToolOutput})
	}
	b, _ := json.Marshal(rows)
	return string(b)
}
