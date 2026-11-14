// Package lockdown keeps the list of principals that are cut off right now.
//
// A lockdown takes effect on the very next call: checks are an in-memory map lookup. The
// list can be persisted to a JSON file so it survives a gateway restart.
package lockdown

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Entry describes one lockdown.
type Entry struct {
	Principal string    `json:"principal"`
	Reason    string    `json:"reason"`
	By        string    `json:"by"`
	Since     time.Time `json:"since"`
	// Auto is true when a policy rule triggered the lockdown.
	Auto   bool   `json:"auto,omitempty"`
	RuleID string `json:"rule_id,omitempty"`
	// Until is optional; a zero value means "until someone lifts it".
	Until time.Time `json:"until,omitempty"`
}

// Active reports whether the lockdown applies at now. A lockdown applies from Since (so a
// replay can ask "what if this agent had been locked at 10:42?") until Until, if set.
func (e Entry) Active(now time.Time) bool {
	if !e.Since.IsZero() && now.Before(e.Since) {
		return false
	}
	return e.Until.IsZero() || now.Before(e.Until)
}

// Registry is the set of current lockdowns. It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Entry
	path    string
	now     func() time.Time
}

// Option configures a Registry.
type Option func(*Registry)

// WithFile persists lockdowns to path. Existing content is loaded by New.
func WithFile(path string) Option { return func(r *Registry) { r.path = path } }

// WithClock replaces the time source.
func WithClock(now func() time.Time) Option { return func(r *Registry) { r.now = now } }

// New creates a registry, loading any persisted lockdowns.
func New(opts ...Option) (*Registry, error) {
	r := &Registry{entries: map[string]Entry{}, now: time.Now}
	for _, o := range opts {
		o(r)
	}
	if r.path != "" {
		if err := r.load(); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// IsLocked reports whether principal is locked at now. It is the hot path: one read lock
// and one map lookup.
func (r *Registry) IsLocked(principal string, now time.Time) bool {
	r.mu.RLock()
	e, ok := r.entries[principal]
	r.mu.RUnlock()
	return ok && e.Active(now)
}

// Get returns the lockdown for principal, if any (active or not).
func (r *Registry) Get(principal string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[principal]
	return e, ok
}

// Lock adds or replaces a lockdown. It returns the stored entry.
func (r *Registry) Lock(e Entry) (Entry, error) {
	if e.Principal == "" {
		return Entry{}, errors.New("lockdown needs a principal")
	}
	if e.Since.IsZero() {
		e.Since = r.now()
	}
	if e.Reason == "" {
		e.Reason = "locked by operator"
	}
	r.mu.Lock()
	prev, had := r.entries[e.Principal]
	r.entries[e.Principal] = e
	err := r.saveLocked()
	if err != nil {
		// Keep memory and disk consistent: undo the change.
		if had {
			r.entries[e.Principal] = prev
		} else {
			delete(r.entries, e.Principal)
		}
	}
	r.mu.Unlock()
	if err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Unlock lifts a lockdown. It returns the removed entry and whether there was one.
func (r *Registry) Unlock(principal string) (Entry, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[principal]
	if !ok {
		return Entry{}, false, nil
	}
	delete(r.entries, principal)
	if err := r.saveLocked(); err != nil {
		r.entries[principal] = e
		return Entry{}, false, err
	}
	return e, true, nil
}

// List returns active lockdowns sorted by start time (oldest first).
func (r *Registry) List() []Entry {
	now := r.now()
	r.mu.RLock()
	out := make([]Entry, 0, len(r.entries))
	for _, e := range r.entries {
		if e.Active(now) {
			out = append(out, e)
		}
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since.Equal(out[j].Since) {
			return out[i].Principal < out[j].Principal
		}
		return out[i].Since.Before(out[j].Since)
	})
	return out
}

// Snapshot returns an immutable copy that can be used by replay.
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := make(Snapshot, len(r.entries))
	for k, v := range r.entries {
		s[k] = v
	}
	return s
}

// Snapshot is a frozen set of lockdowns. It implements policy.LockChecker.
type Snapshot map[string]Entry

// IsLocked reports whether principal is locked at now.
func (s Snapshot) IsLocked(principal string, now time.Time) bool {
	e, ok := s[principal]
	return ok && e.Active(now)
}

type fileFormat struct {
	Version   int     `json:"version"`
	Lockdowns []Entry `json:"lockdowns"`
}

func (r *Registry) load() error {
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lockdown: read %s: %w", r.path, err)
	}
	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return fmt.Errorf("lockdown: parse %s: %w", r.path, err)
	}
	for _, e := range ff.Lockdowns {
		if e.Principal != "" {
			r.entries[e.Principal] = e
		}
	}
	return nil
}

// saveLocked writes the file atomically. Callers hold r.mu.
func (r *Registry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	ff := fileFormat{Version: 1, Lockdowns: make([]Entry, 0, len(r.entries))}
	for _, e := range r.entries {
		ff.Lockdowns = append(ff.Lockdowns, e)
	}
	sort.Slice(ff.Lockdowns, func(i, j int) bool { return ff.Lockdowns[i].Principal < ff.Lockdowns[j].Principal })
	data, err := json.MarshalIndent(ff, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.path)
	tmp, err := os.CreateTemp(dir, ".lockdowns-*.json")
	if err != nil {
		return fmt.Errorf("lockdown: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("lockdown: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("lockdown: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("lockdown: %w", err)
	}
	if err := os.Rename(tmp.Name(), r.path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("lockdown: %w", err)
	}
	return nil
}
