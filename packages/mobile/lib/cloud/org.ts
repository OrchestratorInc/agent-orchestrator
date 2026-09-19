import type { CloudClient, OrganizationMembership } from "@aoagents/cloud-client";

/** The control plane caps an organization's display name at 80 characters. */
const MAX_ORG_NAME = 80;

/** Workspace name for the auto-created org (the control plane caps it at 80 chars). */
export function orgDisplayNameForAccount(user: { displayName: string; email: string }): string {
	const name = user.displayName.trim() || user.email.split("@")[0]?.trim() || "";
	return (name === "" ? "Workspace" : name).slice(0, MAX_ORG_NAME);
}

/**
 * The organization every cloud call is scoped to: the first from /me, or a
 * freshly created one. First-org is the same deliberate v0 simplification the
 * desktop makes (useCloudOrg.ts) — there is no org switcher yet.
 */
export async function resolveOrg(client: CloudClient): Promise<OrganizationMembership> {
	const account = await client.getCurrentAccount();
	const first = account.organizations[0];
	if (first !== undefined) return first;
	const created = await client.createOrganization({
		displayName: orgDisplayNameForAccount(account.user),
	});
	return created.organization;
}
