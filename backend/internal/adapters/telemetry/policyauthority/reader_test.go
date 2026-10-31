package policyauthority

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const generation = "7f80c8a9-ec67-4a16-a067-a444ffcc5cca"

func readRaw(t *testing.T, raw string) (Snapshot, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "telemetry_policy.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := New(path).Read(context.Background())
	return snapshot, err
}

func TestReaderReadsVersionOneChoice(t *testing.T) {
	got, err := readRaw(t, `{"schema_version":1,"events_enabled":true,"consent_generation":"`+generation+`","updated_at":"2026-08-28T10:15:30.000Z"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Present || !got.EventsEnabled || got.ConsentGeneration != generation {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestReaderReadsLegacyVersionTwoChoice(t *testing.T) {
	for _, want := range []bool{true, false} {
		value := "false"
		if want {
			value = "true"
		}
		got, err := readRaw(t, `{"schema_version":2,"events_enabled":true,"consent_generation":"`+generation+`","consent_production_enabled":`+value+`,"updated_at":"2026-08-28T10:15:30.000Z"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Present || !got.EventsEnabled {
			t.Fatalf("legacy production flag %s changed the choice: snapshot = %+v", value, got)
		}
	}
}

func TestReaderRejectsRecordsWhoseShapeDoesNotMatchTheirVersion(t *testing.T) {
	for name, raw := range map[string]string{
		"version 2 without gate state": `{"schema_version":2,"events_enabled":true,"consent_generation":"` + generation + `","updated_at":"2026-08-28T10:15:30.000Z"}`,
		"version 1 with gate state":    `{"schema_version":1,"events_enabled":true,"consent_generation":"` + generation + `","consent_production_enabled":true,"updated_at":"2026-08-28T10:15:30.000Z"}`,
		"version 2 null gate state":    `{"schema_version":2,"events_enabled":true,"consent_generation":"` + generation + `","consent_production_enabled":null,"updated_at":"2026-08-28T10:15:30.000Z"}`,
		"version 2 string gate state":  `{"schema_version":2,"events_enabled":true,"consent_generation":"` + generation + `","consent_production_enabled":"yes","updated_at":"2026-08-28T10:15:30.000Z"}`,
		"unknown version":              `{"schema_version":3,"events_enabled":true,"consent_generation":"` + generation + `","consent_production_enabled":true,"updated_at":"2026-08-28T10:15:30.000Z"}`,
		"unknown extra key":            `{"schema_version":2,"events_enabled":true,"consent_generation":"` + generation + `","consent_production_enabled":true,"updated_at":"2026-08-28T10:15:30.000Z","extra":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := readRaw(t, raw); err == nil {
				t.Fatalf("accepted %s: %+v", name, got)
			}
		})
	}
}

func TestEventsAllowedDefaultsOnUnlessOptedOut(t *testing.T) {
	dir := t.TempDir()
	reader := New(filepath.Join(dir, FileName))
	ctx := context.Background()
	if !reader.EventsAllowed(ctx, true, true) {
		t.Fatal("no policy file: want error reporting on")
	}
	if reader.EventsAllowed(ctx, true, false) {
		t.Fatal("events not set explicitly: want them off")
	}
	write := func(raw string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(raw), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, FileName), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"schema_version":1,"events_enabled":false,"consent_generation":"`+generation+`","updated_at":"2026-08-28T10:15:30.000Z"}`, 0o600)
	if reader.EventsAllowed(ctx, true, true) {
		t.Fatal("opted out in the app: want events off")
	}
	write(`{"schema_version":1,"events_enabled":true,"consent_generation":"`+generation+`","updated_at":"2026-08-28T10:15:30.000Z"}`, 0o600)
	if !reader.EventsAllowed(ctx, true, true) {
		t.Fatal("prior opt-in should send")
	}
	write(`{"schema_version":2,"events_enabled":true,"consent_generation":"`+generation+`","consent_production_enabled":false,"updated_at":"2026-08-28T10:15:30.000Z"}`, 0o600)
	if !reader.EventsAllowed(ctx, true, true) {
		t.Fatal("legacy production flag should not veto an opt-in")
	}
	write(`{"schema_version":2,"events_enabled":true,"consent_generation":"`+generation+`","consent_production_enabled":true,"updated_at":"2026-08-28T10:15:30.000Z"}`, 0o600)
	if !reader.EventsAllowed(ctx, true, true) {
		t.Fatal("opt-in: want error reporting on")
	}
	if reader.EventsAllowed(ctx, true, false) || reader.EventsAllowed(ctx, false, true) {
		t.Fatal("environment veto: want error reporting off")
	}
	write(`{"schema_version":2,"events_enabled":false,"consent_generation":"`+generation+`","consent_production_enabled":true,"updated_at":"2026-08-28T10:15:30.000Z"}`, 0o600)
	if reader.EventsAllowed(ctx, true, true) {
		t.Fatal("explicit opt-out: want error reporting off")
	}
	if runtime.GOOS != "windows" {
		write(`{"schema_version":2,"events_enabled":true,"consent_generation":"`+generation+`","consent_production_enabled":true,"updated_at":"2026-08-28T10:15:30.000Z"}`, 0o644)
		if reader.EventsAllowed(ctx, true, true) {
			t.Fatal("unsafe file: want events off")
		}
	}
}
