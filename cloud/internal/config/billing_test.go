package config

import (
	"strings"
	"testing"
	"time"
)

func TestBillingIsOffWithoutAStripeKey(t *testing.T) {
	setWorkOSTestEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BillingEnabled() {
		t.Fatal("billing enabled without AO_CLOUD_STRIPE_SECRET_KEY")
	}
	if cfg.IdlePauseThreshold != time.Hour || cfg.FreestyleIdlePauseThreshold != 15*time.Minute {
		t.Fatalf("idle pause defaults = %v, freestyle %v; want 1h and 15m",
			cfg.IdlePauseThreshold, cfg.FreestyleIdlePauseThreshold)
	}
}

func TestBillingRequiresWebhookSecretAndPrices(t *testing.T) {
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_PUBLIC_URL", "https://cp.example.test")
	t.Setenv("AO_CLOUD_STRIPE_SECRET_KEY", "rk_test_123")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AO_CLOUD_STRIPE_WEBHOOK_SECRET") {
		t.Fatalf("missing webhook secret: err = %v", err)
	}
	t.Setenv("AO_CLOUD_STRIPE_WEBHOOK_SECRET", "whsec_123")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AO_CLOUD_STRIPE_PRICE_IDS") {
		t.Fatalf("missing prices: err = %v", err)
	}
	t.Setenv("AO_CLOUD_STRIPE_PRICE_IDS", `{"starter":"prod_123"}`)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "price_") {
		t.Fatalf("a product id instead of a price id: err = %v", err)
	}
	t.Setenv("AO_CLOUD_STRIPE_PRICE_IDS", `{"starter":"price_1","pro":"price_2"}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.BillingEnabled() || cfg.BillingReturn() != "https://cp.example.test/billing/return" ||
		cfg.BillingPastDueGrace != 7*24*time.Hour || len(cfg.StripePriceIDs) != 2 {
		t.Fatalf("billing config = %+v", cfg)
	}
}

func TestBillingExemptCutoffParses(t *testing.T) {
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_BILLING_EXEMPT_ORGS_CREATED_BEFORE", "2026-10-11T00:00:00Z")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.BillingExemptOrgsCreatedBefore.Equal(time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("cutoff = %v", cfg.BillingExemptOrgsCreatedBefore)
	}
	t.Setenv("AO_CLOUD_BILLING_EXEMPT_ORGS_CREATED_BEFORE", "yesterday")
	if _, err := Load(); err == nil {
		t.Fatal("an unparseable cutoff was accepted")
	}
}

func TestBillingBusyGraceAndPaywalledFeatures(t *testing.T) {
	setWorkOSTestEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BillingBusyGrace != 5*time.Minute || len(cfg.PaywalledFeatures) != 0 {
		t.Fatalf("defaults: grace %v, paywalled %v; want 5m and nothing gated", cfg.BillingBusyGrace, cfg.PaywalledFeatures)
	}
	t.Setenv("AO_CLOUD_BILLING_BUSY_GRACE", "90s")
	t.Setenv("AO_CLOUD_PAYWALLED_FEATURES", " SSO, audit-log ,")
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if cfg.BillingBusyGrace != 90*time.Second || strings.Join(cfg.PaywalledFeatures, ",") != "sso,audit-log" {
		t.Fatalf("grace %v, paywalled %v", cfg.BillingBusyGrace, cfg.PaywalledFeatures)
	}
}
