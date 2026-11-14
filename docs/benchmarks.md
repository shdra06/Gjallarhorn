# Benchmarks

Raw `go test -bench` results. Every number we quote anywhere must come from a run recorded here.

## 2026-11-14: engine, first measurement

- Machine: Intel Core Ultra 9 185H, Windows 11, Go 1.26.1
- Command: `cd engine && go test -run XXX -bench . -benchtime 2s ./...`

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|---|---:|---:|---:|---|
| `BenchmarkIsLocked` | 39.8 | 0 | 0 | Lockdown check |
| `BenchmarkEvaluate` | 421.6 | 16 | 1 | Policy evaluation, 4 rules, delegated call |
| `BenchmarkVerify` (ticket) | 6,953 | 2,472 | 30 | Verify a 2-hop chain ticket |
| `BenchmarkAppendMemory` | 9,008 | 2,071 | 30 | Hash and store one event in memory |
| `BenchmarkAppendFile` | 17,378 | 3,893 | 57 | Hash, store and enqueue one event for disk |
| `BenchmarkCheckDirectCall` | 7,622 | 2,643 | 22 | Full decision for a direct model call, recorded |
| `BenchmarkCheckDelegatedCall` | 11,037 | 6,052 | 62 | Full decision for a call inside a 2-hop chain, recorded |

### Reading

- The decision logic itself (lockdown check plus policy) costs under half a microsecond.
- Most of the cost is cryptography and JSON:
  - verifying the ticket's HMAC and decoding its JSON (about 7 µs);
  - hashing the recorder event (about 9 µs).
- Bifrost advertises about 11 µs of overhead per request, so today we roughly double it. Phase 9
  targets:
  1. Cache verified tickets by token. Agents reuse one ticket for many calls; expiry is still
     checked on every use.
  2. Move event hashing to the background writer, which already processes events in order.
  3. Use a faster encoding for the hash input.

## 2026-11-14: after the ticket cache and background sealing

- Same machine. Note: a container build was running at the same time, so treat these as upper bounds.
- Command:
  `cd engine && go test -run XXX -bench 'Latency|CheckD|Append|IsLocked|Evaluate|Verify$' -benchtime 2s ./ ./recorder/ ./ticket/ ./policy/ ./lockdown/`

| Benchmark | ns/op | B/op | allocs/op | What changed |
|---|---:|---:|---:|---|
| `BenchmarkVerify` (ticket) | **154.8** | 32 | 1 | Verified tickets are cached (expiry and key retirement still checked every time): **about 45× faster** |
| `BenchmarkAppendLatency` | **723.8** | 850 | 5 | What a caller waits for: the event is queued, and hashing and disk writes happen on the sealer goroutine. **About 12× lower** than hashing inline |
| `BenchmarkCheckDelegatedLatency` | **5,059** | 3,831 | 29 | One full decision for a call inside a 2-hop chain, as a request sees it: **about 2.2× lower** |
| `BenchmarkEvaluate` | 377.8 | 16 | 1 | Unchanged |
| `BenchmarkIsLocked` | 30.5 | 0 | 0 | Unchanged |
| `BenchmarkAppendMemory` / `BenchmarkAppendFile` | 9,682 / 10,576 | | | Sustained throughput with the queue saturated, limited by the single sealer: about 100k events/s |
| `BenchmarkCheckDelegatedCall` / `BenchmarkCheckDirectCall` | 7,783 / 9,161 | | | Same, saturated |

A CPU profile of `BenchmarkCheckDelegatedLatency` shows:
- JSON encoding in the sealer: about 25% (off the request path);
- goroutine hand-off and scheduler costs: about 20%;
- `resolve` plus `decide` on the request path: about 20%.

Next targets: avoid the second SHA-256 for the key fingerprint, reuse principal copies, and use a
faster encoder for the hash input.
