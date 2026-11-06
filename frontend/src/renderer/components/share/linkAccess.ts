import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { aoBridge } from "../../lib/bridge";
import type { CloudCpShareLink, CloudCpShareRole } from "../../lib/cloud-cp";
import { createdShareLinkUrl } from "../../lib/share-links";
import type { ShareModeCap, ShareScope } from "./accessLabels";

/**
 * General access, the one sentence that says who a link lets in.
 *
 * A link is a credential the control plane shows once (it keeps only a hash
 * of its code), so a session has no single URL that admits whoever general
 * access allows. Until it does, general access is derived from the live
 * links: none is "Only people with access", any is "Anyone with the link".
 * A session can have several live links; the sentence describes the broadest
 * and says how many more there are. The URLs this device made are kept,
 * encrypted, in the main process (main/session-share-urls.ts), so Copy link
 * still works after the dialog closes; elsewhere it makes a new link.
 */

export type ShareExpiry = "24" | "168" | "720" | "never";
/** Links may write with Standard at most: Trusted is for people you name. */
export type LinkCap = Extract<ShareModeCap, "read-only" | "standard">;

export const EXPIRY_HOURS: Record<ShareExpiry, number | undefined> = { "24": 24, "168": 168, "720": 720, never: undefined };
export const DEFAULT_EXPIRY: ShareExpiry = "168";

const ROLE_RANK: Record<string, number> = { viewer: 1, editor: 2 };
const CAP_RANK: Record<string, number> = { "read-only": 1, standard: 2, trusted: 3 };

/** The cap a link writes with: Trusted is never offered on a link, and a watch-only link is read-only. */
export function linkCap(role: CloudCpShareRole, cap: ShareModeCap | string | undefined): LinkCap {
	return role === "editor" && cap !== "read-only" ? "standard" : "read-only";
}

/** The request that makes a link for one session, or the whole project (no session). */
export function shareLinkRequest({ scope, role, modeCap, expiresInHours, sessionId }: {
	scope: ShareScope;
	role: CloudCpShareRole;
	modeCap: ShareModeCap;
	expiresInHours: number | undefined;
	sessionId: string;
}) {
	const editor = role === "editor";
	return {
		...(scope === "session" ? { sessionId } : {}),
		role: editor ? "editor" as const : "viewer" as const,
		accessScope: "anyone" as const,
		interaction: editor ? "interact" as const : "view" as const,
		modeCap: editor ? linkCap(role, modeCap) : "read-only" as const,
		...(expiresInHours ? { expiresInHours } : {}),
	};
}

/** Whether a link still opens anything: active, for anyone, not past its expiry. */
export function isLiveLink(link: CloudCpShareLink, now: number): boolean {
	return link.status === "active" && link.accessScope === "anyone" && (!link.expiresAt || Date.parse(link.expiresAt) > now);
}

/** The live links of exactly this scope: this session's, or the whole project's. */
export function scopeLinks(all: readonly CloudCpShareLink[], scope: ShareScope, sessionId: string, now: number): CloudCpShareLink[] {
	return all.filter((link) => isLiveLink(link, now) && (scope === "project" ? !link.sessionId : link.sessionId === sessionId));
}

/** The whole project's live links, which open every session in it too. */
export function projectLinks(all: readonly CloudCpShareLink[], now: number): CloudCpShareLink[] {
	return all.filter((link) => isLiveLink(link, now) && !link.sessionId);
}

/** The link that lets people do the most: writing over watching, then the higher cap, then the later expiry. */
export function broadestLink(links: readonly CloudCpShareLink[]): CloudCpShareLink | undefined {
	const expiry = (link: CloudCpShareLink) => (link.expiresAt ? Date.parse(link.expiresAt) : Number.POSITIVE_INFINITY);
	return [...links].sort((a, b) =>
		(ROLE_RANK[b.role] ?? 0) - (ROLE_RANK[a.role] ?? 0) ||
		(CAP_RANK[b.modeCap ?? ""] ?? 0) - (CAP_RANK[a.modeCap ?? ""] ?? 0) ||
		expiry(b) - expiry(a) ||
		Date.parse(b.createdAt) - Date.parse(a.createdAt),
	)[0];
}

/** Hours until a link expires, so a new link with the same access ends when it does; undefined never expires. */
export function remainingHours(link: Pick<CloudCpShareLink, "expiresAt">, now: number): number | undefined {
	if (!link.expiresAt) return undefined;
	return Math.max(1, Math.ceil((Date.parse(link.expiresAt) - now) / 3_600_000));
}

/**
 * The general access sentence for a link: who it lets in and what writing
 * means. Writing is spelled out because the agent runs in the owner's sandbox
 * with their repository access.
 */
export function anyoneSentence(t: TFunction, scope: ShareScope, role: CloudCpShareRole): string {
	if (scope === "project") return role === "editor" ? t("share.general.anyoneProjectWrite") : t("share.general.anyoneProjectWatch");
	return role === "editor" ? t("share.general.anyoneSessionWrite") : t("share.general.anyoneSessionWatch");
}

/**
 * Whether a workspace is someone's own rather than a team's, so "people in
 * <it>" would read oddly ("people in Asha's organization"). A team workspace
 * is the WorkOS org, whose offering includes Coder. Otherwise the name gives
 * it away: one made from the account (its email, the email's name, the
 * person's name) or "<name>'s organization".
 */
export function isPersonalWorkspace(
	org: { displayName: string; offering?: { kind?: string } } | undefined,
	user: { email?: string; displayName?: string } | undefined,
): boolean {
	if (!org) return false;
	if (org.offering?.kind && org.offering.kind !== "b2b" && org.offering.kind !== "both") return true;
	const name = org.displayName.trim().toLowerCase();
	const email = user?.email?.trim().toLowerCase() ?? "";
	const own = [email, email.split("@")[0] ?? "", user?.displayName?.trim().toLowerCase() ?? ""].filter(Boolean);
	return own.includes(name) || /['’]s (organization|organisation|workspace)$/.test(name);
}

/** "Only people with access" in words that fit the workspace. */
export function restrictedSentence(t: TFunction, { scope, workspaceName, personal, people }: {
	scope: ShareScope;
	workspaceName: string | undefined;
	/** A personal workspace has nobody else in it: "people in Asha's organization" reads oddly. */
	personal: boolean;
	/** Whether people can be added above (Teams); without it only links share. */
	people: boolean;
}): string {
	const project = scope === "project";
	if (!people) return project ? t("share.general.restrictedProjectNoPeople") : t("share.general.restrictedSessionNoPeople");
	if (personal || !workspaceName) return project ? t("share.general.restrictedProjectPersonal") : t("share.general.restrictedSessionPersonal");
	return project
		? t("share.general.restrictedProject", { workspace: workspaceName })
		: t("share.general.restrictedSession", { workspace: workspaceName });
}

export function expiryText(t: TFunction, link: Pick<CloudCpShareLink, "expiresAt">, locale: string): string {
	if (!link.expiresAt) return t("share.general.neverExpires");
	return t("share.general.expires", { date: new Date(link.expiresAt).toLocaleDateString(locale, { month: "short", day: "numeric" }) });
}

export function shareLinksQueryKey(baseUrl: string, orgId: string, projectId: string) {
	return ["cloud-share-links", baseUrl, orgId, projectId] as const;
}

/**
 * Link access for a session or a whole project: its live links, the one
 * general access describes, its URL when this device made it, and the
 * changes general access can make. Every change keeps the URLs this device
 * holds in step.
 */
export function useLinkAccess({ orgId, projectId, sessionId, scope, enabled }: {
	orgId: string;
	projectId: string;
	sessionId: string;
	scope: ShareScope;
	enabled: boolean;
}) {
	const { client, baseUrl, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const linksKey = shareLinksQueryKey(baseUrl, orgId, projectId);
	const all = useQuery({
		queryKey: linksKey,
		enabled: enabled && ready,
		queryFn: async () => (await client.listShareLinks(orgId, projectId)).links,
	});
	const now = Date.now();
	const links = scopeLinks(all.data ?? [], scope, sessionId, now);
	const broadest = broadestLink(links);
	const ids = links.map((link) => link.id).sort();
	const urlsKey = ["share-link-urls", baseUrl, ...ids] as const;
	const urls = useQuery({
		queryKey: urlsKey,
		enabled: enabled && ids.length > 0,
		queryFn: () => aoBridge.sessionShareUrls.recall(baseUrl, ids),
		staleTime: Number.POSITIVE_INFINITY,
	});
	const settle = async () => {
		await queryClient.invalidateQueries({ queryKey: linksKey });
		await queryClient.invalidateQueries({ queryKey: ["share-link-urls", baseUrl] });
	};

	const make = async (role: CloudCpShareRole, modeCap: ShareModeCap, expiresInHours: number | undefined): Promise<string> => {
		const { link } = await client.createShareLink(orgId, projectId, shareLinkRequest({ scope, role, modeCap, expiresInHours, sessionId }));
		const url = createdShareLinkUrl(baseUrl, orgId, link);
		if (!url) throw new Error(MISSING_URL);
		// Kept even when the copy below fails: the person can copy it again.
		await aoBridge.sessionShareUrls.remember(baseUrl, link.id, url).catch(() => undefined);
		return url;
	};
	const revokeAll = async (targets: readonly CloudCpShareLink[], removeJoined: boolean) => {
		for (const link of targets) await client.revokeShareLink(orgId, projectId, link.id, removeJoined ? { removeJoined: true } : undefined);
		await aoBridge.sessionShareUrls.forget(baseUrl, targets.map((link) => link.id)).catch(() => undefined);
	};

	/** Restricted → Anyone with the link: one new link. */
	const turnOn = useMutation({
		mutationFn: ({ role, modeCap, expiry }: { role: CloudCpShareRole; modeCap: ShareModeCap; expiry: ShareExpiry }) =>
			make(role, modeCap, EXPIRY_HOURS[expiry]),
		onSettled: settle,
	});
	/** Anyone with the link → Restricted: every live link of this scope stops working. */
	const turnOff = useMutation({
		mutationFn: ({ removeJoined }: { removeJoined: boolean }) => revokeAll(links, removeJoined),
		onSettled: settle,
	});
	/**
	 * Changes what the link allows. A link's access is fixed when it is made,
	 * so this makes a new link, ending when the broadest did, then turns the
	 * old ones off. The new one is made first so a failure leaves access as it was.
	 */
	const replace = useMutation({
		mutationFn: async ({ role, modeCap }: { role: CloudCpShareRole; modeCap: ShareModeCap }) => {
			const old = links;
			const url = await make(role, modeCap, broadest ? remainingHours(broadest, Date.now()) : EXPIRY_HOURS[DEFAULT_EXPIRY]);
			await revokeAll(old, false);
			return url;
		},
		onSettled: settle,
	});
	/** A new link with the broadest link's access, for a device that never saw its URL. */
	const copyNew = useMutation({
		mutationFn: async () => {
			if (!broadest) throw new Error(MISSING_URL);
			return make(broadest.role, (broadest.modeCap as ShareModeCap | undefined) ?? "read-only", remainingHours(broadest, Date.now()));
		},
		onSettled: settle,
	});
	const revoke = useMutation({
		mutationFn: ({ link, removeJoined }: { link: CloudCpShareLink; removeJoined: boolean }) => revokeAll([link], removeJoined),
		onSettled: settle,
	});

	return {
		loading: all.isLoading,
		error: all.error,
		links,
		/** The whole project's links, which open this session too (session scope only). */
		projectLinks: scope === "session" ? projectLinks(all.data ?? [], now) : [],
		broadest,
		/** The broadest link's URL, when this device made it. */
		url: broadest ? urls.data?.[broadest.id] : undefined,
		/** Every live link's URL this device made, by link id. */
		urls: urls.data ?? {},
		urlsLoading: urls.isLoading && ids.length > 0,
		/** People who hold access because they opened one of these links. */
		joined: links.reduce((sum, link) => sum + (link.joinedCount ?? 0), 0),
		/** Whether this control plane reports, and so can remove, the people who joined by a link. */
		canRemoveJoined: (all.data ?? []).some((link) => typeof link.joinedCount === "number"),
		turnOn,
		turnOff,
		replace,
		copyNew,
		revoke,
	};
}

export type LinkAccess = ReturnType<typeof useLinkAccess>;

/** Thrown when a create response carries no link to copy; the dialog shows share.noToken. */
export const MISSING_URL = "share-link-missing-url";
