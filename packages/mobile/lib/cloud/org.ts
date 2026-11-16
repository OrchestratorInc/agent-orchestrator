import type { CloudClient } from "./client";
import type { CloudMe, CloudOrganization } from "./types";

/**
 * The organization the phone works in, chosen exactly as the desktop chooses
 * it (frontend/src/renderer/lib/cloud-offering.ts selectActiveOrganization),
 * so a phone and a desktop signed in to the same account see the same
 * projects: the one the control plane reports active, else the first that can
 * run Coder (a team workspace), else the first.
 */
export function selectActiveOrganization(me: Pick<CloudMe, "organizations" | "activeOrganizationId">): CloudOrganization | null {
	const { organizations, activeOrganizationId } = me;
	if (!organizations.length) return null;
	const active = activeOrganizationId ? organizations.find((item) => item.id === activeOrganizationId) : undefined;
	if (active) return active;
	return organizations.find((item) => item.offering?.kind === "b2b" || item.offering?.kind === "both") ?? organizations[0];
}

/** The workspace name a first sign-in creates, as the desktop names it: 1-80 characters. */
export function defaultOrganizationName(me: Pick<CloudMe, "user">): string {
	const display = me.user.displayName?.trim();
	const fromEmail = me.user.email.split("@")[0]?.trim();
	const name = display || fromEmail || "My workspace";
	return name.slice(0, 80);
}

/**
 * Resolve the active organization, creating a personal one when the account
 * has none yet (a first sign-in from the phone, before the desktop ever ran).
 */
export async function resolveOrganization(client: Pick<CloudClient, "createOrganization">, me: CloudMe): Promise<CloudOrganization> {
	const selected = selectActiveOrganization(me);
	if (selected) return selected;
	const { organization } = await client.createOrganization(defaultOrganizationName(me));
	return organization;
}
