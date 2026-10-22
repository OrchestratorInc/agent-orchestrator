package domain

import "testing"

// Plans apply to every organization once Stripe is configured: Freestyle is
// paid by everyone, contract and internal organizations included.
func TestBillingMode(t *testing.T) {
	if got := BillingMode(false); got != BillingModeOff {
		t.Fatalf("no stripe: %q, want off", got)
	}
	if got := BillingMode(true); got != BillingModeSelfServe {
		t.Fatalf("stripe configured: %q, want self_serve", got)
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
