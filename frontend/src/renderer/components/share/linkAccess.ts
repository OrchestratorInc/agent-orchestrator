import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { invalidateTeamsAndShares } from "../../hooks/useTeams";
import { aoBridge } from "../../lib/bridge";
import { CloudCpError, type CloudCpShareLink, type CloudCpShareRole } from "../../lib/cloud-cp";
import { createdShareLinkUrl } from "../../lib/share-links";
import type { ShareModeCap, ShareScope } from "./accessLabels";

export type ShareExpiry = "24" | "168" | "720" | "never";
export type LinkCap = Extract<ShareModeCap, "read-only" | "standard">;

export const EXPIRY_HOURS: Record<ShareExpiry, number | undefined> = { "24": 24, "168": 168, "720": 720, never: undefined };
export const DEFAULT_EXPIRY: ShareExpiry = "168";

const ROLE_RANK: Record<string, number> = { viewer: 1, editor: 2 };
const CAP_RANK: Record<string, number> = { "read-only": 1, standard: 2, trusted: 3 };
const HOUR = 3_600_000;

export function linkCap(role: CloudCpShareRole, cap: ShareModeCap | string | undefined): LinkCap {
	return role === "editor" && cap !== "read-only" ? "standard" : "read-only";
}

export function actualLinkCap(role: CloudCpShareRole, cap: ShareModeCap | string | undefined): ShareModeCap {
	if (role !== "editor") return "read-only";
	return cap === "read-only" || cap === "trusted" ? cap : "standard";
}

function accessRank(role: CloudCpShareRole, cap: ShareModeCap | string | undefined): number {
	return (ROLE_RANK[role] ?? 0) * 10 + (CAP_RANK[actualLinkCap(role, cap)] ?? 0);
}

export function isTrustedLink(link: Pick<CloudCpShareLink, "role" | "modeCap"> | undefined): boolean {
	return link !== undefined && actualLinkCap(link.role, link.modeCap) === "trusted";
}

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

export function isLiveLink(link: CloudCpShareLink, now: number): boolean {
	return link.status === "active" && link.accessScope === "anyone" && (!link.expiresAt || Date.parse(link.expiresAt) > now);
}

export function scopeLinks(all: readonly CloudCpShareLink[], scope: ShareScope, sessionId: string, now: number): CloudCpShareLink[] {
	return all.filter((link) => isLiveLink(link, now) && (scope === "project" ? !link.sessionId : link.sessionId === sessionId));
}

export function projectLinks(all: readonly CloudCpShareLink[], now: number): CloudCpShareLink[] {
	return all.filter((link) => isLiveLink(link, now) && !link.sessionId);
}

function endsAt(link: Pick<CloudCpShareLink, "expiresAt">): number {
	return link.expiresAt ? Date.parse(link.expiresAt) : Number.POSITIVE_INFINITY;
}

export function broadestLink(links: readonly CloudCpShareLink[]): CloudCpShareLink | undefined {
	return [...links].sort((a, b) =>
		accessRank(b.role, b.modeCap) - accessRank(a.role, a.modeCap) ||
		endsAt(b) - endsAt(a) ||
		Date.parse(b.createdAt) - Date.parse(a.createdAt),
	)[0];
}

function copyOf(link: CloudCpShareLink, of: CloudCpShareLink): boolean {
	if (link.role !== of.role || actualLinkCap(link.role, link.modeCap) !== actualLinkCap(of.role, of.modeCap)) return false;
	const [a, b] = [endsAt(link), endsAt(of)];
	if (a === b || Math.abs(a - b) < HOUR) return true;
	return a <= b && Date.parse(link.createdAt) >= Date.parse(of.createdAt);
}

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

export function remainingHours(link: Pick<CloudCpShareLink, "expiresAt">, now: number): number | undefined | null {
	if (!link.expiresAt) return undefined;
	const left = Date.parse(link.expiresAt) - now;
	if (!(left > 0)) return null;
	return Math.max(1, Math.floor(left / HOUR));
}

export function anyoneSentence(t: TFunction, scope: ShareScope, role: CloudCpShareRole): string {
	if (scope === "project") return role === "editor" ? t("share.general.anyoneProjectWrite") : t("share.general.anyoneProjectWatch");
	return role === "editor" ? t("share.general.anyoneSessionWrite") : t("share.general.anyoneSessionWatch");
}

export function isPersonalWorkspace(org: { kind?: string } | undefined): boolean {
	return org?.kind === "personal";
}

const EMAIL_IN_NAME = /[^\s@]+@[^\s@]+\.[^\s@]{2,}/;

export function workspaceDisplayName(name: string | undefined): string | undefined {
	const trimmed = name?.trim();
	return trimmed && !EMAIL_IN_NAME.test(trimmed) ? trimmed : undefined;
}

export function restrictedSentence(t: TFunction, { scope, workspaceKnown, solo, people }: {
	scope: ShareScope;
	workspaceKnown: boolean;
	solo: boolean;
	people: boolean;
}): string {
	const project = scope === "project";
	if (solo && people) return project ? t("share.general.restrictedProjectPersonal") : t("share.general.restrictedSessionPersonal");
	if (!people || !workspaceKnown) return project ? t("share.general.restrictedProjectNoPeople") : t("share.general.restrictedSessionNoPeople");
	return project ? t("share.general.restrictedProject") : t("share.general.restrictedSession");
}

export function expiryText(t: TFunction, link: Pick<CloudCpShareLink, "expiresAt">, locale: string): string {
	if (!link.expiresAt) return t("share.general.neverExpires");
	return t("share.general.expires", { date: new Date(link.expiresAt).toLocaleDateString(locale, { month: "short", day: "numeric" }) });
}

export function shareLinksQueryKey(baseUrl: string, orgId: string, projectId: string) {
	return ["cloud-share-links", baseUrl, orgId, projectId] as const;
}

export function shareLinkChangeKey(baseUrl: string, orgId: string, projectId: string) {
	return ["share-link-change", baseUrl, orgId, projectId] as const;
}

export const MISSING_URL = "share-link-missing-url";
export const LINK_EXPIRED = "share-link-expired";

export class LinkRevokeError extends Error {
	constructor(readonly failed: number, readonly reason: unknown, readonly url?: string) {
		super(reason instanceof Error ? reason.message : String(reason));
		this.name = "LinkRevokeError";
	}
}

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

const madeHere = new Map<string, string>();
const madeKey = (baseUrl: string, userId: string, linkId: string) => JSON.stringify([baseUrl.replace(/\/+$/, ""), userId, linkId]);

export function forgetUrlsMadeHere(): void {
	madeHere.clear();
}

function madeHereUrls(baseUrl: string, userId: string, ids: readonly string[]): Record<string, string> {
	return Object.fromEntries(ids.flatMap((id) => {
		const url = madeHere.get(madeKey(baseUrl, userId, id));
		return url ? [[id, url]] : [];
	}));
}

export type CopyMode =
	| "loading"
	| "off"
	| "ready"
	| "new"
	| "replace";

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
		queryFn: async (): Promise<Record<string, string>> => (await aoBridge.sessionShareUrls?.recall(baseUrl, ids)) ?? {},
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	const known: Record<string, string> = { ...madeHereUrls(baseUrl, userId, ids), ...urls.data };
	const url = urlForBroadest(broadest, links, known);
	const urlsLoading = urls.isLoading && ids.length > 0;
	const changing = useIsMutating({ mutationKey: changeKey }) > 0;

	const settle = async () => {
		await queryClient.invalidateQueries({ queryKey: linksKey });
		await queryClient.invalidateQueries({ queryKey: ["share-link-urls", baseUrl] });
	};
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
		await remember(link.id, made);
		const { token: _token, url: _url, code: _code, shortUrl: _shortUrl, ...listed } = link;
		queryClient.setQueryData<CloudCpShareLink[]>(linksKey, (data) => (data && !data.some((other) => other.id === link.id) ? [...data, listed] : data));
		return made;
	};
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

	const turnOn = useMutation({
		mutationKey: changeKey,
		mutationFn: ({ role, modeCap, expiry }: { role: CloudCpShareRole; modeCap: ShareModeCap; expiry: ShareExpiry }) =>
			make(role, modeCap, EXPIRY_HOURS[expiry]),
		onSettled: settle,
	});
	const turnOff = useMutation({
		mutationKey: changeKey,
		mutationFn: ({ removeJoined }: { removeJoined: boolean }) => revokeAll(links, removeJoined),
		onSettled: settleAccess,
	});
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
		fetching: all.isFetching,
		error: all.error,
		links,
		projectLinks: scope === "session" ? projectLinks(all.data ?? [], now) : [],
		broadest,
		url,
		urls: known,
		urlsLoading,
		copy: { mode, busy: changing || urlsLoading },
		joined: links.reduce((sum, link) => sum + (link.joinedCount ?? 0), 0),
		canRemoveJoined: (all.data ?? []).some((link) => typeof link.joinedCount === "number"),
		refresh: async (): Promise<CloudCpShareLink[]> => {
			const result = await all.refetch();
			return scopeLinks(result.data ?? [], scope, sessionId, Date.now());
		},
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
