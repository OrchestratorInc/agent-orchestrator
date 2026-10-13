package domain

import (
	"encoding/json"
	"strings"
)

// Organization billing arrangements, read from the WorkOS organization metadata
// "billing" key (or AO_CLOUD_DEFAULT_ORG_BILLING when the key is absent).
const (
	// OrgBillingSelfServe is the default: the organization pays through the
	// self-serve Stripe plans when billing is enabled on the deployment.
	OrgBillingSelfServe = "self_serve"
	// OrgBillingContract exempts the organization from self-serve plans and
	// Stripe: it is billed under a separate agreement.
	OrgBillingContract = "contract"
)

// Offering kinds summarize which sandbox providers an organization may use:
// coder is the business (bring-your-own or managed Coder) offering, every other
// managed provider (freestyle, and docker on a local control plane) is the
// consumer offering.
const (
	OfferingKindB2B  = "b2b"
	OfferingKindB2C  = "b2c"
	OfferingKindBoth = "both"
	OfferingKindNone = "none"
)

// NormalizeOrgBilling maps a raw metadata or configuration value to an
// OrgBilling constant. It returns "" for a blank or unrecognized value, which
// callers treat as "not set" and fall back to the deployment default.
func NormalizeOrgBilling(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "contract":
		return OrgBillingContract
	case "self_serve", "self-serve", "selfserve":
		return OrgBillingSelfServe
	default:
		return ""
	}
}

// BillingMode is the single rule for whether self-serve billing applies to an
// organization. It is "off" when the deployment has no Stripe configured,
// "contract" when the organization is billed under an agreement (metadata
// billing=contract), runs on the internal plan, or was grandfathered when
// billing was turned on, and "self_serve" otherwise.
func BillingMode(orgBilling, plan string, grandfathered, stripeEnabled bool) string {
	if !stripeEnabled {
		return BillingModeOff
	}
	if NormalizeOrgBilling(orgBilling) == OrgBillingContract || plan == PlanInternal || grandfathered {
		return BillingModeContract
	}
	return BillingModeSelfServe
}

// OfferingKind classifies an organization's allowed sandbox providers: coder
// only is b2b, any other provider only is b2c, a mix is both, and an empty list
// is none (the organization cannot run cloud sessions).
func OfferingKind(providers []string) string {
	b2b, b2c := false, false
	for _, provider := range providers {
		if provider == "coder" {
			b2b = true
		} else if provider != "" {
			b2c = true
		}
	}
	switch {
	case b2b && b2c:
		return OfferingKindBoth
	case b2b:
		return OfferingKindB2B
	case b2c:
		return OfferingKindB2C
	default:
		return OfferingKindNone
	}
}

// OrchestratorPlacement is where a live orchestrator's sandbox runs, read so a
// child it spawns lands on the same provider and, for Coder, the same
// bring-your-own connection and project dev-kit config.
type OrchestratorPlacement struct {
	// Provider is the orchestrator sandbox's immutable provider.
	Provider string
	// ConnectionID is the orchestrator sandbox's provider_connection_id, or ""
	// when it runs on the deployment default connection.
	ConnectionID string
	// ProjectConfig is the orchestrator project's config (coder dev-kit config
	// lives under its "coder" key).
	ProjectConfig json.RawMessage
	// CoderConnections are the organization's Coder provider connections,
	// without secrets.
	CoderConnections []ProviderConnection
}
