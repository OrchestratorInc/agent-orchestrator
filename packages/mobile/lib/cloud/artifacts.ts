import { useCallback, useEffect, useState } from "react";
import type { ArtifactAssetLoader } from "../artifactPageInliner";
import { resolveArtifactReference } from "../artifactPageInliner";
import { isSafeArtifactPath, parseArtifacts, sortArtifacts } from "../artifacts";
import type { ArtifactsBackend, ArtifactSource } from "../artifactsBackend";
import { parseVersion, parseVersionSummaries } from "../artifactVersions";
import { forgetOnCloudAccountChange } from "./accountCaches";
import type { CloudClient } from "./client";
import type { CloudArtifact, CloudArtifactShare } from "./types";
import { cloudErrorMessage } from "./errors";

// A cloud session's artifacts, as the desktop shows them (useCloudArtifacts,
// CloudArtifactFrame): the control plane lists them, and their files load
// from a signed prefix that needs no header, good for ten minutes. A web view
// loads a page from it as it is, which keeps the control plane's sandboxing
// and lets the page's own links work.

type Access = { root: string; expiresAt: number };
type Entry = { key: string; access?: Access; pending?: Promise<Access>; listeners: Set<() => void>; renewedAt?: number };

// Keyed by API, organization and session but not account: the client is the
// same for every account. So the whole map is forgotten when the account
// changes, and an answer that arrives after that is not kept.
const entries = new Map<string, Entry>();
/** Access this close to expiry is renewed before a new load uses it. */
const RENEW_MARGIN_MS = 30_000;
/** At most one renewal for a refused load in this long, so a file that stays refused cannot loop. */
const RENEW_COOLDOWN_MS = 10_000;

function entryFor(client: CloudClient, orgId: string, sessionId: string): Entry {
	const key = JSON.stringify([client.baseUrl, orgId, sessionId]);
	let entry = entries.get(key);
	if (!entry) {
		entry = { key, listeners: new Set() };
		entries.set(key, entry);
	}
	return entry;
}

/** False once the account changed after the entry was made. */
function current(entry: Entry): boolean {
	return entries.get(entry.key) === entry;
}

function usable(access: Access | undefined): Access | undefined {
	return access && access.expiresAt - Date.now() > RENEW_MARGIN_MS ? access : undefined;
}

/** Forget every session's access: on sign-out and an account switch, and in tests. */
export function clearCloudArtifactAccess() {
	entries.clear();
}
forgetOnCloudAccountChange(clearCloudArtifactAccess);

/**
 * The session's signed files prefix: the one held while it is good, else a
 * new one (one request however many ask at once). `renew` asks for a new one
 * whatever is held, for a load that was refused.
 */
export async function cloudArtifactAccess(client: CloudClient, orgId: string, sessionId: string, { renew = false }: { renew?: boolean } = {}): Promise<Access> {
	const entry = entryFor(client, orgId, sessionId);
	if (!renew) {
		const held = usable(entry.access);
		if (held) return held;
	}
	if (entry.pending) return entry.pending;
	const pending = client.createArtifactAccess(orgId, sessionId).then((answer) => {
		// Asked under an account that has since changed: keep none of it, and
		// answer with access asked under the current one.
		if (!current(entry)) return cloudArtifactAccess(client, orgId, sessionId);
		const expiresAt = Date.parse(answer.expiresAt);
		const access = { root: `${client.baseUrl}${answer.filesPrefix.endsWith("/") ? answer.filesPrefix : `${answer.filesPrefix}/`}`, expiresAt: Number.isFinite(expiresAt) ? expiresAt : Date.now() + 9 * 60_000 };
		entry.access = access;
		entry.pending = undefined;
		for (const listener of entry.listeners) listener();
		return access;
	}, (cause: unknown) => {
		entry.pending = undefined;
		if (!current(entry)) return cloudArtifactAccess(client, orgId, sessionId);
		throw cause;
	});
	entry.pending = pending;
	return pending;
}

/**
 * The signed prefix for a screen: read at mount (a fresh one when the held one
 * is nearly spent), then kept, so the files on screen keep their addresses
 * and their image cache. It changes only when a load is refused and `renew`
 * fetches another, which every screen of the session then takes.
 */
export function useCloudArtifactAccess(client: CloudClient, orgId: string | undefined, sessionId: string, enabled: boolean): { root?: string; error?: string; renew(): void } {
	const [root, setRoot] = useState<string>();
	const [error, setError] = useState<string>();
	useEffect(() => {
		setRoot(undefined);
		setError(undefined);
		if (!enabled || !orgId || !sessionId) return;
		// An answer for an earlier organization, session or account, or one that
		// lands after the screen closed, is dropped.
		let wanted = true;
		const entry = entryFor(client, orgId, sessionId);
		const take = () => { if (wanted && entry.access) setRoot(entry.access.root); };
		entry.listeners.add(take);
		cloudArtifactAccess(client, orgId, sessionId).then((access) => { if (wanted) setRoot(access.root); }, (cause: unknown) => { if (wanted) setError(cloudErrorMessage(cause)); });
		return () => {
			wanted = false;
			entry.listeners.delete(take);
		};
	}, [client, enabled, orgId, sessionId]);
	const renew = useCallback(() => {
		if (!orgId || !sessionId) return;
		const entry = entryFor(client, orgId, sessionId);
		if (entry.renewedAt && Date.now() - entry.renewedAt < RENEW_COOLDOWN_MS) return;
		entry.renewedAt = Date.now();
		void cloudArtifactAccess(client, orgId, sessionId, { renew: true }).catch(() => {});
	}, [client, orgId, sessionId]);
	return { root, error, renew };
}

function escapedPath(path: string): string {
	return path.split("/").map(encodeURIComponent).join("/");
}

/** The control plane's refusals in its own words; a status left on them would read as a machine's. */
async function inCloudWords<T>(run: () => Promise<T>): Promise<T> {
	try {
		return await run();
	} catch (cause) {
		if (cause instanceof Error && cause.name === "AbortError") throw cause;
		throw new Error(cloudErrorMessage(cause), { cause });
	}
}

/**
 * The session's files with the public link of each one its creator published,
 * as the gallery and the viewer's menus show a machine file's (shareUrl).
 * Cloud keeps no record of which file a link came from (the desktop keeps
 * its own), so a link is a file's when it names the file's name and the
 * sha256 of the version on screen, as the desktop publishes it (the file's
 * last path segment). A file committed again since keeps no link here: the
 * link still serves the version it was published at.
 */
export function withCloudShareLinks(artifacts: readonly CloudArtifact[], shares: readonly CloudArtifactShare[]): Array<CloudArtifact & { shareUrl?: string }> {
	const links = new Map<string, string>();
	for (const share of shares) {
		if (share.revokedAt) continue;
		const url = share.viewerUrl ?? share.contentUrl;
		if (url) links.set(`${share.sha256}\n${share.name}`, url);
	}
	if (!links.size) return [...artifacts];
	return artifacts.map((artifact) => {
		const name = artifact.path.split("/").pop() || artifact.name;
		const url = artifact.sha256 ? links.get(`${artifact.sha256}\n${name}`) ?? links.get(`${artifact.sha256}\n${artifact.name}`) : undefined;
		return url ? { ...artifact, shareUrl: url } : artifact;
	});
}

/** The person's links, or none where they can't be read: the files list all the same. */
async function readShareLinks(client: CloudClient, signal?: AbortSignal): Promise<CloudArtifactShare[]> {
	try {
		return (await client.listArtifactShares(signal)).shares ?? [];
	} catch (cause) {
		if (cause instanceof Error && cause.name === "AbortError") throw cause;
		return [];
	}
}

export function cloudArtifactsBackend({ client, orgId, sessionId, root, canChange, renew }: {
	client: CloudClient;
	orgId: string;
	sessionId: string;
	/** The signed files prefix, ending in a slash. */
	root: string;
	canChange: boolean;
	renew(): void;
}): ArtifactsBackend {
	const urlOf = (path: string, at = root) => isSafeArtifactPath(path) ? `${at}${escapedPath(path)}` : undefined;
	/** One file's bytes; a refused load asks once for fresh access and tries again with it. */
	const fetchFile = async (path: string, signal?: AbortSignal): Promise<Response> => {
		const url = urlOf(path);
		if (!url) throw new Error("This artifact's path can't be opened.");
		let response = await fetch(url, { signal });
		if (response.status === 401 || response.status === 403) {
			const fresh = await cloudArtifactAccess(client, orgId, sessionId, { renew: true });
			response = await fetch(urlOf(path, fresh.root)!, { signal });
		}
		if (response.status === 404) throw new Error("That file is no longer in this session's artifacts.");
		if (!response.ok) throw new Error(`Orchestrator Cloud couldn't send this file (${response.status}).`);
		return response;
	};
	const imageSource = (path: string, version?: string): ArtifactSource | undefined => {
		const url = urlOf(path);
		// The version (its modified time) is what makes a rewritten image load again.
		return url ? { uri: version ? `${url}?v=${encodeURIComponent(version)}` : url } : undefined;
	};
	const assetLoader: ArtifactAssetLoader = {
		text: (path) => fetchFile(path).then((response) => response.text()).catch(() => undefined),
		dataUri: async (path, maxBytes) => {
			try {
				const response = await fetchFile(path);
				const declared = Number(response.headers.get("content-length"));
				if (declared > maxBytes) {
					void response.body?.cancel().catch(() => {});
					return { uri: "", bytes: declared };
				}
				const blob = await response.blob();
				if (blob.size > maxBytes) return { uri: "", bytes: blob.size };
				const uri = await new Promise<string>((resolve, reject) => {
					const reader = new FileReader();
					reader.onload = () => typeof reader.result === "string" ? resolve(reader.result) : reject(new Error("unreadable"));
					reader.onerror = () => reject(reader.error ?? new Error("unreadable"));
					reader.readAsDataURL(blob);
				});
				return { uri, bytes: blob.size };
			} catch {
				return undefined;
			}
		},
	};
	return {
		// Not the signed prefix: a renewed one reads the same files, and caches keep them.
		key: JSON.stringify(["cloud", client.baseUrl, orgId]),
		cloud: true,
		canChange,
		// Only the session's creator publishes its files, so only their list looks for links.
		list: (_sessionId, signal) => inCloudWords(async () => {
			const [{ artifacts }, shares] = await Promise.all([
				client.listSessionArtifacts(orgId, sessionId, signal),
				canChange ? readShareLinks(client, signal) : Promise.resolve([]),
			]);
			return sortArtifacts(parseArtifacts(withCloudShareLinks(artifacts, shares)));
		}),
		text: async (_sessionId, path, signal) => (await fetchFile(path, signal)).text(),
		// As written: Cloud writes no theme into a page.
		page: async (_sessionId, path, signal) => (await fetchFile(path, signal)).text(),
		imageSource: (_sessionId, path, version) => imageSource(path, version),
		webSource: (_sessionId, path) => {
			const uri = urlOf(path);
			return uri ? { uri } : undefined;
		},
		// The signed address needs no header, so a native player or download reads it as a web view does.
		streamSource: (_sessionId, path, version) => imageSource(path, version),
		markdownImageSource: (_sessionId, documentPath, url, versionOf) => {
			const trimmed = url.trim();
			if (/^(https?:|data:)/i.test(trimmed)) return { uri: trimmed };
			const path = resolveArtifactReference(documentPath, trimmed, sessionId);
			return path ? imageSource(path, versionOf?.(path)) : undefined;
		},
		assetLoader: () => assetLoader,
		remove: (_sessionId, path) => inCloudWords(() => client.deleteSessionArtifact(orgId, sessionId, path)),
		versions: (_sessionId, path, signal) => inCloudWords(async () => parseVersionSummaries((await client.listArtifactVersions(orgId, sessionId, path, signal)).versions)),
		version: (_sessionId, path, version, signal) => inCloudWords(async () => {
			const parsed = parseVersion(await client.getArtifactVersion(orgId, sessionId, path, version, signal));
			if (!parsed) throw new Error("This version couldn't be read.");
			return parsed;
		}),
		restore: (_sessionId, path, version) => inCloudWords(async () => { await client.restoreArtifactVersion(orgId, sessionId, path, version); }),
		renew,
	};
}
