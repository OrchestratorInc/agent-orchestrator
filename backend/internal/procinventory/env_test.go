package procinventory

import (
	"context"
	"errors"
	"testing"
)

func TestEnvDisabled(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "0", want: false},
		{value: "no", want: false},
		{value: "yes", want: false},
		{value: "TRUE", want: false}, // only lowercase literals enable the gate
		{value: "1", want: true},
		{value: "true", want: true},
	} {
		t.Run("AO_NO_PROCINVENTORY="+tt.value, func(t *testing.T) {
			t.Setenv(envDisableFlag, tt.value)
			if got := EnvDisabled(); got != tt.want {
				t.Fatalf("EnvDisabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDisabledServiceServesEmptyInventoryWithoutScanning pins the runtime
// gate's behavioral contract: with AO_NO_PROCINVENTORY set, New reports
// Disabled and Inventory answers an empty inventory without touching the
// process table, so a disabled feature costs no scan work even if a client
// keeps polling.
func TestDisabledServiceServesEmptyInventoryWithoutScanning(t *testing.T) {
	t.Setenv(envDisableFlag, "1")
	svc := New(Deps{
		Scan: func(context.Context) ([]Entry, error) {
			return nil, errors.New("scan must not run while disabled")
		},
		HostStats: func(context.Context) (HostStats, error) {
			return HostStats{}, errors.New("host stats must not run while disabled")
		},
	})
	if !svc.Disabled() {
		t.Fatal("service reports Disabled() = false with AO_NO_PROCINVENTORY=1")
	}
	inv, err := svc.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v, want nil", err)
	}
	if len(inv.Trees) != 0 || len(inv.Remnants) != 0 || inv.Host != nil {
		t.Fatalf("Inventory() = %+v, want the zero inventory", inv)
	}
}

func TestEnabledServiceByDefault(t *testing.T) {
	t.Setenv(envDisableFlag, "")
	svc := New(Deps{})
	if svc.Disabled() {
		t.Fatal("service reports Disabled() = true without AO_NO_PROCINVENTORY")
	}
}
