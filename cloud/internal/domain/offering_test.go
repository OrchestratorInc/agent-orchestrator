package domain

import "testing"

func TestBillingMode(t *testing.T) {
	cases := []struct {
		name          string
		orgBilling    string
		plan          string
		grandfathered bool
		stripe        bool
		want          string
	}{
		{"no stripe is off even for self-serve", OrgBillingSelfServe, "free", false, false, BillingModeOff},
		{"no stripe is off even for contract", OrgBillingContract, "free", false, false, BillingModeOff},
		{"self-serve org on a paid deployment", OrgBillingSelfServe, "free", false, true, BillingModeSelfServe},
		{"unset arrangement reads as self-serve", "", "starter", false, true, BillingModeSelfServe},
		{"contract metadata", OrgBillingContract, "free", false, true, BillingModeContract},
		{"contract metadata any spelling", " Contract ", "free", false, true, BillingModeContract},
		{"internal plan", OrgBillingSelfServe, PlanInternal, false, true, BillingModeContract},
		{"grandfathered org", OrgBillingSelfServe, "free", true, true, BillingModeContract},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BillingMode(tc.orgBilling, tc.plan, tc.grandfathered, tc.stripe); got != tc.want {
				t.Fatalf("BillingMode(%q, %q, %v, %v) = %q, want %q",
					tc.orgBilling, tc.plan, tc.grandfathered, tc.stripe, got, tc.want)
			}
		})
	}
}

func TestOfferingKind(t *testing.T) {
	cases := []struct {
		providers []string
		want      string
	}{
		{nil, OfferingKindNone},
		{[]string{}, OfferingKindNone},
		{[]string{"coder"}, OfferingKindB2B},
		{[]string{"freestyle"}, OfferingKindB2C},
		{[]string{"docker"}, OfferingKindB2C},
		{[]string{"coder", "freestyle"}, OfferingKindBoth},
		{[]string{"freestyle", "coder"}, OfferingKindBoth},
	}
	for _, tc := range cases {
		if got := OfferingKind(tc.providers); got != tc.want {
			t.Fatalf("OfferingKind(%v) = %q, want %q", tc.providers, got, tc.want)
		}
	}
}

func TestNormalizeOrgBilling(t *testing.T) {
	for raw, want := range map[string]string{
		"":           "",
		"contract":   OrgBillingContract,
		"CONTRACT":   OrgBillingContract,
		"self_serve": OrgBillingSelfServe,
		"self-serve": OrgBillingSelfServe,
		"invoice":    "",
	} {
		if got := NormalizeOrgBilling(raw); got != want {
			t.Fatalf("NormalizeOrgBilling(%q) = %q, want %q", raw, got, want)
		}
	}
}
