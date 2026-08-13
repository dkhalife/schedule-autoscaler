# Examples and recipes

Examples assume the Helm release and service account are both named
`schedule-autoscaler`.

## First namespace

The opt-in manifest creates and labels `storefront`; the controller then binds
only that namespace's Deployment scale subresources:

```sh
kubectl apply -f examples/namespace-opt-in.yaml
kubectl apply -f examples/basic.yaml
kubectl -n storefront get scaleschedule,deploymentscaleplan
kubectl -n storefront describe deploymentscaleplan checkout
```

`examples/basic.yaml` demonstrates a baseline of 2 and capacity of 8 during a
monthly local-time window.

## Shared one-off window

An `Always` schedule plus validity represents a globally reusable event:

```sh
kubectl apply -f examples/cluster-schedule.yaml
```

The maintenance rule has priority 100, so it beats the priority-10 capacity
rule and scales to zero only during its bounded interval. Treat cluster
schedules as shared infrastructure API: use RBAC to limit who may edit them.

## Month-end processing

`examples/month-crossing.yaml` starts at 22:00 on selected 31st days and runs
eight hours across the month boundary. It intentionally does not run in months
without day 31. Create the referenced Deployment before applying the plan:

```sh
kubectl -n storefront create deployment month-end-worker \
  --image=registry.k8s.io/pause:3.10
kubectl apply -f examples/month-crossing.yaml
```

For "last day of every month," enumerate separate date-specific bounded
`Always` schedules or update a shared event schedule through GitOps; `days:
[31]` does not mean last day.

## Suspend during an incident

Hold the current scale without schedule writes:

```sh
kubectl patch deploymentscaleplan checkout -n storefront --type=merge -p \
  '{"spec":{"suspension":{"enabled":true,"behavior":"HoldCurrent","reason":"incident-123"}}}'
kubectl scale deployment checkout -n storefront --replicas=12
```

Resume:

```sh
kubectl patch deploymentscaleplan checkout -n storefront --type=merge -p \
  '{"spec":{"suspension":{"enabled":false,"behavior":"HoldCurrent"}}'
```

The next reconciliation immediately reapplies the current rule or baseline.
Use `RestoreBaseline` before deleting a plan or handing replica ownership back
to a human:

```sh
kubectl patch deploymentscaleplan checkout -n storefront --type=merge -p \
  '{"spec":{"suspension":{"enabled":true,"behavior":"RestoreBaseline","reason":"handoff"}}'
```

## Precedence layers

A useful priority convention:

- `1000+`: emergency/maintenance shutdown;
- `100–999`: short-lived events;
- `1–99`: regular business cycles;
- negative: fallback windows that should lose to normal schedules.

Priorities are local to a plan. Reordering equal-priority rules can change the
winner, so review list-only diffs carefully.

## GitOps

Keep namespace label, RoleBinding, schedules, and plans in the same environment
overlay. Apply in this order: CRDs, controller, namespace permissions,
schedules, then plans. During deletion reverse plan ownership first:
suspend/restore, delete plans, delete schedules, then remove permissions.

Use server-side dry-run to exercise OpenAPI/CEL validation:

```sh
kubectl apply --dry-run=server -f examples/basic.yaml
```
