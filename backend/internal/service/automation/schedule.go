// Package automation owns durable recurring-automation behavior.
package automation

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	rrule "github.com/teambition/rrule-go"
)

// ScheduleInput is the user-facing schedule choice. Exactly one source is set.
// AnchorTimezone is set only when RRule is a persisted rule being moved to a
// new Timezone, and names the zone its DTSTART anchor was stored in.
type ScheduleInput struct {
	RRule          string
	Cron           string
	Timezone       string
	AnchorTimezone string
}

// Schedule is the canonical durable rule and its first future occurrence.
type Schedule struct {
	RRuleText string
	Timezone  string
	NextRunAt time.Time
}

// CanonicalizeSchedule validates one schedule input and persists its DTSTART
// anchor with the RRULE so interval semantics survive daemon restarts.
func CanonicalizeSchedule(input ScheduleInput, now time.Time) (Schedule, error) {
	rruleText := strings.TrimSpace(input.RRule)
	cronText := strings.TrimSpace(input.Cron)
	if (rruleText == "") == (cronText == "") {
		return Schedule{}, fmt.Errorf("exactly one of rrule or cron is required")
	}
	zone := strings.TrimSpace(input.Timezone)
	if zone == "" {
		return Schedule{}, fmt.Errorf("timezone is required")
	}
	if zone != "UTC" && !strings.Contains(zone, "/") {
		return Schedule{}, fmt.Errorf("timezone %q must be an IANA location", zone)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return Schedule{}, fmt.Errorf("load timezone %q: %w", zone, err)
	}
	if cronText != "" {
		rruleText, err = cronToRRule(cronText)
		if err != nil {
			return Schedule{}, err
		}
	}
	if anchorZone := strings.TrimSpace(input.AnchorTimezone); anchorZone != "" && anchorZone != zone {
		rruleText, err = rebaseRRule(rruleText, anchorZone, loc, now)
		if err != nil {
			return Schedule{}, err
		}
	}
	if embedded := embeddedTimezone(rruleText); embedded != "" && embedded != zone {
		return Schedule{}, fmt.Errorf("rrule timezone %q conflicts with schedule timezone %q", embedded, zone)
	}

	option, err := rrule.StrToROptionInLocation(rruleText, loc)
	if err != nil {
		return Schedule{}, fmt.Errorf("parse rrule: %w", err)
	}
	if option.Freq == rrule.SECONDLY {
		return Schedule{}, fmt.Errorf("schedule frequency cannot be faster than one minute")
	}
	if option.Count > 0 || !option.Until.IsZero() {
		return Schedule{}, fmt.Errorf("finite COUNT and UNTIL schedules are not supported")
	}
	if option.Dtstart.IsZero() {
		option.Dtstart = now.In(loc).Truncate(time.Minute).Add(time.Minute)
	}
	if len(option.Bysecond) > 1 {
		return Schedule{}, fmt.Errorf("schedule frequency cannot be faster than one minute")
	}
	rule, err := rrule.NewRRule(*option)
	if err != nil {
		return Schedule{}, fmt.Errorf("build rrule: %w", err)
	}
	next := rule.After(now, false)
	if next.IsZero() {
		return Schedule{}, fmt.Errorf("schedule has no future occurrence")
	}
	previous := next
	for i := 0; i < 16; i++ {
		following := rule.After(previous, false)
		if following.IsZero() {
			break
		}
		if following.Sub(previous) < time.Minute {
			return Schedule{}, fmt.Errorf("schedule frequency cannot be faster than one minute")
		}
		previous = following
	}
	return Schedule{RRuleText: option.String(), Timezone: zone, NextRunAt: next.UTC()}, nil
}

// rebaseRRule moves a persisted rule's DTSTART anchor into a new timezone.
// Hourly and minutely rules keep the anchor's instant so the cadence continues
// unbroken. Slower rules keep the anchor's local wall-clock time; when that
// pushes an already-started daily or weekly anchor past now, it steps back by
// whole intervals so the first occurrences in the new zone are not skipped.
func rebaseRRule(rruleText, anchorZone string, loc *time.Location, now time.Time) (string, error) {
	anchorLoc, err := time.LoadLocation(anchorZone)
	if err != nil {
		return "", fmt.Errorf("load timezone %q: %w", anchorZone, err)
	}
	option, err := rrule.StrToROptionInLocation(rruleText, anchorLoc)
	if err != nil {
		return "", fmt.Errorf("parse rrule: %w", err)
	}
	anchor := option.Dtstart
	if anchor.IsZero() {
		return option.String(), nil
	}
	if option.Freq == rrule.HOURLY || option.Freq == rrule.MINUTELY {
		option.Dtstart = anchor.In(loc)
		return option.String(), nil
	}
	started := !anchor.After(now)
	anchor = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), anchor.Hour(), anchor.Minute(), anchor.Second(), 0, loc)
	if started && (option.Freq == rrule.DAILY || option.Freq == rrule.WEEKLY) {
		days := max(option.Interval, 1)
		if option.Freq == rrule.WEEKLY {
			days *= 7
		}
		for anchor.After(now) {
			anchor = anchor.AddDate(0, 0, -days)
		}
	}
	option.Dtstart = anchor
	return option.String(), nil
}

func embeddedTimezone(value string) string {
	const marker = "DTSTART;TZID="
	start := strings.Index(value, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := strings.IndexByte(value[start:], ':')
	if end < 0 {
		return ""
	}
	return value[start : start+end]
}

// NextOccurrence calculates the first logical occurrence strictly after the
// supplied instant using the persisted anchor and named timezone.
func NextOccurrence(rruleText, timezone string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load timezone %q: %w", timezone, err)
	}
	option, err := rrule.StrToROptionInLocation(rruleText, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse rrule: %w", err)
	}
	rule, err := rrule.NewRRule(*option)
	if err != nil {
		return time.Time{}, fmt.Errorf("build rrule: %w", err)
	}
	next := rule.After(after, false)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("schedule has no future occurrence")
	}
	return next.UTC(), nil
}

// cronToRRule intentionally accepts the unambiguous minute/hour + optional
// weekday subset used by the desktop presets. Other valid cron constructs are
// rejected instead of being translated with different semantics.
func cronToRRule(value string) (string, error) {
	fields := strings.Fields(value)
	if len(fields) != 5 {
		return "", fmt.Errorf("cron must contain exactly five fields")
	}
	minute, err := boundedInt(fields[0], 59, "minute")
	if err != nil {
		return "", err
	}
	hour, err := boundedInt(fields[1], 23, "hour")
	if err != nil {
		return "", err
	}
	if fields[2] != "*" || fields[3] != "*" {
		return "", fmt.Errorf("cron day-of-month and month must be *")
	}
	parts := []string{"FREQ=DAILY"}
	if fields[4] != "*" {
		days, err := cronWeekdays(fields[4])
		if err != nil {
			return "", err
		}
		parts[0] = "FREQ=WEEKLY"
		parts = append(parts, "BYDAY="+strings.Join(days, ","))
	}
	parts = append(parts,
		"BYHOUR="+strconv.Itoa(hour),
		"BYMINUTE="+strconv.Itoa(minute),
		"BYSECOND=0",
	)
	return strings.Join(parts, ";"), nil
}

func boundedInt(value string, maxValue int, label string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 || n > maxValue {
		return 0, fmt.Errorf("cron %s must be an integer from 0 to %d", label, maxValue)
	}
	return n, nil
}

func cronWeekdays(value string) ([]string, error) {
	seen := map[int]bool{}
	for _, token := range strings.Split(value, ",") {
		bounds := strings.Split(token, "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("unsupported cron weekday %q", token)
		}
		start, err := boundedInt(bounds[0], 7, "weekday")
		if err != nil {
			return nil, err
		}
		end := start
		if len(bounds) == 2 {
			end, err = boundedInt(bounds[1], 7, "weekday")
			if err != nil {
				return nil, err
			}
			if end < start {
				return nil, fmt.Errorf("cron weekday range %q must be ascending", token)
			}
		}
		for day := start; day <= end; day++ {
			seen[day%7] = true
		}
	}
	names := []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}
	days := make([]string, 0, len(seen))
	// RFC output is stable Monday through Sunday.
	for _, day := range []int{1, 2, 3, 4, 5, 6, 0} {
		if seen[day] {
			days = append(days, names[day])
		}
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("cron weekday set is empty")
	}
	return days, nil
}
