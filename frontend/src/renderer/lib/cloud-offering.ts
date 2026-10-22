// The active organization's AO Cloud offering, resolved once so every surface
// gates on the same facts. The control plane is the source of truth (`/me`
// offering); a control plane that predates it is read through a fallback that
// derives the same shape from `/me` sandboxProviders and the `/billing` summary.
//
// Gating rules the renderer applies:
// - Coder-only UI (Coder settings page, template picker, workspace prefix)
//   appears only when the org's providers include coder.
// - Billing UI (Upgrade page, plan badge, plan prompts) appears only when the
//   billing mode is "self_serve".

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

export interface ResolvedCloudOffering {
	providers: string[];
	/** The provider a project uses when it does not pick one; "" for no cloud. */
	defaultProvider: string;
	kind: CloudCpOfferingKind;
	/** Undefined while a fallback still waits on the billing summary. */
	billingMode: CloudCpBillingMode | undefined;
	features: string[];
	/**
	 * The org may connect its own Coder deployment (it holds the coder
	 * capability), which shows the Coder settings page. An older control plane
	 * that does not send it offered Coder only to such orgs, so the provider
	 * list stands in.
	 */
	customCoder: boolean;
	/**
	 * An ao-worker Coder session checks out in the project's working folder.
	 * Off when the control plane does not say so (an older one never does):
	 * a working folder then applies only with the workspace user.
	 */
	coderWorkerWorkingFolder: boolean;
	/** "server" when `/me` carried an offering, "fallback" when it was derived. */
	source: "server" | "fallback";
}

/** Kind from the provider list, for a control plane that does not send one. */
export function offeringKindFor(providers: readonly string[]): CloudCpOfferingKind {
	const coder = providers.includes(CODER_PROVIDER);
	const freestyle = providers.includes(FREESTYLE_PROVIDER);
	if (coder && freestyle) return "both";
	if (coder) return "b2b";
	if (freestyle) return "b2c";
	// Any other provider (docker, a local control plane) carries nothing
	// B2B-specific, so it presents like the consumer offering.
	return providers.length > 0 ? "b2c" : "none";
}

/** Billing mode from a `/billing` summary, preferring the server's own field. */
export function billingModeFromSummary(summary: CloudCpBillingSummary | undefined): CloudCpBillingMode | undefined {
	if (!summary) return undefined;
	if (summary.billingMode) return summary.billingMode;
	if (!summary.enabled) return "off";
	return summary.exempt ? "contract" : "self_serve";
}

/**
 * The organization every control-plane call is scoped to. /me lists the
 * token's WorkOS org plus any org without a WorkOS link (a personal org),
 * oldest first, and each carries its own offering. A user in both a team org
 * and a personal org gets the team org: the one whose offering includes coder
 * (kind b2b or both). Otherwise, and on a control plane without per-org
 * offerings, the first org. A control plane that reports
 * activeOrganizationId (the token's WorkOS org) decides instead.
 */
export function selectActiveOrganization<T extends Pick<CloudCpOrganization, "id" | "offering">>(
	organizations: readonly T[],
	activeOrganizationId?: string,
): T | undefined {
	const reported = activeOrganizationId ? organizations.find((org) => org.id === activeOrganizationId) : undefined;
	if (reported) return reported;
	return organizations.find((org) => org.offering?.kind === "b2b" || org.offering?.kind === "both") ?? organizations[0];
}

/**
 * Resolves the offering of the active org (`activeOrgId`, else
 * selectActiveOrganization) from `/me`: the org's own offering, else the
 * top-level one when the org is organizations[0] (which it describes). A
 * control plane that predates offerings falls back to `/me` sandboxProviders
 * plus the `/billing` summary. Returns undefined until `/me` loads.
 * `billingFailed` reads a failed `/billing` fetch as "off" so an older control
 * plane without billing still settles.
 */
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
			customCoder: offering.customCoder ?? providers.includes(CODER_PROVIDER),
			coderWorkerWorkingFolder: offering.coderWorkerWorkingFolder === true,
			source: "server",
		};
	}
	const providers = me.sandboxProviders?.available ?? [];
	const configuredDefault = me.sandboxProviders?.default ?? "";
	return {
		providers,
		// An older control plane reports the deployment default even when the org
		// is not entitled to it; only an allowed provider can be the default.
		defaultProvider: providers.includes(configuredDefault) ? configuredDefault : providers[0] ?? "",
		kind: offeringKindFor(providers),
		billingMode: billingModeFromSummary(billing) ?? (billingFailed ? "off" : undefined),
		features: [],
		customCoder: providers.includes(CODER_PROVIDER),
		coderWorkerWorkingFolder: false,
		source: "fallback",
	};
}

/**
 * A project picks its sandbox provider only when the org has more than one and
 * the control plane sent the offering itself. An older control plane's fallback
 * providers come from `/me` sandboxProviders, and it ignores the project's
 * stored provider, so a choice there would not take effect.
 */
export function offersProviderChoice(offering: Pick<ResolvedCloudOffering, "providers" | "source"> | undefined): boolean {
	return offering?.source === "server" && offering.providers.length > 1;
}

/** Org roles that may buy, change or reset a plan (the control plane's RequireOrgAdmin). */
export function canManageBilling(org: Pick<CloudCpOrganization, "role"> | undefined): boolean {
	return org?.role === "owner" || org?.role === "admin";
}

/**
 * The provider a project's sessions run on: the provider stored on the project
 * (config.sandboxProvider), else Coder for a project carrying Coder dev-kit
 * config (set up before projects recorded their provider), else the org's
 * default. The control plane applies the same rule when a session is created
 * without a provider.
 */
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

/**
 * The billing mode from the query cache, for code outside React (the plan
 * refusal handler, the settings deep link). Undefined when nothing has loaded
 * yet, which callers treat as "not self-serve" so a B2B user never lands on
 * plans.
 */
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
