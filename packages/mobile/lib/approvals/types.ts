// Browser approvals: an agent's risky browser action waiting for the person.
// The local daemon (#8426) and Orchestrator Cloud (#8601) describe them
// differently; both become one ApprovalCard.

/** The daemon's pending approval (ApprovalPendingResponse). */
export type LocalApproval = {
	id: string;
	kind: string;
	sessionId: string;
	/** Origin the action happens on, e.g. https://shop.example. */
	site: string;
	url?: string;
	pageTitle?: string;
	/** What will happen, e.g. Click "Place order" on shop.example. Agent- and page-supplied. */
	action: string;
	category: string;
	reason: string;
	element?: string;
	createdAt: string;
	expiresAt: string;
};

/** The control plane's pending approval (BrowserApproval). */
export type CloudApproval = {
	id: string;
	sessionId: string;
	action: string;
	category: string;
	reason: string;
	/** Host without www.; empty when unknown. */
	site: string;
	summary: string;
	createdAt: string;
	expiresAt: string;
	/** Absent from an older control plane: treat as true. */
	canDecide?: boolean;
	decider?: { userId?: string; name?: string };
};

export type ApprovalDecision = "allow-once" | "allow-site" | "deny";

/** A decision that arrived after the approval ended is not an error. */
export type DecisionResult = "decided" | "already-decided";

export type ApprovalCard = {
	id: string;
	sessionId: string;
	summary: string;
	site: string;
	reason: string;
	expiresAt: string;
	canDecide: boolean;
	/** Who decides, when this person can't. */
	deciderName?: string;
	/** "Always on this site" needs a known site. */
	canAllowSite: boolean;
};

/** Longest agent-supplied text a card or banner shows. */
export const MAX_APPROVAL_TEXT = 140;

/** One plain line: no breaks, control or bidi characters, at most 140 characters. */
export function approvalCardText(raw: string): string {
	// eslint-disable-next-line no-control-regex
	const line = raw.replace(/[\u0000-\u001f\u007f-\u009f​-‏‪-‮⁠-⁩﻿]/g, " ").replace(/\s+/g, " ").trim();
	const chars = [...line];
	return chars.length > MAX_APPROVAL_TEXT ? `${chars.slice(0, MAX_APPROVAL_TEXT - 1).join("").trimEnd()}…` : line;
}

// Categories whose effect cannot be read in advance: "script" and AO Cloud's
// "unknown". The daemon refuses an "always allow on this site" rule for them,
// so no card offers one (same rule as the desktop's approval-site-rule.ts).
const CATCH_ALL = new Set(["script", "unknown"]);

/** Whether a card may offer "Always on <site>" for this request. */
export function offersSiteRule(site: string, category: string): boolean {
	return Boolean(site) && !CATCH_ALL.has(category);
}

function siteHost(site: string): string {
	try {
		return new URL(site).host;
	} catch {
		return site;
	}
}

export function localApprovalCard(a: LocalApproval): ApprovalCard {
	const site = siteHost(a.site);
	return {
		id: a.id, sessionId: a.sessionId, summary: approvalCardText(a.action), site: approvalCardText(site),
		reason: approvalCardText(a.reason), expiresAt: a.expiresAt, canDecide: true, canAllowSite: offersSiteRule(site, a.category),
	};
}
