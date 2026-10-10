import { boardZoneOf, eventAtOf, isArchived } from "../agentsView";
import type { HostSnapshot } from "../otherHosts";
import { sessionTitle } from "../sessionStatus";

export type WatchItem = { sessionId: string; title: string; reason: string; mode: "chat" | "tui" };
export type WatchHost = { hostId: string; name: string; capturedAt: number | null; available: boolean; count: number | null; items: WatchItem[] };
export type WatchSnapshot = { version: 1; generatedAt: number; omittedHosts: number; hosts: WatchHost[] };

const short = (text: string, max: number) => Array.from(text).slice(0, max).join("");

/** Only display fields cross the bridge. Never spread a host/config/session into this payload. */
export function buildWatchSnapshot(hosts: HostSnapshot[], now: number, previous?: WatchSnapshot): WatchSnapshot {
	return {
		version: 1,
		generatedAt: now,
		omittedHosts: Math.max(0, hosts.length - 8),
		hosts: hosts.slice(0, 8).map((host) => {
			const identity = { hostId: host.hostId, name: short(host.name, 48) };
			const available = host.connection === "open" && !!host.config && host.config.hostId === host.hostId && !host.error && host.lastSyncAt > 0 && host.lastSyncAt <= now;
			if (!available) {
				const cached = previous?.hosts.find((entry) => entry.hostId === host.hostId);
				return { ...identity, available: false, capturedAt: cached?.capturedAt ?? null, count: cached?.count ?? null, items: cached?.items ?? [] };
			}
			const attention = host.sessions.filter((session) => !isArchived(session) && boardZoneOf(session) === "needs_you");
			attention.sort((a, b) => (Date.parse(eventAtOf(a)) || 0) - (Date.parse(eventAtOf(b)) || 0));
			return {
				...identity, available: true, capturedAt: host.lastSyncAt, count: attention.length,
				items: attention.slice(0, 8).map((session) => ({
					sessionId: session.id,
					title: short(sessionTitle(session), 80),
					reason: session.status === "errored" ? "Agent error" : session.status === "stuck" ? "Agent stuck" : "Needs your input",
					mode: session.mode === "chat" ? "chat" : "tui",
				})),
			};
		}),
	};
}
