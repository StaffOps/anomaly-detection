# Anomaly / Threat Detection Evaluation Scorecard

A vendor-neutral instrument for scoring **any** anomaly-detection or
runtime-detection system — your own, or one being sold to you. It is built to surface
hand-waving: the axes that vendors skip are the ones that decide whether the thing
actually works in a real cluster.

> **How to use**: score each axis **0 / 1 / 2**. Don't average to a single number —
> a system can score 2 on six axes and still be useless if it scores 0 on axis 2 or
> 6. Read the profile, not the sum.

| Score | Meaning |
|-------|---------|
| **0** | Absent, or answered with hand-waving / a feature list |
| **1** | Partial — exists but with a material gap, or incidental rather than designed-for |
| **2** | Solid, with evidence (numbers, a doc, a reproducible test) |

---

## The axes

### 1. Threat / problem model
*"What exactly are you trying to catch, and what are you explicitly not?"*
Cryptomining, container escape, lateral movement, exfil, credential theft — each
leaves a different signature. For a security tool, demand a MITRE ATT&CK mapping. For
a reliability tool, demand the equivalent: which failure modes, which not.
**0** if the answer is "anomalies." That's not a model.

### 2. Source ceiling (the deciding axis)
*"List what your system is structurally incapable of seeing."*
Every system's sources impose a hard ceiling. Metrics/logs/events/audit cannot see
syscalls, in-memory activity, kernel exploits, or east-west L3/L4 traffic. A credible
answer names its own blind spots before you do.
**0** if they claim coverage the sources cannot physically provide (e.g. "detects
container escape" with only Prometheus + Loki).

### 3. Ground truth
*"Where's your eval set, and what's your precision at what recall?"*
Anomaly detection is hard to evaluate because labels are rare. Demand: labeled
incidents, synthetic injection, or continuous red team (Atomic Red Team, kube-hunter).
Reject **accuracy** as a metric — on imbalanced data it's a lie. Demand
precision/recall.
**0** if there are no labels and the numbers are asserted, not measured.

### 4. Non-stationarity & cold start
*"How do you avoid alerting on every deploy, and what's your blind window for new
workloads?"*
K8s workloads are non-stationary: deploys, HPA/VPA, autoscaler, cronjobs, day/night
cycles. Ask: concept-drift handling, cold-start blind time, and **which stable entity
is baselined** — pod (ephemeral, wrong), or Deployment/image/workload-identity
(right).
**1** if seasonality is handled but the baseline entity is the pod.

### 5. Poisoning resistance
*"Can a low-and-slow actor drag your baseline until their activity is normal?"*
If the baseline updates unconditionally on every sample, yes. Demand outlier
rejection before update, frozen-baseline windows, or suspect-sample quarantine. This
also bites benign: a slow organic ramp silently disappears.
**0** if the baseline absorbs anomalous samples without gating.

### 6. Evasion (the security lens)
*"How do you catch an attacker who only does individually-normal things, and do you
detect the silence when someone blinds you?"*
Two sub-questions: (a) **sequence vs point** — living-off-the-land uses only
legitimate primitives; do you model the sequence or just the point? (b)
**absence-of-signal** — killing the collector / filling the disk / dropping the audit
sink is the first move of any competent adversary. A detector that watches noise but
not silence is useless against someone who knows what they're doing.
**0** if point-wise only with no dead-man's-switch.

### 7. Entity resolution & clock
*"How do you join an audit line + a metric series + a log line onto the same
ephemeral pod, across clock skew?"*
Owner-ref chain vs name regex. Event-time vs ingestion-time. 30s of node clock skew
destroys correlation. Also: K8s events are best-effort (1h TTL, deduplicated, dropped
under load) — building detection on events is building on sand.
**1** if correlation works but uses name heuristics and ignores skew.

### 8. Source-trust tiering
*"Do you weight a forgeable container log the same as a control-plane audit record?"*
Container logs are forgeable by the workload. Audit is control-plane, more
trustworthy — but depends on the audit policy (Metadata vs RequestResponse level),
and on **who controls that policy**. Whoever owns the API-server config can blind a
detector that trusts audit blindly.
**0** if everything goes in one bucket with equal weight.

### 9. False-positive budget
*"What's your alert budget for a large cluster?"*
At scale, 1% FP = thousands of alerts/day = alert fatigue = the system gets muted =
it's dead. Demand a quantified budget and the structural mechanisms that hold it
(suppression, dedup, workload collapse, dry-run rollout).
**1** if there are mechanisms but no number.

### 10. Automated-response blast radius
*"If you auto-remediate, what does a false positive cost?"*
A detector that kills/cordons/quarantines on a false positive becomes its own DoS.
Demand the blast radius of any automated action and the kill-switch. **No automated
action at all is a legitimate 2** — signal to humans/existing tools, zero blast
radius.

### 11. Detector as attack surface
*"Does the thing protecting the cluster have least privilege, and is the observer
separated from the observed?"*
A detector needs broad read access — it's a juicy target. Demand least-privilege RBAC
/ IRSA, and privilege separation between the observing plane and the observed plane.
If rooting the cluster lets the attacker tamper the detector, the detector is theater
once they're in.
**0** if it runs cluster-wide read with no privilege separation.

---

## The 30-second opening question

If you only get to ask one thing, combine **axis 2 + axis 6**:

> *"List what your system is structurally incapable of seeing, and explain how you
> catch an attacker who only does individually-normal things."*

The answer tells you in 30 seconds whether you're talking to someone who built the
thing, or someone who packaged a few thresholds and wrote "AI-powered" in the README.

---

## Scoring template

```
System under evaluation: ____________________   Date: __________   Version: _______

 1. Threat/problem model        [ 0 / 1 / 2 ]  notes: ____________________
 2. Source ceiling              [ 0 / 1 / 2 ]  notes: ____________________
 3. Ground truth                [ 0 / 1 / 2 ]  notes: ____________________
 4. Non-stationarity/cold start [ 0 / 1 / 2 ]  notes: ____________________
 5. Poisoning resistance        [ 0 / 1 / 2 ]  notes: ____________________
 6. Evasion (seq + silence)     [ 0 / 1 / 2 ]  notes: ____________________
 7. Entity resolution & clock   [ 0 / 1 / 2 ]  notes: ____________________
 8. Source-trust tiering        [ 0 / 1 / 2 ]  notes: ____________________
 9. FP budget                   [ 0 / 1 / 2 ]  notes: ____________________
10. Automated-response radius   [ 0 / 1 / 2 ]  notes: ____________________
11. Detector as attack surface  [ 0 / 1 / 2 ]  notes: ____________________

Hard-fail axes (any 0 here = do not trust as a security tool): 2, 6, 11
Profile (not sum): ____________________
```

---

For a worked example of this scorecard applied to a real system, see
[`threat-model-and-limitations.md`](threat-model-and-limitations.md) (the
staffops-anomaly-detection self-assessment).
