# Threat Model & Limitations

This document states **what this system can and cannot see**, honestly. It exists
because "anomaly detection" with no threat model is a science fair project — and
because anyone evaluating this system deserves to know its structural blind spots
before trusting it.

> **One-line classification**: this is a **reliability anomaly detector** (SRE
> tooling), **not** a runtime security product. It detects workloads deviating from
> their historical baseline. It does **not** detect adversaries, and several of its
> mechanisms are defeatable by an attacker who knows they exist.

All claims below were verified against the source (`internal/baseline/store.go`,
`internal/baseline/seasonal.go`, `internal/detection/`, `internal/ingestion/`,
`internal/correlation/`, `internal/suppression/`, `internal/readiness/`) as of
controller 0.7.0. Where a capability does not exist, this document says so rather
than implying it.

---

## What this system is

A distributed detector that ingests **three signals** today and flags series that
deviate from a learned per-series baseline:

| Signal | Source | Detector |
|--------|--------|----------|
| `metrics` | Prometheus (PromQL) | static thresholds + adaptive EWMA/Z-score |
| `logs` | Loki (LogQL) | log-rate patterns |
| `events` | K8s API (`Events("").Watch`) | event-reason patterns |

A fourth signal — `security` via **Falco** — is **specced but not implemented**
(see `specs/falco-integration/`, roadmap P2.7). Even when shipped, its v1 is
**enrichment-only**: it annotates an already-existing resource anomaly, it does not
raise security alerts on its own.

Audit logs are **not** ingested at all.

---

## What this system is structurally incapable of seeing

These are not bugs to be fixed incrementally — they are **ceilings imposed by the
signal sources**. No amount of tuning changes them.

| Blind spot | Why it's invisible | Would require |
|------------|--------------------|---------------|
| Syscall-level activity | Not a metric/log/event | eBPF sensor (Falco/Tetragon) |
| In-memory-only attacks | Leaves no persistent signal | Runtime memory inspection |
| Kernel exploits / container escape (clean) | No resource/log footprint | Runtime security sensor |
| East-west L3/L4 traffic | Not collected | CNI flow logs (Cilium/Hubble) |
| Sequences of individually-normal actions | Detector is point-wise, not sequence-aware | Behavioral/graph model |
| Anything the workload chooses not to emit | Self-reported logs are forgeable | Control-plane-level source |

**Bottom line for the security framing**: a competent attacker using only legitimate
primitives (`kubectl exec`, a valid service-account token, legitimate API calls) is
invisible to this system, because each action in isolation is within baseline and the
detector has no notion of sequence.

---

## MITRE ATT&CK for Containers — coverage map

Honest mapping. "Partial" means *only if the technique happens to move a resource
metric or log rate enough to breach baseline* — i.e. incidental, not designed-for.

| Tactic / Technique | Coverage | Note |
|--------------------|----------|------|
| Resource Hijacking (T1496, e.g. cryptomining) | **Partial** | Only via CPU/memory anomaly after the fact; the *cause* is unseen |
| Container Escape (T1611) | **None** | No syscall/runtime visibility |
| Valid Accounts / token abuse (T1078) | **None** | No audit ingestion |
| Exec into Container (T1609) | **None today** | Falco would see it (P2.7), not current sources |
| Lateral Movement | **None** | No east-west visibility |
| Exfiltration | **None** | No network/data-flow signal |
| Defense Evasion via killing the collector | **None** | No absence-of-signal detection (see below) |
| Application-layer DoS surfacing as latency/error spike | **Partial** | Detected as a reliability anomaly, not attributed to an adversary |

This map should be revisited every time a new signal source is added.

---

## Known weaknesses within the current (SRE) scope

These matter **even if you never call this a security tool** — they degrade the
reliability product itself.

### 1. Baseline is keyed on raw labels, not workload identity

`baselineKey()` hashes the sorted label set. If `pod` is among the labels, the
baseline **dies on every pod restart** — pods are cattle, UIDs are ephemeral. The
codebase *has* `correlation.ExtractWorkload()` (Deployment/StatefulSet/DaemonSet from
pod name), but it is used in **correlation**, not in the **baseline key**. The stable
entity exists in the code and is not used where it matters most.

**Impact**: cold-start blindness on every rollout for pod-keyed series.

### 2. EWMA baseline poisoning (low-and-slow drift)

`Evaluate()` updates the EWMA **unconditionally** on every sample, including
anomalous ones (`newEWMA = alpha*value + (1-alpha)*EWMA`). There is no outlier
rejection before the update, no frozen baseline, no suspect-sample quarantine.

**Impact (benign)**: a slow organic traffic ramp is absorbed and stops alerting.
**Impact (adversarial)**: an attacker who ramps activity slowly drags the baseline
until malicious load reads as normal. Fatal in a security context.

### 3. No absence-of-signal ("dead man's switch") detection

`/readyz` probes check the **detector's own dependencies** (Prometheus, Loki, Alertmanager,
ML). There is **no** detection of an *expected* signal going silent — "this workload
always emits N logs/min and stopped" raises nothing. Blinding the pipeline (killing
the collector, filling the disk) is silent.

**Impact**: first move of any competent adversary is undetected; also misses genuine
reliability events (a crashed exporter looks like calm).

### 4. No source-confidence tiering

Container logs (forgeable by the workload) and K8s events are weighted the same as
everything else. K8s events are best-effort (1h TTL, deduplicated, dropped under
load) yet detection is built directly on event reasons (`PatternDetector`). Building
on events is building on sand, undiscounted.

### 5. No clock-skew handling in correlation

Correlation groups by `namespace/pod` within a time window using timestamps as-is.
Node clock skew (event-time vs ingestion-time) degrades grouping; no mitigation.

### 6. The detector is a cluster-wide-read attack surface

`EventWatcher` runs `Events("").Watch` — cluster-wide read across all namespaces. No
IRSA / least-privilege RBAC split yet (roadmap P5.2). No privilege separation between
the observing plane and the observed plane: root the cluster, tamper the detector.

---

## What it does *not* do, on purpose (these are strengths)

| Property | Why it's deliberate |
|----------|---------------------|
| **No automated response** (no pod kill/quarantine) | Explicit anti-goal. A false positive can never turn the detector into a self-DoS. Blast radius of automated action = zero, because there is none. |
| **Dry-run by default** (no real alerts yet, P5.3) | Avoids alert flooding before FP rate is understood. |
| **Complementary, not a replacement** for VMAlert/Falcosidekick | Ships signal to existing tools; does not duplicate native alerting. |
| **Namespace suppression + workload collapse** | ≥3 sibling pods anomalous → 1 workload alert, not N. Reduces fatigue structurally. |

---

## Ground truth: how we know if it works (we mostly don't, yet)

There is **no eval set, no labeled incidents, no red team, no synthetic injection**.
No precision/recall figures exist, and none should be invented.

What *does* exist: **replay mode** (`internal/replay/`) runs detection over
historical data with zero side effects — the substrate for building an eval set. But
ground-truth comparison (TP/FP/FN scoring) is explicitly **V2, out of scope** in the
replay spec. So: the harness to measure exists; the measurement does not.

"Anomalous" is also not separated from "malicious" or even "bad": a node drain and a
deploy are both anomalous and benign. The current mitigation is suppression +
workload collapse, **not** semantic distinction.

---

## Evaluation scorecard

Use this to score this system — or any "AI-powered" detector someone tries to sell
you. A vendor-neutral, reusable version of this scorecard (with the full question set
per axis) lives in [`evaluation-scorecard.md`](evaluation-scorecard.md). The scoring
below applies it to this system.

Each axis: **0 = absent/hand-waved, 1 = partial, 2 = solid with evidence.**

| # | Axis | Question | This system (0.7.0) |
|---|------|----------|---------------------|
| 1 | Threat model | What exactly are you trying to catch? MITRE mapping? | **0** — none; reliability framing only |
| 2 | Source ceiling | What can you *structurally* not see? | **2** — stated honestly (this doc) |
| 3 | Ground truth | Eval set? Precision @ recall on imbalanced data? | **0** — replay harness exists, no labels |
| 4 | Non-stationarity | Drift without alerting on every deploy? Cold start? Stable baseline entity? | **1** — seasonal profile yes; workload-keying + cold-start gap no |
| 5 | Poisoning resistance | Low-and-slow baseline drag prevented? | **0** — unconditional EWMA update |
| 6 | Evasion | Sequence detection? Absence-of-signal detection? | **0** — point-wise only, no silence detection |
| 7 | Entity resolution | Join audit+metric+log on one ephemeral pod? Clock skew? | **1** — window+regex; no owner-ref, no skew handling |
| 8 | Source trust tiering | Forgeable logs vs control-plane treated differently? | **0** — single bucket |
| 9 | FP budget | Quantified alert budget for a large cluster? | **1** — dry-run + suppression, but no number |
| 10 | Automated-response blast radius | Can a FP cause harm? | **2** — no automated action by design |
| 11 | Detector as attack surface | Least privilege? Observer/observed separation? | **0** — cluster-wide read, RBAC split is roadmap |

**Opening question worth more than the rest** (axes 2 + 6 combined): *"List what your
system is structurally incapable of seeing, and explain how you catch an attacker who
only does individually-normal things."* If a vendor answers axis 2 with a feature
list and can't answer axis 6, they packaged thresholds and wrote "AI-powered" in the
README.

---

## Roadmap implications

These limitations map to concrete roadmap items (see `ROADMAP.md`):

- **Workload-identity baseline keying** (weakness #1) — **P2.8**.
- **Outlier rejection before baseline update** (weakness #2) — **P2.9**.
- **Absence-of-signal detection** (weakness #3) — **P2.10**, high-value for SRE
  independent of any security goal.
- **Falco integration** (closes part of the source ceiling) — P2.7,
  `specs/falco-integration/`. Enrichment-only in v1.
- **Least-privilege RBAC + IRSA** (weakness #6) — P5.2, plus the broader hardening
  bundle in [Phase 5 Pre-Reqs](../ROADMAP.md#phase-5-pre-reqs--production-hardening-blocks-phase-5-deploy)
  (PH.1, PH.21, PH.22, PH.23).
- **Ground-truth eval set on replay** — replay-mode V2.

---

## Additional concerns from independent security review (2026-06-16)

The original document was written by the orchestrator reading source. A subsequent
review by the `security` specialist subagent corroborated all 11 scorecard axes
**and** surfaced three concerns that the original framing missed. They are not
weaknesses of the algorithm — they are weaknesses of the **detector as a deployed
system**, and they belong here because each one would be exploitable without anyone
touching the EWMA math.

### A. Supply chain of the detector itself

The original threat-model discusses data-plane attacks (poisoning the baseline,
blinding the pipeline) but **does not address attacks on the detector's own image
and dependencies**. Concrete gaps observed in `controller/Dockerfile`,
`ml/Dockerfile`, and `go.mod`:

- Base images are `golang:1.25-alpine`, `alpine:3.20`, `python:3.11-slim` — **not
  golden apko-built and not cosign-signed**. A compromised upstream image (or its
  registry) ships malicious code into every replica.
- ~~`go.mod` imports `github.com/karlipegomes/staffops-otel-libs/go` at a
  pseudo-version — a personal GitHub repository.~~ **Resolved (2026-07-02, PH.13)**:
  moved to the org module `github.com/staffops/staffops-otel-libs/go` at tagged
  release `v0.1.0`. No longer a personal-account single-point-of-compromise.
- ~~The ML image keeps `gcc/g++` in the runtime layer (single-stage).~~ **Resolved
  (PH.5)**: the ML image is now multi-stage — the runtime layer has no compiler or
  build tooling.
- ~~`grpcio==1.62.1` carries CVE-2024-7246 (DoS).~~ **Resolved (PH.24)**: runtime
  `grpcio` bumped to 1.65.4. A vulnerability-scanning gate still does not exist.

**Mitigation**: PH.5 (multi-stage ML) ✅ and PH.24 (grpcio bump) ✅ are done;
PH.3 (golden bases + cosign) and PH.13 (org rename for the Go dep) remain in
Phase 5 Pre-Reqs.

### B. Redis as a single point of failure for state integrity

`weakness #2` covers slow-and-low EWMA poisoning. It does **not** cover the more
direct attack: **flushing Redis**. With `REDIS_PASSWORD=""` (the default in
`config.yaml` and `.env.example`) and the namespace open to any pod, a foothold
anywhere in `monitoring` namespace can:

- `FLUSHDB` → wipe all baselines → mass cold-start blindness across every
  monitored series for `warm_up_samples` (60) cycles ≈ 30 minutes of pipeline
  blindness.
- Overwrite individual baseline keys to set arbitrary mean/stddev → tailored
  blindness on chosen workloads.
- Manipulate dedup TTLs to suppress alerts that *would have* fired.

This is mass blindness via state tampering, achievable without any algorithm
knowledge.

**Mitigation**: PH.4 (Redis AUTH + file-mounted secret), PH.21 (NetworkPolicy
limiting Redis ingress to controller+worker), and a roadmap candidate not yet
written: integrity protection (HMAC on baseline values, anomaly on impossible
state transitions).

### C. gRPC plaintext between controller, worker, and ML

The controller↔worker and controller↔ML paths are gRPC over plaintext. The
manifests label the namespace `istio.io/dataplane-mode: ambient`, which would
provide ztunnel mTLS — but:

- Ambient enrollment is unverified in deploy. If the namespace is not actually in
  the mesh, the gRPC traffic is plaintext on the cluster network.
- The OTel SDK is wired with gRPC interceptors but no client auth (no JWT, no
  mTLS handshake at the application layer) — the security model is entirely
  delegated to the network plane.

**Mitigation**: explicit verification that ambient is active in the target
namespace; otherwise add `Istio AuthorizationPolicy` or an application-level auth
token. Tracked under broader Phase 5 Pre-Reqs deploy verification (not a separate
PH.x — it is part of validating the deploy itself).

### D. Roadmap items P2.8 / P2.9 / P2.10 — coverage validation

The independent reviewer agreed that the three roadmap items address weaknesses
#1, #2, and #3 with adequate scope. Two refinements were noted:

- **P2.9** should specify whether the frozen-baseline window persists across
  controller restarts (Redis key TTL semantics). Otherwise an attacker times
  their drift across known restart windows.
- **P2.10** should define what "expected cadence" means concretely — last-seen
  timestamp, declared SLO, or both. Without precision the detector reads benign
  KEDA scale-to-zero as silence.

These are spec-level refinements when each item is implemented, not new roadmap
entries.

---

## A note on how this document was produced

This was originally written by the orchestrator reading the source directly. On
**2026-06-16** the `security` specialist subagent reviewed it independently. The
review corroborated all 11 scorecard axes (no inflation detected) and surfaced the
three additional concerns documented in
[Additional concerns from independent security review](#additional-concerns-from-independent-security-review-2026-06-16)
above — supply chain of the detector itself, Redis state integrity, and gRPC
plaintext between components. Treat the scorecard as a code-grounded baseline that
has now received one independent pass; further review by `sre` and a real red team
remains valuable but is out of scope for the current pre-deploy phase.
