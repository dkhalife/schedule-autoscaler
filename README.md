# Schedule Autoscaler

Schedule Autoscaler gives Kubernetes Deployments predictable capacity before
traffic arrives. Reuse timezone-aware calendars across workloads, set a safe
baseline, and express precedence without CronJobs that race the HPA or each
other.

It provides:

- namespace and cluster reusable schedules;
- deterministic overlap, validity, month-boundary, and DST behavior;
- explicit per-namespace opt-in and least-privilege scale access; and
- status, structured logs, health probes, metrics, and leader election.

## Install

Kubernetes 1.26+ is required for CRD CEL validation.

```sh
helm upgrade --install schedule-autoscaler \
  ./charts/schedule-autoscaler \
  --namespace schedule-autoscaler-system --create-namespace
kubectl apply -f examples/namespace-opt-in.yaml
kubectl apply -f examples/basic.yaml
```

The example keeps `storefront/checkout` at 2 replicas and raises it to 8 from
08:00–18:00 local time on the first three days of every month.

```sh
kubectl -n storefront get deploymentscaleplans
```

See the [documentation index](docs/README.md) for installation, API semantics,
recipes, operations, upgrades, and safe removal.

## Go module

Tagged releases are available through the standard Go module proxy:

```sh
go get dkhalife.dev/schedule-autoscaler@latest
```
