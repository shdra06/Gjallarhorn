# Architecture

This document describes how Gjallarhorn works today. The phase plan is in [PLAN.md](PLAN.md).

## Components

```
                       +-------------------------- Bifrost ---------------------------+
  caller (human/app) ->| HTTP transport hook  ->  PreA2A / PreMCP / PreLLM  -> upstream |
                       |        |                       |                             |
                       |   Gjallarhorn plugin (thin adapter, package main, .so)       |
                       +--------|-----------------------|-----------------------------+
                                v                       v
                     engine.Delegate(req)        engine.Check(req)
                                \                      /
                                 v                    v
   identity ──> ticket ──> policy.Evaluate <── lockdown, grants
                                 │
                                 v
                    recorder (hash-chained log) ──> replay / control API / console
```

The **engine** (`engine/`) is plain Go with no Bifrost import. The **plugin** maps Bifrost's hook
arguments to an `engine.Request` and turns an engine decision into a Bifrost short-circuit error.
Keeping the engine separate means every rule is tested quickly on any OS. Only the adapter needs
Linux and Bifrost's exact dependency versions.

## Request flow

### 1. An app or person asks an agent to do something (A2A)

The `HTTPTransportPreHook` on `/agents/a2a/{name}/...` calls `engine.Delegate`:

1. **Resolve.** The virtual key maps to a principal (for example `service:customer-chat`). If the
   request carries a chain ticket, the ticket is verified and bound to the key (see below).
2. **Evaluate** the call on the `a2a` plane, with the target agent resolved to its principal ID.
3. **If allowed**, mint a new ticket: `Start` for a fresh chain, or `Extend` when the caller already
   holds one. The plugin puts it in the `x-gjallarhorn-chain` header. Bifrost forwards that header to
   the agent, because only auth headers are stripped.
4. **If denied**, the plugin returns a 403 before the agent is ever contacted.

### 2. The agent calls a model or a tool, or asks another agent

The agent sends the ticket back on its own calls, the same way Bifrost already asks agents to send
`x-bf-agent-correlation-id`. `PreLLMHook`, `PreMCPHook` and the A2A hook call `engine.Check` or
`engine.Delegate`. A denied call short-circuits with `AllowFallbacks=false`, so Bifrost doesn't
retry it on another provider.

### 3. Afterwards

The post hooks call `engine.RecordOutcome` with the status, latency and tokens.

## Identity and tickets

| Situation | Credential presented | Ticket | Who the engine says is acting |
|---|---|---|---|
| Direct call | anyone's key | none | the key's owner |
| Agent uses its own key inside a chain | the agent's key | the agent's ticket | the ticket holder; key and holder must match |
| Agent uses a forwarded key ("Forward Bifrost authentication") | the key of whoever called it (the human origin or an earlier agent) | the agent's ticket | the ticket holder; the key must belong to someone in the chain |
| Ticket lifted by another agent | someone else's key | stolen ticket | denied: `ticket_mismatch` |
| Forged or edited ticket | any | bad signature | denied: `ticket_bad_signature` |

Tickets are `gh1.<payload>.<mac>`, where the MAC is HMAC-SHA256 over the payload. The payload holds
the root principal, the list of agent hops, an optional task binding, issue and expiry times, the
parent ticket ID and the signing key ID.

- Children can never outlive their parent.
- Chains are capped at 32 hops.
- Decoding is strict, so each ticket has exactly one valid spelling.
- Keys rotate without breaking tickets in flight: the active key signs, and every listed key verifies.

## Policy evaluation order

`policy.Evaluate` decides in this fixed order. Deny always wins over allow.

1. **Ticket problems**: a forged, expired or mismatched ticket is denied.
2. **Lockdown**: any locked principal in the chain, or a locked target agent, is denied.
3. **Unknown callers**: denied only if `guards.unknown_callers` is `deny`.
4. **Loops**: a chain that visits the same agent twice is denied.
5. **Depth**: a chain longer than `guards.max_depth` (default 4) is denied.
6. **Grants**: a time-boxed, audited exception allows the call.
7. **Rules**:
   - Any enforced deny rule that applies denies the call.
   - Otherwise an allow rule allows it.
   - Monitor-mode rules never block, but they are reported.
8. **Default effect** (allow, unless configured otherwise).

Rules match on plane, target, actor, origin (by ID glob, kind or labels), principals in between
(`via`), whether the call is delegated, and depth. An `unless` block carves out exceptions. A deny
rule can set `lockdown: true` to lock the actor automatically when it fires.

Example: block refunds unless a human operator started the chain.

```json
{
  "id": "refunds-need-an-operator",
  "effect": "deny",
  "description": "Refunds only when a human operator started the chain",
  "match":  { "planes": ["mcp"], "targets": ["billing.refund*"] },
  "unless": { "origin_kinds": ["human"], "origin_labels": { "role": "operator" } }
}
```

## Lockdown

Lockdown is an in-memory map that is checked on every principal in the chain, so it takes effect on
the very next call. Locking an agent blocks four things:
- its own calls, including calls made with a forwarded human key (identified by its ticket);
- calls by other agents working for a chain that includes it;
- anyone calling it;
- nothing else: the humans it worked for keep working.

Lockdowns persist to a JSON file and can have an end time. Replay can apply a lockdown "as of" a past
moment.

## Flight recorder

The recorder writes JSON Lines, one event per decision, outcome or operator action. Each event stores:
- `prev`: the previous event's hash;
- `hash`: SHA-256 of its own content, including `prev`.

`recorder.Verify` reports the first broken line, and `Open` refuses to extend a tampered log.

- **Never blocks the caller, never loses an event.** `Append` hashes and enqueues; a background
  writer flushes. It blocks rather than drop an event.
- **Privacy:** facts and decisions are stored, but prompts and tool results are not. Raw keys and
  tickets are never written; only a short key fingerprint and the ticket ID are.

## Replay

`replay.Run` re-evaluates recorded facts under a candidate policy and reports:
- calls newly blocked and newly allowed;
- denials by rule;
- monitor hits;
- per-label statistics for traffic tagged `attack` or `benign`.

`Report.Safe()` passes only if the candidate blocks every attack the old policy blocked and no more
benign calls than before. The AI rule suggester will be held to this check.

## Measured performance

See [benchmarks.md](benchmarks.md):
- Policy evaluation: about 0.4 µs.
- Lockdown check: about 30 ns.
- Chain-ticket check: about 0.15 µs (cached).
- A full decision for a delegated call, as a request sees it: about 5 µs. The flight recorder seals
  events in the background.
