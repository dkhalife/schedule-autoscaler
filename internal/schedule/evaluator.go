// Package schedule evaluates schedule specifications without Kubernetes dependencies.
package schedule

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	v1alpha1 "dkhalife.dev/schedule-autoscaler/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Result is the state of a reusable calendar at a particular instant.
type Result struct {
	Active         bool
	NextTransition *time.Time
}

// Evaluate evaluates spec at now. Ambiguous local starts use the earlier UTC
// occurrence. Nonexistent local starts shift forward by the DST gap.
func Evaluate(spec v1alpha1.ScheduleSpec, now time.Time) (Result, error) {
	loc, startWall, months, err := validate(spec)
	if err != nil {
		return Result{}, err
	}
	now = now.UTC()
	inRange := (spec.ValidFrom == nil || !now.Before(spec.ValidFrom.Time.UTC())) &&
		(spec.ValidUntil == nil || now.Before(spec.ValidUntil.Time.UTC()))
	var boundaries []time.Time
	if spec.ValidFrom != nil && spec.ValidFrom.Time.After(now) {
		boundaries = append(boundaries, spec.ValidFrom.Time.UTC())
	}
	if spec.ValidUntil != nil && spec.ValidUntil.Time.After(now) {
		boundaries = append(boundaries, spec.ValidUntil.Time.UTC())
	}

	switch spec.Schedule.Type {
	case v1alpha1.WindowAlways:
		return Result{Active: inRange, NextTransition: earliest(boundaries)}, nil
	case v1alpha1.WindowMonthly:
		active := false
		localYear := now.In(loc).Year()
		for year := localYear - 1; year <= localYear+8; year++ {
			for _, month := range months {
				for _, day := range spec.Schedule.Monthly.Days {
					start, ok := occurrenceStart(year, time.Month(month), int(day), startWall, loc)
					if !ok {
						continue
					}
					end := start.Add(time.Duration(spec.Schedule.Monthly.DurationMinutes) * time.Minute)
					effectiveStart, effectiveEnd, ok := intersect(start.UTC(), end.UTC(), spec.ValidFrom, spec.ValidUntil)
					if !ok {
						continue
					}
					if effectiveStart.After(now) {
						boundaries = append(boundaries, effectiveStart)
					}
					if effectiveEnd.After(now) {
						boundaries = append(boundaries, effectiveEnd)
					}
					if !now.Before(effectiveStart) && now.Before(effectiveEnd) {
						active = true
					}
				}
			}
		}
		return Result{Active: active && withinValidity(now, spec), NextTransition: earliest(boundaries)}, nil
	default:
		panic("validated schedule type became invalid")
	}
}

type wallTime struct{ hour, minute int }

func validate(spec v1alpha1.ScheduleSpec) (*time.Location, wallTime, []int, error) {
	timeZone := spec.TimeZone
	if timeZone == "" {
		timeZone = "UTC"
	}
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return nil, wallTime{}, nil, fmt.Errorf("invalid timeZone %q: %w", timeZone, err)
	}
	if spec.ValidFrom != nil && spec.ValidUntil != nil && !spec.ValidFrom.Time.Before(spec.ValidUntil.Time) {
		return nil, wallTime{}, nil, errors.New("validFrom must be before validUntil")
	}
	switch spec.Schedule.Type {
	case v1alpha1.WindowAlways:
		if spec.Schedule.Monthly != nil {
			return nil, wallTime{}, nil, errors.New("monthly must be omitted for Always")
		}
		return loc, wallTime{}, nil, nil
	case v1alpha1.WindowMonthly:
		monthly := spec.Schedule.Monthly
		if monthly == nil {
			return nil, wallTime{}, nil, errors.New("monthly is required")
		}
		if monthly.DurationMinutes < 1 || monthly.DurationMinutes > 44640 {
			return nil, wallTime{}, nil, errors.New("durationMinutes must be between 1 and 44640")
		}
		start, err := parseWallTime(monthly.StartTime)
		if err != nil {
			return nil, wallTime{}, nil, err
		}
		months, err := validateSet(monthly.Months, 1, 12, true, "months")
		if err != nil {
			return nil, wallTime{}, nil, err
		}
		if len(months) == 0 {
			for month := 1; month <= 12; month++ {
				months = append(months, month)
			}
		}
		if _, err := validateSet(monthly.Days, 1, 31, false, "days"); err != nil {
			return nil, wallTime{}, nil, err
		}
		return loc, start, months, nil
	default:
		return nil, wallTime{}, nil, fmt.Errorf("unknown schedule type %q", spec.Schedule.Type)
	}
}

func validateSet(values []int32, minimum, maximum int, optional bool, name string) ([]int, error) {
	if len(values) == 0 && !optional {
		return nil, fmt.Errorf("%s must not be empty", name)
	}
	seen := map[int32]struct{}{}
	result := make([]int, 0, len(values))
	for _, value := range values {
		if int(value) < minimum || int(value) > maximum {
			return nil, fmt.Errorf("%s value %d is out of range", name, value)
		}
		if _, ok := seen[value]; ok {
			return nil, fmt.Errorf("%s contains duplicate %d", name, value)
		}
		seen[value] = struct{}{}
		result = append(result, int(value))
	}
	sort.Ints(result)
	return result, nil
}

func parseWallTime(value string) (wallTime, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return wallTime{}, fmt.Errorf("invalid startTime %q", value)
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return wallTime{}, fmt.Errorf("invalid startTime %q", value)
	}
	return wallTime{hour: hour, minute: minute}, nil
}

func occurrenceStart(year int, month time.Month, day int, wall wallTime, loc *time.Location) (time.Time, bool) {
	date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if date.Month() != month || date.Day() != day {
		return time.Time{}, false
	}
	requested := time.Date(year, month, day, wall.hour, wall.minute, 0, 0, time.UTC)
	if candidates := exactCandidates(requested, loc); len(candidates) > 0 {
		return candidates[0], true
	}

	// Locate offsets immediately around the local gap, then preserve the
	// requested minutes by shifting the wall time by the offset change.
	var beforeOffset, afterOffset int
	foundBefore, foundAfter := false, false
	for minutes := 1; minutes <= 48*60 && (!foundBefore || !foundAfter); minutes++ {
		if !foundBefore {
			candidates := exactCandidates(requested.Add(-time.Duration(minutes)*time.Minute), loc)
			if len(candidates) > 0 {
				_, beforeOffset = candidates[len(candidates)-1].In(loc).Zone()
				foundBefore = true
			}
		}
		if !foundAfter {
			candidates := exactCandidates(requested.Add(time.Duration(minutes)*time.Minute), loc)
			if len(candidates) > 0 {
				_, afterOffset = candidates[0].In(loc).Zone()
				foundAfter = true
			}
		}
	}
	if !foundBefore || !foundAfter || afterOffset <= beforeOffset {
		return time.Time{}, false
	}
	shifted := requested.Add(time.Duration(afterOffset-beforeOffset) * time.Second)
	candidates := exactCandidates(shifted, loc)
	if len(candidates) == 0 {
		return time.Time{}, false
	}
	return candidates[0], true
}

func exactCandidates(localFields time.Time, loc *time.Location) []time.Time {
	offsets := map[int]struct{}{}
	for hour := -36; hour <= 36; hour++ {
		_, offset := localFields.Add(time.Duration(hour) * time.Hour).In(loc).Zone()
		offsets[offset] = struct{}{}
	}
	var result []time.Time
	for offset := range offsets {
		candidate := localFields.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(loc)
		if local.Year() == localFields.Year() && local.Month() == localFields.Month() &&
			local.Day() == localFields.Day() && local.Hour() == localFields.Hour() &&
			local.Minute() == localFields.Minute() && local.Second() == 0 {
			result = append(result, candidate.UTC())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Before(result[j]) })
	return result
}

func intersect(start, end time.Time, from, until *metav1.Time) (time.Time, time.Time, bool) {
	if from != nil && start.Before(from.Time.UTC()) {
		start = from.Time.UTC()
	}
	if until != nil && end.After(until.Time.UTC()) {
		end = until.Time.UTC()
	}
	return start, end, end.After(start)
}

func withinValidity(now time.Time, spec v1alpha1.ScheduleSpec) bool {
	return (spec.ValidFrom == nil || !now.Before(spec.ValidFrom.Time.UTC())) &&
		(spec.ValidUntil == nil || now.Before(spec.ValidUntil.Time.UTC()))
}

func earliest(values []time.Time) *time.Time {
	if len(values) == 0 {
		return nil
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Before(values[j]) })
	value := values[0].UTC()
	return &value
}
