// Package ticket implements chain tickets: short-lived, gateway-signed records of who asked whom.
//
// When a caller asks an agent to do something through the gateway, the gateway mints a ticket
// listing the whole chain: the origin (whoever started it) followed by every agent that was
// asked along the way. The agent receives the ticket and sends it back on its own calls. Agents
// can drop a ticket, but they cannot forge or extend one: only the gateway holds the signing key.
//
// Wire format (all parts base64url without padding):
//
//	gh1.<payload>.<mac>
//
// where payload is the JSON encoding of Claims and mac is HMAC-SHA256 over "gh1.<payload>"
// using the key named by Claims.KID.
package ticket

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// Prefix identifies the ticket format and version.
	Prefix = "gh1"
	// MaxEncodedLen bounds the size of a ticket we are willing to parse.
	MaxEncodedLen = 4096
	// MaxHops bounds the length of a delegation chain a ticket can describe.
	MaxHops = 32
	// MinKeyLen is the minimum signing key length in bytes.
	MinKeyLen = 32
	// DefaultTTL is how long a minted ticket stays valid.
	DefaultTTL = 10 * time.Minute
	// DefaultCacheSize is how many verified tickets are remembered.
	DefaultCacheSize = 8192
)

// Errors returned by Verify. They are stable and safe to show to operators.
var (
	ErrMalformed  = errors.New("ticket is malformed")
	ErrTooLarge   = errors.New("ticket is too large")
	ErrUnknownKey = errors.New("ticket was signed with an unknown key")
	ErrSignature  = errors.New("ticket signature is invalid")
	ErrExpired    = errors.New("ticket has expired")
	ErrNotYet     = errors.New("ticket is not valid yet")
	ErrTooDeep    = errors.New("ticket chain is too long")
)

// strictB64 rejects non-canonical encodings, so every ticket has exactly one valid spelling.
var strictB64 = base64.RawURLEncoding.Strict()

// Claims is the signed content of a ticket.
type Claims struct {
	Version int    `json:"v"`
	ID      string `json:"jti"`
	KID     string `json:"kid"`
	// Root is the principal that started the chain (a human, a service or an agent).
	Root     string `json:"root"`
	RootKind string `json:"rk"`
	// Hops lists the agent principals that were asked, in order. The last hop is the agent
	// that holds this ticket.
	Hops []string `json:"hops"`
	// Context optionally binds the ticket to an A2A context or task.
	Context   string `json:"ctx,omitempty"`
	Parent    string `json:"pid,omitempty"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// Holder returns the principal that holds the ticket (the last hop).
func (c Claims) Holder() string {
	if len(c.Hops) == 0 {
		return ""
	}
	return c.Hops[len(c.Hops)-1]
}

// Depth is the number of agent hops.
func (c Claims) Depth() int { return len(c.Hops) }

// Chain returns the root followed by every hop.
func (c Claims) Chain() []string {
	out := make([]string, 0, len(c.Hops)+1)
	out = append(out, c.Root)
	return append(out, c.Hops...)
}

// Keyring holds signing keys by ID. The active key signs; every key verifies, which allows
// rotation without invalidating tickets in flight.
type Keyring struct {
	mu     sync.RWMutex
	active string
	keys   map[string][]byte
}

// NewKeyring creates a keyring. keys maps key IDs to secrets encoded as "hex:<...>",
// "base64:<...>" or raw text (at least MinKeyLen bytes in every case).
func NewKeyring(active string, keys map[string]string) (*Keyring, error) {
	kr := &Keyring{}
	if err := kr.Load(active, keys); err != nil {
		return nil, err
	}
	return kr, nil
}

// Load replaces the keys. On error the previous keys stay in place.
func (k *Keyring) Load(active string, keys map[string]string) error {
	if len(keys) == 0 {
		return errors.New("at least one ticket key is required")
	}
	decoded := make(map[string][]byte, len(keys))
	for id, enc := range keys {
		if id == "" || len(id) > 64 || strings.ContainsAny(id, ". \t\n") {
			return fmt.Errorf("invalid key id %q", id)
		}
		b, err := decodeSecret(enc)
		if err != nil {
			return fmt.Errorf("key %q: %w", id, err)
		}
		if len(b) < MinKeyLen {
			return fmt.Errorf("key %q: must be at least %d bytes, got %d", id, MinKeyLen, len(b))
		}
		decoded[id] = b
	}
	if _, ok := decoded[active]; !ok {
		return fmt.Errorf("active key %q is not in the keyring", active)
	}
	k.mu.Lock()
	k.active, k.keys = active, decoded
	k.mu.Unlock()
	return nil
}

func decodeSecret(enc string) ([]byte, error) {
	switch {
	case strings.HasPrefix(enc, "hex:"):
		return hex.DecodeString(strings.TrimPrefix(enc, "hex:"))
	case strings.HasPrefix(enc, "base64:"):
		s := strings.TrimPrefix(enc, "base64:")
		if b, err := base64.StdEncoding.DecodeString(s); err == nil {
			return b, nil
		}
		return base64.RawURLEncoding.DecodeString(s)
	default:
		return []byte(enc), nil
	}
}

func (k *Keyring) signingKey() (string, []byte) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.active, k.keys[k.active]
}

func (k *Keyring) key(id string) ([]byte, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	b, ok := k.keys[id]
	return b, ok
}

// Issuer mints and verifies tickets.
type Issuer struct {
	keys  *Keyring
	ttl   time.Duration
	skew  time.Duration
	now   func() time.Time
	idFn  func() string // replaced in tests for deterministic tickets
	cache *verifiedCache
}

// IssuerOption configures an Issuer.
type IssuerOption func(*Issuer)

// WithTTL sets the ticket lifetime.
func WithTTL(d time.Duration) IssuerOption {
	return func(i *Issuer) {
		if d > 0 {
			i.ttl = d
		}
	}
}

// WithClock replaces the time source (used by tests and replay).
func WithClock(now func() time.Time) IssuerOption { return func(i *Issuer) { i.now = now } }

// WithSkew sets the tolerated clock skew when checking issued-at and expiry.
func WithSkew(d time.Duration) IssuerOption { return func(i *Issuer) { i.skew = d } }

// NewIssuer creates an issuer backed by keys.
func NewIssuer(keys *Keyring, opts ...IssuerOption) *Issuer {
	i := &Issuer{keys: keys, ttl: DefaultTTL, skew: 30 * time.Second, now: time.Now, idFn: newID}
	WithCache(DefaultCacheSize)(i)
	for _, o := range opts {
		o(i)
	}
	return i
}

// Start mints a ticket for the first hop: root asked agent.
func (i *Issuer) Start(root, rootKind, agent, context string) (string, Claims, error) {
	return i.mint(Claims{Root: root, RootKind: rootKind, Hops: []string{agent}, Context: context}, 0)
}

// Extend mints a ticket for the next hop: the holder of parent asked agent. The chain is
// copied from the verified parent, so a holder can never rewrite its own history.
func (i *Issuer) Extend(parent Claims, agent, context string) (string, Claims, error) {
	if len(parent.Hops)+1 > MaxHops {
		return "", Claims{}, ErrTooDeep
	}
	hops := make([]string, 0, len(parent.Hops)+1)
	hops = append(hops, parent.Hops...)
	hops = append(hops, agent)
	if context == "" {
		context = parent.Context
	}
	c := Claims{Root: parent.Root, RootKind: parent.RootKind, Hops: hops, Context: context, Parent: parent.ID}
	// A child ticket never outlives its parent.
	return i.mint(c, parent.ExpiresAt)
}

// mint fills in the ID and validity window and signs. notAfter (unix seconds, 0 = none) caps
// the expiry.
func (i *Issuer) mint(c Claims, notAfter int64) (string, Claims, error) {
	if c.Root == "" || len(c.Hops) == 0 {
		return "", Claims{}, errors.New("ticket needs a root and at least one hop")
	}
	if len(c.Hops) > MaxHops {
		return "", Claims{}, ErrTooDeep
	}
	now := i.now()
	c.Version = 1
	c.ID = i.idFn()
	c.IssuedAt = now.Unix()
	c.ExpiresAt = now.Add(i.ttl).Unix()
	if notAfter > 0 && c.ExpiresAt > notAfter {
		c.ExpiresAt = notAfter
	}
	tok, err := i.sign(&c)
	if err != nil {
		return "", Claims{}, err
	}
	return tok, c, nil
}

// sign sets c.KID to the active key and returns the encoded ticket.
func (i *Issuer) sign(c *Claims) (string, error) {
	kid, key := i.keys.signingKey()
	c.KID = kid
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	head := Prefix + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(head))
	tok := head + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(tok) > MaxEncodedLen {
		return "", ErrTooLarge
	}
	return tok, nil
}

// Verify checks a ticket's format, signature and validity window and returns its claims.
//
// Agents send the same ticket on every call they make for one task, so tickets that passed
// the signature check are cached. A cache hit still re-checks the validity window and that the
// signing key is still in the keyring, so expiry and key retirement take effect immediately.
func (i *Issuer) Verify(tok string) (Claims, error) {
	if len(tok) > MaxEncodedLen {
		return Claims{}, ErrTooLarge
	}
	if i.cache != nil {
		if c, ok := i.cache.get(tok); ok {
			if _, known := i.keys.key(c.KID); !known {
				i.cache.drop(tok)
				return Claims{}, ErrUnknownKey
			}
			if err := i.checkWindow(c); err != nil {
				return Claims{}, err
			}
			return c.clone(), nil
		}
	}
	c, err := i.verifySlow(tok)
	if err != nil {
		return Claims{}, err
	}
	if err := i.checkWindow(c); err != nil {
		return Claims{}, err
	}
	if i.cache != nil {
		i.cache.put(tok, c.clone())
	}
	return c, nil
}

func (i *Issuer) checkWindow(c Claims) error {
	now := i.now()
	if c.IssuedAt > now.Add(i.skew).Unix() {
		return ErrNotYet
	}
	if c.ExpiresAt <= now.Add(-i.skew).Unix() {
		return ErrExpired
	}
	return nil
}

func (c Claims) clone() Claims {
	c.Hops = append([]string(nil), c.Hops...)
	return c
}

// verifySlow decodes and checks the signature and structure (not the validity window).
func (i *Issuer) verifySlow(tok string) (Claims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] != Prefix || parts[1] == "" || parts[2] == "" {
		return Claims{}, ErrMalformed
	}
	payload, err := strictB64.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	sig, err := strictB64.DecodeString(parts[2])
	if err != nil || len(sig) != sha256.Size {
		return Claims{}, ErrMalformed
	}
	var c Claims
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Claims{}, ErrMalformed
	}
	key, ok := i.keys.key(c.KID)
	if !ok {
		return Claims{}, ErrUnknownKey
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Claims{}, ErrSignature
	}
	// Only trust the content after the signature is verified.
	if c.Version != 1 || c.ID == "" || c.Root == "" || len(c.Hops) == 0 {
		return Claims{}, ErrMalformed
	}
	if len(c.Hops) > MaxHops {
		return Claims{}, ErrTooDeep
	}
	return c, nil
}

// verifiedCache remembers tickets whose signature checked out. It is bounded: when full, an
// arbitrary entry is evicted (a cache miss only costs one more signature check).
type verifiedCache struct {
	mu   sync.Mutex
	max  int
	data map[string]Claims
}

func (vc *verifiedCache) get(tok string) (Claims, bool) {
	vc.mu.Lock()
	c, ok := vc.data[tok]
	vc.mu.Unlock()
	return c, ok
}

func (vc *verifiedCache) put(tok string, c Claims) {
	vc.mu.Lock()
	if len(vc.data) >= vc.max {
		for k := range vc.data {
			delete(vc.data, k)
			break
		}
	}
	vc.data[tok] = c
	vc.mu.Unlock()
}

func (vc *verifiedCache) drop(tok string) {
	vc.mu.Lock()
	delete(vc.data, tok)
	vc.mu.Unlock()
}

// WithCache keeps up to n verified tickets in memory (0 disables the cache).
func WithCache(n int) IssuerOption {
	return func(i *Issuer) {
		if n > 0 {
			i.cache = &verifiedCache{max: n, data: make(map[string]Claims, n)}
		} else {
			i.cache = nil
		}
	}
}

// Problem converts a Verify error into a short machine-readable code.
func Problem(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrExpired):
		return "ticket_expired"
	case errors.Is(err, ErrNotYet):
		return "ticket_not_yet_valid"
	case errors.Is(err, ErrUnknownKey):
		return "ticket_unknown_key"
	case errors.Is(err, ErrSignature):
		return "ticket_bad_signature"
	case errors.Is(err, ErrTooLarge), errors.Is(err, ErrTooDeep):
		return "ticket_too_large"
	default:
		return "ticket_malformed"
	}
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unrecoverable for a security component.
		panic("gjallarhorn: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
