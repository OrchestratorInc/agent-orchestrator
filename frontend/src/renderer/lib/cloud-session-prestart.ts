import type { CloudCpClient } from "./cloud-cp/client";
import { CloudCpError } from "./cloud-cp/errors";

const STORAGE_KEY = "ao.cloudPreparedSessions.v1";

interface LegacyPreparedSession {
	orgId: string;
	sessionId: string;
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
	}
}

const cleaning = new Set<string>();

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
