// Earlier builds of the new chat page started a Cloud session as the page
// opened, before its brief was sent, and recorded it here until a send claimed
// it. Sessions are now created only when the person sends. This removes what
// those builds left behind: a recorded session that never received a message
// is deleted, and the record goes with it.

import type { CloudCpClient } from "./cloud-cp/client";
import { CloudCpError } from "./cloud-cp/errors";

const STORAGE_KEY = "ao.cloudPreparedSessions.v1";

interface LegacyPreparedSession {
	orgId: string;
	sessionId: string;
	/** Already deleted by the build that recorded it. */
	discarded?: boolean;
}

function readLegacy(): LegacyPreparedSession[] {
	try {
		const saved: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "[]");
		return Array.isArray(saved)
			? saved.filter((entry): entry is LegacyPreparedSession =>
				Boolean(entry) && typeof entry.orgId === "string" && typeof entry.sessionId === "string")
			: [];
	} catch {
		return [];
	}
}

function forget(sessionId: string): void {
	try {
		const next = readLegacy().filter((entry) => entry.sessionId !== sessionId);
		if (next.length === 0) localStorage.removeItem(STORAGE_KEY);
		else localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
	} catch {
		// Nothing to forget without storage.
	}
}

const cleaning = new Set<string>();

/**
 * Deletes the org's sessions an earlier build started ahead of a brief and no
 * send claimed. A recorded session with a message of its own became a task (a
 * send claimed it just before the app quit), so it is kept; only its record
 * goes. A failure other than "already gone" keeps the record for the next run.
 */
export async function cleanUpLegacyPreparedCloudSessions(
	client: Pick<CloudCpClient, "listChatEvents" | "deleteSession">,
	orgId: string,
): Promise<void> {
	const recorded = readLegacy().filter((entry) => entry.orgId === orgId);
	if (recorded.length === 0 || cleaning.has(orgId)) return;
	cleaning.add(orgId);
	try {
		for (const entry of recorded) {
			if (!entry.discarded) {
				try {
					const page = await client.listChatEvents(orgId, entry.sessionId, { after: 0, limit: 1, types: ["chat.user_message"] });
					if (page.events.length === 0) await client.deleteSession(orgId, entry.sessionId);
				} catch (error) {
					const gone = error instanceof CloudCpError && (error.status === 404 || error.code === "SESSION_TERMINATED");
					if (!gone) continue;
				}
			}
			forget(entry.sessionId);
		}
	} finally {
		cleaning.delete(orgId);
	}
}
