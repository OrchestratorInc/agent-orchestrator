import { getSession, type DashboardSession } from "../api";
import type { ServerConfig } from "../config";

// One request per session however many rows and links name it. A failed read
// leaves the cache so the next one to need it asks again, and the oldest read
// goes first once the cache holds a long history's worth of sessions.
const reads = new Map<string, Promise<DashboardSession | null>>();
const READS_LIMIT = 200;

export const sessionReadKey = (config: ServerConfig, sessionId: string) => `${config.hostId ?? config.host}:${sessionId}`;

/**
 * A session the board leaves out (a daemon that pages its archive omits finished
 * ones), read once from its machine; null when it cannot be read.
 */
export function readSession(config: ServerConfig, sessionId: string): Promise<DashboardSession | null> {
	const key = sessionReadKey(config, sessionId);
	let request = reads.get(key);
	if (!request) {
		request = getSession(config, sessionId).catch(() => {
			reads.delete(key);
			return null;
		});
		if (reads.size >= READS_LIMIT) reads.delete(reads.keys().next().value!);
		reads.set(key, request);
	}
	return request;
}
