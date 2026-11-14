package ticket

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const key1 = "0123456789abcdef0123456789abcdef"
const key2 = "fedcba9876543210fedcba9876543210"

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestIssuer(t *testing.T, c *clock) (*Issuer, *Keyring) {
	t.Helper()
	kr, err := NewKeyring("k1", map[string]string{"k1": key1, "k2": "hex:" + strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	return NewIssuer(kr, WithClock(c.now), WithTTL(10*time.Minute)), kr
}

func TestStartAndVerify(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	is, _ := newTestIssuer(t, c)
	tok, claims, err := is.Start("human:priya", "human", "agent:support", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "gh1.") {
		t.Fatalf("unexpected token %q", tok)
	}
	got, err := is.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != claims.ID || got.Root != "human:priya" || got.Holder() != "agent:support" || got.Depth() != 1 || got.Context != "task-1" {
		t.Fatalf("claims mismatch: %+v", got)
	}
	if strings.Join(got.Chain(), ">") != "human:priya>agent:support" {
		t.Fatalf("chain %v", got.Chain())
	}
}

func TestExtendCopiesHistoryAndNeverOutlivesParent(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	is, _ := newTestIssuer(t, c)
	_, parent, err := is.Start("human:priya", "human", "agent:support", "")
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(9 * time.Minute) // parent has one minute left
	tok, child, err := is.Extend(parent, "agent:billing", "")
	if err != nil {
		t.Fatal(err)
	}
	if child.Parent != parent.ID || strings.Join(child.Hops, ",") != "agent:support,agent:billing" {
		t.Fatalf("child %+v", child)
	}
	if child.ExpiresAt != parent.ExpiresAt {
		t.Fatalf("child expires %d, parent %d: a child must not outlive its parent", child.ExpiresAt, parent.ExpiresAt)
	}
	if v, err := is.Verify(tok); err != nil || v.ExpiresAt != parent.ExpiresAt {
		t.Fatalf("re-signed child does not verify: %v", err)
	}
}

func TestVerifyFailures(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	is, _ := newTestIssuer(t, c)
	tok, _, _ := is.Start("human:priya", "human", "agent:support", "")
	parts := strings.Split(tok, ".")

	// Tamper with the payload: add a hop without re-signing.
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var cl Claims
	_ = json.Unmarshal(payload, &cl)
	cl.Hops = append(cl.Hops, "agent:billing")
	forged, _ := json.Marshal(cl)
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(forged) + "." + parts[2]

	// Sign with a key the issuer does not know.
	other, _ := NewKeyring("x", map[string]string{"x": key2})
	foreign, _, _ := NewIssuer(other, WithClock(c.now)).Start("human:priya", "human", "agent:support", "")

	cases := map[string]struct {
		tok  string
		want error
	}{
		"empty":        {"", ErrMalformed},
		"wrong prefix": {"gh2." + parts[1] + "." + parts[2], ErrMalformed},
		"two parts":    {parts[0] + "." + parts[1], ErrMalformed},
		"bad base64":   {parts[0] + ".!!!." + parts[2], ErrMalformed},
		"short mac":    {parts[0] + "." + parts[1] + ".AAAA", ErrMalformed},
		"tampered":     {tampered, ErrSignature},
		"unknown key":  {foreign, ErrUnknownKey},
		"too large":    {strings.Repeat("a", MaxEncodedLen+1), ErrTooLarge},
	}
	for name, tc := range cases {
		if _, err := is.Verify(tc.tok); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}

	c.t = c.t.Add(11 * time.Minute)
	if _, err := is.Verify(tok); !errors.Is(err, ErrExpired) {
		t.Errorf("expired: got %v", err)
	}
	c.t = time.Unix(1_800_000_000, 0).Add(-time.Hour)
	if _, err := is.Verify(tok); !errors.Is(err, ErrNotYet) {
		t.Errorf("not yet valid: got %v", err)
	}
}

func TestKeyRotation(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	is, kr := newTestIssuer(t, c)
	old, _, _ := is.Start("human:priya", "human", "agent:support", "")
	if err := kr.Load("k2", map[string]string{"k1": key1, "k2": "hex:" + strings.Repeat("ab", 32)}); err != nil {
		t.Fatal(err)
	}
	if _, err := is.Verify(old); err != nil {
		t.Fatalf("ticket signed by the previous key must still verify during rotation: %v", err)
	}
	_, fresh, _ := is.Start("human:priya", "human", "agent:support", "")
	if fresh.KID != "k2" {
		t.Fatalf("new tickets must use the active key, got %q", fresh.KID)
	}
	if err := kr.Load("k2", map[string]string{"k2": "hex:" + strings.Repeat("ab", 32)}); err != nil {
		t.Fatal(err)
	}
	if _, err := is.Verify(old); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("ticket from a retired key must fail, got %v", err)
	}
}

func TestVerifiedCache(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	is, kr := newTestIssuer(t, c)
	tok, _, _ := is.Start("human:priya", "human", "agent:support", "")
	first, err := is.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	first.Hops[0] = "agent:attacker" // callers must not be able to poison the cache
	again, err := is.Verify(tok)
	if err != nil || again.Hops[0] != "agent:support" {
		t.Fatalf("cached claims were mutated: %+v %v", again, err)
	}
	c.t = c.t.Add(11 * time.Minute)
	if _, err := is.Verify(tok); !errors.Is(err, ErrExpired) {
		t.Fatalf("a cached ticket must still expire: %v", err)
	}
	c.t = time.Unix(1_800_000_000, 0)
	if _, err := is.Verify(tok); err != nil {
		t.Fatal(err)
	}
	if err := kr.Load("k2", map[string]string{"k2": "hex:" + strings.Repeat("ab", 32)}); err != nil {
		t.Fatal(err)
	}
	if _, err := is.Verify(tok); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a cached ticket from a retired key must fail: %v", err)
	}

	small := NewIssuer(kr, WithClock(c.now), WithCache(2))
	for n := 0; n < 5; n++ {
		tk, _, _ := small.Start("human:priya", "human", "agent:x", "")
		if _, err := small.Verify(tk); err != nil {
			t.Fatal(err)
		}
	}
	if len(small.cache.data) > 2 {
		t.Fatalf("cache exceeded its bound: %d", len(small.cache.data))
	}
	if NewIssuer(kr, WithCache(0)).cache != nil {
		t.Fatal("WithCache(0) disables the cache")
	}
}

func TestKeyringValidation(t *testing.T) {
	bad := []struct {
		active string
		keys   map[string]string
	}{
		{"k", nil},
		{"k", map[string]string{"k": "short"}},
		{"missing", map[string]string{"k": key1}},
		{"k.1", map[string]string{"k.1": key1}},
		{"k", map[string]string{"k": "hex:zz"}},
	}
	for i, b := range bad {
		if _, err := NewKeyring(b.active, b.keys); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	if _, err := NewKeyring("k", map[string]string{"k": "base64:" + base64.StdEncoding.EncodeToString([]byte(key1))}); err != nil {
		t.Errorf("base64 key: %v", err)
	}
}

func TestDepthLimit(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	is, _ := newTestIssuer(t, c)
	_, cl, err := is.Start("human:priya", "human", "agent:a0", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < MaxHops; i++ {
		if _, cl, err = is.Extend(cl, "agent:a", ""); err != nil {
			t.Fatalf("hop %d: %v", i, err)
		}
	}
	if _, _, err := is.Extend(cl, "agent:a", ""); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("expected ErrTooDeep, got %v", err)
	}
}

func TestProblemCodes(t *testing.T) {
	want := map[error]string{
		nil:           "",
		ErrExpired:    "ticket_expired",
		ErrNotYet:     "ticket_not_yet_valid",
		ErrUnknownKey: "ticket_unknown_key",
		ErrSignature:  "ticket_bad_signature",
		ErrTooLarge:   "ticket_too_large",
		ErrTooDeep:    "ticket_too_large",
		ErrMalformed:  "ticket_malformed",
	}
	for err, code := range want {
		if got := Problem(err); got != code {
			t.Errorf("Problem(%v) = %q, want %q", err, got, code)
		}
	}
}

func FuzzVerify(f *testing.F) {
	kr, _ := NewKeyring("k1", map[string]string{"k1": key1})
	is := NewIssuer(kr, WithClock(func() time.Time { return time.Unix(1_800_000_000, 0) }))
	// Fuzz workers run this setup in separate processes, so the one ticket we issue must be
	// identical in all of them.
	is.idFn = func() string { return "fuzz-fixed-id" }
	tok, _, _ := is.Start("human:priya", "human", "agent:support", "")
	f.Add(tok)
	f.Add("gh1..")
	f.Add("gh1.e30.AAAA")
	f.Fuzz(func(t *testing.T, s string) {
		c, err := is.Verify(s)
		if err == nil && s != tok {
			// Any accepted ticket must have been produced with our key; the only one we made
			// is tok, so anything else verifying means the signature check is broken.
			t.Fatalf("accepted a ticket we did not issue: %q -> %+v", s, c)
		}
	})
}

func BenchmarkVerify(b *testing.B) {
	kr, _ := NewKeyring("k1", map[string]string{"k1": key1})
	is := NewIssuer(kr)
	_, parent, _ := is.Start("human:priya", "human", "agent:support", "")
	tok, _, _ := is.Extend(parent, "agent:billing", "")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := is.Verify(tok); err != nil {
			b.Fatal(err)
		}
	}
}
