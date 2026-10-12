import type { CloudClient, CurrentAccount, OrganizationMembership } from "@aoagents/cloud-client";
import { resolveMobileCloudOffering, selectActiveOrganization, type ResolvedMobileCloudOffering } from "./offering";

/** The control plane caps an organization's display name at 80 characters. */
const MAX_ORG_NAME = 80;

/** Workspace name for the auto-created org (the control plane caps it at 80 chars). */
export function orgDisplayNameForAccount(user: { displayName: string; email: string }): string {
	const name = user.displayName.trim() || user.email.split("@")[0]?.trim() || "";
	return (name === "" ? "Workspace" : name).slice(0, MAX_ORG_NAME);
}

/**
 * Resolve the same active workspace desktop uses; no independent org switcher.
 */
export async function resolveOrg(client: CloudClient, isCurrentSession: () => boolean = () => true): Promise<OrganizationMembership> {
	return (await resolveCloudWorkspace(client, isCurrentSession)).organization;
}

export async function resolveCloudWorkspace(client: CloudClient, isCurrentSession: () => boolean = () => true): Promise<{
	account: CurrentAccount; organization: OrganizationMembership; offering: ResolvedMobileCloudOffering;
}> {
	const account = await client.getCurrentAccount();
	// The shared client may already carry a replacement account's credentials.
	// A late lookup must not create that account's org from its predecessor.
	if (!isCurrentSession()) throw new Error("Cloud account changed.");
	const selected = selectActiveOrganization(account.organizations, account.activeOrganizationId);
	if (selected) return { account, organization: selected, offering: resolveMobileCloudOffering(account, selected) };
	const created = await client.createOrganization({
		displayName: orgDisplayNameForAccount(account.user),
	});
	if (!isCurrentSession()) throw new Error("Cloud account changed.");
	const resolvedAccount = { ...account, organizations: [created.organization], activeOrganizationId: created.organization.id };
	return { account: resolvedAccount, organization: created.organization, offering: resolveMobileCloudOffering(resolvedAccount, created.organization) };
}
