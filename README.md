# Gjallarhorn

**Badges, chain tracking, an agent lockdown button and a flight recorder for AI agents, built into the
[Bifrost](https://github.com/maximhq/bifrost) AI gateway.**

> In Norse myth, Heimdall guards the Bifröst bridge and blows the Gjallarhorn to warn everyone that
> trouble is coming. Gjallarhorn does the same for the agents crossing your gateway.

**Status:** under active development. See [docs/PLAN.md](docs/PLAN.md) for the full plan.

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
| `engine/` | The core, in plain Go with no Bifrost dependency: identity, tickets, policy, lockdown, recorder, replay |
| `docs/` | Plan, architecture, decisions |

More components (the Bifrost plugin, demo world, console) land as the phases in the plan are completed.

## Developing

```bash
cd engine
go test ./...
```

## License

Apache License 2.0, the same as Bifrost.
