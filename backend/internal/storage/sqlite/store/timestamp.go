package store

import (
	"database/sql"
	"time"
)

// normalizeTime returns t converted to UTC, preserving zero-time.
func normalizeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}

// timeToNullTime converts a time.Time to sql.NullTime in UTC, preserving zero-time as invalid (null).
func timeToNullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

// timePtrToNullTime converts a *time.Time to sql.NullTime in UTC, preserving nil as invalid (null).
// A non-nil pointer (even if zero-valued) produces a valid sql.NullTime marker.
func timePtrToNullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: normalizeTime(*t), Valid: true}
}

// nullTimeToTime bridges a nullable database timestamp to domain zero-time in UTC.
func nullTimeToTime(t sql.NullTime) time.Time {
	if !t.Valid || t.Time.IsZero() {
		return time.Time{}
	}
	return t.Time.UTC()
}

// nullTimeToTimePtr bridges a nullable database timestamp to a *time.Time in UTC.
// A valid database timestamp (even if zero-valued) produces a non-nil pointer.
func nullTimeToTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	utc := normalizeTime(t.Time)
	return &utc
}
