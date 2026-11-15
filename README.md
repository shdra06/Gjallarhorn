# Gjallarhorn

**Badges, chain tracking, an agent lockdown button and a flight recorder for AI agents, built into the
[Bifrost](https://github.com/maximhq/bifrost) AI gateway.**

> In Norse myth, Heimdall guards the Bifröst bridge and blows the Gjallarhorn to warn everyone that
> trouble is coming. Gjallarhorn does the same for the agents crossing your gateway.

**Status:** under active development. See [docs/PLAN.md](docs/PLAN.md) for the full plan.

**Results so far.** Every claim links to the raw file it comes from (more in
[results.md](docs/results.md)).

| Claim | Raw result |
|---|---|
| In a real Bifrost with real agents and tools, **12 of 31 attacks cause real harm without Gjallarhorn, and 0 of 31 with it** | [`results/attacklab-20261115T055939Z.json`](results/attacklab-20261115T055939Z.json) |
| All **11 pieces of normal work** still get done, with **zero false blocks** | [`results/attacklab-20261115T055939Z.json`](results/attacklab-20261115T055939Z.json) |
| A lockdown refuses the agent's next call a **median of 1.8 ms** after the lock request. Across 40 lockdowns during 21,745 calls, **no call** that started after the lock got through | [`results/measure-20261115T055411Z/lockdown.json`](results/measure-20261115T055411Z/lockdown.json) |
| Gjallarhorn's decision takes a **median of 45–56 µs inside the running gateway** (on a laptop VM; about 5 µs in an isolated benchmark). End to end, its cost was below the noise between container starts | [`results/measure-20261115T055411Z/`](results/measure-20261115T055411Z/), [benchmarks](docs/benchmarks.md) |
| Co-pilot fire drill: with a rule removed, the AI co-pilot suggested 9 rules. Replaying real traffic proved 3 safe and **rejected 6 on its own** (5 would have blocked normal work, 1 was malformed). The proven rule stopped the attack and normal work kept running | [`results/attacklab-20261115T055939Z.json`](results/attacklab-20261115T055939Z.json) (`copilot_drill`) |
| If Gjallarhorn fails to start, Bifrost serves traffic without it. Our images **stop the container instead** | [`results/startup-failure-20261115T060217Z.txt`](results/startup-failure-20261115T060217Z.txt) |
| Engine test coverage **95.3%**, at least 90% in every package | [`results/coverage-20261115T053752Z.txt`](results/coverage-20261115T053752Z.txt) |
| No known vulnerabilities in our Go code (Go 1.27.4) or the console's npm packages | [security review](docs/security-review.md) |

What Gjallarhorn does **not** protect against is written down in the
[threat model](docs/threat-model.md).

## Try it

```bash
podman build -t gjallarhorn-bifrost -f deploy/Containerfile .
podman build -t gjallarhorn-demo -f deploy/demo/Containerfile .
bash deploy/demo/run-console.sh
```

Open <http://localhost:18788/> and sign in with the token the script prints. Turn on normal work,
then launch an attack and watch Gjallarhorn cut it off mid-air. Click the red ✕ to see the whole
chain, ask the AI co-pilot what happened, or lock the agent. See [docs/console.md](docs/console.md).

| Done | Next |
|---|---|
| Core engine: identity, chain tickets, policy, lockdown, grants, flight recorder, replay | Usability test of the console with 3–5 people who have never seen it ([script](docs/usability-test.md)) |
| Operator control API with a live event stream and an enforce/monitor switch | Open the upstream Bifrost issue ([draft](docs/upstream.md)) |
| Bifrost plugin, running in real Bifrost v2.5.1 | Submission package |
| Measurements, threat model and security review ([results](docs/results.md), [threat model](docs/threat-model.md), [review](docs/security-review.md)) | |
| Demo world (4 A2A agents, 5 MCP tools) and an attack lab ([how it works](docs/attack-lab.md)) | |
| AI co-pilot: incident explainer and a rule suggester proven by replay ([how it works](docs/copilot.md)) | |
| Operator console: live 3D airspace, table, incidents, lockdowns, policy editor with live replay, replay timeline ([how it works](docs/console.md)) | |

## The problem

Teams now run several AI agents that call models, use tools and ask each other for help. When one of
them is tricked, today's gateways can't answer basic questions:

- **Which agent did this?** With forwarded credentials, an agent's calls are logged as the human's.
- **Who asked whom?** There is no record of the chain from the human to the last agent.
- **Was a permission borrowed?** An agent that may not issue refunds can ask one that can. OWASP calls
  this a confused-deputy attack (Agentic Top 10, 2026, ASI03).
- **Can we cut one agent off right now** without locking out the people it works for?
- **What exactly happened, and would a new rule have stopped it?**

## What Gjallarhorn adds

| Feature | In one line |
|---|---|
| Badges | Every call is tied to a human, agent or service |
| Chain tickets | Gateway-signed record of who asked whom; agents can drop it but not forge it |
| Policy engine | Rules like "refunds only if a human started the chain" and "max 2 hops", with deny-wins and monitor mode |
| Lockdown | One action cuts an agent off from models, tools and other agents, on its next call |
| Flight recorder | Tamper-evident, hash-chained log of every decision |
| Replay | Re-run real traffic against a new rule before switching it on |
| AI co-pilot | Explains incidents and suggests rules, proven against the replay first |
| Console | A live 3D airspace of agents and calls, with a plain table view |

## Repository layout

| Path | What it is |
|---|---|
| `engine/` | The core, in plain Go with no Bifrost dependency: identity, tickets, policy, lockdown, recorder, replay, incidents, AI co-pilot, control API |
| `plugin/` | The Bifrost plugin: a thin adapter from Bifrost's hooks to the engine |
| `console/` | The operator console (React, Three.js, TypeScript), served by the control API |
| `demo/` | The demo company (A2A agents, MCP tools, effects ledger, mock model), the attack lab and the co-pilot fire drill |
| `deploy/` | Container builds: Bifrost with the plugin, the full demo, a dev image with integration tests, the Phase 1 spike |
| `results/` | Raw reports behind every number we publish: attack lab, measurements, coverage, start-up failure |
| `docs/` | Plan, architecture, console, co-pilot, attack lab, results, benchmarks, threat model, security review, upstream proposals, usability test, spike results |

## Developing

The engine runs and tests on any OS:

```bash
cd engine
go test ./...
```

Race detector, benchmarks and fuzzing:

```bash
cd engine
go test -race ./...
go test -run XXX -bench . ./...
go test ./ticket -run XXX -fuzz FuzzVerify -fuzztime 60s
```

The plugin needs Linux, because Go plugins don't load on Windows. Build Bifrost and the plugin in a
container, then run the integration tests against the real gateway (use `docker` in place of
`podman` if you prefer):

```bash
podman build -t gjallarhorn-bifrost -f deploy/Containerfile .
podman build -t gjallarhorn-dev -f deploy/dev/Containerfile .
bash deploy/dev/test.sh
```

The console needs Node 22.12 or later:

```bash
cd console
npm ci
npm test
npm run dev        # proxies the API of a running demo (bash deploy/demo/run-console.sh)
```

A clean build takes about 10–15 minutes, because Bifrost and the plugin are compiled together so
their dependency versions match exactly. On Linux and macOS, the `Makefile` wraps all of these
(`make test`, `make dev-image`, `make dev-test`).

## License

Apache License 2.0, the same as Bifrost.
