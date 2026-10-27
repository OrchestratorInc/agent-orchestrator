package config

import (
	"slices"
	"strings"
	"testing"
)

func TestDefaultOrgOfferingDefaults(t *testing.T) {
	setWorkOSTestEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DefaultOrgCapabilities) != 0 || cfg.DefaultOrgBilling != "self_serve" {
		t.Fatalf("defaults = %v / %q, want no capabilities and self_serve", cfg.DefaultOrgCapabilities, cfg.DefaultOrgBilling)
	}
}

func TestDefaultOrgOfferingParses(t *testing.T) {
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_DEFAULT_ORG_CAPABILITIES", " Freestyle, freestyle,feature-x ")
	t.Setenv("AO_CLOUD_DEFAULT_ORG_BILLING", "Contract")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.DefaultOrgCapabilities, []string{"freestyle", "feature-x"}) {
		t.Fatalf("capabilities = %v", cfg.DefaultOrgCapabilities)
	}
	if cfg.DefaultOrgBilling != "contract" {
		t.Fatalf("billing = %q, want contract", cfg.DefaultOrgBilling)
	}
}

func TestDefaultOrgBillingRejectsUnknown(t *testing.T) {
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_DEFAULT_ORG_BILLING", "invoice")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AO_CLOUD_DEFAULT_ORG_BILLING") {
		t.Fatalf("err = %v, want an AO_CLOUD_DEFAULT_ORG_BILLING error", err)
	}
}

// The Coder rollout flags are on for a development control plane, off on a
// hosted one unless its offering turns them on, and refuse a value that is
// not true or false.
func TestCoderRolloutFlags(t *testing.T) {
	setWorkOSTestEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CoderReadableWorkspaceNames || !cfg.CoderWorkerWorkingFolder {
		t.Fatalf("development defaults = %v / %v, want both on", cfg.CoderReadableWorkspaceNames, cfg.CoderWorkerWorkingFolder)
	}
	for _, key := range []string{"AO_CLOUD_CODER_READABLE_NAMES", "AO_CLOUD_CODER_WORKER_WORKING_FOLDER"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "false")
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if key == "AO_CLOUD_CODER_READABLE_NAMES" && cfg.CoderReadableWorkspaceNames ||
				key == "AO_CLOUD_CODER_WORKER_WORKING_FOLDER" && cfg.CoderWorkerWorkingFolder {
				t.Fatalf("%s=false left it on", key)
			}
			t.Setenv(key, "yes")
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("err = %v, want a %s error", err, key)
			}
		})
	}
}

// A hosted control plane passes false as the fallback, so an unset flag stays
// off there; only an explicit true or false overrides the fallback.
func TestStrictBoolEnvKeepsTheFallbackOnlyWhenUnset(t *testing.T) {
	const key = "AO_CLOUD_CODER_READABLE_NAMES"
	for value, want := range map[string]bool{"": false, " TRUE ": true, "false": false} {
		t.Setenv(key, value)
		if got, err := strictBoolEnv(key, false); err != nil || got != want {
			t.Fatalf("%q = %v, %v; want %v", value, got, err, want)
		}
	}
	t.Setenv(key, "")
	if got, _ := strictBoolEnv(key, true); !got {
		t.Fatal("unset did not keep the fallback")
	}
}
