// Package main is the Gjallarhorn plugin for the Bifrost AI gateway.
//
// It is a thin adapter: every hook turns Bifrost's arguments into an engine.Request, asks the
// engine for a decision, and either lets the call continue or blocks it. All decision logic
// lives in the engine module, which has no Bifrost dependency and is tested separately.
//
// Build it in the same toolchain and module graph as the Bifrost binary that loads it (see
// deploy/Containerfile), because Go plugins require identical dependency versions.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/shdra06/Gjallarhorn/engine"
	"github.com/shdra06/Gjallarhorn/engine/control"
	"github.com/shdra06/Gjallarhorn/engine/copilot"
	"github.com/shdra06/Gjallarhorn/engine/gateway"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/recorder"
)

// Version is set at build time with -ldflags "-X main.Version=...".
var Version = "dev"

// Context keys this plugin uses to pass state from pre hooks to post hooks.
const (
	ctxDecided schemas.BifrostContextKey = "gjallarhorn-a2a-decided"
	ctxReqID   schemas.BifrostContextKey = "gjallarhorn-request-id"
	ctxStart   schemas.BifrostContextKey = "gjallarhorn-start"
)

var (
	mu      sync.RWMutex
	eng     *engine.Engine
	cfg     gateway.Config
	ctrlSrv *http.Server
)

// Init is called once by Bifrost with the plugin's "config" object.
func Init(config any) error {
	c, err := gateway.ParseConfig(config)
	if err != nil {
		return err
	}
	e, err := engine.New(c.Config)
	if err != nil {
		return fmt.Errorf("gjallarhorn: %w", err)
	}
	if c.AcceptLabels {
		fmt.Fprintln(os.Stderr, "gjallarhorn: WARNING accept_labels is on: any caller can label its own traffic as attack or benign, which feeds replay verdicts and co-pilot proofs. Use it in test environments only.")
	}
	// monitor_only in the config sets the starting mode; operators can switch it at runtime
	// through the control API (PUT /v1/mode).
	e.SetEnforcing(!c.MonitorOnly, "config")
	var srv *http.Server
	if !c.Control.Disabled {
		h, err := control.NewHandler(e, control.Options{AdminToken: c.Control.AdminToken,
			AllowedOrigins: c.Control.AllowedOrigins, Version: Version, Copilot: copilot.NewLLM(c.Copilot),
			ConsoleDir: c.Control.ConsoleDir, DemoDriver: c.Control.DemoDriver})
		if err != nil {
			e.Close()
			return fmt.Errorf("gjallarhorn: %w", err)
		}
		ln, err := net.Listen("tcp", c.Control.Listen)
		if err != nil {
			e.Close()
			return fmt.Errorf("gjallarhorn: control API: %w", err)
		}
		srv = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintf(os.Stderr, "gjallarhorn: control API stopped: %v\n", err)
			}
		}()
	}
	mu.Lock()
	old, oldSrv := eng, ctrlSrv
	eng, cfg, ctrlSrv = e, c, srv
	mu.Unlock()
	// Bifrost calls Init again when the plugin config is updated; retire the previous instance.
	if oldSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = oldSrv.Shutdown(ctx)
		cancel()
	}
	if old != nil {
		_ = old.Close()
	}
	fmt.Fprintf(os.Stdout, "gjallarhorn %s: ready (%d principals, %d rules, control API %s)\n",
		Version, len(e.Registry().All()), len(e.Policy().Rules), controlAddr(c))
	return nil
}

func controlAddr(c gateway.Config) string {
	if c.Control.Disabled {
		return "disabled"
	}
	return c.Control.Listen
}

// GetName is required by Bifrost.
func GetName() string { return "gjallarhorn" }

// Cleanup is required by Bifrost. It stops the control API and flushes the flight recorder.
func Cleanup() error {
	mu.Lock()
	e, srv := eng, ctrlSrv
	eng, ctrlSrv = nil, nil
	mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	if e != nil {
		return e.Close()
	}
	return nil
}

func current() (*engine.Engine, gateway.Config) {
	mu.RLock()
	defer mu.RUnlock()
	return eng, cfg
}

// headersOf returns the lowercased request headers Bifrost stored in the context.
func headersOf(ctx *schemas.BifrostContext) map[string]string {
	h, _ := ctx.Value(schemas.BifrostContextKeyRequestHeaders).(map[string]string)
	return h
}

func credentialOf(ctx *schemas.BifrostContext, h map[string]string) string {
	if vk, ok := ctx.Value(schemas.BifrostContextKeyVirtualKey).(string); ok && vk != "" {
		return vk
	}
	return gateway.Credential(func(k string) string { return h[strings.ToLower(k)] })
}

func requestIDOf(ctx *schemas.BifrostContext) string {
	id, _ := ctx.Value(schemas.BifrostContextKeyRequestID).(string)
	return id
}

func labelOf(c gateway.Config, h map[string]string) string {
	if !c.AcceptLabels {
		return ""
	}
	return h[gateway.LabelHeader]
}

func forbidden(res engine.Result, quiet bool) *schemas.BifrostError {
	d := res.Decision
	status := http.StatusForbidden
	noFallback := false
	code := "gjallarhorn_" + d.Code
	typ := "gjallarhorn_denied"
	return &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &status,
		Type:           &typ,
		AllowFallbacks: &noFallback,
		Error:          &schemas.ErrorField{Message: gateway.DenyMessageFor(d, res.Seq, quiet), Code: &code, Type: &typ},
	}
}

// HTTPTransportPreHook decides Agent Gateway calls and stamps the chain ticket on the request
// that Bifrost forwards to the agent.
func HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	e, c := current()
	if e == nil {
		return nil, nil
	}
	agent, ok := gateway.A2AAgent(req.Path, c.A2APrefix)
	if !ok {
		return nil, nil
	}
	get := req.CaseInsensitiveHeaderLookup
	label := ""
	if c.AcceptLabels {
		label = get(gateway.LabelHeader)
	}
	res := e.Delegate(engine.Request{
		ID:         requestIDOf(ctx),
		Target:     agent,
		VirtualKey: gateway.Credential(get),
		Ticket:     get(e.TicketHeader()),
		Label:      label,
	})
	ctx.SetValue(ctxDecided, true)
	if res.Block() {
		return &schemas.HTTPResponse{
			StatusCode: http.StatusForbidden,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       gateway.DenyBodyFor(res.Decision, res.Seq, c.QuietDenials),
		}, nil
	}
	if req.Headers == nil {
		req.Headers = map[string]string{}
	}
	// Replace the caller's ticket with the new one. Header names arrive in whatever case the
	// client used ("X-Gjallarhorn-Chain"), so remove every spelling first; otherwise the agent
	// could receive both and pick up the caller's old ticket.
	for k := range req.Headers {
		if strings.EqualFold(k, e.TicketHeader()) {
			delete(req.Headers, k)
		}
	}
	if res.Ticket != "" {
		req.Headers[e.TicketHeader()] = res.Ticket
	}
	return nil, nil
}

// PreA2AHook covers Agent Gateway calls that bypass the HTTP transport (the gRPC binding).
// Those can be decided but can't carry a new chain ticket.
func PreA2AHook(ctx *schemas.BifrostContext, req *schemas.BifrostA2ARequest) (*schemas.BifrostA2ARequest, *schemas.A2APluginShortCircuit, error) {
	e, c := current()
	if e == nil {
		return req, nil, nil
	}
	if decided, _ := ctx.Value(ctxDecided).(bool); decided {
		ctx.SetValue(ctxStart, time.Now())
		return req, nil, nil
	}
	h := headersOf(ctx)
	res := e.Check(engine.Request{ID: requestIDOf(ctx), Plane: policy.PlaneA2A, Target: req.AgentName,
		VirtualKey: credentialOf(ctx, h), Ticket: h[e.TicketHeader()], Label: labelOf(c, h)})
	if res.Block() {
		return req, &schemas.A2APluginShortCircuit{Error: forbidden(res, c.QuietDenials)}, nil
	}
	ctx.SetValue(ctxStart, time.Now())
	return req, nil, nil
}

// PostA2AHook records the outcome of an agent call.
func PostA2AHook(ctx *schemas.BifrostContext, resp *schemas.BifrostA2AResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostA2AResponse, *schemas.BifrostError, error) {
	recordOutcome(ctx, bifrostErr)
	return resp, bifrostErr, nil
}

// PreLLMHook decides model calls. It runs once per fallback attempt; the decision does not
// depend on the provider chosen, so a denied call is denied on every attempt, and the error
// disables fallbacks anyway.
func PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	e, c := current()
	if e == nil {
		return req, nil, nil
	}
	provider, model, _ := req.GetRequestFields()
	h := headersOf(ctx)
	id := requestIDOf(ctx)
	res := e.Check(engine.Request{ID: id, Plane: policy.PlaneLLM, Target: gateway.LLMTarget(string(provider), model),
		VirtualKey: credentialOf(ctx, h), Ticket: h[e.TicketHeader()], Label: labelOf(c, h)})
	if res.Block() {
		return req, &schemas.LLMPluginShortCircuit{Error: forbidden(res, c.QuietDenials)}, nil
	}
	ctx.SetValue(ctxReqID, id)
	ctx.SetValue(ctxStart, time.Now())
	return req, nil, nil
}

// PostLLMHook records the outcome of a model call.
func PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	recordOutcome(ctx, bifrostErr)
	return resp, bifrostErr, nil
}

// PreMCPHook decides tool calls (and other MCP operations, such as listing tools, so a locked
// agent can't even look around).
func PreMCPHook(ctx *schemas.BifrostContext, req *schemas.BifrostMCPRequest) (*schemas.BifrostMCPRequest, *schemas.MCPPluginShortCircuit, error) {
	e, c := current()
	if e == nil {
		return req, nil, nil
	}
	h := headersOf(ctx)
	id := requestIDOf(ctx)
	target := gateway.MCPTarget(req.ClientName, req.GetToolName(), string(req.RequestType))
	res := e.Check(engine.Request{ID: id, Plane: policy.PlaneMCP, Target: target,
		VirtualKey: credentialOf(ctx, h), Ticket: h[e.TicketHeader()], Label: labelOf(c, h)})
	if res.Block() {
		return req, &schemas.MCPPluginShortCircuit{Error: forbidden(res, c.QuietDenials)}, nil
	}
	ctx.SetValue(ctxReqID, id)
	ctx.SetValue(ctxStart, time.Now())
	return req, nil, nil
}

// PostMCPHook records the outcome of a tool call.
func PostMCPHook(ctx *schemas.BifrostContext, resp *schemas.BifrostMCPResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostMCPResponse, *schemas.BifrostError, error) {
	recordOutcome(ctx, bifrostErr)
	return resp, bifrostErr, nil
}

func recordOutcome(ctx *schemas.BifrostContext, bifrostErr *schemas.BifrostError) {
	e, _ := current()
	if e == nil {
		return
	}
	id, _ := ctx.Value(ctxReqID).(string)
	if id == "" {
		id = requestIDOf(ctx)
	}
	out := recorder.Outcome{Status: "ok"}
	if start, ok := ctx.Value(ctxStart).(time.Time); ok {
		out.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
	}
	if bifrostErr != nil {
		out.Status = "error"
		if bifrostErr.Error != nil {
			out.Error = bifrostErr.Error.Message
		}
	}
	e.RecordOutcome(id, out)
}
