package contract

type AgentCapability string

const (
	CapabilityInterfaceChat  AgentCapability = "interface.chat"
	CapabilityInterfaceTUI   AgentCapability = "interface.tui"
	CapabilityModelCatalog   AgentCapability = "model.catalog"
	CapabilityCustomModel    AgentCapability = "model.custom"
	CapabilityAttachments    AgentCapability = "attachments"
	CapabilityBrowserPreview AgentCapability = "browser.preview"
	CapabilityReviewExecute  AgentCapability = "review.execute"
	CapabilityResume         AgentCapability = "session.resume"
)

type AgentInstallationState string

const (
	AgentInstallationInstalled     AgentInstallationState = "installed"
	AgentInstallationNotInstalled  AgentInstallationState = "not_installed"
	AgentInstallationUnknown       AgentInstallationState = "unknown"
	AgentInstallationNotApplicable AgentInstallationState = "not_applicable"
)

type AgentAuthenticationState string

const (
	AgentAuthenticationAuthorized    AgentAuthenticationState = "authorized"
	AgentAuthenticationUnauthorized  AgentAuthenticationState = "unauthorized"
	AgentAuthenticationUnknown       AgentAuthenticationState = "unknown"
	AgentAuthenticationNotApplicable AgentAuthenticationState = "not_applicable"
)

type AgentOrganizationPolicy string

const (
	OrganizationPolicyNotApplicable AgentOrganizationPolicy = "not_applicable"
	OrganizationPolicyAllowed       AgentOrganizationPolicy = "allowed"
	OrganizationPolicyDenied        AgentOrganizationPolicy = "denied"
)

type AgentAvailability struct {
	Available          bool                     `json:"available"`
	Installation       AgentInstallationState   `json:"installation"`
	Authentication     AgentAuthenticationState `json:"authentication"`
	OrganizationPolicy AgentOrganizationPolicy  `json:"organizationPolicy"`
	Reason             string                   `json:"reason,omitempty"`
}

type AgentProfile struct {
	ID           string            `json:"id"`
	Label        string            `json:"label"`
	Capabilities []AgentCapability `json:"capabilities"`
	Availability AgentAvailability `json:"availability"`
}

func HasAgentCapability(profile AgentProfile, capability AgentCapability) bool {
	for _, candidate := range profile.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}
