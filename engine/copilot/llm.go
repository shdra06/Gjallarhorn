// Package copilot is Gjallarhorn's AI co-pilot. It explains incidents in plain words and
// suggests policy rules, and it never trusts its own suggestions: every rule is validated and
// then replayed against the recorded traffic, and only rules proven to stop the incident
// without blocking anything else are offered as safe to apply.
//
// The co-pilot calls any OpenAI-compatible chat endpoint. In production that is Bifrost
// itself, so the co-pilot's own model calls are governed, logged and budgeted like any other.
// Without a model it still works: explanations are written from the recorded facts, and rules
// are synthesised from the incident, then proven the same way.
package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Config configures the model the co-pilot uses. Leave Endpoint empty to run without one.
type Config struct {
	// Endpoint is the base URL of an OpenAI-compatible API, e.g. http://127.0.0.1:8080 for the
	// Bifrost the plugin runs in.
	Endpoint string `json:"endpoint,omitempty"`
	// Model is the model name as the endpoint expects it, e.g. "openai/gpt-4o-mini".
	Model string `json:"model,omitempty"`
	// VirtualKey is sent as x-bf-vk when calling Bifrost. Accepts "env:VAR".
	VirtualKey string `json:"virtual_key,omitempty"`
	// APIKey is sent as a bearer token for other endpoints. Accepts "env:VAR".
	APIKey string `json:"api_key,omitempty"`
	// TimeoutSeconds bounds each model call (default 45).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

func resolveSecret(v string) string {
	if strings.HasPrefix(v, "env:") {
		return os.Getenv(strings.TrimPrefix(v, "env:"))
	}
	return v
}

// Enabled reports whether a model is configured.
func (c Config) Enabled() bool { return c.Endpoint != "" && c.Model != "" }

// LLM is a minimal OpenAI-compatible chat client.
type LLM struct {
	cfg  Config
	http *http.Client
}

// NewLLM returns a client, or nil when no model is configured.
func NewLLM(cfg Config) *LLM {
	if !cfg.Enabled() {
		return nil
	}
	t := time.Duration(cfg.TimeoutSeconds) * time.Second
	if t <= 0 {
		t = 45 * time.Second
	}
	cfg.VirtualKey = resolveSecret(cfg.VirtualKey)
	cfg.APIKey = resolveSecret(cfg.APIKey)
	return &LLM{cfg: cfg, http: &http.Client{Timeout: t}}
}

// Model returns the configured model name.
func (l *LLM) Model() string { return l.cfg.Model }

// ErrNoModel is returned when the co-pilot has no model.
var ErrNoModel = errors.New("copilot: no model configured")

// Complete sends a system and a user message and returns the reply text.
func (l *LLM) Complete(ctx context.Context, system, user string, jsonMode bool) (string, error) {
	if l == nil {
		return "", ErrNoModel
	}
	body := map[string]any{
		"model": l.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.2,
	}
	if jsonMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	b, _ := json.Marshal(body)
	url := strings.TrimRight(l.cfg.Endpoint, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if l.cfg.VirtualKey != "" {
		req.Header.Set("x-bf-vk", l.cfg.VirtualKey)
	}
	if l.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.cfg.APIKey)
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("copilot: model call failed: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("copilot: model returned %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("copilot: unexpected model response: %s", truncate(string(data), 300))
	}
	return out.Choices[0].Message.Content, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// extractJSON returns the first JSON object in s (models sometimes wrap JSON in prose or code
// fences).
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}
