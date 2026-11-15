# Security review

A review of Gjallarhorn's own code: the engine, the Bifrost plugin, the control API, the console
and the demo. It was done on 15 Nov 2026, by the people who built it, using automated tools and
a manual pass. It is not an independent audit. What it can't cover is in
[threat-model.md](threat-model.md).

## Method

| Check | Tool or method | Result |
|---|---|---|
| Static analysis | `go vet`; `staticcheck` 2026.2.2 (v0.8.2) on the engine and the demo | Clean, after removing two dead assignments in tests |
| Known vulnerabilities, Go | `govulncheck` v1.9.0, run with the Go version the product is built with | Go 1.27.3: one standard-library vulnerability reachable (S2). Go 1.27.4: **none** in the engine or the demo |
| Known vulnerabilities, console | `npm audit` | **0** vulnerabilities |
| Races | `go test -race` on every package, in CI | Clean |
| Tests | Unit, integration and fuzz tests; coverage at least 90% per engine package, enforced in CI | 95.3% overall |
| Hostile input | Fuzzing the ticket parser (`FuzzVerify`), plus tests of every endpoint's bad-input responses | No crash; every bad request gets a 4xx JSON answer |
| Behaviour under attack | The attack lab, 31 attacks through the real gateway | 0 harmful with Gjallarhorn ([results.md](results.md)) |
| Performance under load | `demo/cmd/measure` in the real stack | Found S1 |
| Manual review | The checklist below | S3–S7 |

`govulncheck` was first run with the laptop's Go 1.26.1, which reported 24 standard-library
issues that the shipped build (Go 1.27.x) doesn't have. It was then re-run inside the same
`golang:1.27.x` image the product is built with, which is the result that counts.

## Findings

| # | Severity | Finding | Status |
|---|---|---|---|
| S1 | High | Writing the lockdowns file held the lock every call's lockdown check reads | Fixed |
| S2 | Medium | Go 1.27.3 `net/http`: GO-2026-6859, an HTTP/1 server crash (`MaxBytesReader` header race) | Fixed: Go 1.27.4 |
| S3 | Medium | Replays and co-pilot proofs loaded the whole flight log into memory | Fixed: `replay_limit` |
| S4 | Low | The public health check showed event and lockdown counts | Fixed |
| S5 | Low | Blocked callers are told the rule and its reason | Option added: `quiet_denials` |
| S6 | Low | Grants were accepted for a malformed actor id | Fixed |
| S7 | Info | `accept_labels` was trusted silently | Fixed: start-up warning |
| S8 | High | If Gjallarhorn fails to start, Bifrost serves traffic without it (fail open) | Mitigated in our images: `deploy/guard.sh`; upstream fix proposed |

### S1: a lockdown paused every call through the gateway

**What:** to save a lockdown, the registry wrote the file and waited for the disk (`fsync`)
while holding the lock that `IsLocked`, the hot path of every decision, reads. On a slow disk,
locking one agent paused every call for every agent until the write finished. It also delayed
the lockdown itself by the same amount.

**How it was found:** by measuring lockdown speed in the real stack. In a first run (its raw
files were not kept), the lock request took 5–250 ms, and calls by other agents stalled for as
long.

**Fix:** a lockdown now takes effect in memory first. The file is written afterwards, outside
that lock, with writes serialised so the last write always saves the latest state. If the write
fails, the change is undone, and a failed unlock keeps the agent locked (failing closed). The
test `TestSlowDiskDoesNotStallTheHotPath` holds the disk busy and checks that:
- the lockdown applies at once;
- other agents' checks don't wait.

**After the fix:** the first refused call arrives a median of 1.8 ms after the lock request (worst
18.8 ms). Across 40 lockdowns during 21,745 calls, no call started after the lock was let
through. See [results.md](results.md#lockdown-speed).

### S2: GO-2026-6859 in Go 1.27.3

`govulncheck` found the vulnerability reachable from our HTTP servers (the control API and the
demo services). All builds and CI moved to Go 1.27.4, pinned by image digest.

This is the one dependency adopted less than two weeks after its release: it is a security
patch from the Go project for a crash in exactly the kind of server we run.

### S3: replay memory

`History()` read and kept every event in the log file, so a replay, a simulation or a co-pilot
proof on a long-running gateway could use as much memory as the log was large. It now verifies
the whole log but keeps only the most recent events (`recorder.replay_limit`, default 200,000).
Tested by `TestReplayReadsOnlyTheRecentTail`, and by `TestReadTailVerifiesEverythingKeepsTheEnd`,
which also checks that tampering early in the log is still caught.

### S4: health check details

`/v1/health` stays public for load balancers, but event and lockdown counts now need the admin
token.

### S5: what a blocked caller learns

Denials say which rule fired and why. That is useful while building agents, but it helps an
attacker probe the policy. `quiet_denials: true` returns only the kind of denial (`rule`,
`lockdown`, `ticket`, ...) and the flight-recorder event number, which an operator can look up.
It is off by default, and recommended for production.

### S6: grants for malformed actors

A grant matches its actor exactly, so a grant for `"not an id"` could never match anything. It
was harmless, but it was accepted silently. Found by the new error-path tests; such grants are
now refused.

### S7: `accept_labels`

The demo lets callers label their traffic as attack or benign, so the attack lab can measure
results. Labels feed replay verdicts and co-pilot proofs, so in production they would let any
caller skew both. The plugin now logs a warning at start-up when this is on.

## Plugin start-up failure

### S8: if Gjallarhorn fails to start, Bifrost serves traffic without it

**Test** (`deploy/demo/run-failtest.sh`, raw output in
`results/startup-failure-20261115T060217Z.txt`): the demo was started with a signing key that is
too short, so the plugin's `Init` returns an error. Bifrost logs
`failed to load plugin gjallarhorn`, then answers `/health` with 200 and serves calls. A
premium-model call by the research agent, which Gjallarhorn refuses, returned 200. This is
Bifrost's design: `loadCustomPlugins` logs plugin errors and continues. For a security plugin,
it means failing open.

**Mitigation in our images:** `deploy/guard.sh` is now the entrypoint of every image. It starts
Bifrost and waits for Gjallarhorn's control API. It stops Bifrost with exit code 1 if the API
doesn't answer within `GJ_START_TIMEOUT` (120 s by default), or stops answering for three checks
in a row. In the same test with the guard, the container stopped after 31 s (with a 30 s limit).

**Upstream:** a `required` flag for plugins would make Bifrost itself refuse to start. The issue
draft is in [upstream.md](upstream.md).

**If you run Gjallarhorn without our images,** watch the control API's `/v1/health` and alert
when it doesn't answer.

## Checklist

| Area | What was checked | Result |
|---|---|---|
| Control API authentication | Every route except `/v1/health` and the console's static files needs the admin token; constant-time compare; token at least 24 characters | `TestEveryEndpointNeedsTheToken` covers all 25 routes with no token, a wrong token and a gateway key |
| Input handling | JSON bodies capped at 1 MiB and decoded with unknown fields refused; ids validated; path values checked | Error-path tests for every endpoint |
| Chain tickets | HMAC-SHA256 with `hmac.Equal`; keys at least 32 bytes with IDs for rotation; expiry; depth limit; binding to the keys of chain members; size limits before parsing | Unit tests plus `FuzzVerify` |
| Secrets at rest | Raw keys and tickets are never recorded, only a short fingerprint and the ticket ID; flight log and lockdown file created owner-only (0600); writes are atomic | `TestRawKeysAreNeverRecorded`; recorder and lockdown tests |
| Concurrency | Race detector in CI; lockdown hot path never waits on the disk (S1); policy replaced by compare-and-swap with `If-Match` | Race-clean |
| Denial of service | Request bodies capped; ticket cache bounded; in-memory ring bounded; replay bounded (S3); co-pilot limited to two concurrent calls; slow live-stream subscribers drop messages rather than stall the recorder | Tests for each |
| Console | Strict Content-Security-Policy (`default-src 'self'`, no inline scripts, no framing); token in `sessionStorage` and only in the `Authorization` header; no HTML built from data (React escaping; DOM labels use `textContent`) | Console tests; CSP header test |
| Static files | Path cleaning; no directory listings; no escape from the console directory; unknown `/v1/` paths are JSON 404s | `TestConsoleIsServed` |
| Demo proxy | Needs the admin token, which is stripped (with cookies) before forwarding; off unless configured | `TestDemoDriverProxy` |
| Co-pilot | Model output parsed strictly, rules validated and replayed, applied only by an operator with `If-Match`; model text rendered as text | Co-pilot tests and the fire drill |
| Dependencies | `govulncheck` and `npm audit` clean; all images and packages pinned (images by digest) | See Method |
