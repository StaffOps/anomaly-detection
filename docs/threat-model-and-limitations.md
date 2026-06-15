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
| `metrics` | VictoriaMetrics (PromQL) | static thresholds + adaptive EWMA/Z-score |
| `logs` | Loki (LogQL) | log-rate patterns |
| `events` | K8s API (`Events("").Watch`) | event-reason patterns |

A fourth signal — `security` via **Falco** — is **specced but not implemented**
(see `.kiro/specs/falco-integration/`, roadmap P2.7). Even when shipped, its v1 is
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

`/readyz` probes check the **detector's own dependencies** (VM, Loki, Alertmanager,
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
  `.kiro/specs/falco-integration/`. Enrichment-only in v1.
- **Least-privilege RBAC + IRSA** (weakness #6) — P5.2.
- **Ground-truth eval set on replay** — replay-mode V2.

---

## A note on how this document was produced

This was written by the orchestrator reading the source directly, **not** via the
specialist subagent fan-out (the subagent tool is recorded as non-functional in this
environment — see `ROADMAP.md` decision log, 2026-05-30). The MITRE mapping and the
poisoning/silence findings would benefit from independent review by `security` and
`sre` specialists once that tooling is available; treat the scorecard as a
first-pass, code-grounded baseline, not an audited verdict.
