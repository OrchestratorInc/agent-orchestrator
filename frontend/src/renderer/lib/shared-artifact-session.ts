import type { SharedArtifactCopy, SharedArtifactCopyInput, SharedArtifactCopyResult, SharedArtifactSessionKey, SharedArtifactSessionRecord } from "../../preload";
import type { CloudCpArtifactShareView } from "./cloud-cp";

/** What the session a shared artifact's record names looks like now. */
export type LinkedSession =
	| { exists: false }
	| { exists: true; projectId: string; terminated: boolean; busy: boolean };

export interface SharedArtifactSessionDeps {
	getRecord: (key: SharedArtifactSessionKey) => Promise<SharedArtifactSessionRecord | null>;
	setRecord: (key: SharedArtifactSessionKey, record: SharedArtifactSessionRecord) => Promise<void>;
	removeRecord: (key: SharedArtifactSessionKey) => Promise<void>;
	getSession: (sessionId: string) => Promise<LinkedSession>;
	view: (linkId: string) => Promise<CloudCpArtifactShareView>;
	copy: (input: SharedArtifactCopyInput) => Promise<SharedArtifactCopyResult>;
}

/** What "Start a session with this" does next for one shared artifact. */
export type SharedArtifactSessionStep =
	/** Start a new draft from a fresh copy. */
	| { kind: "draft" }
	/** Open the session that works from the artifact. */
	| { kind: "open"; sessionId: string; projectId: string; entry: string }
	/** The session is archived: unarchive it, or start a new draft. */
	| { kind: "archived"; record: SharedArtifactSessionRecord }
	/** The owner changed the artifact since it was copied: update the copy, or keep it. */
	| { kind: "update"; sessionId: string; projectId: string; busy: boolean; record: SharedArtifactSessionRecord; copy: SharedArtifactCopy; updatedAt: string };

/** The step for a shared artifact: from its record, the session it names, and the link as it is now. */
export async function nextSharedArtifactStep(deps: SharedArtifactSessionDeps, key: SharedArtifactSessionKey, name: string): Promise<SharedArtifactSessionStep> {
	const record = await deps.getRecord(key).catch(() => null);
	if (!record) return { kind: "draft" };
	const session = await deps.getSession(record.sessionId);
	if (!session.exists) {
		// Deleted: the link starts over with a new copy.
		await deps.removeRecord(key).catch(() => undefined);
		return { kind: "draft" };
	}
	if (session.terminated) return { kind: "archived", record };
	return checkForUpdate(deps, key, name, record, session);
}

/**
 * Opens the session as it is unless the owner really changed the artifact: a
 * new date with the same bytes only moves the record's date, and a version the
 * person already chose to keep theirs over is not offered again. Anything that
 * stops the check (signed out, access removed, offline) opens the session as it is.
 */
export async function checkForUpdate(
	deps: Pick<SharedArtifactSessionDeps, "view" | "copy" | "setRecord">,
	key: SharedArtifactSessionKey,
	name: string,
	record: SharedArtifactSessionRecord,
	session: { projectId: string; busy: boolean },
): Promise<SharedArtifactSessionStep> {
	const open: SharedArtifactSessionStep = { kind: "open", sessionId: record.sessionId, projectId: session.projectId, entry: record.entry };
	let view: CloudCpArtifactShareView;
	try {
		view = await deps.view(key.linkId);
	} catch {
		return open;
	}
	if (view.updatedAt === record.updatedAt) return open;
	const copyNow = () => deps.copy({ contentUrl: view.contentUrl, name: view.name || name, into: { dir: record.dir, entry: record.entry } }).catch(() => null);
	const result = await copyNow();
	if (!result?.ok) return open;
	let { copy } = result;
	// A page file that failed to download changes the fingerprint too: such a
	// copy is offered only if a second one agrees, so a passing failure is
	// never taken for the owner's change.
	if (copy.missing.length > 0 && copy.fingerprint !== record.fingerprint && copy.fingerprint !== record.declinedFingerprint) {
		const again = await copyNow();
		if (!again?.ok || again.copy.fingerprint !== copy.fingerprint) return open;
		copy = again.copy;
	}
	// Content the copy already holds, or that the person kept theirs over: the
	// record takes the link's new date, so later clicks don't download it again.
	if (copy.fingerprint === record.fingerprint || copy.fingerprint === record.declinedFingerprint) {
		await deps.setRecord(key, { ...record, updatedAt: view.updatedAt }).catch(() => undefined);
		return open;
	}
	return { kind: "update", sessionId: record.sessionId, projectId: session.projectId, busy: session.busy, record, copy, updatedAt: view.updatedAt };
}
