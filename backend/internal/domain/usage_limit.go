package domain

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Provider IANA zones also work in packaged Windows builds.
)

// ConversationUsageLimit describes a settled provider quota refusal. A missing
// deadline means the provider did not give AO a reset time it can safely use.
type ConversationUsageLimit struct {
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
	AutoResume  bool       `json:"autoResume"`
	ResumeError string     `json:"resumeError,omitempty"`
}

var usageLimitMessage = regexp.MustCompile(`(?i)^(?:you(?:'ve| have) (?:hit|reached) your (?:session |weekly |daily |usage |individual spend )?limit\b|usage limit (?:reached|exceeded)\b|(?:session|weekly|daily) usage limit (?:reached|exceeded)\b|the Claude account has no available quota\b)`)
var usageLimitResetClock = regexp.MustCompile(`(?i)\bresets?(?:\s+at)?\s+(?:(tomorrow)\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\s*\(([^)]+)\)`)
var usageLimitResetISO = regexp.MustCompile(`(?i)\bresets?(?:\s+at)?\s+(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2}))`)

// ParseConversationUsageLimit recognizes only known quota refusals, never
// assistant prose. Clock-only deadlines require the provider's explicit IANA
// zone and are anchored to the failure, so rereading history cannot move them.
func ParseConversationUsageLimit(message string, failedAt *time.Time) *ConversationUsageLimit {
	message = strings.TrimSpace(strings.ReplaceAll(message, "’", "'"))
	if !usageLimitMessage.MatchString(message) {
		return nil
	}
	limit := &ConversationUsageLimit{}
	if match := usageLimitResetISO.FindStringSubmatch(message); match != nil {
		if reset, err := time.Parse(time.RFC3339, match[1]); err == nil {
			reset = reset.UTC()
			limit.ResetsAt = &reset
		}
		return limit
	}
	match := usageLimitResetClock.FindStringSubmatch(message)
	if match == nil || failedAt == nil {
		return limit
	}
	zoneName := strings.TrimSpace(match[5])
	if zoneName != "UTC" && !strings.Contains(zoneName, "/") {
		return limit
	}
	zone, err := time.LoadLocation(zoneName)
	if err != nil {
		return limit
	}
	hour, _ := strconv.Atoi(match[2])
	minute, _ := strconv.Atoi(match[3])
	if hour < 1 || hour > 12 || minute > 59 {
		return limit
	}
	hour %= 12
	if strings.EqualFold(match[4], "pm") {
		hour += 12
	}
	local := failedAt.In(zone)
	reset := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, zone)
	if match[1] == "" && usageLimitClockRepeated(reset) {
		return limit
	}
	if match[1] != "" || !reset.After(*failedAt) {
		reset = reset.AddDate(0, 0, 1)
	}
	// Nonexistent or repeated wall clocks must not become guessed deadlines.
	if reset.Hour() != hour || reset.Minute() != minute || usageLimitClockRepeated(reset) {
		return limit
	}
	reset = reset.UTC()
	limit.ResetsAt = &reset
	return limit
}

func usageLimitClockRepeated(reset time.Time) bool {
	start, end := reset.ZoneBounds()
	_, offset := reset.Zone()
	clock := reset.Format(time.DateTime)
	for i, boundary := range []time.Time{start, end} {
		if boundary.IsZero() {
			continue
		}
		if i == 0 {
			boundary = boundary.Add(-time.Nanosecond)
		}
		_, adjacentOffset := boundary.Zone()
		if adjacentOffset == offset {
			continue
		}
		// Both neighboring offsets can represent this clock during a backward jump.
		other := reset.Add(time.Duration(offset-adjacentOffset) * time.Second)
		if other.Format(time.DateTime) == clock {
			return true
		}
	}
	return false
}
