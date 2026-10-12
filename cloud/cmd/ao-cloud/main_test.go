package main

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/config"
)

// TestReconcilerKeepWarmFollowsOnlyGeneralThreshold pins the config to
// reconciler wiring for keep-warm: AO_CLOUD_IDLE_PAUSE_THRESHOLD=0 keeps Coder
// (and every non-Freestyle provider) warm regardless of the Freestyle
// threshold, and the idle scanner still runs whenever either threshold is set.
func TestReconcilerKeepWarmFollowsOnlyGeneralThreshold(t *testing.T) {
	cases := []struct {
		name         string
		general      time.Duration
		freestyle    time.Duration
		wantKeepWarm bool
		wantScanner  bool
	}{
		{"keep-warm with freestyle pausing", 0, 15 * time.Minute, true, true},
		{"keep-warm everywhere", 0, 0, true, false},
		{"general pausing freestyle warm", time.Hour, 0, false, true},
		{"both pausing", time.Hour, 15 * time.Minute, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{
				IdlePauseThreshold:          tc.general,
				FreestyleIdlePauseThreshold: tc.freestyle,
			}
			options := reconcilerOptions(cfg, nil, nil, nil, nil)
			if options.KeepWarm != tc.wantKeepWarm {
				t.Fatalf("reconciler KeepWarm = %v, want %v", options.KeepWarm, tc.wantKeepWarm)
			}
			if got := cfg.IdlePauseScannerEnabled(); got != tc.wantScanner {
				t.Fatalf("IdlePauseScannerEnabled = %v, want %v", got, tc.wantScanner)
			}
		})
	}
}
