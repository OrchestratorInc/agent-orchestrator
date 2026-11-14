import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { invalidateTeamsAndShares } from "../../hooks/useTeams";
import { aoBridge } from "../../lib/bridge";
import { CloudCpError, type CloudCpShareLink, type CloudCpShareRole } from "../../lib/cloud-cp";
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
const HOUR = 3_600_000;

/** The cap a new link writes with: never Trusted, and a watch-only link is read-only. */
export function linkCap(role: CloudCpShareRole, cap: ShareModeCap | string | undefined): LinkCap {
	return role === "editor" && cap !== "read-only" ? "standard" : "read-only";
}

/**
 * What a live link lets people do. Links no longer offer Trusted, but one
 * made before that still writes with it, and the dialog says so.
 */
export function actualLinkCap(role: CloudCpShareRole, cap: ShareModeCap | string | undefined): ShareModeCap {
	if (role !== "editor") return "read-only";
	return cap === "read-only" || cap === "trusted" ? cap : "standard";
}

function accessRank(role: CloudCpShareRole, cap: ShareModeCap | string | undefined): number {
	return (ROLE_RANK[role] ?? 0) * 10 + (CAP_RANK[actualLinkCap(role, cap)] ?? 0);
}

/** Whether a live link is Trusted, which a new link can never be. */
export function isTrustedLink(link: Pick<CloudCpShareLink, "role" | "modeCap"> | undefined): boolean {
	return link !== undefined && actualLinkCap(link.role, link.modeCap) === "trusted";
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

function endsAt(link: Pick<CloudCpShareLink, "expiresAt">): number {
	return link.expiresAt ? Date.parse(link.expiresAt) : Number.POSITIVE_INFINITY;
}

/** The link that lets people do the most: writing over watching, then the higher cap, then the later expiry. */
export function broadestLink(links: readonly CloudCpShareLink[]): CloudCpShareLink | undefined {
	return [...links].sort((a, b) =>
		accessRank(b.role, b.modeCap) - accessRank(a.role, a.modeCap) ||
		endsAt(b) - endsAt(a) ||
		Date.parse(b.createdAt) - Date.parse(a.createdAt),
	)[0];
}

/**
 * Whether a link is as good as another to copy: the same access, and either
 * ending within the hour a copy of it ends in (see remainingHours), or made
 * after it and ending no later, as Copy new link makes one. That second test
 * uses only the control plane's own times: a copy's hours are counted on this
 * device's clock, so with the two clocks apart it can end well over an hour
 * sooner, and Copy new link would then make another on every click.
 */
function copyOf(link: CloudCpShareLink, of: CloudCpShareLink): boolean {
	if (link.role !== of.role || actualLinkCap(link.role, link.modeCap) !== actualLinkCap(of.role, of.modeCap)) return false;
	const [a, b] = [endsAt(link), endsAt(of)];
	if (a === b || Math.abs(a - b) < HOUR) return true;
	return a <= b && Date.parse(link.createdAt) >= Date.parse(of.createdAt);
}

/**
 * The URL to copy for the broadest link: its own when this device made it,
 * else that of a link this device made with the same access standing in for
 * it (see copyOf), the one that ends last. Without this, the copy would rank
 * below the link it copies, and Copy new link would make another on every click.
 */
export function urlForBroadest(
	broadest: CloudCpShareLink | undefined,
	links: readonly CloudCpShareLink[],
	urls: Readonly<Record<string, string>>,
): string | undefined {
	if (!broadest) return undefined;
	if (urls[broadest.id]) return urls[broadest.id];
	const copy = links.filter((link) => urls[link.id] && copyOf(link, broadest)).sort((a, b) => endsAt(b) - endsAt(a))[0];
	return copy ? urls[copy.id] : undefined;
}

/**
 * Whole hours until a link expires, rounded down so a new link with the same
 * access never outlasts it; undefined when it never expires. Links are made in
 * whole hours, so under an hour left is one hour. null once it has expired:
 * a new link must not bring it back.
 */
export function remainingHours(link: Pick<CloudCpShareLink, "expiresAt">, now: number): number | undefined | null {
	if (!link.expiresAt) return undefined;
	const left = Date.parse(link.expiresAt) - now;
	if (!(left > 0)) return null;
	return Math.max(1, Math.floor(left / HOUR));
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
 * Whether a workspace is someone's own rather than a team's, so it has
 * nobody else in it. Only the control plane's word for it (`kind`) counts: a
 * name or an offering can't tell (a team can be named after a person, or run
 * on Freestyle only). Unknown (an older control plane, or the workspace still
 * loading) is a team's: counting the workspace's people never understates who
 * can open the session.
 */
export function isPersonalWorkspace(org: { kind?: string } | undefined): boolean {
	return org?.kind === "personal";
}

/** An email anywhere in a name: "asha@example.com", or "asha@example.com's organization". */
const EMAIL_IN_NAME = /[^\s@]+@[^\s@]+\.[^\s@]{2,}/;

/**
 * A workspace's name when it reads as one, else undefined. A workspace made
 * for one person can be named after their email, which reads as an address
 * rather than a group ("People in asha@example.com"), so it is left out.
 */
export function workspaceDisplayName(name: string | undefined): string | undefined {
	const trimmed = name?.trim();
	return trimmed && !EMAIL_IN_NAME.test(trimmed) ? trimmed : undefined;
}

/** "Only people with access" in words that fit the workspace. */
export function restrictedSentence(t: TFunction, { scope, workspaceKnown, solo, people }: {
	scope: ShareScope;
	/** Whether the workspace that owns the project has been read: false while it loads or when it couldn't be. */
	workspaceKnown: boolean;
	/** Nobody else in the workspace can open it: a personal workspace, a team's with only you in it, or a standalone agent. */
	solo: boolean;
	/** Whether people can be added above (Teams); without it only links share. */
	people: boolean;
}): string {
	const project = scope === "project";
	if (solo && people) return project ? t("share.general.restrictedProjectPersonal") : t("share.general.restrictedSessionPersonal");
	// Without the workspace, the plain sentence: "only you" could understate a team's.
	if (!people || !workspaceKnown) return project ? t("share.general.restrictedProjectNoPeople") : t("share.general.restrictedSessionNoPeople");
	// Never the workspace's name: it can be an email, and the list above names it.
	return project ? t("share.general.restrictedProject") : t("share.general.restrictedSession");
}

export function expiryText(t: TFunction, link: Pick<CloudCpShareLink, "expiresAt">, locale: string): string {
	if (!link.expiresAt) return t("share.general.neverExpires");
	return t("share.general.expires", { date: new Date(link.expiresAt).toLocaleDateString(locale, { month: "short", day: "numeric" }) });
}

export function shareLinksQueryKey(baseUrl: string, orgId: string, projectId: string) {
	return ["cloud-share-links", baseUrl, orgId, projectId] as const;
}

/** Every change to a project's links, from the dialog or the Share menu, so either knows one is running. */
export function shareLinkChangeKey(baseUrl: string, orgId: string, projectId: string) {
	return ["share-link-change", baseUrl, orgId, projectId] as const;
}

/** Thrown when a create response carries no link to copy; the dialog shows share.noToken. */
export const MISSING_URL = "share-link-missing-url";
/** Thrown instead of making a link to stand in for one that has already expired. */
export const LINK_EXPIRED = "share-link-expired";

/**
 * Some links could not be turned off, so they still work. A link already
 * revoked (404) is not one of them. After a replacement the new link exists
 * and its URL is here, so it can still be copied.
 */
export class LinkRevokeError extends Error {
	constructor(readonly failed: number, readonly reason: unknown, readonly url?: string) {
		super(reason instanceof Error ? reason.message : String(reason));
		this.name = "LinkRevokeError";
	}
}

/** A link change's error in words: which links still work, or what went wrong. */
export function linkErrorMessage(t: TFunction, error: unknown): string {
	if (error instanceof LinkRevokeError) {
		return error.url ? t("share.error.replaceStillLive", { count: error.failed }) : t("share.error.revokeFailed", { count: error.failed });
	}
	if (error instanceof Error) {
		if (error.message === MISSING_URL) return t("share.noToken");
		if (error.message === LINK_EXPIRED) return t("share.error.linkExpired");
		return error.message;
	}
	return String(error);
}

function alreadyRevoked(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 404;
}

/**
 * The URLs this window made, by control plane, account and link. The main
 * process keeps them across restarts; this copy means a window whose preload
 * lacks that store, or a write that failed, never loses a link just made.
 */
const madeHere = new Map<string, string>();
const madeKey = (baseUrl: string, userId: string, linkId: string) => JSON.stringify([baseUrl.replace(/\/+$/, ""), userId, linkId]);

/** Forgets the URLs this window made; tests start from none. */
export function forgetUrlsMadeHere(): void {
	madeHere.clear();
}

function madeHereUrls(baseUrl: string, userId: string, ids: readonly string[]): Record<string, string> {
	return Object.fromEntries(ids.flatMap((id) => {
		const url = madeHere.get(madeKey(baseUrl, userId, id));
		return url ? [[id, url]] : [];
	}));
}

/** What Copy link does for the link general access describes. */
export type CopyMode =
	/** The links, or this device's URLs for them, are still loading. */
	| "loading"
	/** Link access is off: nothing to copy. */
	| "off"
	/** This device has the URL. */
	| "ready"
	/** Made elsewhere: a new link with the same access can be made. */
	| "new"
	/** A Trusted link made elsewhere: a new link can't be Trusted, so it can only be replaced. */
	| "replace";

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
	const { client, baseUrl, ready, userId } = useCloudCp();
	const queryClient = useQueryClient();
	const linksKey = shareLinksQueryKey(baseUrl, orgId, projectId);
	const changeKey = shareLinkChangeKey(baseUrl, orgId, projectId);
	const all = useQuery({
		queryKey: linksKey,
		enabled: enabled && ready,
		queryFn: async () => (await client.listShareLinks(orgId, projectId)).links,
	});
	const now = Date.now();
	const links = scopeLinks(all.data ?? [], scope, sessionId, now);
	const broadest = broadestLink(links);
	const ids = links.map((link) => link.id).sort();
	const urls = useQuery({
		queryKey: ["share-link-urls", baseUrl, userId, ...ids],
		enabled: enabled && ids.length > 0,
		// A store that can't answer leaves Copy new link for the person to choose; it isn't asked again and again.
		queryFn: async (): Promise<Record<string, string>> => (await aoBridge.sessionShareUrls?.recall(baseUrl, ids)) ?? {},
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	const known: Record<string, string> = { ...madeHereUrls(baseUrl, userId, ids), ...urls.data };
	const url = urlForBroadest(broadest, links, known);
	const urlsLoading = urls.isLoading && ids.length > 0;
	// Any change to this project's links, from the dialog or the Share menu.
	const changing = useIsMutating({ mutationKey: changeKey }) > 0;

	const settle = async () => {
		await queryClient.invalidateQueries({ queryKey: linksKey });
		await queryClient.invalidateQueries({ queryKey: ["share-link-urls", baseUrl] });
	};
	// Turning a link off can take away the access of the people who joined by it.
	const settleAccess = async () => {
		invalidateTeamsAndShares(queryClient);
		await settle();
	};

	const remember = async (linkId: string, value: string) => {
		madeHere.set(madeKey(baseUrl, userId, linkId), value);
		await aoBridge.sessionShareUrls?.remember(baseUrl, linkId, value).catch(() => undefined);
	};
	const forget = async (linkIds: readonly string[]) => {
		for (const linkId of linkIds) madeHere.delete(madeKey(baseUrl, userId, linkId));
		await aoBridge.sessionShareUrls?.forget(baseUrl, linkIds).catch(() => undefined);
	};
	const make = async (role: CloudCpShareRole, modeCap: ShareModeCap, expiresInHours: number | undefined): Promise<string> => {
		const { link } = await client.createShareLink(orgId, projectId, shareLinkRequest({ scope, role, modeCap, expiresInHours, sessionId }));
		const made = createdShareLinkUrl(baseUrl, orgId, link);
		if (!made) throw new Error(MISSING_URL);
		// Kept even when the copy below fails: the person can copy it again.
		await remember(link.id, made);
		// Listed at once, without its secrets: a list read that fails afterwards
		// must not leave Copy new link offering to make it again.
		const { token: _token, url: _url, code: _code, shortUrl: _shortUrl, ...listed } = link;
		queryClient.setQueryData<CloudCpShareLink[]>(linksKey, (data) => (data && !data.some((other) => other.id === link.id) ? [...data, listed] : data));
		return made;
	};
	/**
	 * Turns every link off, each whatever happens to the others. One already
	 * revoked (on another device, or by a list read before) counts as off. The
	 * URLs of the links that ended are forgotten; those still working are one
	 * error. Resolves to how many grants removing who joined changed, as the
	 * control plane counted them; undefined when it didn't say (a plain revoke,
	 * or an older control plane).
	 */
	const revokeAll = async (targets: readonly CloudCpShareLink[], removeJoined: boolean): Promise<number | undefined> => {
		const results = await Promise.allSettled(
			targets.map((link) => client.revokeShareLink(orgId, projectId, link.id, removeJoined ? { removeJoined: true } : undefined)),
		);
		const ended = targets.filter((_link, index) => {
			const result = results[index]!;
			return result.status === "fulfilled" || alreadyRevoked(result.reason);
		});
		const failures = results.flatMap((result) => (result.status === "rejected" && !alreadyRevoked(result.reason) ? [result.reason as unknown] : []));
		if (ended.length > 0) await forget(ended.map((link) => link.id));
		if (failures.length > 0) throw new LinkRevokeError(failures.length, failures[0]);
		const counts = results.flatMap((result) => (result.status === "fulfilled" && typeof result.value?.removedGrants === "number" ? [result.value.removedGrants] : []));
		return counts.length > 0 ? counts.reduce((sum, count) => sum + count, 0) : undefined;
	};
	const hoursLeft = (link: CloudCpShareLink): number | undefined => {
		const hours = remainingHours(link, Date.now());
		if (hours === null) throw new Error(LINK_EXPIRED);
		return hours;
	};

	/** Restricted → Anyone with the link: one new link. */
	const turnOn = useMutation({
		mutationKey: changeKey,
		mutationFn: ({ role, modeCap, expiry }: { role: CloudCpShareRole; modeCap: ShareModeCap; expiry: ShareExpiry }) =>
			make(role, modeCap, EXPIRY_HOURS[expiry]),
		onSettled: settle,
	});
	/** Anyone with the link → Restricted: every live link of this scope stops working. */
	const turnOff = useMutation({
		mutationKey: changeKey,
		mutationFn: ({ removeJoined }: { removeJoined: boolean }) => revokeAll(links, removeJoined),
		onSettled: settleAccess,
	});
	/**
	 * Changes what the link allows. A link's access is fixed when it is made,
	 * so this makes a new link, ending no later than the broadest does, then
	 * turns the old ones off. If making it fails nothing has changed; if an old
	 * one can't be turned off, the error says it still works and carries the new URL.
	 */
	const replace = useMutation({
		mutationKey: changeKey,
		mutationFn: async ({ role, modeCap, removeJoined }: { role: CloudCpShareRole; modeCap: ShareModeCap; removeJoined: boolean }) => {
			const old = links;
			const made = await make(role, modeCap, broadest ? hoursLeft(broadest) : EXPIRY_HOURS[DEFAULT_EXPIRY]);
			try {
				await revokeAll(old, removeJoined);
			} catch (error) {
				throw error instanceof LinkRevokeError ? new LinkRevokeError(error.failed, error.reason, made) : error;
			}
			return made;
		},
		onSettled: settleAccess,
	});
	/** A new link with the broadest link's access, for a device that never saw its URL. Never a Trusted one. */
	const copyNew = useMutation({
		mutationKey: changeKey,
		mutationFn: async () => {
			if (!broadest || isTrustedLink(broadest)) throw new Error(MISSING_URL);
			return make(broadest.role, actualLinkCap(broadest.role, broadest.modeCap), hoursLeft(broadest));
		},
		onSettled: settle,
	});
	const revoke = useMutation({
		mutationKey: changeKey,
		mutationFn: ({ link, removeJoined }: { link: CloudCpShareLink; removeJoined: boolean }) => revokeAll([link], removeJoined),
		onSettled: settleAccess,
	});

	const mode: CopyMode = all.isLoading
		? "loading"
		: !broadest
			? "off"
			: url
				? "ready"
				: urlsLoading
					? "loading"
					: isTrustedLink(broadest) ? "replace" : "new";

	return {
		loading: all.isLoading,
		/** The list is being read again, as a confirmation does before it counts who joined. */
		fetching: all.isFetching,
		error: all.error,
		links,
		/** The whole project's links, which open this session too (session scope only). */
		projectLinks: scope === "session" ? projectLinks(all.data ?? [], now) : [],
		broadest,
		/** The URL that copies the broadest link's access, when this device made one. */
		url,
		/** Every live link's URL this device made, by link id. */
		urls: known,
		urlsLoading,
		/**
		 * What Copy link does, and whether it must wait: while a change to the
		 * links runs, or this device's URLs load, a copy would act on old links.
		 */
		copy: { mode, busy: changing || urlsLoading },
		/** People who hold access because they opened one of these links. */
		joined: links.reduce((sum, link) => sum + (link.joinedCount ?? 0), 0),
		/** Whether this control plane reports, and so can remove, the people who joined by a link. */
		canRemoveJoined: (all.data ?? []).some((link) => typeof link.joinedCount === "number"),
		/** Reads the links again; resolves to this scope's live links, fresh or (when the read fails) as they were. */
		refresh: async (): Promise<CloudCpShareLink[]> => {
			const result = await all.refetch();
			return scopeLinks(result.data ?? [], scope, sessionId, Date.now());
		},
		/**
		 * Reads the links again and resolves to the broadest of them with the URL
		 * to copy for it; undefined when the read fails, no link is live, or this
		 * device has no URL for it. The links last read may hold one revoked elsewhere since.
		 */
		freshUrl: async (): Promise<{ link: CloudCpShareLink; url: string } | undefined> => {
			const result = await all.refetch();
			if (result.isError || !result.data) return undefined;
			const fresh = scopeLinks(result.data, scope, sessionId, Date.now());
			const link = broadestLink(fresh);
			const url = urlForBroadest(link, fresh, known);
			return link && url ? { link, url } : undefined;
		},
		turnOn,
		turnOff,
		replace,
		copyNew,
		revoke,
	};
}

export type LinkAccess = ReturnType<typeof useLinkAccess>;
