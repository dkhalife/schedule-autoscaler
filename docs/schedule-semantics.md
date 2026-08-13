# Schedule semantics

All controller decisions are made at an instant on the UTC timeline. Time zones
only map monthly wall-clock starts onto that timeline.

## Always and validity

`Always` means continuously active, not "ignore validity." The effective
interval is:

```text
[validFrom, validUntil)
```

with an unbounded side when its field is absent. `validFrom` is inclusive;
`validUntil` is exclusive. RFC 3339 offsets in these fields are honored
directly and are not reinterpreted using `timeZone`.

An `Always` schedule with both boundaries is useful for one-off maintenance or
events. Without boundaries it remains active indefinitely.

## Monthly occurrences

For every selected month, each `days` value forms a candidate local date.
Missing dates are skipped: day 31 produces no occurrence in April, and day 29
is absent in non-leap-year February. The controller does not clamp to the last
day of a month.

`months` filters the month in which an occurrence **starts**. Omitted means all
months. The occurrence starts at `startTime` in `timeZone` and remains active
for `durationMinutes` of elapsed time. Validity intersects every occurrence.
If occurrences overlap, the schedule is simply active; it does not produce
multiple rule matches.

## Crossing boundaries

Durations are not clipped at midnight or month/year end. For example:

```yaml
monthly:
  months: [1]
  days: [31]
  startTime: "22:00"
  durationMinutes: 480
```

starts in January and remains active for eight elapsed hours into February.
The `months: [1]` filter applies because the start is in January. A validity
end during the window clips it at that exclusive instant.

## Daylight-saving transitions

The IANA time-zone database determines offset transitions:

- **Nonexistent local start (spring gap):** shift the start forward by the
  size of the gap. A requested 02:30 in a one-hour gap starts at 03:30.
- **Ambiguous local start (fall overlap):** choose the earlier UTC occurrence,
  which normally uses the pre-transition offset.
- **Duration:** count elapsed minutes from the resolved instant. Consequently,
  local end time can appear one hour later after a spring change or one hour
  earlier after a fall change.

These rules produce exactly one occurrence per configured local date and remain
stable across restarts. Pin tested controller images because time-zone database
updates can change historical or future government-defined offsets.

## Rule overlap and baseline

Each active schedule makes its plan rule eligible. The controller sorts only
for selection:

1. highest numeric `priority`;
2. earliest position in `spec.rules` for an equal priority.

Negative priorities are valid. Baseline applies only when no rule is eligible.
It is not an implicit priority-zero rule. Deleting or invalidating the winning
schedule triggers reevaluation; an invalid/unresolved reference makes the plan
not ready and does not silently choose another rule or baseline.

## Evaluation timing

Status transition times are best-effort hints, not cron guarantees. Resource
events enqueue immediately; timers enqueue near calculated boundaries. API
server, leader election, or scheduler delays can make an action late. On the
next reconcile, the controller computes current truth rather than replaying
every missed transition. Design schedules with operational tolerance around
hard external deadlines.
