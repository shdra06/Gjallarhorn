package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shdra06/Gjallarhorn/engine/identity"
	"github.com/shdra06/Gjallarhorn/engine/policy"
	"github.com/shdra06/Gjallarhorn/engine/ticket"
)

// Config is the engine configuration. In Bifrost it is the plugin's "config" object.
type Config struct {
	Principals   []identity.PrincipalConfig `json:"principals"`
	Tickets      TicketConfig               `json:"tickets"`
	Policy       *policy.Policy             `json:"policy,omitempty"`
	Recorder     RecorderConfig             `json:"recorder"`
	LockdownFile string                     `json:"lockdown_file,omitempty"`
}

// TicketConfig configures chain tickets.
type TicketConfig struct {
	// ActiveKey is the ID of the key used to sign new tickets.
	ActiveKey string `json:"active_key"`
	// Keys maps key IDs to secrets: "hex:<...>", "base64:<...>", "env:<VAR>" or raw text,
	// each at least 32 bytes. Keep old keys listed for one TTL after rotating.
	Keys map[string]string `json:"keys"`
	// TTL is how long a ticket stays valid (default 10m).
	TTL Duration `json:"ttl,omitempty"`
	// Header is the HTTP header carrying the ticket (default x-gjallarhorn-chain).
	Header string `json:"header,omitempty"`
}

// RecorderConfig configures the flight recorder.
type RecorderConfig struct {
	// Path is the JSON Lines log file. Empty keeps events in memory only.
	Path string `json:"path,omitempty"`
	// RingSize is how many recent events are kept in memory for the live view (default 4096).
	RingSize int `json:"ring_size,omitempty"`
	// ReplayLimit is how many of the most recent events a replay, simulation or co-pilot proof
	// reads from the log file (default 200000). The whole file is still verified.
	ReplayLimit int `json:"replay_limit,omitempty"`
}

// Duration is a time.Duration that reads and writes strings like "10m".
type Duration struct{ time.Duration }

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON implements json.Unmarshaler. It accepts "10m" or a number of seconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		d.Duration = v
		return nil
	}
	var secs float64
	if err := json.Unmarshal(b, &secs); err != nil {
		return errors.New("duration must be a string like \"10m\" or a number of seconds")
	}
	d.Duration = time.Duration(secs * float64(time.Second))
	return nil
}

func (c Config) withDefaults() Config {
	if c.Tickets.TTL.Duration <= 0 {
		c.Tickets.TTL.Duration = ticket.DefaultTTL
	}
	if c.Tickets.Header == "" {
		c.Tickets.Header = DefaultTicketHeader
	}
	c.Tickets.Header = strings.ToLower(c.Tickets.Header)
	if c.Recorder.RingSize <= 0 {
		c.Recorder.RingSize = 4096
	}
	if c.Recorder.ReplayLimit <= 0 {
		c.Recorder.ReplayLimit = 200000
	}
	// Resolve env: references so secrets can live in the environment, not in config files.
	if len(c.Tickets.Keys) > 0 {
		keys := make(map[string]string, len(c.Tickets.Keys))
		for id, v := range c.Tickets.Keys {
			if strings.HasPrefix(v, "env:") {
				v = os.Getenv(strings.TrimPrefix(v, "env:"))
			}
			keys[id] = v
		}
		c.Tickets.Keys = keys
	}
	return c
}

// Validate checks the configuration.
func (c Config) Validate() error {
	var errs []error
	if c.Tickets.ActiveKey == "" {
		errs = append(errs, errors.New("tickets.active_key is required"))
	}
	if len(c.Tickets.Keys) == 0 {
		errs = append(errs, errors.New("tickets.keys needs at least one key"))
	}
	for id, v := range c.Tickets.Keys {
		if v == "" {
			errs = append(errs, fmt.Errorf("tickets.keys.%s is empty (is its environment variable set?)", id))
		}
	}
	if c.Tickets.TTL.Duration < time.Second || c.Tickets.TTL.Duration > 24*time.Hour {
		errs = append(errs, errors.New("tickets.ttl must be between 1s and 24h"))
	}
	if c.Policy != nil {
		if err := c.Policy.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("policy: %w", err))
		}
	}
	return errors.Join(errs...)
}

// ParseConfig decodes configuration from JSON, rejecting unknown fields.
func ParseConfig(data []byte) (Config, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

// ConfigFromAny converts the untyped config Bifrost passes to a plugin's Init into a Config.
func ConfigFromAny(v any) (Config, error) {
	if v == nil {
		return Config{}, errors.New("config is empty")
	}
	if c, ok := v.(Config); ok {
		return c, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return ParseConfig(b)
}
