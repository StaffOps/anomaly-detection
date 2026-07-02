# Degradation Model — .NET, Python, Go

This document encodes **how services fail, as causal chains** — not as a list of
metrics, but as ordered sequences of "X causes Y causes Z". It is the core asset of
this system: the difference between *listing symptoms* and *explaining an incident*.

> **Why this exists**: generic AIOps/RCA tools operate at the *edge between services*
> (service A calls service B). They do **not** know the **intra-runtime** causality —
> how a .NET threadpool, a Go GC, or a Python event loop degrades *inside a single
> process*, in a specific order. That ordered, language-specific knowledge is the
> defensible core. Everything else (detection, grouping, dispatch, enrichment) is
> commodity.

---

## How to read this document

Each degradation chain has:

- **Trigger** — what starts it.
- **Causal sequence** — the ordered steps, each with the **observable signal** (metric
  that rises/falls) and, where it exists, the **exact metric already collected** by
  this system (see `controller/config.yaml`).
- **Leading vs lagging** — which signal moves *first* (the early-warning signal) and
  which moves *last* (the user-visible symptom). Alerting on the leading signal is the
  whole point.
- **Confidence** — how well-established the chain is:
  - 🟢 **established** — textbook runtime behavior, broadly documented.
  - 🟡 **plausible** — generally true, but ordering/thresholds vary by workload.
  - 🔴 **to validate** — specific to this environment; confirm against real data.
- **Validation hook** — how to confirm the chain empirically using metrics this system
  already has + replay mode. **No chain here is "true" until validated this way.**

> Confidence markers are honest labels, not decoration. A 🟢 means the *mechanism* is
> well-understood; it does **not** mean it was measured in *your* clusters yet. The
> validation hook is how 🟢/🟡/🔴 all become "confirmed here".

---

## .NET

### Chain N1 — Threadpool starvation → latency → errors (🟢 established)

**Trigger**: inbound request rate exceeds the rate the threadpool can process
(or threads are blocked on sync-over-async calls, starving the pool).

| # | Step | Observable signal | Metric already collected |
|---|------|-------------------|--------------------------|
| 1 | Threadpool can't keep up | threadpool queue grows | `dotnet_threadpool_queue_length` / `kestrel_queued_requests` |
| 2 | Requests wait in queue | inbound request latency rises (p99 first, then p95) | `latency_p99_by_service` (spanmetrics) |
| 3 | Waiting requests exceed timeout | error rate rises | `error_rate_by_service` |
| 4 | Clients retry on timeout | inbound rate rises further → snowball | `request_rate_by_service` |

**Leading signal**: threadpool queue length (step 1). **Lagging**: error rate (step 3).
The queue fills *before* latency rises, and latency rises *before* errors. That order
is the early-warning window.

**Validation hook**: in replay, find a window where `error_rate_by_service` spiked.
Walk backwards — did `threadpool_queue_length` rise first, then `latency_p99`, then
errors? If the order holds across several incidents → chain confirmed.

---

### Chain N2 — GC pressure → pause → latency (🟢 established)

**Trigger**: high allocation rate or a memory leak grows the managed heap.

| # | Step | Observable signal | Metric already collected |
|---|------|-------------------|--------------------------|
| 1 | Heap grows | heap size climbs (often monotonic = leak) | `dotnet_heap_growth` (`process_runtime_dotnet_gc_heap_size_bytes`) |
| 2 | GC runs more often / longer | GC pause time rises | `dotnet_gc_pause_rate` |
| 3 | App threads pause during GC | latency rises in bursts (saw-tooth) | `latency_p99_by_service` |
| 4 | (if leak) heap hits limit | OOMKilled → pod restart | `restarts_5m`, `oom_kills` (enrichment) |

**Leading signal**: heap growth (step 1) for leaks; GC pause rate (step 2) for
allocation spikes. **Lagging**: OOMKill/restart (step 4).

**Distinguishing leak vs spike**: a *leak* shows monotonic heap growth over hours
ending in OOMKill; an *allocation spike* shows GC pause correlated with request rate,
heap stable. Same metrics, different shape — the model must tell them apart because
the fix differs (leak = code bug; spike = capacity/tuning).

**Validation hook**: replay a window ending in an `OOMKilled` event. Was heap growth
monotonic beforehand (leak) or correlated with request rate (spike)?

---

### Chain N3 — Downstream dependency slow → connection pool exhaustion (🟡 plausible)

**Trigger**: a downstream dependency (DB, HTTP API) slows down.

| # | Step | Observable signal | Metric already collected |
|---|------|-------------------|--------------------------|
| 1 | Dependency responds slower | outbound call duration rises | `http_client_active_requests` rises (calls in-flight longer) |
| 2 | Connections held longer | connection pool / DB pool saturates | `hikaricp_pending` (DB pool wait) |
| 3 | New requests wait for a connection | inbound latency rises | `latency_p99_by_service` |
| 4 | Waits exceed timeout | errors rise | `error_rate_by_service` |

**Leading signal**: outbound call duration / active requests (step 1). This is the key
distinguisher from N1: in N1 the *threadpool* is the bottleneck (self-inflicted); in
N3 a *dependency* is (external). The model must separate them — the fix is in a
different place.

**Validation hook**: when latency rises, check whether `http_client_active_requests`
rose first (→ N3, dependency) or `threadpool_queue` rose first (→ N1, self). This is
exactly the kind of disambiguation that makes "explain cause" > "list symptoms".

---

## Python

### Chain P1 — Blocked event loop (asyncio) → connection pileup → timeouts (🟢 established)

**Trigger**: synchronous/CPU-bound work runs on the asyncio event loop (the cardinal
sin of async Python), blocking it.

| # | Step | Observable signal | Metric already collected |
|---|------|-------------------|--------------------------|
| 1 | Event loop blocked by sync work | event loop lag rises | *not collected yet* (gap — see below) |
| 2 | Loop can't accept/progress connections | in-flight requests pile up | `http.server.active_requests` (OTel default) |
| 3 | Pending work accumulates | inbound latency rises across *all* endpoints at once | `latency_p99_by_service` |
| 4 | Clients time out | errors rise | `error_rate_by_service` |

**Distinguishing mark**: a blocked event loop degrades **every endpoint
simultaneously** (one loop serves all), unlike a slow DB query that hits one path.
That "everything got slow at once, on one pod" signature is the fingerprint.

**Leading signal**: event loop lag — **but this system does not collect it today**.
That's a concrete gap: P1 is only *partially* observable now (you'd see steps 2-4 but
not the root at step 1). Adding an event-loop-lag metric is a candidate roadmap item.

**Validation hook**: when a Python pod shows latency rising on *all* routes at once
with CPU near a core's limit → consistent with P1. Confirm with `system.cpu.utilization`.

---

### Chain P2 — GIL contention / CPU saturation → latency (🟡 plausible)

**Trigger**: CPU-bound load on a single Python process (GIL serializes CPU work).

| # | Step | Observable signal | Metric already collected |
|---|------|-------------------|--------------------------|
| 1 | CPU-bound work saturates one core | CPU utilization approaches 100% of a core | `system.cpu.utilization` (OTel) |
| 2 | GIL serializes; requests can't parallelize | latency rises despite "spare" CPU on other cores | `latency_p99_by_service` |
| 3 | Throughput plateaus | request rate flattens even as demand grows | `request_rate_by_service` |

**Distinguishing mark**: CPU pegged at ~1 core (not all cores) + latency up =
GIL-bound. The "one core maxed, others idle" shape distinguishes it from genuine
whole-machine CPU exhaustion.

**Validation hook**: replay a latency spike on a Python service; check if CPU was
saturated on ~1 core while latency rose and throughput plateaued.

---

### Chain P3 — Memory growth → OOMKill (🟢 established)

**Trigger**: leak (unbounded cache, accumulating references) or genuine high memory
demand.

| # | Step | Observable signal | Metric already collected |
|---|------|-------------------|--------------------------|
| 1 | RSS grows | memory utilization climbs | `system.memory.utilization` / `process.runtime.cpython.memory` |
| 2 | Approaches container limit | memory ratio → 1.0 | `memory_ratio` (enrichment) |
| 3 | Hits limit | OOMKilled → restart | `oom_kills`, `restarts_5m` |

**Leading**: memory growth slope (step 1). **Lagging**: OOMKill (step 3). Same
leak-vs-demand distinction as .NET N2: monotonic slope = leak; correlated-with-load =
demand.

**Validation hook**: same as N2 — shape of the memory curve before an OOMKill.

---

## Go

### Chain G1 — Goroutine leak → memory growth → OOMKill (🟢 established)

**Trigger**: goroutines that block forever (unbuffered channel with no receiver,
missing context cancellation) accumulate.

| # | Step | Observable signal | Metric already collected |
|---|------|-------------------|--------------------------|
| 1 | Goroutines accumulate | goroutine count climbs monotonically | *not collected yet* (gap) |
| 2 | Each holds stack + referenced memory | heap/RSS grows | `memory_ratio` (enrichment) |
| 3 | Approaches limit | OOMKilled → restart | `oom_kills`, `restarts_5m` |

**Distinguishing mark**: goroutine count rising *without* a matching rise in request
rate = leak (goroutines not being released). If goroutines track request rate, it's
normal load.

**Leading signal**: goroutine count — **not collected today** (gap, like Python's loop
lag). Without it, G1 looks identical to "generic memory growth" until OOMKill. Adding
`go_goroutines` is a high-value, cheap candidate.

**Validation hook**: instrument `go_goroutines`; in replay, check if it rose
independently of `request_rate` before an OOMKill.

---

### Chain G2 — GC pressure / high allocation → CPU + latency (🟡 plausible)

**Trigger**: high allocation rate drives frequent GC.

| # | Step | Observable signal | Metric already collected |
|---|------|-------------------|--------------------------|
| 1 | Allocation rate high | GC runs frequently | *Go runtime GC metrics — partial* |
| 2 | GC consumes CPU (concurrent, but steals cycles) | CPU rises, GC CPU fraction rises | `system.cpu.utilization` |
| 3 | GC assist slows allocating goroutines | latency rises | `latency_p99_by_service` |

**Note**: Go's GC is concurrent (low pause vs .NET historically), so the symptom is
more *CPU cost* than *pause time*. The model must not blindly copy the .NET GC chain —
the signature differs by runtime. (This is exactly why one generic model fails and a
per-language model wins.)

**Validation hook**: correlate GC CPU fraction with latency on a Go service under load.

---

### Chain G3 — Connection pool / DB saturation → latency → errors (🟡 plausible)

**Trigger**: downstream DB slows or connection pool undersized.

| # | Step | Observable signal | Metric already collected |
|---|------|-------------------|--------------------------|
| 1 | DB slow or pool too small | connections wait | `go_sql_waiting` (`go_sql_stats_connections_waited_for_total`) |
| 2 | Requests block on a connection | latency rises | `latency_p99_by_service` |
| 3 | Waits exceed timeout | errors rise | `error_rate_by_service` |

**Leading signal**: `go_sql_waiting` (step 1) — directly collected, good coverage.
Structurally the same as .NET N3, different metric name.

**Validation hook**: when a Go service's latency rises, did `go_sql_waiting` rise
first? If yes → DB/pool root, not the service itself.

---

## Cross-language patterns (the reusable shapes)

Stepping back, the chains collapse into a few **archetypes** that repeat across
languages with different metric names:

| Archetype | .NET | Python | Go |
|-----------|------|--------|-----|
| **Saturation of the work executor** | threadpool (N1) | event loop (P1) | — (goroutines cheap; rarely the bottleneck) |
| **CPU/serialization limit** | — | GIL (P2) | GC CPU (G2) |
| **Memory growth → OOMKill** | heap leak (N2) | RSS leak (P3) | goroutine leak (G1) |
| **Dependency / pool saturation** | HikariCP (N3) | (DB driver) | database/sql (G3) |

**Why this matters for the build**: the *archetype* is shared, so the correlation
engine can be generic; the *signals and ordering* are language-specific, so the
knowledge lives here, per language. This split is the architecture: generic engine,
language-specific causal knowledge.

---

## Known observability gaps (surfaced by writing this)

Writing the model exposed signals the system **should** collect but doesn't — each is
a leading (root-cause) signal currently invisible:

| Gap | Language | Why it matters | Cost |
|-----|----------|----------------|------|
| Event loop lag | Python | Root of P1; without it, P1 is only half-visible | Low (OTel metric) |
| `go_goroutines` | Go | Root of G1; without it, leak looks like generic memory growth | Low (one gauge) |
| GC CPU fraction | Go | Distinguishes G2 from generic CPU | Low |

These are concrete, cheap roadmap candidates — and they matter *because the causal
model says so*, which is the model already earning its keep.

---

## Status & validation plan

**Every chain above is currently 🟢/🟡/🔴 by mechanism, not yet confirmed in BDC
clusters.** The honest next step (gate before building on this model) is:

1. Use **replay mode** over historical windows containing known incidents.
2. For each incident, walk the chain **backwards** from the symptom and check the
   predicted ordering of leading→lagging signals.
3. Mark each chain **confirmed / refuted / inconclusive** with the evidence.
4. Where a leading signal is a "gap" (not collected), note that the chain can't be
   fully validated until the metric is added.

Until step 3 is done for a chain, this document is a **hypothesis written down** — which
is already far more than a list of metric names, but is not yet ground truth.
