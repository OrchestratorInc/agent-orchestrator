/**
 * Resolves the organization all renderer control-plane calls are scoped to:
 * the active org from GET /me (selectActiveOrganization), or, for a user with
 * none, a freshly created one (POST /orgs) named from the account. The UI has
 * no org switcher yet.
 *
 * /me exposes no active-org id and the renderer never sees the WorkOS token.
 * The control plane scopes /me `organizations` to the token's WorkOS org plus
 * orgs with no WorkOS link (a personal org), oldest first, and gives each its
 * own offering. selectActiveOrganization prefers the org whose offering
 * includes coder (the WorkOS team org) over a personal org, else the first.
 * lib/cloud-orchestrator.ts applies the same rule. If /me gains an active-org
 * id, prefer it here.
 */

import { useQuery } from "@tanstack/react-query";
import type { CloudCpOrganization, CloudCpUser } from "../lib/cloud-cp";
import { selectActiveOrganization } from "../lib/cloud-offering";
import { useCloudCp } from "./useCloudCp";

export const cloudOrgQueryKey = ["cloud-org"] as const;

/** Workspace name for the auto-created org (the control plane caps it at 80 chars). */
export function orgDisplayNameForAccount(user: Pick<CloudCpUser, "displayName" | "email">): string {
	const name = user.displayName.trim() || user.email.split("@")[0]?.trim() || "";
	return (name === "" ? "Workspace" : name).slice(0, 80);
}

export interface UseCloudOrgResult {
	/** The resolved org, or undefined while loading / not ready / failed. */
	org: CloudCpOrganization | undefined;
	isLoading: boolean;
	error: unknown;
	/** Retry the get-or-create request (used when a dialog opens before it settles). */
	refetch: () => Promise<{ data?: CloudCpOrganization; error?: unknown }>;
	/** Mirrors useCloudCp().ready so callers can gate on one hook. */
	ready: boolean;
}

export function useCloudOrg(): UseCloudOrgResult {
	const { client, ready, baseUrl } = useCloudCp();
	const query = useQuery({
		queryKey: [...cloudOrgQueryKey, baseUrl],
		enabled: ready,
		// Re-resolve the org eagerly: membership CAN change out from under a live
		// session (a user is moved to their own org, or removed from a shared one),
		// and a stale selection strands every org-scoped call on a dead org with a
		// 403. A short staleTime plus refetch-on-focus keeps the selection current;
		// a 403 anywhere also invalidates this query (see lib/query-client) so the
		// app self-heals without a restart. /me is a cheap, deduped read.
		staleTime: 30_000,
		refetchOnWindowFocus: true,
		refetchOnReconnect: true,
		retry: 1,
		queryFn: async (): Promise<CloudCpOrganization> => {
			const me = await client.me();
			const active = selectActiveOrganization(me.organizations, me.activeOrganizationId);
			if (active !== undefined) return active;
			const created = await client.createOrganization({
				displayName: orgDisplayNameForAccount(me.user),
			});
			return created.organization;
		},
	});
	return {
		org: query.data,
		isLoading: query.isLoading,
		error: query.error ?? undefined,
		refetch: query.refetch,
		ready,
	};
}
