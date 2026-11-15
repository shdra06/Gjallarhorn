# Upstream proposals for Bifrost

Building Gjallarhorn on Bifrost's public plugin interface turned up two gaps worth fixing in
Bifrost itself. Both are written up below as issue drafts. Nothing has been posted yet. They
will be opened from the project owner's GitHub account, issue first, so the maintainers can
choose the design before a PR.

Related open work by others, which these follow in spirit:
- [maximhq/bifrost#9710](https://github.com/maximhq/bifrost/issues/9710) and
  [maximhq/bifrost#9711](https://github.com/maximhq/bifrost/pull/9711) (by 01infinity) add the
  Relay hooks to `.so` plugins. They show the same pattern: a hook surface that built-in
  plugins have but shared-object plugins don't.

## 1. Let operators require a plugin (fail closed)

**Status:** draft, ready to open.

### Problem

When a plugin fails to load, Bifrost logs the error and keeps serving traffic without it. For an
observability plugin that is the right call. For a security or governance plugin (policy
enforcement, budgets, lockdowns), it means failing open: every request goes through unchecked,
and nothing but a log line says so.

In `transports/v2.5.1` (commit `7cb75fb`),
`transports/bifrost-http/server/plugins.go` → `loadCustomPlugins` logs
`failed to load plugin %s` and does `continue`. `loadCustomPlugins` always returns `nil`, so
start-up succeeds either way.

### Reproduction

With a `.so` plugin whose `Init` returns an error (Gjallarhorn with a signing key that is too
short):

```
{"level":"error","message":"failed to load plugin gjallarhorn: failed to load custom plugin: plugin Init failed: gjallarhorn: tickets: key \"demo-1\": must be at least 32 bytes, got 9"}
```

Bifrost then answers `/health` with 200, serves `/v1/chat/completions` with 200 and executes MCP
tools, all without the plugin.

### Proposal

An opt-in `required` field on a plugin entry:

```json
{ "name": "gjallarhorn", "path": "/app/plugins/gjallarhorn.so", "enabled": true, "required": true, "config": { } }
```

- **Start-up:** if a required plugin fails to instantiate, `loadCustomPlugins` returns an error
  and Bifrost exits non-zero. The orchestrator then restarts it or alerts.
- **Runtime:** through the plugin API, a required plugin can't be disabled or deleted, and a
  config update whose `Init` fails keeps the running instance and returns the error.
- **Status:** the plugin status shows `required`, so a dashboard can tell an error that matters
  from one that doesn't.
- **Default:** `false`, so nothing changes for existing setups.

**Touch points:**
- `core/schemas.PluginConfig` (new field);
- `framework/configstore/tables.TablePlugin` plus a migration (new column, included in the config
  hash);
- `transports/config.schema.json`;
- `server/plugins.go` (start-up);
- `handlers/plugins.go` (runtime);
- the plugin docs.

**Smaller alternative**, if the maintainers prefer no schema or database change: an environment
variable such as `BIFROST_REQUIRED_PLUGINS=gjallarhorn,other`, checked at start-up only.

### Workaround we ship today

`deploy/guard.sh` wraps Bifrost in our images. It waits for Gjallarhorn's control API and stops
Bifrost if the plugin never comes up or stops answering. It works, but every security plugin
author would need to write the same thing, and it can't see plugins that have no health
endpoint.

## 2. Say which agent made a call with a forwarded credential

**Status:** idea, for discussion. Bigger than issue 1.

### Problem

With "Forward Bifrost authentication", the Agent Gateway passes the caller's virtual key to the
agent. The agent's own model and tool calls then carry that key, and Bifrost logs and governs
them as the human's. Gjallarhorn adds a signed chain ticket to tell them apart, but an agent
that drops the ticket while holding a forwarded human key is indistinguishable from the human
(see limit 2 in [threat-model.md](threat-model.md)).

### Possible directions

- When forwarding, Bifrost hands the agent a derived, short-lived credential bound to
  (original key, agent name). Calls made with it carry both identities in the request context,
  so the logs, the governance plugin and any `.so` plugin can see "agent X on behalf of user Y".
- Or, a smaller step: record the agent name the key was forwarded to, and expose it in the
  context for calls arriving with that key within the same task.

Either way, the confused-deputy question ("which agent did this, for whom?") becomes answerable
by Bifrost itself, with Gjallarhorn's chain tickets on top for multi-hop chains.
