// Package main is the Phase 1 feasibility plugin. It answers four questions with real Bifrost:
//
//  1. Does a custom .so plugin load into a dynamically linked Bifrost?
//  2. Can PreLLMHook block a call with a custom error, without Bifrost retrying a fallback?
//  3. Can the plugin see the caller's virtual key and request headers?
//  4. Does a header added in HTTPTransportPreHook on an Agent Gateway route reach the agent?
//
// It prints one JSON line per observation, prefixed with "GJSPIKE ", so the test script can
// read the results from the container log.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/core/schemas"
)

var (
	mu         sync.RWMutex
	blockModel = "blocked-model"
	header     = "x-gjallarhorn-chain"
)

func observe(kind string, fields map[string]any) {
	fields["kind"] = kind
	b, _ := json.Marshal(fields)
	fmt.Fprintln(os.Stdout, "GJSPIKE "+string(b))
}

func fingerprint(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// Init receives the plugin's "config" object from config.json.
func Init(config any) error {
	if m, ok := config.(map[string]any); ok {
		mu.Lock()
		if v, ok := m["block_model"].(string); ok && v != "" {
			blockModel = v
		}
		if v, ok := m["header"].(string); ok && v != "" {
			header = strings.ToLower(v)
		}
		mu.Unlock()
	}
	observe("init", map[string]any{"block_model": blockModel, "header": header})
	return nil
}

// GetName is required.
func GetName() string { return "gjallarhorn-spike" }

// Cleanup is required.
func Cleanup() error { return nil }

// HTTPTransportPreHook stamps a header on Agent Gateway requests.
func HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	path := req.Path
	if strings.Contains(path, "/agents/a2a/") {
		mu.RLock()
		h := header
		mu.RUnlock()
		if req.Headers == nil {
			req.Headers = map[string]string{}
		}
		req.Headers[h] = "spike-ticket-from-gateway"
		observe("transport_pre_a2a", map[string]any{"path": path, "set_header": h,
			"vk_fp": fingerprint(req.CaseInsensitiveHeaderLookup("x-bf-vk"))})
	}
	return nil, nil
}

// PreLLMHook reports what it can see and blocks the configured model.
func PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	provider, model, _ := req.GetRequestFields()
	vk, _ := ctx.Value(schemas.BifrostContextKeyVirtualKey).(string)
	headers, _ := ctx.Value(schemas.BifrostContextKeyRequestHeaders).(map[string]string)
	var seen []string
	for k := range headers {
		if strings.HasPrefix(k, "x-gjallarhorn") || strings.HasPrefix(k, "x-bf-") {
			seen = append(seen, k)
		}
	}
	mu.RLock()
	blocked := model == blockModel || strings.HasSuffix(model, "/"+blockModel)
	mu.RUnlock()
	observe("pre_llm", map[string]any{"provider": string(provider), "model": model, "vk_fp": fingerprint(vk),
		"headers_seen": seen, "chain_header": headers[header], "blocking": blocked})
	if !blocked {
		return req, nil, nil
	}
	status := 403
	noFallback := false
	msg := "blocked by gjallarhorn-spike: model " + model + " is not allowed"
	code := "gjallarhorn_denied"
	return req, &schemas.LLMPluginShortCircuit{Error: &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &status,
		AllowFallbacks: &noFallback,
		Error:          &schemas.ErrorField{Message: msg, Code: &code},
	}}, nil
}

// PostLLMHook records the outcome.
func PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	status := "ok"
	if bifrostErr != nil {
		status = "error"
	}
	observe("post_llm", map[string]any{"status": status})
	return resp, bifrostErr, nil
}

// PreA2AHook reports Agent Gateway calls.
func PreA2AHook(ctx *schemas.BifrostContext, req *schemas.BifrostA2ARequest) (*schemas.BifrostA2ARequest, *schemas.A2APluginShortCircuit, error) {
	vk, _ := ctx.Value(schemas.BifrostContextKeyVirtualKey).(string)
	headers, _ := ctx.Value(schemas.BifrostContextKeyRequestHeaders).(map[string]string)
	observe("pre_a2a", map[string]any{"agent": req.AgentName, "vk_fp": fingerprint(vk), "chain_header": headers[header]})
	return req, nil, nil
}
