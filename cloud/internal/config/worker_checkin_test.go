package config

import (
	"strings"
	"testing"
	"time"
)

func TestWorkerCheckInTimeoutDefaultsAndOverrides(t *testing.T) {
	setWorkOSTestEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerCheckInTimeout != 12*time.Minute {
		t.Fatalf("default check-in timeout = %s, want 12m", cfg.WorkerCheckInTimeout)
	}
	t.Setenv("AO_CLOUD_WORKER_CHECKIN_TIMEOUT", "15m")
	if cfg, err = Load(); err != nil || cfg.WorkerCheckInTimeout != 15*time.Minute {
		t.Fatalf("override = %s, %v; want 15m", cfg.WorkerCheckInTimeout, err)
	}
}

// A check-in timeout inside the worker's own bootstrap retries would relaunch
// a worker that is still checking in.
func TestWorkerCheckInTimeoutMustOutlastBootstrapRetries(t *testing.T) {
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_WORKER_CHECKIN_TIMEOUT", "8m")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AO_CLOUD_WORKER_CHECKIN_TIMEOUT") {
		t.Fatalf("err = %v, want the check-in timeout rejected", err)
	}
}
