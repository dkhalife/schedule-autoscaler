package schedule

import (
	"testing"
	"time"

	v1alpha1 "dkhalife.dev/schedule-autoscaler/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEvaluateMonthAndYearCrossing(t *testing.T) {
	spec := monthly("UTC", []int32{12}, []int32{31}, "22:00", 480)
	now := time.Date(2027, 1, 1, 1, 0, 0, 0, time.UTC)
	got, err := Evaluate(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active {
		t.Fatal("expected year-crossing occurrence to be active")
	}
	want := time.Date(2027, 1, 1, 6, 0, 0, 0, time.UTC)
	if got.NextTransition == nil || !got.NextTransition.Equal(want) {
		t.Fatalf("next = %v, want %v", got.NextTransition, want)
	}
}

func TestEvaluateValidityIntersection(t *testing.T) {
	from := metav1.NewTime(time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC))
	until := metav1.NewTime(time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC))
	spec := v1alpha1.ScheduleSpec{
		TimeZone: "UTC", ValidFrom: &from, ValidUntil: &until,
		Schedule: v1alpha1.ScheduleWindow{Type: v1alpha1.WindowAlways},
	}
	before, err := Evaluate(spec, from.Time.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if before.Active || before.NextTransition == nil || !before.NextTransition.Equal(from.Time) {
		t.Fatalf("before = %#v", before)
	}
	atEnd, err := Evaluate(spec, until.Time)
	if err != nil {
		t.Fatal(err)
	}
	if atEnd.Active {
		t.Fatal("validUntil must be exclusive")
	}
}

func TestEvaluateDSTPolicies(t *testing.T) {
	tests := []struct {
		name string
		spec v1alpha1.ScheduleSpec
		at   time.Time
		want time.Time
	}{
		{
			name: "spring gap shifts by gap",
			spec: monthly("America/New_York", []int32{3}, []int32{8}, "02:30", 60),
			at:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC), // 03:30 EDT
		},
		{
			name: "fall overlap chooses earlier",
			spec: monthly("America/New_York", []int32{11}, []int32{1}, "01:30", 60),
			at:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), // first 01:30, EDT
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(tt.spec, tt.at)
			if err != nil {
				t.Fatal(err)
			}
			if got.NextTransition == nil || !got.NextTransition.Equal(tt.want) {
				t.Fatalf("next = %v, want %v", got.NextTransition, tt.want)
			}
		})
	}
}

func TestEvaluateSkipsMissingDateAndRejectsInvalid(t *testing.T) {
	spec := monthly("UTC", []int32{2}, []int32{29}, "10:00", 60)
	got, err := Evaluate(spec, time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2028, 2, 29, 10, 0, 0, 0, time.UTC)
	if got.NextTransition == nil || !got.NextTransition.Equal(want) {
		t.Fatalf("next = %v, want leap occurrence %v", got.NextTransition, want)
	}

	spec.Schedule.Monthly.Days = []int32{1, 1}
	if _, err := Evaluate(spec, time.Now()); err == nil {
		t.Fatal("expected duplicate-day validation error")
	}
}

func monthly(zone string, months, days []int32, start string, duration int32) v1alpha1.ScheduleSpec {
	return v1alpha1.ScheduleSpec{
		TimeZone: zone,
		Schedule: v1alpha1.ScheduleWindow{
			Type: v1alpha1.WindowMonthly,
			Monthly: &v1alpha1.MonthlyWindow{
				Months: months, Days: days, StartTime: start, DurationMinutes: duration,
			},
		},
	}
}
