/**
 * Unpublishes the public links of artifacts the person deletes, so a deleted
 * file's link stops working with it. Only the app window can unpublish (the
 * main process holds the sign-in and the link IDs), so every delete the person
 * clicks goes through here; the daemon's own deletes never reach a published
 * file, since auto-expiry keeps sessions that still have artifacts.
 *
 * It runs in two steps around the delete: the links are looked up first, while
 * the files are still there to name them, and unpublished only once the delete
 * succeeded, so a delete that fails leaves its links working.
 */
import type { TFunction } from "i18next";
import type { CloudShareFile, HostShareLink } from "../../shared/cloud-share";
import { findHostShareLink } from "./artifacts";
import { useUiStore } from "../stores/ui-store";
import { sessionArtifacts, STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";
import { aoBridge } from "./bridge";

/** A file to unpublish, as the share panel names it, without the control plane. */
export type UnpublishTarget = Omit<CloudShareFile, "baseUrl" | "shareId">;

/** The part of the share bridge unpublishing uses. */
export type UnpublishBridge = Pick<typeof aoBridge.cloudShare, "inspect" | "unpublish" | "hostLinks">;

/**
 * An artifact as a delete names it: its ID when it has one, its path, its
 * link when a local file is published, and a cloud file's committed version.
 */
export type DeletedArtifact = { id?: string; path: string; shareUrl?: string; sha256?: string };

/** The links a delete will unpublish once it succeeds. */
export interface PendingUnpublish {
	/**
	 * Unpublishes the links and resolves to how many are still live: those
	 * that could not be looked up or unpublished, signed out or offline among
	 * them. It never throws: the delete already happened, and the caller warns
	 * about the links left live.
	 */
	unpublish(): Promise<number>;
}

const NOTHING_TO_UNPUBLISH: PendingUnpublish = { unpublish: async () => 0 };

const listeners = new Set<() => void>();

/**
 * Publishes made here, running or done, by control plane, host, session and
 * path, with the file each publishes. A done one is kept until a delete covers
 * its file: the file shows its link only once its artifact list is read again,
 * which can come after the delete, or not at all if that read fails.
 */
const publishes = new Map<string, { baseUrl: string; target: UnpublishTarget; done: Promise<unknown> }>();

/** How many publishes are kept; past it, the oldest go first. */
const MAX_TRACKED_PUBLISHES = 200;

function publishKey(baseUrl: string, target: Pick<UnpublishTarget, "hostId" | "sessionId" | "path">): string {
	return JSON.stringify([baseUrl, target.hostId ?? null, target.sessionId, target.path]);
}

/**
 * Records a publish. A delete of its file, started while it runs or before the
 * file's list shows the link, waits for it and then looks up the link it made.
 */
export function trackPublish(baseUrl: string, target: UnpublishTarget, done: Promise<unknown>): void {
	const key = publishKey(baseUrl, target);
	publishes.delete(key);
	publishes.set(key, { baseUrl, target, done });
	for (const oldest of publishes.keys()) {
		if (publishes.size <= MAX_TRACKED_PUBLISHES) break;
		publishes.delete(oldest);
	}
}

/**
 * What a delete removes: one file, or every file of a session when path is
 * unset, on the remote host hostId names, or on this computer when unset.
 */
export type DeleteScope = { hostId?: string; sessionId: string; path?: string };

/** Waits for the publishes made on what a delete removes, and returns their files. */
async function settledPublishes(baseUrl: string, deleting: readonly DeleteScope[]): Promise<UnpublishTarget[]> {
	const running = [...publishes.values()].filter((entry) =>
		entry.baseUrl === baseUrl
		&& deleting.some((scope) => (scope.hostId ?? null) === (entry.target.hostId ?? null)
			&& scope.sessionId === entry.target.sessionId
			&& (scope.path === undefined || scope.path === entry.target.path)));
	await Promise.allSettled(running.map(({ done }) => done));
	return running.map(({ target }) => target);
}

/** Calls listener after links were unpublished here, so link lists are read again. */
export function onArtifactLinksUnpublished(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

/**
 * Warns that a delete went ahead while some of its links could not be
 * unpublished: anyone with one can still open the deleted file.
 */
export function warnLinksStillLive(t: TFunction, count: number): void {
	if (count <= 0) return;
	useUiStore.getState().showGlobalToast(t("links.stillLive", { count }), t("links.stillLiveBody", { count }), {
		tone: "error",
		dismissible: true,
	});
}

/** The published artifacts among those a delete removes. */
export function publishedArtifacts<T extends Pick<DeletedArtifact, "shareUrl">>(artifacts: readonly T[]): T[] {
	return artifacts.filter((artifact) => Boolean(artifact.shareUrl));
}

/**
 * Whether a delete must look an artifact's link up. A local file says whether
 * it is published (its shareUrl, from this computer's records), as does a
 * remote host's file once matched to this computer's host links. A cloud
 * file's list never does: its link is recorded on this computer by org,
 * session and artifact ID, so each one is looked up.
 */
export function mayBePublished(session: Pick<WorkspaceSession, "cloud">, artifact: Pick<DeletedArtifact, "shareUrl">): boolean {
	return Boolean(session.cloud) || Boolean(artifact.shareUrl);
}

/**
 * The share file of a session's artifact. A cloud file carries its committed
 * version, so looking its link up reads only this computer's records. A local file's project only names
 * the document a publish writes to, but the main process requires one, so a
 * session without a project uses the Scratchpad's, as the session view does.
 */
export function unpublishTarget(
	session: Pick<WorkspaceSession, "id" | "cloud"> & { workspaceId?: string; hostId?: string },
	artifact: DeletedArtifact,
): UnpublishTarget {
	const artifactId = artifact.id ? { artifactId: artifact.id } : {};
	if (session.cloud) {
		const fingerprint = artifact.sha256 ? { fingerprint: artifact.sha256 } : {};
		return { source: "cloud", orgId: session.cloud.orgId, sessionId: session.id, path: artifact.path, projectId: "", ...artifactId, ...fingerprint };
	}
	const projectId = session.workspaceId || STANDALONE_WORKSPACE_ID;
	if (session.hostId) return { source: "host", hostId: session.hostId, sessionId: session.id, path: artifact.path, projectId, ...artifactId };
	return { sessionId: session.id, path: artifact.path, projectId, ...artifactId };
}

/** The files whose links a delete of these artifacts must look up and unpublish. */
export function deleteUnpublishTargets(
	session: Pick<WorkspaceSession, "id" | "cloud" | "hostId"> & { workspaceId?: string },
	artifacts: readonly DeletedArtifact[],
): UnpublishTarget[] {
	return artifacts.filter((artifact) => mayBePublished(session, artifact)).map((artifact) => unpublishTarget(session, artifact));
}

/**
 * A session's artifacts with their links. A remote daemon lists none, so a
 * remote host's files take theirs from this computer's host links.
 */
function sessionArtifactsWithLinks(session: WorkspaceSession, hostLinks: readonly HostShareLink[]): DeletedArtifact[] {
	const { hostId } = session;
	return sessionArtifacts(session).map((artifact) => {
		const link = hostId && !artifact.shareUrl ? findHostShareLink(hostLinks, hostId, session.id, artifact) : undefined;
		return link ? { ...artifact, shareUrl: link.url } : artifact;
	});
}

/**
 * The files whose links deleting a session's artifacts must look up and
 * unpublish. A remote host's session needs this computer's host links.
 */
export function sessionUnpublishTargets(session: WorkspaceSession, hostLinks: readonly HostShareLink[] = []): UnpublishTarget[] {
	return deleteUnpublishTargets(session, sessionArtifactsWithLinks(session, hostLinks));
}

/**
 * deleteUnpublishTargets, reading this computer's host links at the delete for
 * a remote host's files, rather than trusting a list that may not have loaded
 * or may be empty while signed out. When they cannot be read, every file is
 * looked up.
 */
export async function deleteTargets(
	baseUrl: string,
	session: Pick<WorkspaceSession, "id" | "cloud" | "hostId"> & { workspaceId?: string },
	artifacts: readonly DeletedArtifact[],
	bridge: UnpublishBridge = aoBridge.cloudShare,
): Promise<UnpublishTarget[]> {
	const { hostId } = session;
	if (!hostId || !baseUrl) return deleteUnpublishTargets(session, artifacts);
	const hostLinks = await bridge.hostLinks(baseUrl).catch(() => undefined);
	if (!hostLinks) return artifacts.map((artifact) => unpublishTarget(session, artifact));
	return deleteUnpublishTargets(session, artifacts.map((artifact) => {
		const link = artifact.shareUrl ? undefined : findHostShareLink(hostLinks, hostId, session.id, artifact);
		return link ? { ...artifact, shareUrl: link.url } : artifact;
	}));
}

/** deleteTargets for every artifact of a session. */
export function sessionDeleteTargets(baseUrl: string, session: WorkspaceSession, bridge: UnpublishBridge = aoBridge.cloudShare): Promise<UnpublishTarget[]> {
	return deleteTargets(baseUrl, session, sessionArtifacts(session), bridge);
}

/**
 * What a delete's confirm counts as published: the local files known to be
 * (`known`) and the cloud files whose links must be looked up first (`lookup`).
 */
export type PublishedCheck = { known: number; lookup: UnpublishTarget[] };

export const NOTHING_PUBLISHED: PublishedCheck = { known: 0, lookup: [] };

/** The PublishedCheck of deleting these artifacts of one session. */
export function publishedCheck(
	session: Pick<WorkspaceSession, "id" | "cloud" | "hostId"> & { workspaceId?: string },
	artifacts: readonly DeletedArtifact[],
): PublishedCheck {
	if (session.cloud) return { known: 0, lookup: deleteUnpublishTargets(session, artifacts) };
	return { known: publishedArtifacts(artifacts).length, lookup: [] };
}

/** The PublishedCheck of deleting a whole session's artifacts; see sessionUnpublishTargets for hostLinks. */
export function sessionPublishedCheck(session: WorkspaceSession | undefined, hostLinks: readonly HostShareLink[] = []): PublishedCheck {
	return session ? publishedCheck(session, sessionArtifactsWithLinks(session, hostLinks)) : NOTHING_PUBLISHED;
}

/** Adds checks up, for a delete that spans sessions. */
export function combinePublishedChecks(checks: readonly PublishedCheck[]): PublishedCheck {
	return { known: checks.reduce((sum, check) => sum + check.known, 0), lookup: checks.flatMap((check) => check.lookup) };
}

/**
 * How many of these files have a link recorded on this computer. A file that
 * cannot be looked up is not counted: this only words a confirm.
 */
export async function countPublished(baseUrl: string, files: readonly UnpublishTarget[], bridge: UnpublishBridge = aoBridge.cloudShare): Promise<number> {
	if (!baseUrl || files.length === 0) return 0;
	const lookups = await Promise.allSettled(files.map((file) => bridge.inspect({ ...file, baseUrl }, { linkOnly: true })));
	return new Set(lookups.flatMap((lookup) => lookup.status === "fulfilled" && lookup.value.shareId ? [lookup.value.shareId] : [])).size;
}

/**
 * Looks up the links of files about to be deleted. Call it before the
 * delete, then `unpublish` once the delete succeeded. It never throws.
 * `deleting` names what the delete removes, so a publish still running on it
 * is waited for and its link looked up too; it defaults to `files`.
 */
export async function prepareUnpublish(
	baseUrl: string,
	known: readonly UnpublishTarget[],
	{ deleting = known, bridge = aoBridge.cloudShare }: { deleting?: readonly DeleteScope[]; bridge?: UnpublishBridge } = {},
): Promise<PendingUnpublish> {
	if (!baseUrl) return known.length === 0 ? NOTHING_TO_UNPUBLISH : { unpublish: async () => known.length };
	const publishedMeanwhile = (await settledPublishes(baseUrl, deleting))
		.filter((target) => !known.some((file) => publishKey(baseUrl, file) === publishKey(baseUrl, target)));
	const files = [...known, ...publishedMeanwhile];
	if (files.length === 0) return NOTHING_TO_UNPUBLISH;
	const lookups = await Promise.allSettled(files.map((file) => bridge.inspect({ ...file, baseUrl }, { linkOnly: true })));
	const shareIds = new Set<string>();
	let unresolved = 0;
	for (const lookup of lookups) {
		if (lookup.status === "rejected") unresolved += 1;
		else if (lookup.value.shareId) shareIds.add(lookup.value.shareId);
	}
	return {
		unpublish: async () => {
			// The files are gone: their publishes no longer need finding.
			for (const file of files) publishes.delete(publishKey(baseUrl, file));
			if (shareIds.size === 0) return unresolved;
			const results = await Promise.allSettled([...shareIds].map((shareId) => bridge.unpublish(baseUrl, shareId)));
			for (const listener of listeners) listener();
			return unresolved + results.filter((result) => result.status === "rejected").length;
		},
	};
}
