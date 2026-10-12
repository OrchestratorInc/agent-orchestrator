import type { CurrentAccount, OrganizationMembership } from "@aoagents/cloud-client";

export interface ResolvedMobileCloudOffering {
	providers: string[];
	defaultProvider: string;
	kind: "none" | "b2b" | "b2c" | "both";
	billingMode: "off" | "contract" | "self_serve" | undefined;
	features: string[];
	source: "server" | "fallback";
}
export function selectActiveOrganization(organizations: readonly OrganizationMembership[], activeOrganizationId?: string): OrganizationMembership | undefined {
	return organizations.find((org) => org.id === activeOrganizationId)
		?? organizations.find((org) => org.offering?.kind === "b2b" || org.offering?.kind === "both")
		?? organizations[0];
}
function offeringKind(providers: readonly string[]): ResolvedMobileCloudOffering["kind"] {
	if (providers.includes("coder")) return providers.includes("freestyle") ? "both" : "b2b";
	return providers.length ? "b2c" : "none";
}
export function resolveMobileCloudOffering(account: CurrentAccount, organization: OrganizationMembership): ResolvedMobileCloudOffering {
	const described = account.activeOrganizationId ? organization.id === account.activeOrganizationId : organization.id === account.organizations[0]?.id;
	const offering = organization.offering ?? (described ? account.offering : undefined);
	if (offering) return {
		providers: offering.providers, defaultProvider: offering.defaultProvider || offering.providers[0] || "",
		kind: offering.kind, billingMode: offering.billingMode, features: offering.features, source: "server",
	};
	const providers = account.sandboxProviders?.available ?? [];
	const configuredDefault = account.sandboxProviders?.default ?? "";
	return { providers, defaultProvider: providers.includes(configuredDefault) ? configuredDefault : providers[0] ?? "",
		kind: offeringKind(providers), billingMode: undefined, features: [], source: "fallback" };
}
export function offersProviderChoice(offering: ResolvedMobileCloudOffering | undefined): boolean {
	return offering?.source === "server" && offering.providers.length > 1;
}
