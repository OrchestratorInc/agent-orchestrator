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
import type { CloudShareFile } from "../../shared/cloud-share";
import { useUiStore } from "../stores/ui-store";
import { sessionArtifacts, STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";
import { aoBridge } from "./bridge";

/** A file to unpublish, as the share panel names it, without the control plane. */
export type UnpublishTarget = Omit<CloudShareFile, "baseUrl" | "shareId">;

/** The part of the share bridge unpublishing uses. */
export type UnpublishBridge = Pick<typeof aoBridge.cloudShare, "inspect" | "unpublish">;

/** An artifact as a delete names it: its ID when it has one, its path, and whether it is published. */
export type DeletedArtifact = { id?: string; path: string; shareUrl?: string };

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
 * The share file of a session's artifact. A local file's project only names
 * the document a publish writes to, but the main process requires one, so a
 * session without a project uses the Scratchpad's, as the session view does.
 */
export function unpublishTarget(
	session: Pick<WorkspaceSession, "id" | "cloud"> & { workspaceId?: string },
	artifact: DeletedArtifact,
): UnpublishTarget {
	const artifactId = artifact.id ? { artifactId: artifact.id } : {};
	if (session.cloud) {
		return { source: "cloud", orgId: session.cloud.orgId, sessionId: session.id, path: artifact.path, projectId: "", ...artifactId };
	}
	return { sessionId: session.id, path: artifact.path, projectId: session.workspaceId || STANDALONE_WORKSPACE_ID, ...artifactId };
}

/**
 * The published artifacts of a session, as files to unpublish. A remote
 * host's session has none: links are published from, and recorded on, this
 * computer only.
 */
export function sessionUnpublishTargets(session: WorkspaceSession): UnpublishTarget[] {
	if (session.hostId) return [];
	return publishedArtifacts(sessionArtifacts(session)).map((artifact) => unpublishTarget(session, artifact));
}

/** How many published links a session's delete would stop. */
export function sessionPublishedCount(session: WorkspaceSession | undefined): number {
	return session ? sessionUnpublishTargets(session).length : 0;
}

/**
 * Looks up the links of files about to be deleted. Call it before the
 * delete, then `unpublish` once the delete succeeded. It never throws.
 */
export async function prepareUnpublish(
	baseUrl: string,
	files: readonly UnpublishTarget[],
	bridge: UnpublishBridge = aoBridge.cloudShare,
): Promise<PendingUnpublish> {
	if (files.length === 0) return NOTHING_TO_UNPUBLISH;
	if (!baseUrl) return { unpublish: async () => files.length };
	const lookups = await Promise.allSettled(files.map((file) => bridge.inspect({ ...file, baseUrl })));
	const shareIds = new Set<string>();
	let unresolved = 0;
	for (const lookup of lookups) {
		if (lookup.status === "rejected") unresolved += 1;
		else if (lookup.value.shareId) shareIds.add(lookup.value.shareId);
	}
	return {
		unpublish: async () => {
			if (shareIds.size === 0) return unresolved;
			const results = await Promise.allSettled([...shareIds].map((shareId) => bridge.unpublish(baseUrl, shareId)));
			for (const listener of listeners) listener();
			return unresolved + results.filter((result) => result.status === "rejected").length;
		},
	};
}
