package store

import (
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestInsightWindow(t *testing.T) {
	t.Parallel()
	// Wednesday afternoon, so week and month starts differ from today.
	now := time.Date(2026, 9, 16, 15, 30, 0, 0, time.UTC)
	day := func(month time.Month, d int) time.Time { return time.Date(2026, month, d, 0, 0, 0, 0, time.UTC) }
	for _, tt := range []struct {
		name       string
		rng        model.InsightRange
		earliest   time.Time
		wantBucket model.InsightBucket
		wantStart  time.Time
		wantCount  int
	}{
		{name: "two weeks", rng: model.InsightRangeTwoWeeks, wantBucket: model.InsightBucketDay, wantStart: day(9, 3), wantCount: 14},
		{name: "thirty days", rng: model.InsightRangeThirtyDays, wantBucket: model.InsightBucketDay, wantStart: day(8, 18), wantCount: 30},
		{name: "ninety days", rng: model.InsightRangeNinetyDays, wantBucket: model.InsightBucketWeek, wantStart: day(6, 22), wantCount: 13},
		{name: "all, new project keeps two weeks", rng: model.InsightRangeAll, earliest: day(9, 15), wantBucket: model.InsightBucketDay, wantStart: day(9, 3), wantCount: 14},
		{name: "all, a month of days", rng: model.InsightRangeAll, earliest: day(8, 17).Add(20 * time.Hour), wantBucket: model.InsightBucketDay, wantStart: day(8, 17), wantCount: 31},
		{name: "all, weeks", rng: model.InsightRangeAll, earliest: day(8, 16), wantBucket: model.InsightBucketWeek, wantStart: day(8, 10), wantCount: 6},
		{name: "all, half a year of weeks", rng: model.InsightRangeAll, earliest: day(3, 19), wantBucket: model.InsightBucketWeek, wantStart: day(3, 16), wantCount: 27},
		{name: "all, months", rng: model.InsightRangeAll, earliest: time.Date(2025, 11, 20, 8, 0, 0, 0, time.UTC), wantBucket: model.InsightBucketMonth, wantStart: time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC), wantCount: 11},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bucket, periods := insightWindow(tt.rng, now, tt.earliest)
			if bucket != tt.wantBucket || len(periods) != tt.wantCount || !periods[0].Start.Equal(tt.wantStart) {
				t.Fatalf("insightWindow = %s, %d periods from %s; want %s, %d from %s", bucket, len(periods), periods[0].Start, tt.wantBucket, tt.wantCount, tt.wantStart)
			}
			for i := 1; i < len(periods); i++ {
				if !periods[i].Start.Equal(periods[i-1].End) {
					t.Fatalf("period %d starts %s, previous ends %s", i, periods[i].Start, periods[i-1].End)
				}
			}
			if last := periods[len(periods)-1]; !last.End.Equal(now) {
				t.Fatalf("last period ends %s, want now", last.End)
			}
		})
	}
}

func TestInsightPeriodsEndExactlyAtBoundary(t *testing.T) {
	t.Parallel()
	midnight := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	periods := insightPeriods(model.InsightBucketDay, midnight.AddDate(0, 0, -2), midnight)
	if len(periods) != 2 || !periods[1].End.Equal(midnight) || !periods[1].Start.Equal(midnight.AddDate(0, 0, -1)) {
		t.Fatalf("periods = %+v", periods)
	}
}

func TestInsightSprintPeriods(t *testing.T) {
	t.Parallel()
	day := func(d, hour int) time.Time { return time.Date(2026, 3, d, hour, 0, 0, 0, time.UTC) }
	ptr := func(t time.Time) *time.Time { return &t }
	for _, tt := range []struct {
		name      string
		sprint    model.Sprint
		startedAt *time.Time
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
		wantEnds  []time.Time
	}{
		{
			name:      "active sprint runs to its planned end",
			sprint:    model.Sprint{StartDate: ptr(day(1, 0)), EndDate: ptr(day(6, 0))},
			startedAt: ptr(day(2, 9)),
			now:       day(3, 12),
			wantStart: day(2, 0),
			wantEnd:   day(7, 0),
			wantEnds:  []time.Time{day(3, 0), day(3, 12)},
		},
		{
			name:      "overdue active sprint runs to today",
			sprint:    model.Sprint{EndDate: ptr(day(2, 0)), CreatedAt: day(1, 8)},
			now:       day(4, 6),
			wantStart: day(1, 0),
			wantEnd:   day(5, 0),
			wantEnds:  []time.Time{day(2, 0), day(3, 0), day(4, 0), day(4, 6)},
		},
		{
			name:      "completed sprint stops at completion",
			sprint:    model.Sprint{StartDate: ptr(day(10, 0)), CompletedAt: ptr(day(11, 15))},
			now:       day(20, 0),
			wantStart: day(10, 0),
			wantEnd:   day(12, 0),
			wantEnds:  []time.Time{day(11, 0), day(11, 15)},
		},
		{
			name:      "completion at midnight adds no empty day",
			sprint:    model.Sprint{StartDate: ptr(day(10, 0)), CompletedAt: ptr(day(12, 0))},
			now:       day(20, 0),
			wantStart: day(10, 0),
			wantEnd:   day(12, 0),
			wantEnds:  []time.Time{day(11, 0), day(12, 0)},
		},
		{
			name:      "planned start in the future has no points yet",
			sprint:    model.Sprint{StartDate: ptr(day(25, 0))},
			now:       day(20, 0),
			wantStart: day(25, 0),
			wantEnd:   day(26, 0),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			periods, start, end := insightSprintPeriods(tt.sprint, tt.startedAt, tt.now)
			if !start.Equal(tt.wantStart) || !end.Equal(tt.wantEnd) || len(periods) != len(tt.wantEnds) {
				t.Fatalf("insightSprintPeriods = %d periods, %s to %s; want %d, %s to %s", len(periods), start, end, len(tt.wantEnds), tt.wantStart, tt.wantEnd)
			}
			for i, want := range tt.wantEnds {
				if !periods[i].End.Equal(want) {
					t.Fatalf("period %d ends %s, want %s", i, periods[i].End, want)
				}
			}
		})
	}
}

func TestInsightSprintPeriodsCapsLongSprints(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	now := started.AddDate(0, 0, 200)
	periods, start, end := insightSprintPeriods(model.Sprint{}, &started, now)
	if len(periods) != insightSprintBurnupMaxDays || end.Sub(start) != insightSprintBurnupMaxDays*24*time.Hour || !periods[len(periods)-1].End.Equal(now) {
		t.Fatalf("capped sprint = %d periods, %s to %s", len(periods), start, end)
	}
}
