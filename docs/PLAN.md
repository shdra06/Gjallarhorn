# Gjallarhorn: master plan

> Badges, chain tracking, an agent lockdown button and a flight recorder for AI agents, built into the
> [Bifrost](https://github.com/maximhq/bifrost) AI gateway.

This document collects all the research behind the project and turns it into a phase-by-phase build
plan. We build for quality, not speed. Each phase has a clear "done when" test, and we don't start a
phase until the one before it passes.

---

## 1. The problem, in one paragraph

Companies now run teams of AI agents: one answers customers, one handles billing, one does research.
These agents call AI models, use tools (through MCP) and ask each other for help (through A2A). Bifrost
sits in the middle of all of that traffic, but it can't answer the questions that matter when an agent
misbehaves:

- **Who did this?** Bifrost's own docs tell agents to reuse the caller's key ("Forward Bifrost
  authentication"), "preserving the original caller's access rules and usage attribution". So an
  agent's actions are logged as if a human did them.
- **Who asked whom?** The code has no field for the original caller, the hop count or the delegating
  agent.
- **Can an agent borrow another agent's powers?** Yes. A support agent that may not issue refunds can
  ask the billing agent, which can. This is the "confused deputy" attack, listed in OWASP's
  *Top 10 for Agentic Applications (2026)* as ASI03, Identity and Privilege Abuse.
- **Can we stop one agent right now, without locking out the people it works for?** Not today.
- **What exactly happened, and would a new rule have stopped it?** There is no replay.

## 2. The evidence that this is a real gap

| Evidence | Source |
|---|---|
| Forwarded credentials make agent calls look like the human's | Bifrost docs, `docs/features/agent-gateway/authentication.mdx`; code `core/agent/manager.go` |
| No original-caller, hop-count or delegating-agent field anywhere in the request context | Bifrost source (dev @ d71d844, 14 Nov 2026) |
| Cross-agent confused deputy and delegated privilege abuse are named attack scenarios | OWASP Top 10 for Agentic Applications 2026, ASI03 |
| Building blocks for agent-to-agent on-behalf-of authority "had simply not been composed" | arXiv 2609.00267, *Delegation Without Trust* |
| Real-world confused-deputy abuse of an AI support agent (about 20,000 accounts) | Stack Overflow blog, June 2026 |
| Enterprise vendors launched agent "kill switch" products in Oct–Nov 2026 | Okta, Aembit, Tigera, CrowdStrike |
| None of about 200 public hackathon entries builds this, and neither OSS nor Enterprise Bifrost has chain tracking, per-agent lockdown or replay | Our repo census and Bifrost Enterprise changelogs |

**Honest limits we design around:**
1. Bifrost OSS can already disable a virtual key by hand. Our lockdown adds three things: it is per
   agent even when the agent uses a forwarded human key, it can trigger automatically from a rule, and
   it doesn't lock out the human.
2. The A2A agent gateway is about a month old. Everything except chain tracking must also be useful for
   a single agent using the MCP gateway.
3. Chain tracking needs agents to send back one header. This is the same kind of cooperation Bifrost
   already asks for with `x-bf-agent-correlation-id`.

## 3. Hackathon fit (PS04: "Build a feature the open-source gateway is missing")

| Mandatory requirement | How we meet it |
|---|---|
| Real operator problem, solved end to end | Detect → block → alert → AI explanation → replay → AI-suggested rule → re-test → enforce |
| Public extension points | A Go plugin on Bifrost's official hooks (LLM, MCP, A2A, HTTP transport). No fork of Bifrost core |
| Works with a standard Bifrost install, clear setup | Dynamically linked build per Bifrost's own docs (organizers confirmed on 14 Nov 2026 that this counts). Ready image plus a one-command setup. Lockdown also works on a stock install through the governance API |
| Tests, docs, a repo ready for an upstream PR | Unit tests, integration tests against real Bifrost, fuzz tests, benchmarks, full docs |

Other rules we must follow (see `../notes/rules_check.md` in the research folder):
- Disclose the AI tools we used.
- Join the official Discord.
- Only use code and assets we have the right to use.
- The attack lab attacks only our own demo agents.
- Include "Use of AI" features: the incident explainer and the rule suggester.

## 4. What we are building

| # | Component | What it does |
|---|---|---|
| 1 | **Badges** (identity) | Every call is tied to a principal: a human, agent or service. The principal comes from the virtual key, or from a signed chain ticket when the key was forwarded |
| 2 | **Chain tickets** | When one agent calls another through Bifrost, the gateway signs a ticket listing the whole chain (origin → hop → hop). Agents pass it back on their own calls. They can drop it, but they can't forge it |
| 3 | **Policy engine** | Context-aware rules beyond yes/no, e.g. "refunds only if a human started the chain", "max 2 hops", "no loops", "this tool only for this task, for 30 minutes". Deny wins. Rules can run in *monitor* mode before *enforce* |
| 4 | **Lockdown** (the red button) | Instantly blocks one agent on all three planes, including any chain it appears in, without touching the human's key. Manual, or automatic when a rule fires |
| 5 | **Flight recorder** | Every decision is written to a tamper-evident, hash-chained log, with privacy-aware capture levels |
| 6 | **Replay and simulator** | Re-runs recorded traffic against a new policy and shows what would have changed. A fire drill using the real fire |
| 7 | **AI co-pilot** (through Bifrost) | Explains an incident in plain words, and suggests a rule. The suggestion is checked against the replay before it can be switched on |
| 8 | **Control API** | Lockdowns, policy, grants, events, live stream (SSE) and simulation, protected by an admin token |
| 9 | **Console** | A live 3D "airspace": agents are aircraft, models, tools and agents are airports, every call is a flight, suspicious routes glow red. A clean table mode and a replay timeline show the same data |
| 10 | **Demo world and attack lab** | Real agents (support, billing, research) talking through Bifrost, real MCP tools, and an open attack suite (injection, borrowed permissions, loops, data exfiltration) |
| 11 | **Upstream contribution** | A small PR so `.so` plugins can receive A2A observation events (issues #9710/#9711) |

## 5. Architecture (short version)

```
             +---------------------------- Bifrost (dynamic build) ----------------------------+
 caller ---> | HTTP transport hook --> PreA2A / PreMCP / PreLLM hooks --> provider/tool/agent  |
             |        |                         |                                              |
             |        +------- Gjallarhorn plugin (thin adapter, package main) ----------------+
             |                         |                                                       |
             +-------------------------|-------------------------------------------------------+
                                       v
                      engine (pure Go, stdlib only, fully unit tested)
            identity · tickets · policy · lockdown · recorder · replay · control API
                                       |
                    control API + SSE  v
                 console (3D airspace + table + timeline)   ·   AI co-pilot (calls back through Bifrost)
```

Key design decisions:
- **The engine is plain Go with no Bifrost import.** It is tested on any OS, including Windows. The
  plugin is a thin adapter, and only it needs Linux and Bifrost's exact versions.
- **Fail closed where it matters.** Tools marked sensitive need a valid chain ticket. Every other call
  keeps working if the engine is unsure, so we never take down the gateway.
- **Speed is a feature.** Bifrost advertises about 11 µs of overhead. We benchmark our hook path and
  publish the number.
- **Privacy by default.** The recorder stores metadata, not prompts, unless capture is turned up.

Full details: `docs/ARCHITECTURE.md`.

## 6. Phases

### Phase 0: Foundations
- Repo, license (Apache-2.0, same as Bifrost), plan, architecture doc, coding standards, CI.
- **Done when:** the repo builds and tests run with one command, and CI is green.

### Phase 1: Feasibility spike (the go/no-go)
- Build Bifrost `transports/v2.5.1` as a dynamically linked binary in a container.
- Build a tiny "hello" plugin in the *same* builder (same Go and module versions, both `-trimpath`).
- Prove four things:
  1. the plugin loads;
  2. it can block an LLM call with a custom error (no fallback retry);
  3. it can see the virtual key and headers;
  4. a header it adds in the HTTP transport hook on `/agents/a2a/...` reaches the agent.
- **Done when:** all four are shown by an automated script, and the result is written in
  `docs/spike-results.md`.

### Phase 2: Core engine
- Principals and identity resolution.
- Chain tickets: HMAC-SHA256, key IDs for rotation, expiry, depth limit, task binding.
- Policy language and validator.
- Decision engine with deny-overrides, monitor mode, loop detection and max hops.
- Lockdown registry.
- **Done when:** more than 90% test coverage on the engine; fuzz tests for ticket parsing; table tests
  for every rule type; benchmarks recorded.

### Phase 3: Flight recorder and replay
- Append-only JSONL with a SHA-256 hash chain, verification, rotation and a ring buffer.
- Live subscribers.
- Replay: re-evaluate events under a new policy and produce a diff.
- **Done when:** tampering with any event is detected; replay is deterministic; the diff is correct on
  golden test files.

### Phase 4: Control API
- Lockdowns, policy (validate and apply), grants, events, SSE stream, simulate.
- Admin-token auth (constant-time compare), localhost bind by default, CORS for the console.
- **Done when:** a full HTTP test suite passes, and lockdown takes effect on the next call (measured).

### Phase 5: Bifrost plugin adapter
- Map Bifrost hooks to engine decisions:
  - `HTTPTransportPreHook` mints chain tickets on A2A routes.
  - `PreLLMHook`, `PreMCPHook` and `PreA2AHook` enforce.
  - The post hooks record outcomes.
- Use short-circuit errors with `AllowFallbacks=false`.
- Start the control API on its own listener from `Init`.
- Fallback: lockdown also disables the agent's key through the governance API.
- **Done when:** integration tests run against real Bifrost in containers, covering all three planes.

### Phase 6: Demo world and attack lab
- Three real agents (support, billing, research) registered in Bifrost's Agent Gateway.
- Real MCP tools: order lookup, refund, email, web fetch.
- An attack suite of at least 30 scenarios plus a benign traffic set for false-positive testing.
- **Done when:** `make demo` brings everything up with one command, and the attacks run automatically.

### Phase 7: AI co-pilot
- **Incident explainer:** LLM through Bifrost, grounded only on recorded events.
- **Rule suggester:** structured output, validated by the policy validator, then proven against the
  replay before it can be applied.
- **Done when:** suggestions that would block benign traffic are rejected automatically.

### Phase 8: Console
- React + Three.js airspace, a table mode, a replay timeline, the lockdown button, a policy editor with
  live simulation.
- Accessible: keyboard navigation and reduced motion.
- **Done when:**
  - It works with real data from the demo world.
  - It is tested with 3–5 people who have never seen it.
  - Anyone can reach the wow moment in the first 30 seconds.

### Phase 9: Proof, hardening, upstream
- Measure and publish, with raw logs committed:
  - attacks blocked (with and without Gjallarhorn);
  - false blocks on benign traffic;
  - time from lockdown to the first blocked call;
  - per-request overhead (µs).
- A threat model with honest limits, and a security review of our own code.
- Open the upstream PR for `.so` A2A observation events.
- **Done when:** every claim in the README links to a raw result file.

### Phase 10: Submission package
- README (problem, setup, features, architecture, AI usage, AI-tools disclosure, results, limits).
- Pitch deck.
- Demo video under 3 minutes, with a human voice and the wow in the first 30 seconds.
- An always-on live demo.
- The organizer confirmation about the dynamic build, noted in setup.
- Make the demo start without internet access. Bifrost downloads pricing data at start-up and
  exits if that times out; it happened once in Phase 9.
- **Done when:** a stranger can clone the repo, run one command and see the demo.

## 7. The demo story (target: 3 minutes)

1. Three agents are working normally in the 3D airspace.
2. A customer email hides an instruction. The support agent asks the billing agent to refund ₹50,000.
3. Gjallarhorn sees the chain (customer email → support → billing → refund tool), the route turns red,
   and the refund is blocked before it reaches the tool.
4. The red button locks the support agent across models, tools and agents in under a second. The human
   operator is unaffected.
5. Rewind. The AI co-pilot explains what happened and suggests a rule. Replay proves the rule blocks
   the attack and doesn't block normal traffic.
6. The numbers are shown at the end.

## 8. Engineering standards
- Go: `gofmt`, `go vet`, `staticcheck`, table-driven tests, `-race` in CI, no panics on the request path.
- Every public behaviour has a test. Every claim in docs has a result file.
- Small commits with clear messages. No secrets in the repo.
- Privacy: no prompt content stored by default.

## 9. Progress (15 Nov 2026)

| Phase | State |
|---|---|
| 0 Foundations | Done: repo, license, plan, CI workflow, Makefile |
| 1 Feasibility spike | Done: all four questions answered. A2A header forwarding was proven by the Phase 6 demo world |
| 2 Core engine | Done. Coverage is 95.1% overall and at least 90% in every package (lowest: lockdown 91.9%); CI fails below 90% |
| 3 Recorder and replay | Done, with background sealing and tamper detection |
| 4 Control API | Done, with an SSE stream |
| 5 Plugin adapter | Done for all three planes, verified against real Bifrost: LLM (16/16 dev checks), MCP and A2A (attack lab) |
| 6 Demo world and attack lab | Done: 4 real A2A agents, 5 MCP tool servers, an effects ledger, 31 attacks and 11 pieces of normal work. With Gjallarhorn: 0/31 attacks cause harm (12/31 without), 11/11 normal work done, 0 false blocks. See [results.md](results.md) |
| 7 AI co-pilot | Done: incident rebuilt from the flight recorder; grounded explainer with an optional model narrative through Bifrost; rule suggester whose rules are validated, replayed and ranked proven/review/rejected; apply re-proves and needs the policy ETag. Fire drill in real Bifrost: gap closed by a proven rule, 6/9 unsafe or malformed suggestions rejected automatically, 7/7 normal work. See [copilot.md](copilot.md) |
| 8 Console | Built and verified against the real demo stack: live 3D airspace (flights, interceptions, lockdown shields, air corridors), table, incident panel with the co-pilot (explain, suggest, apply), node panel with lock and unlock, policy editor with live replay and If-Match saves, replay timeline, demo traffic panel, first-run tour. Keyboard, screen-reader and reduced-motion support. 22 console tests. **Still to do:** the test with 3–5 people who have never seen it ([script](usability-test.md)). See [console.md](console.md) |
| 9 Proof, hardening, upstream | Done except posting upstream: 31-attack lab, lockdown speed, in-gateway decision cost and the plugin-off/on comparison measured with raw files ([results.md](results.md)); [threat model](threat-model.md) and [security review](security-review.md), with fixes (lockdowns no longer stall the gateway, Go 1.27.4, fail-closed guard, quiet denials, bounded replays). Every README claim links to a raw result. **Still to do:** post the upstream Bifrost issue ([draft](upstream.md)), with the owner's approval |
| 10 | Not started |

## 10. Open items
- [x] Set the Go module path to the real repository: `github.com/shdra06/Gjallarhorn`.
- [ ] Confirm the deadline in an official announcement (pages still say 15 Nov 2026, 11:59 PM).
- [ ] All team members join the Geek Room Discord (required by the rules).
