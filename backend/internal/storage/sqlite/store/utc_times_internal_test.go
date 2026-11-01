package store

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestUTCArgsPutEveryTimeInUTC(t *testing.T) {
	at := time.Date(2026, 10, 21, 18, 15, 40, 647754000, time.FixedZone("", 2*3600))
	want := at.UTC()

	t.Run("time.Time", func(t *testing.T) {
		got := utcArgs([]any{at})[0].(time.Time)
		if !got.Equal(want) || got.Location() != time.UTC {
			t.Fatalf("got %s (zone %v), want %s in UTC", got, got.Location(), want)
		}
	})
	t.Run("pointer", func(t *testing.T) {
		in := at
		got := utcArgs([]any{&in})[0].(*time.Time)
		if !got.Equal(want) || got.Location() != time.UTC {
			t.Fatalf("got %s, want %s in UTC", got, want)
		}
		if in.Location() == time.UTC {
			t.Fatal("converting a pointer argument changed the caller's value")
		}
	})
	t.Run("nil pointer", func(t *testing.T) {
		var p *time.Time
		got := utcArgs([]any{p})[0].(*time.Time)
		if got != nil {
			t.Fatalf("nil pointer became %v", got)
		}
	})
	t.Run("valid NullTime", func(t *testing.T) {
		got := utcArgs([]any{sql.NullTime{Time: at, Valid: true}})[0].(sql.NullTime)
		if !got.Valid || !got.Time.Equal(want) || got.Time.Location() != time.UTC {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("invalid NullTime stays NULL", func(t *testing.T) {
		got := utcArgs([]any{sql.NullTime{}})[0].(sql.NullTime)
		if got.Valid {
			t.Fatalf("NULL became %+v", got)
		}
	})
	t.Run("monotonic reading is dropped", func(t *testing.T) {
		got := utcArgs([]any{time.Now()})[0].(time.Time)
		if strings.Contains(got.String(), "m=") {
			t.Fatalf("stored text would carry the monotonic reading: %q", got.String())
		}
	})
	t.Run("named argument", func(t *testing.T) {
		got := utcArgs([]any{sql.NamedArg{Name: "at", Value: at}})[0].(sql.NamedArg)
		if got.Name != "at" || !got.Value.(time.Time).Equal(want) || got.Value.(time.Time).Location() != time.UTC {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("zero time stays zero", func(t *testing.T) {
		got := utcArgs([]any{time.Time{}})[0].(time.Time)
		if !got.IsZero() {
			t.Fatalf("zero time became %s", got)
		}
	})
}

func TestUTCArgsLeaveEverythingElseAlone(t *testing.T) {
	args := []any{"text", 42, int64(7), 1.5, true, nil, []byte("blob"), sql.NullString{String: "x", Valid: true}, sql.NamedArg{Name: "n", Value: "v"}}
	got := utcArgs(args)
	if &got[0] != &args[0] {
		t.Fatal("arguments with no time were copied")
	}
	for i := range args {
		if got[i] != nil && args[i] != nil && strings.Compare(sprint(got[i]), sprint(args[i])) != 0 {
			t.Fatalf("argument %d changed: %v -> %v", i, args[i], got[i])
		}
	}
}

func TestUTCArgsDoNotModifyTheCallersSlice(t *testing.T) {
	zone := time.FixedZone("", 5*3600+45*60)
	original := time.Date(2026, 10, 21, 18, 15, 40, 0, zone)
	args := []any{"id", original, "tail"}
	got := utcArgs(args)
	if args[1].(time.Time).Location() != zone {
		t.Fatal("the caller's slice was rewritten in place")
	}
	if got[0] != "id" || got[2] != "tail" || got[1].(time.Time).Location() != time.UTC {
		t.Fatalf("got %v", got)
	}
}

// Already-UTC times cost nothing and are passed through untouched.
func TestUTCArgsSkipTimesAlreadyInUTC(t *testing.T) {
	args := []any{time.Date(2026, 10, 21, 16, 15, 40, 0, time.UTC), sql.NullTime{Time: time.Now().UTC(), Valid: true}, time.Time{}}
	got := utcArgs(args)
	if &got[0] != &args[0] {
		t.Fatal("UTC arguments were copied")
	}
}

// The zones that failed before: every one must come out as the same instant in UTC.
func TestUTCArgsCoverEveryZoneThatBrokeTheDriver(t *testing.T) {
	names := []string{"Asia/Kathmandu", "Asia/Tehran", "Asia/Kabul", "Asia/Yangon", "Asia/Colombo", "Pacific/Chatham",
		"Australia/Lord_Howe", "Asia/Dubai", "America/Sao_Paulo", "Europe/Rome", "America/St_Johns", "Pacific/Kiritimati"}
	for _, name := range names {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		in := time.Date(2026, 10, 21, 18, 15, 40, 123456000, loc)
		out := utcArgs([]any{in})[0].(time.Time)
		if !out.Equal(in) || out.Location() != time.UTC {
			t.Fatalf("%s: got %s, want the instant %s in UTC", name, out, in)
		}
		if strings.Contains(out.String(), "+0") && !strings.HasSuffix(out.String(), "+0000 UTC") {
			t.Fatalf("%s: stored text %q is not the UTC rendering", name, out.String())
		}
	}
}

func sprint(v any) string { return fmt.Sprintf("%v", v) }
