# tenant-core load testing

Network-level load testing for tenant-core, using [Vegeta](https://github.com/tsenart/vegeta). This complements, and does not replace, the Go-level micro-benchmarks already in this repo (`ratelimit/ratelimit_bench_test.go`, `store/cached_bench_test.go`, `banchecker/banchecker_bench_test.go`, `ratelimit/redis/redis_bench_test.go`) — those measure a single function call in isolation; this measures a full HTTP request/response cycle, including the Go HTTP server, the network stack, and everything in between.

## Methodology: three layers, not one number

A single load test against `cmd/server` (Resolver + CachedStore + Manager + RBAC, all at once) would produce exactly one number — total req/s — that conflates four different costs into one figure: the load-testing machine and tool's own ceiling, the Go HTTP server's baseline overhead, tenant-core's resolution pipeline, and RBAC. If that number looks lower than expected, there is no way to tell *which* of those four is responsible.

Instead, three servers isolate each layer, so scenarios can be compared pairwise to attribute a cost to a specific thing:

| Scenario | Server | Route | What it measures |
|---|---|---|---|
| **A** | `loadtest/servers/httponly` | `GET /ping` | The ceiling of the machine and Vegeta itself — zero tenant-core dependency. Every other scenario's numbers should be read *relative to A*, never in isolation. |
| **B** | `loadtest/servers/tenantcore` | `GET /api/me` | `A`'s baseline **plus** the cost of `Resolver → CachedStore → Manager → tenantctx` — no RBAC. `B - A` ≈ the cost of tenant-core's core resolution path. |
| **C** | `loadtest/servers/rbac` | `GET /api/users` | `B`'s pipeline **plus** an `RBAC.Can(...)` check on every request. `C - B` ≈ the cost of RBAC specifically. |

Each scenario runs on its own port (8081/8082/8083) so all three servers could, in principle, run simultaneously — the orchestration script still runs them one at a time, to avoid one scenario's server competing for CPU with another's.

## Running it

```bash
./loadtest/scenarios/run.sh        # all three scenarios
./loadtest/scenarios/run.sh A      # only scenario A (or B, or C)
```

Prerequisites: `go`, `curl`, `jq`, `taskset` (util-linux — normally preinstalled on Linux; no equivalent on macOS), and Vegeta:

```bash
go install github.com/tsenart/vegeta@latest
```

The script checks for all five and fails fast with a clear message if any is missing, rather than failing midway through a run. It also requires at least 2 CPUs, so the server can be pinned away from the rest of the system — see "Known limitation of this protocol" below.

For each scenario, in order, the script:

1. Builds the server and starts it, pinned to CPU 0 via `taskset -c 0`.
2. Waits for it to accept connections (polling, up to 5s).
3. Runs Vegeta — deliberately **unpinned** (see below for why) — at each rate in `100, 500, 1000, 2000, 3000` req/s (adjustable via the `RATES` array at the top of `run.sh`), 10 seconds per step (`vegeta attack -rate=<N> -duration=10s`).
4. Saves a full text report per step to `loadtest/results/scenario-<A|B|C>-<N>rps.txt`.
5. Stops the server cleanly before moving to the next scenario.

At the end, it prints a comparative table (scenario, target rate, actual rate, success rate, p99) for all three scenarios side by side, alongside the single-machine warning described below.

`loadtest/results/` is gitignored — these numbers are specific to whatever machine produced them and must never be committed or treated as a portable, universal claim about tenant-core's performance.

## Known limitation of this protocol

**The load generator (Vegeta) and the server under test run on the same physical machine.** This is not an oversight — it's the honest constraint of this stage, made explicit rather than glossed over: no second machine was available on the local network for this round of testing.

Running both roles on one machine means they compete for the same finite CPU resources. Early, unmitigated runs showed exactly the failure mode this causes: at a 20,000 req/s target, only ~733 req/s were actually achieved, yet with a *better* success rate than the 10,000 req/s step — a curve that isn't monotonic in throughput vs. load is a sign the *generator* saturated, not that the server did. A generator fighting the server for CPU cycles cannot reliably characterize the server's own capacity.

**What was tried first, and why it made things worse, not better.** The reasonable-sounding first fix was symmetric isolation: pin the server to CPU 0 *and* pin Vegeta's attack to CPU 1, via `taskset`, so neither could steal cycles from the other. This was verified empirically rather than trusted on theory alone — and the theory did not survive contact with this specific machine (an AMD A6-4400M, dual-core). With Vegeta itself pinned to a single CPU, a `-rate=500 -duration=10s` attack against the (still CPU-0-pinned) server stopped emitting requests after ~2.3 seconds instead of running the full 10-second window, producing exactly the misleading, non-monotonic numbers this protocol exists to avoid — success rates and p99s that looked like server-side collapse, but were actually the *generator* failing to sustain its own schedule. Rerunning the identical attack against the identical server with Vegeta left unpinned completed cleanly: full 10s window, 5000/5000 requests delivered, p99 3.9ms. Vegeta's own worker pool needs genuine multi-core concurrency to pace requests and process responses at the same time; confined to one core, it doesn't degrade gracefully, it breaks. This was the third time in this project that a reasonable-sounding assumption failed to survive an actual empirical check (see also: the go-redis reconnection investigation in `docs/ARCHITECTURE.md` §10.16, and this same discovery about non-monotonic curves).

**What `run.sh` actually does, given that finding**: pin the server only, to CPU 0, via `taskset -c 0`, so it always runs in a fixed, comparable environment across scenarios A/B/C. Vegeta runs unpinned, free to use whatever CPU capacity the OS scheduler gives it. This is a narrower mitigation than originally planned — the server can still be interrupted by Vegeta or anything else landing on CPU 0, and both processes still share the same memory bus, the same loopback network stack, and (on this specific chip) the same L2 cache — but it is the version that was actually verified to produce trustworthy, monotonic measurements on this hardware, rather than a symmetric-looking design that quietly measured the generator's own failure instead of the server's behavior.

**Consequence for interpreting results**: the absolute req/s and latency numbers produced by this protocol must never be quoted as "tenant-core's production capacity." What *does* remain interpretable is the **relative comparison between scenarios A, B, and C run under the identical protocol** — since whatever interference exists on this machine affects all three scenarios roughly equally, the *difference* between B and A (tenant-core's resolution overhead) or between C and B (RBAC's overhead) is a more trustworthy signal than any single scenario's absolute throughput. Even that relative reading should be treated as directional, not a precise multiplier, until validated on separate hardware.

A real capacity benchmark — one whose absolute numbers could legitimately be quoted — requires the generator and the server to run on physically separate machines connected over a real network, so neither competes with the other for CPU, memory bandwidth, or cache. That setup is out of reach for this round (no second machine on the local network) and is recorded here as a known, assumed limitation of this stage — not a gap that was missed.

## Investigation: intermittent latency spikes at high load

*Investigation carried out on 2026-08-25, on the same AMD A6-4400M dual-core machine documented above. Recorded here in full — including the parts that didn't pan out — because the process matters as much as the conclusion, and because a future reader hitting a similar anomaly on this protocol should be able to retrace these steps instead of repeating them from scratch.*

**Initial signal.** In the first full runs of this protocol, Scenario B (`tenantcore`, no RBAC) showed a p99 of roughly 1000-2000ms at the 2000 req/s step, while Scenario C (`rbac`, identical pipeline plus an RBAC check) stayed stable at roughly 10ms at the same step, on the same machine, under the same protocol. Since C does strictly *more* work than B per request, C being dramatically faster than B was backwards from any plausible cost model — worth investigating rather than writing off.

**Hypotheses tested, in order, each with the evidence that ruled it out:**

1. **Pure measurement noise.** Ruled out: the same B-slower-than-C pattern reproduced in 4 independent, separately-invoked runs, not just one. A one-off fluke does not reproduce 4/4 times.
2. **JSON encoding asymmetry between B and C.** B's handler originally encoded `map[string]any{"tenant_id": ..., "tenant_state": ...}` (interface boxing, more allocation) while C's encoded a plain `map[string]string{"message": ...}`. Ruled out: both handlers were rewritten to encode the exact same `map[string]string{"tenant_id": ...}` shape, differing only in the presence of the `authz.Can(...)` check — the anomaly persisted at essentially the same magnitude.
3. **Execution order effect** (B was always run before C, chronologically, across all prior trials). Ruled out: reversing the order (C run first, then B) in a subsequent trial still showed B collapsing at 2000 req/s (p99 2053ms) while C stayed clean (p99 11ms) — the anomaly followed the scenario, not its position in the sequence.
4. **GOMAXPROCS / CPU affinity mismatch.** Theory: `taskset -c 0` restricts the process's affinity mask at the OS level, and the Go runtime derives `GOMAXPROCS` from that mask via `sched_getaffinity` at startup — so an explicit `GOMAXPROCS=1` should be redundant, but the two mechanisms were never confirmed to agree. Ruled out: setting `GOMAXPROCS=1` explicitly in the server's environment (in addition to the existing `taskset` pinning) changed nothing — B's p99 at 2000 req/s remained in the same ~1000-2000ms range, and its 3000 req/s step was, if anything, worse (p99 5131ms) than prior runs without the explicit setting.

**CPU profiling.** With 4 black-box hypotheses eliminated, `net/http/pprof` was added to Scenario B's server (a separate `:6061` listener, never exposed on the load-tested `:8082` port), and `run.sh` was instrumented to capture a live 10-second CPU profile specifically during Scenario B's 2000 req/s step — the exact conditions that had reproduced the anomaly before. A standalone reproduction attempt (a fresh server jumped straight to 2000 req/s, no warm-up) failed to reproduce the anomaly at all (p99 ~60ms); replicating the full 100 → 500 → 1000 req/s warm-up sequence first got closer (p99 ~170ms) but still fell well short of the ~1000-2000ms previously observed. In both cases, and in the profile captured live from inside `run.sh` itself, the CPU profile showed nothing pathological: time was spent almost entirely in ordinary `net/http` connection handling and network I/O syscalls (`syscall.write`/`read` via `bufio.Writer.Flush` and `net.conn.Write`/`Read`), JSON encoding accounted for only ~5-7% of cumulative CPU time, and GC (`runtime.gcBgMarkWorker`) was negligible (~2%). No lock contention, no runaway allocation path, no smoking gun — either the anomaly is invisible at the CPU-profile level, or (as turned out to be the case) it simply wasn't happening during any of the profiled windows.

**The finding that revised the conclusion.** In the same `run.sh` execution that included the live CPU profile capture, Scenario B's 2000 req/s step was, in fact, healthy (p99 30.97ms) — the profile captured a normal window, which is why it showed nothing unusual. But that same run showed severe collapses **elsewhere**: Scenario A (the RBAC-free, tenant-core-free baseline) hit p99 527ms at 2000 req/s and **p99 5718ms at 3000 req/s** — worse than anything ever observed on Scenario B — and Scenario C hit p99 2516ms at 3000 req/s. This matters because, up to that point, Scenario B had been re-run in isolation five separate times specifically because it was the scenario under suspicion, while Scenario A and C's high-rate steps had each only been exercised once per full-suite run in between. Five chances for an intermittent phenomenon to hit B, versus one chance each for A and C, is a sampling bias that can manufacture the appearance of a B-specific pattern out of a phenomenon that isn't actually specific to B at all.

**Current conclusion — stated carefully, not overclaimed.** The hypothesis of a defect specific to Scenario B's code is not supported by the evidence above: unifying its response encoding, reversing execution order, and fixing GOMAXPROCS all failed to change the anomaly, and a run that gave equal opportunity for A and C to fail showed *them* collapsing more severely than B did that same day. The more likely explanation is that severe, intermittent latency spikes at high load (≥2000 req/s) are a characteristic of this specific test machine — a dual-core laptop chip with the generator and server co-resident, already flagged as a protocol limitation above — rather than a defect in tenant-core itself. That said: **one run in which A and C were affected is not sufficient to confirm the phenomenon is evenly distributed across all three scenarios.** It could still be that B is disproportionately susceptible for a reason not yet identified, just rarer than initially believed. This question is left open rather than closed on insufficient evidence.

**Recommendation for anyone continuing this investigation:** to settle this properly, either (a) run several repetitions of the full A/B/C suite — not just repeated isolated runs of B — and compare how often each scenario, not just B, shows a high-rate collapse, giving all three equal sampling opportunity; or (b) run the protocol on two physically separate machines (generator and server on distinct hardware), which was already the known limitation of this entire protocol (see above) and would eliminate host-level contention as a variable entirely rather than requiring more black-box statistical inference on a single shared machine.

## Reading the results

Each `loadtest/results/scenario-<X>-<N>rps.txt` file is Vegeta's own text report: total requests, actual throughput, latency percentiles (p50/p90/p95/p99/max), success ratio, and the status code breakdown. Read a scenario's numbers **across rates** first (does p99 stay flat as load increases, or does it fall off a cliff past some rate — that cliff is the scenario's real capacity, not whatever rate you asked Vegeta for), then compare **across scenarios** at the same rate (B vs A, C vs B) to attribute cost to a specific layer.

## The one rule that matters more than any single number

**Never quote a req/s figure without stating, alongside it: the exact machine it was measured on, the exact scenario (A, B, or C — and at which target rate), and the success rate / p99 that came with it.**

A throughput number with no success rate attached is meaningless — 50,000 req/s at 40% success is not a result worth quoting. A throughput number with no p99 attached hides the fact that the median may look fine while a meaningful fraction of requests are timing out. And a throughput number with no machine attached cannot be compared to anything, by anyone, ever — including your own future self on different hardware. This is the same discipline already applied to every Go-level benchmark in this repo; load testing at the network level doesn't get an exception.
