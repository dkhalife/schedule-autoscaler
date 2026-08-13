# API reference

All resources use `apiVersion: dkhalife.dev/v1alpha1`. Kubernetes
standard `apiVersion`, `kind`, and `metadata` fields are omitted below.
Unknown fields are pruned by the API server. Replica counts are limited to
0–1,000,000.

## Shared schedule specification

`ScaleSchedule.spec` and `ClusterScaleSchedule.spec` have the same fields.
Only scope differs.

| Field | Type / default | Required | Description |
| --- | --- | --- | --- |
| `timeZone` | string / `UTC` | no | IANA location used for monthly wall-clock starts. `UTC`, `Etc/UTC`, and slash-form names such as `America/New_York` pass schema validation; the controller also verifies the name exists. |
| `validFrom` | RFC 3339 date-time | no | Inclusive global activation instant. It is independent of `timeZone`. |
| `validUntil` | RFC 3339 date-time | no | Exclusive global deactivation instant. Must be later than `validFrom` when both exist. |
| `schedule` | object | yes | Calendar discriminator and settings. |
| `schedule.type` | `Always` or `Monthly` | yes | `Always` is active throughout validity. `Monthly` activates occurrences. |
| `schedule.monthly` | object | for `Monthly` | Must be absent for `Always` and present for `Monthly`. |
| `schedule.monthly.months` | unique integer array, 1–12 | no | Restricts occurrence **start months**. Omission means every month. |
| `schedule.monthly.days` | unique integer array, 1–31 | yes | Local day numbers on which occurrences start. A date that does not exist is skipped. |
| `schedule.monthly.startTime` | `HH:mm` | yes | Local 24-hour wall time, validated from `00:00` through `23:59`. |
| `schedule.monthly.durationMinutes` | integer, 1–44,640 | yes | Elapsed length of each occurrence. It may cross date, month, year, or DST boundaries. |

Admission rejects an inverted validity range, invalid enums, duplicate
month/day values, malformed times, and a `type`/`monthly` mismatch.

### Shared schedule status

| Field | Type | Description |
| --- | --- | --- |
| `observedGeneration` | int64 | Latest `metadata.generation` evaluated by the controller. |
| `conditions` | map keyed by `type` | Kubernetes conditions described below; at most 16. |
| `nextTransitionTime` | RFC 3339 date-time | Best-effort next instant this calendar changes state, including validity boundaries. Omitted when no future transition is known. |

Schedule conditions:

| Type | True meaning | Common false reasons |
| --- | --- | --- |
| `Valid` | Time zone and runtime calendar semantics are valid. | `InvalidSchedule` |
| `Ready` | The schedule is valid and available for plan evaluation. | `Invalid`, `ReconcileError` |

## ScaleSchedule

`ScaleSchedule` is namespaced. A plan can reference a `ScaleSchedule` only in
the plan's own namespace. Cross-namespace references are intentionally
unsupported.

## ClusterScaleSchedule

`ClusterScaleSchedule` is cluster-scoped and can be referenced by plans in any
opted-in namespace. Creating a shared schedule does not grant scale permission
in those namespaces.

## DeploymentScalePlan specification

| Field | Type / default | Required | Description |
| --- | --- | --- | --- |
| `targetRef` | object | yes | Deployment target in the plan namespace. |
| `targetRef.apiVersion` | `apps/v1` / `apps/v1` | no | Fixed target API version. |
| `targetRef.kind` | `Deployment` / `Deployment` | no | Fixed target kind. |
| `targetRef.name` | DNS-like string | yes | Deployment name; cross-namespace targets are unsupported. |
| `baseline` | integer >= 0 | yes | Desired replicas whenever no rule is active. |
| `rules` | ordered array, 1–128 | yes | Schedule-to-replica mappings. The array is atomic so ordering is explicit. |
| `rules[].name` | lowercase name, max 63 | yes | Unique rule identifier, also reported in status. |
| `rules[].scheduleRef` | object | yes | Reusable schedule reference. |
| `rules[].scheduleRef.apiGroup` | `dkhalife.dev` / same | no | Fixed API group. |
| `rules[].scheduleRef.kind` | `ScaleSchedule` or `ClusterScaleSchedule` | yes | Selects namespaced or cluster reference resolution. |
| `rules[].scheduleRef.name` | DNS-like string | yes | Referenced schedule name. |
| `rules[].scheduleRef.namespace` | empty string only | no | Reserved for forward compatibility. It must not be set; namespaced references resolve locally. |
| `rules[].replicas` | integer >= 0 | yes | Desired replicas while the rule wins. Zero is allowed. |
| `rules[].priority` | integer / `0` | no | Higher priority wins overlap. Equal priority is resolved by earlier array position. |
| `suspension` | object / disabled | no | Explicit operator override. |
| `suspension.enabled` | boolean / `false` | when object set | Stops ordinary schedule-driven writes. |
| `suspension.behavior` | `HoldCurrent` / `HoldCurrent`, or `RestoreBaseline` | when object set | `HoldCurrent` performs no scale write. `RestoreBaseline` writes baseline once and then holds. |
| `suspension.reason` | string, max 256 | no | Human-readable/operator-facing context. It has no control semantics. |

Rule names must be unique. Admission fixes target type/group and prevents
cross-namespace schedule references. The controller treats an unresolved
schedule as a plan error rather than ignoring that rule.

### DeploymentScalePlan status

| Field | Type | Description |
| --- | --- | --- |
| `observedGeneration` | int64 | Latest plan generation fully evaluated. |
| `conditions` | map keyed by `type` | Kubernetes conditions described below; at most 16. |
| `activeRule` | string | Winning rule name; empty/omitted while baseline applies. |
| `desiredReplicas` | int32 | Replica intent computed at the last evaluation. |
| `currentReplicas` | int32 | Target scale value read at the last evaluation. |
| `lastEvaluationTime` | date-time | Most recent completed evaluation attempt. |
| `nextEvaluationTime` | date-time | Best-effort next transition-driven evaluation. Changes still trigger immediate reconciliation. |
| `lastScaleTime` | date-time | Last successful write to the target scale subresource. |
| `lastAppliedReplicas` | int32 | Value written by the last successful scale action. |

Plan conditions:

| Type | True meaning | Common false reasons |
| --- | --- | --- |
| `Ready` | Plan resolved, evaluated, and its desired state is applied or already present. | `TargetNotFound`, `ScheduleNotFound`, `InvalidSchedule`, `ScaleFailed`, `PermissionDenied` |
| `TargetFound` | The referenced Deployment exists. | `NotFound`, `ReadFailed` |
| `SchedulesResolved` | Every rule reference exists and is valid. | `ScheduleNotFound`, `ScheduleInvalid` |
| `ScalingAllowed` | Namespace opt-in and scale RBAC permit operation. | `NamespaceNotEnabled`, `PermissionDenied` |
| `Suspended` | Suspension is enabled and its selected behavior has taken effect. | `NotSuspended`, `RestoreFailed` |

Conditions follow Kubernetes conventions: `status` is `"True"`, `"False"`, or
`Unknown`; `reason` is stable machine-readable text; `message` is human
context; `lastTransitionTime` changes only when status changes; and optional
`observedGeneration` identifies the evaluated generation. Consumers should
key automation on type/status/reason, not message text.
