import type { QueryClient } from "@tanstack/react-query";
import type {
	CloudCpBillingMode,
	CloudCpBillingSummary,
	CloudCpMeResponse,
	CloudCpOfferingKind,
	CloudCpOrganization,
} from "./cloud-cp/types";

export const CODER_PROVIDER = "coder";
export const FREESTYLE_PROVIDER = "freestyle";
export const SHARE_FEATURE = "share";
export const STANDALONE_AGENTS_FEATURE = "standalone_agents";
export const ATTACHMENTS_FEATURE = "attachments";
export const QUEUED_EDIT_FEATURE = "queued-edit";
export const QUEUED_DELETE_FEATURE = "queued-delete";
export const QUEUED_STEER_FEATURE = "queued-steer";
export const CLOUD_MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024;

export interface ResolvedCloudOffering {
	providers: string[];
	defaultProvider: string;
	kind: CloudCpOfferingKind;
	billingMode: CloudCpBillingMode | undefined;
	features: string[];
	maxAttachmentBytes: number | undefined;
	customCoder: boolean;
	artifacts: boolean;
	coderWorkerWorkingFolder: boolean;
	source: "server" | "fallback";
}

export function offeringKindFor(providers: readonly string[]): CloudCpOfferingKind {
	const coder = providers.includes(CODER_PROVIDER);
	const freestyle = providers.includes(FREESTYLE_PROVIDER);
	if (coder && freestyle) return "both";
	if (coder) return "b2b";
	if (freestyle) return "b2c";
	return providers.length > 0 ? "b2c" : "none";
}

export function billingModeFromSummary(summary: CloudCpBillingSummary | undefined): CloudCpBillingMode | undefined {
	if (!summary) return undefined;
	if (summary.billingMode) return summary.billingMode;
	if (!summary.enabled) return "off";
	return summary.exempt ? "contract" : "self_serve";
}

export function selectActiveOrganization<T extends Pick<CloudCpOrganization, "id" | "offering">>(
	organizations: readonly T[],
	activeOrganizationId?: string,
): T | undefined {
	const reported = activeOrganizationId ? organizations.find((org) => org.id === activeOrganizationId) : undefined;
	if (reported) return reported;
	return organizations.find((org) => org.offering?.kind === "b2b" || org.offering?.kind === "both") ?? organizations[0];
}

export function resolveCloudOffering(
	me: CloudCpMeResponse | undefined,
	billing?: CloudCpBillingSummary,
	billingFailed = false,
	activeOrgId?: string,
): ResolvedCloudOffering | undefined {
	if (!me) return undefined;
	const organizations = me.organizations ?? [];
	const org = organizations.find((candidate) => candidate.id === activeOrgId) ?? selectActiveOrganization(organizations, me.activeOrganizationId);
	const describedByTopLevel = me.activeOrganizationId ? org?.id === me.activeOrganizationId : org === organizations[0];
	const offering = org?.offering ?? (org === undefined || describedByTopLevel ? me.offering : undefined);
	if (offering) {
		const providers = offering.providers ?? [];
		return {
			providers,
			defaultProvider: offering.defaultProvider || providers[0] || "",
			kind: offering.kind ?? offeringKindFor(providers),
			billingMode: offering.billingMode ?? billingModeFromSummary(billing) ?? (billingFailed ? "off" : undefined),
			features: offering.features ?? [],
			maxAttachmentBytes: offering.features?.includes(ATTACHMENTS_FEATURE)
				? (offering.maxAttachmentBytes && offering.maxAttachmentBytes > 0 ? offering.maxAttachmentBytes : CLOUD_MAX_ATTACHMENT_BYTES)
				: undefined,
			customCoder: offering.customCoder ?? providers.includes(CODER_PROVIDER),
			artifacts: offering.artifacts ?? false,
			coderWorkerWorkingFolder: offering.coderWorkerWorkingFolder === true,
			source: "server",
		};
	}
	const providers = me.sandboxProviders?.available ?? [];
	const configuredDefault = me.sandboxProviders?.default ?? "";
	return {
		providers,
		defaultProvider: providers.includes(configuredDefault) ? configuredDefault : providers[0] ?? "",
		kind: offeringKindFor(providers),
		billingMode: billingModeFromSummary(billing) ?? (billingFailed ? "off" : undefined),
		features: [],
		maxAttachmentBytes: undefined,
		customCoder: providers.includes(CODER_PROVIDER),
		artifacts: false,
		coderWorkerWorkingFolder: false,
		source: "fallback",
	};
}

export function offersProviderChoice(offering: Pick<ResolvedCloudOffering, "providers" | "source"> | undefined): boolean {
	return offering?.source === "server" && offering.providers.length > 1;
}

export function canManageBilling(org: Pick<CloudCpOrganization, "role"> | undefined): boolean {
	return org?.role === "owner" || org?.role === "admin";
}

export function projectSandboxProvider(
	config: object | null | undefined,
	offering: Pick<ResolvedCloudOffering, "defaultProvider"> | undefined,
): string {
	const fields = config as { sandboxProvider?: unknown; coder?: unknown } | null | undefined;
	const stored = fields?.sandboxProvider;
	if (typeof stored === "string" && stored.trim() !== "") return stored.trim().toLowerCase();
	if (fields?.coder !== null && typeof fields?.coder === "object") return CODER_PROVIDER;
	return offering?.defaultProvider ?? "";
}

export const cloudMeQueryKey = ["cloud-me"] as const;
const cloudBillingQueryPrefix = ["cloud-billing"] as const;

export function cachedCloudBillingMode(queryClient: QueryClient): CloudCpBillingMode | undefined {
	for (const [, me] of queryClient.getQueriesData<CloudCpMeResponse>({ queryKey: cloudMeQueryKey })) {
		const resolved = resolveCloudOffering(me);
		if (resolved?.source === "server" && resolved.billingMode) return resolved.billingMode;
	}
	for (const [, summary] of queryClient.getQueriesData<CloudCpBillingSummary>({ queryKey: cloudBillingQueryPrefix })) {
		const mode = billingModeFromSummary(summary);
		if (mode) return mode;
	}
	return undefined;
}
