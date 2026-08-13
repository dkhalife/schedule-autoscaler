# Architecture and reconciliation

## Model

Three resources separate **when** from **what**:

- `ScaleSchedule` is a reusable calendar visible only in its namespace.
- `ClusterScaleSchedule` is a reusable cluster-wide calendar.
- `DeploymentScalePlan` targets one Deployment in its own namespace, defines a
  baseline, and maps schedules to replica counts.

The controller is cluster-installed but namespace participation is explicit.
It observes only namespaces labeled
`scaling.dkhalife.dev/enabled=true`. Its base ClusterRole can read Deployments
and schedules and update custom-resource status, but cannot scale. A
managed namespace-local RoleBinding to the fixed `deployment-scale-editor`
ClusterRole grants access to `deployments/scale`.

## Reconciliation

For each plan, the controller:

1. Rejects or ignores work outside an opted-in namespace.
2. Resolves the target Deployment and every schedule reference from the
   informer cache.
3. Evaluates each schedule at a single UTC instant using that schedule's IANA
   time zone and validity interval.
4. Selects active rules by highest `priority`, then earliest position in
   `spec.rules`.
5. Chooses the winning rule's replicas, or `spec.baseline` when no rule wins.
6. Applies suspension behavior.
7. Reads the Deployment scale subresource and writes only when the desired
   value differs.
8. Updates status. It requeues for the earliest known schedule/validity
   transition and also reacts to resource changes.

Reconciliation is level-based and idempotent. Leader election uses a
`coordination.k8s.io/Lease`; multiple replicas provide availability but only
the leader writes. Status is informational and never the source of desired
state.

## Ownership and conflicts

A plan owns the target's scheduled replica intent. Only one plan should target
a Deployment. Admission cannot enforce that cross-object invariant; operators
must avoid duplicates. Do not combine a plan with an HPA, KEDA ScaledObject, or
another replica writer unless deliberate last-writer-wins behavior is
acceptable. A VerticalPodAutoscaler that does not change replicas is
compatible.

Manual `kubectl scale` changes are corrected at the next reconciliation unless
the plan uses `HoldCurrent` suspension. Changes to a target pod template do not
affect plan operation.

## Failure behavior

Invalid shapes are rejected by CRD OpenAPI/CEL admission. Runtime failures
(missing references, missing target, unavailable time-zone data, or denied
RBAC) leave the Deployment unchanged, set a non-ready condition, log context,
and retry with backoff. A missing or invalid rule never silently
falls back to a potentially unsafe replica count. Controller restart and
leader change recompute from declarative state; missed transitions do not need
replay.
