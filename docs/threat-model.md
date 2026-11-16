# Threat model

What Gjallarhorn protects, who it is up against, what it guarantees, and where it stops. The
limits are as important as the guarantees: an operator who trusts a control for something it
doesn't do is worse off than one who knows the gap.

## What is worth protecting

| Asset | Example in the demo | Why it matters |
|---|---|---|
| Tools that move money | `billing.refund` | One borrowed call pays an attacker |
| Tools that send data out | `email.send` | Customer data leaves the company |
| Tools that run code | `shell.exec` | Full compromise of the host |
| The model budget | premium models | Cost abuse |
| The chain of authority | "Priya started this" | Every other decision depends on it |
| Gjallarhorn's own controls | policy, mode, lockdowns | An attacker who changes them changes everything |
| The flight recorder | every decision | It is the evidence after an incident |

## Who Gjallarhorn is up against

1. **Instructions hidden in content.** These come in customer messages, web pages, documents and
   tool results: indirect prompt injection. This is the main threat, and agents follow such
   instructions.
2. **A compromised or careless agent**, including third-party agents. It can leak, replay or
   forge chain tickets, drop its ticket, call itself in loops, or call tools it has no reason to
   call.
3. **A compromised app** that starts chains, such as the customer chat.
4. **Someone holding a gateway key but not the admin token.** That includes a stolen agent key,
   or an agent trying to undo its own lockdown.

**Out of scope:** an operator holding the admin token (they are trusted to run Gjallarhorn), and
anyone who controls the gateway host itself.

## What Gjallarhorn assumes

- **Bifrost is configured with virtual keys.** It rejects calls without a valid key, and it
  decides what each key may reach (agents, tools, models). Gjallarhorn adds the chain on top; it
  does not replace those permissions.
- **The ticket signing keys are secret.** They are at least 32 bytes and given through the
  environment (`env:VAR`). They can be rotated with key IDs.
- **The admin token is secret** and at least 24 characters. The control API listens on localhost
  by default. Put it behind TLS if it is reached over a network.
- **Clocks are roughly in sync.** Tickets expire (10 minutes by default), and a small skew is
  allowed.
- **Agents send the chain ticket back on their own calls.** An agent that doesn't is judged as
  itself, never as whoever it was helping (see "Dropped tickets" below).

## What it guarantees, and the evidence

| Threat | What stops it | Evidence |
|---|---|---|
| A permission borrowed through another agent (confused deputy) | The chain ticket keeps the real origin at every hop. Rules can require, for example, that a human operator started the chain | Attacks `borrowed-refund`, `refund-laundered-via-research`, `poisoned-coupon-page`; engine tests |
| A powerful forwarded key used by an agent | The actor is the agent holding the ticket, not the key's owner. Rules can say which agents may use a tool | `refund-with-operator-key` |
| A dropped ticket | Without a ticket, an agent is judged as itself and can't claim anyone else's authority | `dropped-ticket` |
| Stolen, edited, home-made or unsigned tickets | HMAC-SHA256 signature, expiry, and binding to the keys of chain members. A ticket used with a key outside its chain is refused | `stolen-ticket`, `stolen-ticket-email`, `forged-ticket`, `made-up-ticket`, `unsigned-ticket`, `forged-chain-at-entry`; fuzz test of the ticket parser |
| Loops and runaway chains | The loop guard and the depth guard | `delegation-loop`, `self-delegation-loop`, `depth-bomb` |
| A misbehaving agent or app | Lockdown, effective on the next call. It also covers chains the agent appears in, and chains a locked app started. Rules can lock automatically | `locked-agent`, `locked-link-in-chain`, `locked-origin`, `multi-step-injection`; [lockdown measurement](results.md#lockdown-speed) |
| Changes to Gjallarhorn itself without the admin token | Every control API route needs the token, compared in constant time | `locked-agent-unlocks-itself`, `policy-wipe`, `switch-off-enforcement`, `flight-recorder-snooping`; test `TestEveryEndpointNeedsTheToken` |
| A rule that breaks normal work | Replay before saving, and the co-pilot's proof. Apply proves the rule again and refuses a policy that changed in the meantime | The co-pilot fire drill; [copilot.md](copilot.md) |
| Edited or missing history | Hash-chained log, verified on start and on every replay | Recorder tamper tests |
| Secrets in logs | Raw keys and tickets are never recorded, only a short fingerprint and the ticket ID | `TestRawKeysAreNeverRecorded` |

The numbers are in [results.md](results.md): in the real stack, 0 of 31 attacks did harm with
Gjallarhorn (12 did without it), and no normal work was blocked.

## Limits

### 1. Gjallarhorn does not read contents

It decides on who is calling, through which chain, and which tool or model is called. It never
reads prompts or tool arguments. So it can't tell:
- an email to a colleague from an email to an attacker, when the same agent sends both in the
  same kind of chain;
- a ₹500 refund from a ₹50,000 one;
- a normal URL from one that reaches an internal metadata service (SSRF).

**What to do:**
- Validate arguments in the tools themselves (recipient allow-lists, refund limits, URL
  filters).
- Use Bifrost's guardrails for contents.
- Keep powerful tools behind narrow chain rules.

### 2. A forwarded human key without a ticket looks like the human

Bifrost can forward a caller's key to an agent. If an agent holds a human's forwarded key and
calls without its ticket, Gjallarhorn sees the human: there is nothing in the request that says
an agent made it. Dropping the ticket does help an agent that has its own key, which is judged
as itself (row "A dropped ticket" above), but not one holding a forwarded human key.

**What to do:**
- Give agents their own keys, and forward keys only where an agent truly needs the caller's
  permissions.
- An upstream change would close this for good: Bifrost recording which agent a forwarded
  key was handed to.

### 3. The policy is only as good as its rules

The default effect is allow. A missing rule is a gap. The co-pilot fire drill removes one rule
and shows exactly that: the attack gets through until a proven rule closes the gap. Monitor-only
mode records everything but blocks nothing.

### 4. Replays judge only what was recorded

- A replay, a simulation or a co-pilot proof can only judge traffic Gjallarhorn has seen, and it
  reads only the most recent events (`recorder.replay_limit`, default 200,000). A rule for a kind
  of call that never happened will look safe.
- "Normal work" needs labels. In production there are no labels unless `accept_labels` is on,
  and it must not be (see 9). Without labels, any other call a candidate rule would block counts
  as collateral. Such a rule goes to **review**, and it is proven only if it blocks nothing else
  at all.

### 5. One gateway instance

Lockdowns, grants, the ticket cache and the mode live in one process; the lockdown file belongs
to that instance. Several Bifrost instances behind a load balancer would each need the lockdown
applied. Shared state across instances is not built yet.

### 6. The log can be rebuilt by someone who can write it

The hash chain detects an edited, missing or reordered event. But anyone who can write the log
file can recompute the whole chain. Nothing anchors it outside the host yet.

**What to do:** copy `last_hash` from `/v1/stats` to somewhere the gateway host can't write,
such as a ticketing system or an append-only store, every few minutes.

### 7. One shared admin token

- There are no per-operator accounts or roles. `X-Operator` names the operator in the audit
  trail, but whoever holds the token can set it to anything.
- There is no rate limit on the control API. A token of 24 or more random characters, compared
  in constant time, makes guessing impractical. Use a long random token, keep the API on
  localhost or a private network, and rotate the token if it leaks.

### 8. If Gjallarhorn isn't running, nothing is enforced

Gjallarhorn is a plugin. Bifrost keeps serving traffic when a plugin fails to load: tested, see
[security-review.md](security-review.md#plugin-start-up-failure). Bifrost's own permissions
still apply then, but chain rules and lockdowns don't.

- **Our images:** they run Bifrost under `deploy/guard.sh`, which stops the container if
  Gjallarhorn doesn't come up or stops answering. A broken setup therefore fails closed.
- **Elsewhere:** watch the control API's `/v1/health`.
- **Upstream:** a `required` plugin flag is proposed in [upstream.md](upstream.md).

### 9. Settings that are for demos and tests only

| Setting | Why not in production |
|---|---|
| `accept_labels: true` | Any caller can label its own traffic as attack or benign. Labels feed replay verdicts and co-pilot proofs. The plugin warns when this is on |
| `control.demo_driver` | Lets token holders run scripted traffic |
| `control.public_demo: true` | Hands the admin token to anyone who can open the console (`GET /v1/demo-access`), so the landing page can start the demo in one click. The plugin warns when this is on |
| `guards.accept_bad_tickets: true` | Lets calls with forged, expired or mismatched tickets through (they are still recorded) |
| Demo keys and tokens | They are in this repository |

### 10. What blocked callers learn

By default, a blocked caller is told which rule stopped it and why. That helps agent developers,
and it also helps an attacker map the policy. Set `quiet_denials: true` to give only the kind
of denial and the flight-recorder reference.

### 11. The co-pilot's model sees metadata

The co-pilot sends recorded facts (principal names, targets, decisions) to the model configured
in `copilot`. It never sends prompts or keys. Choose that model and provider with this in mind;
in production it should go through Bifrost with its own virtual key. The model can be
prompt-injected through names in the facts. Its output is only a suggestion: strictly parsed,
replayed, and applied only by an operator.

### 12. A chain member can reuse its own ticket

A ticket is bound to the members of its chain, so nobody else can use it. A member can reuse it
until it expires (10 minutes by default). Shorter TTLs narrow this.

## Recommended production settings

```json
{
  "accept_labels": false,
  "quiet_denials": true,
  "control": { "listen": "127.0.0.1:8787", "admin_token": "env:GJ_ADMIN_TOKEN" },
  "tickets": { "active_key": "k2026", "keys": { "k2026": "env:GJ_TICKET_KEY" }, "ttl": "5m" },
  "policy": { "version": 1, "guards": { "max_depth": 3 } }
}
```

Also:
- Leave `guards.unknown_callers` at allow. Bifrost already refuses calls without a valid key,
  so the only unknown callers that reach Gjallarhorn are Bifrost's own MCP housekeeping (tool
  discovery every 10 minutes), and denying them would break tool discovery.
- Start in monitor-only mode, read the flight recorder for a while, then switch to enforcing.
- Keep the control API off the public network.
