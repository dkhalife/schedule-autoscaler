# Operations

## Health and availability

The controller exposes:

- `GET :8081/healthz` — process/liveness health;
- `GET :8081/readyz` — process readiness;
- `:8080/metrics` — Prometheus text metrics.

The Deployment configures both probes and uses two replicas plus leader
election by default. Controller-runtime starts probes with the manager process.
Only the Lease holder performs writes. Inspect leader state with:

```sh
kubectl -n schedule-autoscaler-system get lease
kubectl -n schedule-autoscaler-system get pods
```

The metrics Service is cluster-internal and has no authentication or TLS.
Apply NetworkPolicy or disable it when unneeded. Add a ServiceMonitor externally
if using Prometheus Operator; the chart does not require its CRDs.

## Metrics, logs, and status

Monitor controller-runtime reconciliation/workqueue metrics and these domain
signals when present in the running release:

- reconcile errors and duration by controller;
- active workqueue depth and oldest item age;
- plan desired/current replicas;
- scale action/error counters;
- leader-election changes and process restarts.

Metric names can change during `v1alpha1`; discover the exact release surface:

```sh
kubectl -n schedule-autoscaler-system port-forward \
  service/schedule-autoscaler-metrics 8080:8080
curl http://127.0.0.1:8080/metrics
```

Logs are structured. Filter by namespace, plan, target, schedule, and reconcile
result fields rather than message text. Status conditions are the durable
operator signal:

```sh
kubectl -n storefront describe deploymentscaleplan checkout
```

Alert on a sustained `Ready=False`, repeated scale errors, absent leader,
reconcile backlog, or a difference between `status.desiredReplicas` and the
Deployment scale value beyond normal reconcile latency.

## Troubleshooting

### Plan is absent from controller activity

1. Verify namespace label spelling and value:
   `kubectl get ns storefront --show-labels`.
2. Confirm the namespace is selected by
   `scaling.dkhalife.dev/enabled=true`.
3. Check controller logs after label changes; informer scope updates can require
   cache resynchronization.

### `ScalingAllowed=False` or forbidden errors

Check the controller-managed RoleBinding subject and effective authorization:

```sh
kubectl -n storefront get rolebinding schedule-autoscaler -o yaml
kubectl auth can-i patch deployments/scale -n storefront \
  --as=system:serviceaccount:schedule-autoscaler-system:schedule-autoscaler
```

The answer should be `yes` only in explicitly bound namespaces. Ensure the
fixed `deployment-scale-editor` ClusterRole exists. Do not add scale verbs to
the controller's broad ClusterRole as a shortcut.

### `TargetFound=False`

The target must be an `apps/v1` Deployment in the plan namespace and the name
is case-sensitive. Verify `kubectl -n <ns> get deployment <name>`. A plan cannot
target a StatefulSet or cross namespaces.

### `SchedulesResolved=False`

Check kind, name, and scope. `ScaleSchedule` must be in the plan namespace;
`ClusterScaleSchedule` has no namespace. Inspect the referenced schedule's
`Valid`/`Ready` conditions. Schema validation cannot confirm that an accepted
IANA-looking time-zone string is installed.

### Replicas keep changing

Find competing writers: HPA, KEDA, another plan, GitOps applying
`spec.replicas`, or a human. Check managed fields and related autoscalers:

```sh
kubectl -n storefront get deployment checkout -o yaml
kubectl -n storefront get hpa
kubectl -n storefront get deploymentscaleplans
```

Suspend the plan with `HoldCurrent` while resolving ownership.

### Unexpected transition near DST/month end

Read [schedule semantics](schedule-semantics.md). Confirm image time-zone data,
the schedule's IANA name, missing dates, the start-month filter, validity
offsets, and elapsed duration. Compare status timestamps in UTC before
converting for display.

### Controller not ready

Describe the pod and inspect logs. Common causes are missing CRDs, denied
list/watch permissions, unavailable API server, or Lease permission. Confirm
CRDs are Established and the controller ClusterRoleBinding points to the actual
service account.

## Recovery

The API objects are the recovery source. Restore CRDs first, then schedule and
plan objects, namespace labels/RoleBindings, and finally controller replicas.
On startup the leader recomputes current desired state; no timer database or
transition history needs restoration. Status can be discarded and rebuilt.

For emergency stop without changing workloads, remove the namespace label or
scale controller replicas to zero. For deliberate handoff, prefer plan
suspension and the safe-removal procedure in
[installation](installation.md#safe-removal).
