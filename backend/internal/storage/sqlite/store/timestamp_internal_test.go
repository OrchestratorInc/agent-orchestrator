package store

import (
	"database/sql"
	"testing"
	"time"
)

func TestTimestampNormalizationHelpers(t *testing.T) {
	locEST := time.FixedZone("EST", -5*3600)
	locJST := time.FixedZone("JST", 9*3600)

	t.Run("normalizeTime", func(t *testing.T) {
		// Zero time preservation
		zero := time.Time{}
		normZero := normalizeTime(zero)
		if !normZero.IsZero() {
			t.Errorf("expected zero time, got %v", normZero)
		}

		// Non-UTC converted to UTC
		tEST := time.Date(2026, 5, 1, 10, 30, 0, 0, locEST)
		normEST := normalizeTime(tEST)
		if normEST.Location() != time.UTC {
			t.Errorf("expected UTC location, got %v", normEST.Location())
		}
		if !normEST.Equal(tEST) {
			t.Errorf("expected instant equality: %v != %v", normEST, tEST)
		}
		if normEST.Hour() != 15 || normEST.Minute() != 30 {
			t.Errorf("expected 15:30 UTC, got %02d:%02d", normEST.Hour(), normEST.Minute())
		}
	})

	t.Run("normalizeTimePtr", func(t *testing.T) {
		// Nil pointer
		if res := normalizeTimePtr(nil); res != nil {
			t.Errorf("expected nil, got %v", res)
		}

		// Pointer to zero-time
		zero := time.Time{}
		normZeroPtr := normalizeTimePtr(&zero)
		if normZeroPtr == nil || !normZeroPtr.IsZero() {
			t.Errorf("expected pointer to zero-time, got %v", normZeroPtr)
		}

		// Non-UTC pointer converted to UTC
		tJST := time.Date(2026, 5, 1, 15, 0, 0, 0, locJST)
		normJSTPtr := normalizeTimePtr(&tJST)
		if normJSTPtr == nil {
			t.Fatal("expected non-nil pointer")
		}
		if normJSTPtr.Location() != time.UTC {
			t.Errorf("expected UTC location, got %v", normJSTPtr.Location())
		}
		if !normJSTPtr.Equal(tJST) {
			t.Errorf("expected instant equality: %v != %v", *normJSTPtr, tJST)
		}
		if normJSTPtr.Hour() != 6 {
			t.Errorf("expected 06:00 UTC, got %02d:00", normJSTPtr.Hour())
		}
	})

	t.Run("normalizeNullTime", func(t *testing.T) {
		// Invalid
		invalid := sql.NullTime{Valid: false}
		if res := normalizeNullTime(invalid); res.Valid {
			t.Errorf("expected invalid, got valid %v", res)
		}

		// Valid but zero
		validZero := sql.NullTime{Valid: true, Time: time.Time{}}
		if res := normalizeNullTime(validZero); !res.Time.IsZero() {
			t.Errorf("expected zero time, got %v", res)
		}

		// Valid non-UTC
		tEST := time.Date(2026, 5, 1, 10, 0, 0, 0, locEST)
		res := normalizeNullTime(sql.NullTime{Valid: true, Time: tEST})
		if !res.Valid {
			t.Fatal("expected valid")
		}
		if res.Time.Location() != time.UTC {
			t.Errorf("expected UTC location, got %v", res.Time.Location())
		}
		if !res.Time.Equal(tEST) {
			t.Errorf("expected instant equality")
		}
	})

	t.Run("timeToNullTime and timePtrToNullTime", func(t *testing.T) {
		// Zero time -> invalid NullTime
		if res := timeToNullTime(time.Time{}); res.Valid {
			t.Errorf("expected invalid NullTime for zero time")
		}
		if res := timePtrToNullTime(nil); res.Valid {
			t.Errorf("expected invalid NullTime for nil pointer")
		}
		zero := time.Time{}
		if res := timePtrToNullTime(&zero); !res.Valid {
			t.Errorf("expected valid NullTime for zero-time pointer marker")
		}

		// Non-UTC non-zero -> valid UTC NullTime
		tEST := time.Date(2026, 5, 1, 10, 0, 0, 0, locEST)
		res1 := timeToNullTime(tEST)
		if !res1.Valid || res1.Time.Location() != time.UTC || !res1.Time.Equal(tEST) {
			t.Errorf("expected valid UTC NullTime, got %v", res1)
		}

		res2 := timePtrToNullTime(&tEST)
		if !res2.Valid || res2.Time.Location() != time.UTC || !res2.Time.Equal(tEST) {
			t.Errorf("expected valid UTC NullTime from pointer, got %v", res2)
		}
	})

	t.Run("nullTimeToTime and nullTimeToTimePtr", func(t *testing.T) {
		// Invalid -> zero time / nil pointer
		if res := nullTimeToTime(sql.NullTime{Valid: false}); !res.IsZero() {
			t.Errorf("expected zero time for invalid NullTime")
		}
		if res := nullTimeToTimePtr(sql.NullTime{Valid: false}); res != nil {
			t.Errorf("expected nil pointer for invalid NullTime")
		}

		// Valid zero -> zero time / non-nil pointer to zero time (presence preserved)
		if res := nullTimeToTime(sql.NullTime{Valid: true, Time: time.Time{}}); !res.IsZero() {
			t.Errorf("expected zero time for valid zero NullTime")
		}
		if res := nullTimeToTimePtr(sql.NullTime{Valid: true, Time: time.Time{}}); res == nil || !res.IsZero() {
			t.Errorf("expected non-nil pointer for valid zero NullTime, got %v", res)
		}

		// Valid non-UTC -> UTC
		tEST := time.Date(2026, 5, 1, 10, 0, 0, 0, locEST)
		nt := sql.NullTime{Valid: true, Time: tEST}
		resTime := nullTimeToTime(nt)
		if resTime.Location() != time.UTC || !resTime.Equal(tEST) {
			t.Errorf("expected UTC time, got %v", resTime)
		}

		resPtr := nullTimeToTimePtr(nt)
		if resPtr == nil || resPtr.Location() != time.UTC || !resPtr.Equal(tEST) {
			t.Errorf("expected UTC time pointer, got %v", resPtr)
		}
	})
}
