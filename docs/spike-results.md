# Phase 1 spike results

**Date:** 14 Nov 2026
**Bifrost:** `transports/v2.5.1`, built dynamically linked (no `-extldflags -static`) with Go 1.27.3 on Alpine
**Plugin:** `deploy/spike/plugin` (a `.so` built in the same stage and module graph as the binary)
**How to reproduce:**

```bash
podman build -t gjallarhorn-spike -f deploy/spike/Containerfile deploy/spike
bash deploy/spike/test.sh
```

## Result: go

| # | Question | Result | Evidence |
|---|---|---|---|
| 1 | Does a custom `.so` plugin load into a dynamically linked Bifrost? | **Yes** | `plugin status: gjallarhorn-spike - active`; plugin `Init` ran |
| 2 | Can `PreLLMHook` block a call with our own error? | **Yes** | HTTP 403, body carries our message and `gjallarhorn_denied` code |
| 2b | Does Bifrost retry a blocked call on a fallback? | **No** (with `AllowFallbacks=false`) | Request asked for a fallback model; the mock model received **zero** calls |
| 3 | Can the plugin see the caller's virtual key? | **Yes** | `BifrostContextKeyVirtualKey` fingerprint matched the key sent |
| 3b | Can it see custom request headers (our chain ticket)? | **Yes** | `x-gjallarhorn-chain` visible in `BifrostContextKeyRequestHeaders` |
| 4 | Does a header set in `HTTPTransportPreHook` on `/agents/a2a/...` reach the agent? | **Yes** (proven in Phase 6) | Every delegated chain in the attack lab is built from tickets the plugin stamped and Bifrost forwarded. See [results.md](results.md) |

## What we learned

1. **Bifrost's governance runs before our hook.** An unregistered virtual key is rejected with 401
   before Gjallarhorn sees the call. Test and demo configs must declare their virtual keys under
   `governance.virtual_keys`. This is also good news: Gjallarhorn adds to Bifrost's own checks and
   doesn't replace them.
2. **Build both in one stage.** Building the plugin inside Bifrost's `transports` module guarantees
   identical dependency versions. Each `-buildmode=plugin` build recompiles every dependency, so a
   clean build takes about 10–15 minutes on a 2 GB Podman VM. Layer caching makes config-only
   rebuilds take seconds.
3. **Windows + Podman port forwarding listens on `::1` only.** Tests must call `localhost`, not
   `127.0.0.1`.
4. **First start takes about 15 s** while Bifrost runs its log-store migrations and loads the model
   catalog. Health checks must wait for it.
