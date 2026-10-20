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
