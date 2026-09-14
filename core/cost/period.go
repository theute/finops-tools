// period.go defines inclusive/exclusive date ranges and period resolution for cost queries.
package cost

import (
	"fmt"
	"strings"
	"time"
)

// DateRange holds inclusive start and exclusive end calendar dates (YYYY-MM-DD).
type DateRange struct {
	Start time.Time
	End   time.Time
}

// IsZero reports whether the range is unset.
func (r DateRange) IsZero() bool {
	return r.Start.IsZero() && r.End.IsZero()
}

// PeriodSpec describes a cost query window before applying AWS CE lag.
type PeriodSpec struct {
	Days              int
	Months            int
	From              string
	To                string
	ExcludeRecentDays int
}

// LastNDaysRange returns a range covering the last n calendar days ending at the start of today (UTC).
// Start is inclusive; End is exclusive (Cost Explorer TimePeriod convention).
func LastNDaysRange(n int, now time.Time) DateRange {
	return lastNDaysRangeEnding(n, dateOnly(now.UTC()))
}

func lastNDaysRangeEnding(n int, endExclusive time.Time) DateRange {
	if n <= 0 {
		n = DefaultDays
	}
	start := endExclusive.AddDate(0, 0, -n)
	return DateRange{Start: start, End: endExclusive}
}

// LastNCalendarMonthsRange returns a range from the 1st day of the month N months before
// the month containing the last included day through endExclusive (exclusive).
func LastNCalendarMonthsRange(n int, now time.Time) DateRange {
	return lastNCalendarMonthsRangeEnding(n, dateOnly(now.UTC()))
}

func lastNCalendarMonthsRangeEnding(n int, endExclusive time.Time) DateRange {
	if n <= 0 {
		n = 1
	}
	lastIncluded := endExclusive.AddDate(0, 0, -1)
	y, m, _ := lastIncluded.Date()
	monthStart := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	start := monthStart.AddDate(0, -n, 0)
	return DateRange{Start: start, End: endExclusive}
}

// ParseDate parses a UTC calendar date (YYYY-MM-DD).
func ParseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("date is required")
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q (expected YYYY-MM-DD): %w", s, err)
	}
	return t, nil
}

// ResolvePeriod computes the Cost Explorer date range for spec at now (UTC).
func ResolvePeriod(spec PeriodSpec, now time.Time) (DateRange, error) {
	if spec.ExcludeRecentDays < 0 {
		return DateRange{}, fmt.Errorf("exclude_recent_days must be >= 0")
	}
	now = now.UTC()
	naturalEnd := dateOnly(now)
	stableEnd := naturalEnd.AddDate(0, 0, -spec.ExcludeRecentDays)
	lastIncludable := stableEnd.AddDate(0, 0, -1)

	if err := validatePeriodModes(spec); err != nil {
		return DateRange{}, err
	}

	fromSet := strings.TrimSpace(spec.From) != ""
	toSet := strings.TrimSpace(spec.To) != ""

	var (
		dr         DateRange
		endFromNow bool
	)

	switch {
	case fromSet:
		start, err := ParseDate(spec.From)
		if err != nil {
			return DateRange{}, fmt.Errorf("from: %w", err)
		}
		dr.Start = start
		if toSet {
			to, err := ParseDate(spec.To)
			if err != nil {
				return DateRange{}, fmt.Errorf("to: %w", err)
			}
			dr.End = to.AddDate(0, 0, 1)
			endFromNow = false
			if to.After(lastIncludable) {
				return DateRange{}, fmt.Errorf(
					"cost period cannot include future dates: end %s is after %s (Cost Explorer is historical only; use a lower --to or adjust exclude-recent-days)",
					FormatDate(to), FormatDate(lastIncludable),
				)
			}
		} else {
			dr.End = naturalEnd
			endFromNow = true
		}
	case spec.Months > 0:
		end := naturalEnd
		if spec.ExcludeRecentDays > 0 {
			end = stableEnd
		}
		dr = lastNCalendarMonthsRangeEnding(spec.Months, end)
		endFromNow = true
	case spec.Days > 0:
		end := naturalEnd
		if spec.ExcludeRecentDays > 0 {
			end = stableEnd
		}
		dr = lastNDaysRangeEnding(spec.Days, end)
		endFromNow = true
	default:
		end := naturalEnd
		if spec.ExcludeRecentDays > 0 {
			end = stableEnd
		}
		dr = lastNDaysRangeEnding(DefaultDays, end)
		endFromNow = true
	}

	if endFromNow && dr.End.After(stableEnd) {
		dr.End = stableEnd
	}

	if !dr.Start.Before(dr.End) {
		return DateRange{}, fmt.Errorf("cost period is empty (start %s must be before end %s)", FormatDate(dr.Start), FormatDate(dr.End))
	}
	return dr, nil
}

func validatePeriodModes(spec PeriodSpec) error {
	fromSet := strings.TrimSpace(spec.From) != ""
	toSet := strings.TrimSpace(spec.To) != ""
	if toSet && !fromSet {
		return fmt.Errorf("--to requires --from")
	}
	modes := 0
	if spec.Days > 0 {
		modes++
	}
	if spec.Months > 0 {
		modes++
	}
	if fromSet || toSet {
		modes++
	}
	if modes > 1 {
		return fmt.Errorf("only one period mode allowed (use one of --days, --months, or --from/--to)")
	}
	if (spec.Days > 0 || spec.Months > 0) && (fromSet || toSet) {
		return fmt.Errorf("cannot combine --days or --months with --from/--to")
	}
	return nil
}

// EffectiveRange returns q.Range or the default last-30-days window at now.
func EffectiveRange(q CostQuery, now time.Time) DateRange {
	if !q.Range.IsZero() {
		return q.Range
	}
	return LastNDaysRange(DefaultDays, now)
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// FormatDate formats a calendar date as YYYY-MM-DD.
func FormatDate(t time.Time) string {
	return t.Format("2006-01-02")
}
