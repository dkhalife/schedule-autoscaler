# Schedule Autoscaler documentation

Schedule Autoscaler reconciles reusable calendars into the `scale` subresource
of Kubernetes Deployments.

| Guide | Purpose |
| --- | --- |
| [Architecture and reconciliation](architecture.md) | Components, ownership, reconciliation, and failure behavior |
| [Installation and lifecycle](installation.md) | Helm/raw install, permissioning, upgrades, and safe removal |
| [API reference](api-reference.md) | Every field and reported condition |
| [Schedule semantics](schedule-semantics.md) | Time zones, validity, DST, missing dates, and overlap |
| [Examples and recipes](recipes.md) | Common deployment patterns and operator actions |
| [Operations](operations.md) | Health, metrics, events, troubleshooting, and runbooks |

## API compatibility

The API is `dkhalife.dev/v1alpha1`. Alpha resources can evolve. Pin the
controller and chart to the same release, inspect release notes, and apply CRD
updates before upgrading the controller.
