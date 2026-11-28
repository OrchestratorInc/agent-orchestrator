import type { Href } from "expo-router";
import { CLOUD_HOST_ID } from "./mapping";

/**
 * Where a tapped Orchestrator Cloud notification goes: its session, else its project,
 * else the cloud inbox. Null for a notification a paired machine sent, which
 * keeps its own routing.
 */
export function cloudPushTarget(data: { source?: string; type?: string; sessionId?: string; projectId?: string; approvalId?: string }): Href | null {
	if (data.source !== "cloud") return null;
	// A browser approval opens its session with the approval's card.
	if (data.type === "browser_approval" && data.sessionId && data.approvalId) {
		return { pathname: "/session/[id]", params: { id: data.sessionId, hostId: CLOUD_HOST_ID, approval: data.approvalId } };
	}
	if (data.sessionId) return { pathname: "/session/[id]", params: { id: data.sessionId, hostId: CLOUD_HOST_ID } };
	if (data.projectId) return { pathname: "/project/[id]", params: { id: data.projectId, hostId: CLOUD_HOST_ID } };
	return "/notifications";
}

/**
 * The name of the organization a tapped Cloud notification came from, when it
 * isn't the one the phone works in. The phone works in one organization, as
 * the desktop does (selectActiveOrganization), and every Cloud screen loads
 * through it, so a session from another can't open here. Undefined when it
 * is the same one, or either is not known yet.
 */
export function foreignCloudPushOrganization(
	data: { source?: string; orgId?: string },
	currentOrgId: string | undefined,
	organizations: readonly { id: string; displayName: string }[],
): string | undefined {
	if (data.source !== "cloud" || !data.orgId || !currentOrgId || data.orgId === currentOrgId) return undefined;
	return organizations.find((item) => item.id === data.orgId)?.displayName || "another organization";
}
