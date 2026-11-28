import { CloudError } from "../cloud/errors";
import { approvalCardText, offersSiteRule, type ApprovalCard, type ApprovalDecision, type CloudApproval, type DecisionResult } from "./types";

type Client = {
	listBrowserApprovals(orgId: string, sessionId: string): Promise<{ pending: CloudApproval[] }>;
	decideBrowserApproval(orgId: string, sessionId: string, approvalId: string, decision: ApprovalDecision): Promise<unknown>;
};

export function cloudApprovalCard(a: CloudApproval): ApprovalCard {
	// Only the session's owner or starter may decide (#8601).
	const canDecide = a.canDecide !== false;
	return {
		id: a.id, sessionId: a.sessionId, summary: approvalCardText(a.summary || a.action), site: approvalCardText(a.site),
		reason: approvalCardText(a.reason), expiresAt: a.expiresAt, canDecide,
		deciderName: canDecide ? undefined : approvalCardText(a.decider?.name ?? "") || undefined,
		canAllowSite: offersSiteRule(a.site, a.category),
	};
}

export async function listCloudApprovals(client: Client, orgId: string, sessionId: string): Promise<ApprovalCard[]> {
	const { pending } = await client.listBrowserApprovals(orgId, sessionId);
	return (pending ?? []).map(cloudApprovalCard);
}

export async function decideCloudApproval(client: Client, orgId: string, sessionId: string, id: string, decision: ApprovalDecision): Promise<DecisionResult> {
	try {
		await client.decideBrowserApproval(orgId, sessionId, id, decision);
		return "decided";
	} catch (cause) {
		if (cause instanceof CloudError && (cause.status === 409 || cause.status === 404)) return "already-decided";
		throw cause;
	}
}
