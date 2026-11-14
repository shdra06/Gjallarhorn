# Gjallarhorn

**Badges, chain tracking, an agent lockdown button and a flight recorder for AI agents, built into the
[Bifrost](https://github.com/maximhq/bifrost) AI gateway.**

> In Norse myth, Heimdall guards the Bifröst bridge and blows the Gjallarhorn to warn everyone that
> trouble is coming. Gjallarhorn does the same for the agents crossing your gateway.

**Status:** under active development. See [docs/PLAN.md](docs/PLAN.md) for the full plan.

**Result so far** ([details](docs/results.md)): in a real Bifrost with real agents and tools,
**4 of 11 attacks cause real harm without Gjallarhorn, and 0 of 11 with it**. All 7 pieces of normal
work still get done, with **zero false blocks**.

| Done | Next |
|---|---|
| Core engine: identity, chain tickets, policy, lockdown, grants, flight recorder, replay | AI co-pilot: incident explainer and rule suggester |
| Operator control API with a live event stream and an enforce/monitor switch | 3D airspace console |
| Bifrost plugin, running in real Bifrost v2.5.1 | Published measurements, threat model, upstream PR |
| Demo world (4 A2A agents, 5 MCP tools) and an attack lab ([how it works](docs/attack-lab.md)) | Submission package |

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
| `engine/` | The core, in plain Go with no Bifrost dependency: identity, tickets, policy, lockdown, recorder, replay, control API |
| `plugin/` | The Bifrost plugin: a thin adapter from Bifrost's hooks to the engine |
| `demo/` | The demo company (A2A agents, MCP tools, effects ledger) and the attack lab |
| `deploy/` | Container builds: Bifrost with the plugin, the full demo, a dev image with integration tests, the Phase 1 spike |
| `results/` | Raw attack-lab reports behind every number we publish |
| `docs/` | Plan, architecture, benchmarks, spike results |

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

A clean build takes about 10–15 minutes, because Bifrost and the plugin are compiled together so
their dependency versions match exactly. On Linux and macOS, the `Makefile` wraps all of these
(`make test`, `make dev-image`, `make dev-test`).

## License

Apache License 2.0, the same as Bifrost.
