# Installation and lifecycle

## Requirements

- Kubernetes 1.26 or newer (CEL validation in CRDs)
- cluster-admin access to install CRDs and cluster roles
- access to the controller image
- IANA time-zone data in custom images

No admission webhook or certificate manager is required. Structural OpenAPI
schemas and CEL rules validate the API at the Kubernetes API server.

## Helm

```sh
helm upgrade --install schedule-autoscaler \
  ./charts/schedule-autoscaler \
  --namespace schedule-autoscaler-system \
  --create-namespace \
  --set image.repository=ghcr.io/dkhalife/schedule-autoscaler \
  --set image.tag=v0.1.0
```

Important values:

| Value | Default | Meaning |
| --- | --- | --- |
| `replicaCount` | `2` | Controller replicas; leader election permits one active writer |
| `controller.leaderElect` | `true` | Use a Lease for active/passive operation |
| `controller.namespaceSelector` | `scaling.dkhalife.dev/enabled=true` | Label selector limiting managed namespaces |
| `controller.logLevel` | `info` | Structured log threshold |
| `rbac.create` | `true` | Create controller ClusterRole and binding |
| `rbac.createDeploymentScaleEditor` | `true` | Create the fixed, unbound scale editor ClusterRole |
| `serviceAccount.*` | chart-managed | Controller identity |
| `metrics.service.enabled` | `true` | Expose port 8080 inside the cluster |
| `resources` | small requests/limits | Pod resources |

The chart grants no cluster-wide scale permission. The controller
creates/removes a narrow RoleBinding in labeled namespaces.

## Raw manifests

```sh
kubectl apply -k config/default
```

The raw manifest uses
`ghcr.io/dkhalife/schedule-autoscaler:v0.1.0`; pin or replace that image for
your release process.

## Opt in a namespace

```sh
kubectl label namespace storefront scaling.dkhalife.dev/enabled=true
```

The label controls discovery and reconciliation. The namespace reconciler
creates a managed RoleBinding that grants the controller identity `get`,
`update`, and `patch` on `deployments/scale` only in that namespace. Its base
ClusterRole cannot change replicas. For multiple controller installations,
create `deployment-scale-editor` once and set
`rbac.createDeploymentScaleEditor=false` on later releases.

To opt out, first suspend plans or choose a final replica count, then remove
the label. The controller removes its managed RoleBinding:

```sh
kubectl label namespace storefront scaling.dkhalife.dev/enabled-
```

## Upgrades

Helm does not upgrade files in a chart's `crds/` directory. Apply CRDs first,
wait for them to become Established, and then upgrade the controller:

```sh
kubectl apply --server-side \
  -f config/crd/bases/dkhalife.dev_schedules.yaml
kubectl wait --for=condition=Established --timeout=60s \
  crd/clusterscaleschedules.dkhalife.dev \
  crd/scaleschedules.dkhalife.dev \
  crd/deploymentscaleplans.dkhalife.dev
helm upgrade schedule-autoscaler ./charts/schedule-autoscaler \
  --namespace schedule-autoscaler-system --reuse-values
```

Back up resources before alpha API upgrades:

```sh
kubectl get clusterscaleschedules -o yaml > cluster-schedules.yaml
kubectl get scaleschedules,deploymentscaleplans -A -o yaml > plans.yaml
```

Do not downgrade across an API/storage migration unless that release explicitly
supports it.

## Safe removal

Deleting a plan does not restore baseline because no controller remains to own
the target. For each plan, choose one:

1. Set `spec.suspension.enabled=true` and
   `spec.suspension.behavior=RestoreBaseline`; wait for `Suspended=True` and
   verify Deployment replicas.
2. Scale the Deployment manually to its intended post-controller value while
   using `HoldCurrent`.

Then remove plans/schedules, remove namespace labels and RoleBindings, and
uninstall:

```sh
helm uninstall schedule-autoscaler -n schedule-autoscaler-system
```

CRDs are retained by Helm to avoid deleting user data. Delete them only after
confirming no resources remain:

```sh
kubectl delete crd \
  clusterscaleschedules.dkhalife.dev \
  scaleschedules.dkhalife.dev \
  deploymentscaleplans.dkhalife.dev
```
