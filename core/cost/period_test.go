// period_test.go tests date range helpers and ResolvePeriod.
package cost

import (
	"strings"
	"testing"
	"time"
)

func TestLastNDaysRange(t *testing.T) {
	now := time.Date(2026, 5, 25, 15, 30, 0, 0, time.UTC)
	dr := LastNDaysRange(30, now)

	wantStart := "2026-04-25"
	wantEnd := "2026-05-25"

	if got := FormatDate(dr.Start); got != wantStart {
		t.Errorf("Start = %q, want %q", got, wantStart)
	}
	if got := FormatDate(dr.End); got != wantEnd {
		t.Errorf("End (exclusive) = %q, want %q", got, wantEnd)
	}
}

func TestLastNDaysRangeDefaultsOnZero(t *testing.T) {
	now := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	dr := LastNDaysRange(0, now)
	if FormatDate(dr.End) != "2026-01-31" {
		t.Fatalf("unexpected end: %s", FormatDate(dr.End))
	}
	if FormatDate(dr.Start) != "2026-01-01" {
		t.Fatalf("unexpected start: %s", FormatDate(dr.Start))
	}
}

func TestLastNCalendarMonthsRange(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	dr := LastNCalendarMonthsRange(3, now)
	if got := FormatDate(dr.Start); got != "2026-02-01" {
		t.Errorf("Start = %q, want 2026-02-01", got)
	}
	if got := FormatDate(dr.End); got != "2026-05-26" {
		t.Errorf("End = %q, want 2026-05-26", got)
	}
}

func TestResolvePeriodDefault(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	dr, err := ResolvePeriod(PeriodSpec{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if FormatDate(dr.Start) != "2026-04-26" {
		t.Errorf("Start = %s", FormatDate(dr.Start))
	}
	if FormatDate(dr.End) != "2026-05-26" {
		t.Errorf("End = %s", FormatDate(dr.End))
	}
}

func TestResolvePeriodDaysWithExcludeRecent(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	dr, err := ResolvePeriod(PeriodSpec{Days: 30, ExcludeRecentDays: 2}, now)
	if err != nil {
		t.Fatal(err)
	}
	if FormatDate(dr.End) != "2026-05-24" {
		t.Errorf("End = %s, want 2026-05-24", FormatDate(dr.End))
	}
	if FormatDate(dr.Start) != "2026-04-24" {
		t.Errorf("Start = %s, want 2026-04-24", FormatDate(dr.Start))
	}
}

func TestResolvePeriodExplicitRange(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	dr, err := ResolvePeriod(PeriodSpec{From: "2026-01-01", To: "2026-03-31"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if FormatDate(dr.Start) != "2026-01-01" || FormatDate(dr.End) != "2026-04-01" {
		t.Fatalf("range %s..%s", FormatDate(dr.Start), FormatDate(dr.End))
	}
}

func TestResolvePeriodFromOnly(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	dr, err := ResolvePeriod(PeriodSpec{From: "2026-04-01"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if FormatDate(dr.Start) != "2026-04-01" || FormatDate(dr.End) != "2026-05-26" {
		t.Fatalf("range %s..%s", FormatDate(dr.Start), FormatDate(dr.End))
	}
}

func TestResolvePeriodFromOnlyWithExcludeRecent(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	dr, err := ResolvePeriod(PeriodSpec{From: "2026-04-01", ExcludeRecentDays: 2}, now)
	if err != nil {
		t.Fatal(err)
	}
	if FormatDate(dr.End) != "2026-05-24" {
		t.Errorf("End = %s", FormatDate(dr.End))
	}
}

func TestResolvePeriodFutureToRejected(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	_, err := ResolvePeriod(PeriodSpec{From: "2026-05-01", To: "2026-12-31"}, now)
	if err == nil {
		t.Fatal("expected error for future to")
	}
	if !strings.Contains(err.Error(), "future") {
		t.Errorf("error = %v", err)
	}
}

func TestResolvePeriodConflictingModes(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	_, err := ResolvePeriod(PeriodSpec{Days: 7, Months: 1}, now)
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = ResolvePeriod(PeriodSpec{Days: 7, From: "2026-01-01"}, now)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolvePeriodToRequiresFrom(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	_, err := ResolvePeriod(PeriodSpec{To: "2026-03-01"}, now)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolvePeriodEmptyRange(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	_, err := ResolvePeriod(PeriodSpec{From: "2026-05-26"}, now)
	if err == nil {
		t.Fatal("expected empty range error")
	}
}
