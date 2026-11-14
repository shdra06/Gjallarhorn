// Package identity resolves who is behind a request: a human, an agent or a service.
//
// Every call that passes through the gateway carries a credential (a Bifrost virtual key).
// The registry maps those credentials to principals. Raw keys are never stored by this
// package: they are reduced to a SHA-256 fingerprint on the way in, and configuration may
// list either the raw key or its fingerprint ("sha256:<hex>") so secrets need not appear in
// plain text.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Kind is the type of a principal.
type Kind string

const (
	KindHuman   Kind = "human"
	KindAgent   Kind = "agent"
	KindService Kind = "service"
	KindUnknown Kind = "unknown"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindHuman, KindAgent, KindService, KindUnknown:
		return true
	}
	return false
}

// Principal is an identity that can make or receive calls.
type Principal struct {
	// ID is unique and conventionally "<kind>:<name>", e.g. "agent:support".
	ID     string            `json:"id"`
	Kind   Kind              `json:"kind"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Label returns the value of a label, or "" when it is not set.
func (p Principal) Label(key string) string {
	if p.Labels == nil {
		return ""
	}
	return p.Labels[key]
}

// Clone returns a deep copy so callers can't mutate registry state.
func (p Principal) Clone() Principal {
	out := Principal{ID: p.ID, Kind: p.Kind}
	if len(p.Labels) > 0 {
		out.Labels = make(map[string]string, len(p.Labels))
		for k, v := range p.Labels {
			out.Labels[k] = v
		}
	}
	return out
}

// IsZero reports whether p is the empty principal.
func (p Principal) IsZero() bool { return p.ID == "" }

// PrincipalConfig declares one principal and the credentials and agent names that map to it.
type PrincipalConfig struct {
	ID     string            `json:"id"`
	Kind   Kind              `json:"kind"`
	Labels map[string]string `json:"labels,omitempty"`
	// VirtualKeys are raw Bifrost virtual key values or "sha256:<hex>" fingerprints.
	VirtualKeys []string `json:"virtual_keys,omitempty"`
	// AgentNames are Bifrost Agent Gateway registration names that refer to this principal.
	// Only valid for agents.
	AgentNames []string `json:"agent_names,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*:[A-Za-z0-9._@/-]{1,128}$`)

// ValidID reports whether id has the "<kind>:<name>" shape.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Fingerprint returns the canonical fingerprint of a raw credential: "sha256:<hex>".
func Fingerprint(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ShortFingerprint returns a short, log-safe form of a credential fingerprint.
func ShortFingerprint(raw string) string {
	if raw == "" {
		return ""
	}
	return Fingerprint(raw)[:7+12]
}

func normaliseKey(k string) (string, error) {
	k = strings.TrimSpace(k)
	if k == "" {
		return "", errors.New("empty virtual key")
	}
	if strings.HasPrefix(k, "sha256:") {
		h := strings.TrimPrefix(k, "sha256:")
		if len(h) != 64 {
			return "", fmt.Errorf("fingerprint %q must have 64 hex characters", k)
		}
		if _, err := hex.DecodeString(h); err != nil {
			return "", fmt.Errorf("fingerprint %q is not hex", k)
		}
		return "sha256:" + strings.ToLower(h), nil
	}
	return Fingerprint(k), nil
}

// Registry maps credentials and agent names to principals. It is safe for concurrent use
// and can be replaced atomically with Load.
type Registry struct {
	mu      sync.RWMutex
	byID    map[string]Principal
	byKey   map[string]string // fingerprint -> principal id
	byAgent map[string]string // agent registration name -> principal id
}

// NewRegistry builds a registry from configuration.
func NewRegistry(cfg []PrincipalConfig) (*Registry, error) {
	r := &Registry{}
	if err := r.Load(cfg); err != nil {
		return nil, err
	}
	return r, nil
}

// Load validates cfg and replaces the registry contents. On error the old contents stay.
func (r *Registry) Load(cfg []PrincipalConfig) error {
	byID := make(map[string]Principal, len(cfg))
	byKey := make(map[string]string)
	byAgent := make(map[string]string)
	for i, pc := range cfg {
		if !ValidID(pc.ID) {
			return fmt.Errorf("principals[%d]: id %q must look like \"agent:support\"", i, pc.ID)
		}
		if !pc.Kind.Valid() || pc.Kind == KindUnknown {
			return fmt.Errorf("principals[%d] (%s): kind must be human, agent or service", i, pc.ID)
		}
		if _, dup := byID[pc.ID]; dup {
			return fmt.Errorf("principals[%d]: duplicate id %q", i, pc.ID)
		}
		p := Principal{ID: pc.ID, Kind: pc.Kind, Labels: pc.Labels}.Clone()
		byID[pc.ID] = p
		for _, vk := range pc.VirtualKeys {
			fp, err := normaliseKey(vk)
			if err != nil {
				return fmt.Errorf("principals[%d] (%s): %w", i, pc.ID, err)
			}
			if owner, dup := byKey[fp]; dup {
				return fmt.Errorf("principals[%d] (%s): virtual key already belongs to %s", i, pc.ID, owner)
			}
			byKey[fp] = pc.ID
		}
		if len(pc.AgentNames) > 0 && pc.Kind != KindAgent {
			return fmt.Errorf("principals[%d] (%s): agent_names is only valid for agents", i, pc.ID)
		}
		for _, name := range pc.AgentNames {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("principals[%d] (%s): empty agent name", i, pc.ID)
			}
			if owner, dup := byAgent[name]; dup {
				return fmt.Errorf("principals[%d] (%s): agent name %q already belongs to %s", i, pc.ID, name, owner)
			}
			byAgent[name] = pc.ID
		}
	}
	r.mu.Lock()
	r.byID, r.byKey, r.byAgent = byID, byKey, byAgent
	r.mu.Unlock()
	return nil
}

// ByVirtualKey resolves a raw virtual key. Unknown or empty keys resolve to an "unknown"
// principal whose ID carries a short fingerprint, so different unknown callers stay apart.
func (r *Registry) ByVirtualKey(raw string) (Principal, bool) {
	if raw == "" {
		return Principal{ID: "unknown:anonymous", Kind: KindUnknown}, false
	}
	fp := Fingerprint(raw)
	r.mu.RLock()
	id, ok := r.byKey[fp]
	var p Principal
	if ok {
		p = r.byID[id].Clone()
	}
	r.mu.RUnlock()
	if ok {
		return p, true
	}
	return Principal{ID: "unknown:" + fp[7:19], Kind: KindUnknown}, false
}

// ByAgentName resolves an Agent Gateway registration name. Unregistered names become an
// agent principal "agent:<name>" so they are still tracked and can be locked.
func (r *Registry) ByAgentName(name string) (Principal, bool) {
	r.mu.RLock()
	id, ok := r.byAgent[name]
	var p Principal
	if ok {
		p = r.byID[id].Clone()
	}
	r.mu.RUnlock()
	if ok {
		return p, true
	}
	return Principal{ID: "agent:" + sanitiseName(name), Kind: KindAgent}, false
}

// ByID returns a configured principal.
func (r *Registry) ByID(id string) (Principal, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[id]
	if !ok {
		return Principal{}, false
	}
	return p.Clone(), true
}

// All returns every configured principal, sorted by ID.
func (r *Registry) All() []Principal {
	r.mu.RLock()
	out := make([]Principal, 0, len(r.byID))
	for _, p := range r.byID {
		out = append(out, p.Clone())
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._@/-]`)

func sanitiseName(s string) string {
	s = unsafeName.ReplaceAllString(s, "_")
	if s == "" {
		return "unnamed"
	}
	if len(s) > 128 {
		s = s[:128]
	}
	return s
}
