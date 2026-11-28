// What a browser approval notification does when answered. Deny acts from the
// notification; Allow (and a plain tap) opens the app to the card, so an
// action is never allowed from a locked phone.

export const BROWSER_APPROVAL_CATEGORY = "browser_approval";

/** For Notifications.setNotificationCategoryAsync. */
export const browserApprovalCategoryActions = [
	{ identifier: "deny", buttonTitle: "Deny", options: { opensAppToForeground: false, isDestructive: true, isAuthenticationRequired: false } },
	{ identifier: "allow", buttonTitle: "Review…", options: { opensAppToForeground: true, isDestructive: false, isAuthenticationRequired: true } },
] as const;

export type ApprovalPushAction =
	| { kind: "open" }
	| { kind: "deny"; sessionId: string; approvalId: string; orgId?: string };

export function approvalPushAction(
	data: { type?: string; sessionId?: string; approvalId?: string; source?: string; orgId?: string },
	actionIdentifier: string,
): ApprovalPushAction | null {
	if (data.type !== BROWSER_APPROVAL_CATEGORY) return null;
	if (actionIdentifier === "deny" && data.sessionId && data.approvalId) {
		return { kind: "deny", sessionId: data.sessionId, approvalId: data.approvalId, ...(data.source === "cloud" && data.orgId ? { orgId: data.orgId } : {}) };
	}
	return { kind: "open" };
}
