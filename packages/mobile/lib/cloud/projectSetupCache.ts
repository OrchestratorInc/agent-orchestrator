import type { GitHubInstallation, GitHubRepository, RedactedProviderConnection } from "@aoagents/cloud-client";

export type ProjectGitHubSetup = { installations: GitHubInstallation[]; repositories: GitHubRepository[] };

type CachedValue<T> = { value: T; expiresAt: number };
type Entry = { github?: CachedValue<ProjectGitHubSetup>; connections?: CachedValue<RedactedProviderConnection[]> };
type Snapshot = { github?: { value: ProjectGitHubSetup; fresh: boolean }; connections?: { value: RedactedProviderConnection[]; fresh: boolean } };

/** Completed, non-secret setup data only. Callers scope owner keys to the auth session. */
export function createCloudProjectSetupCache({ now = () => Date.now(), maxEntries = 4 }: { now?: () => number; maxEntries?: number } = {}) {
	const entries = new Map<string, Entry>();
	const limit = Number.isFinite(maxEntries) ? Math.max(1, Math.floor(maxEntries)) : 4;
	const touch = (ownerKey: string, entry: Entry) => {
		entries.delete(ownerKey);
		entries.set(ownerKey, entry);
	};
	const writableEntry = (ownerKey: string) => {
		const entry = entries.get(ownerKey) ?? {};
		touch(ownerKey, entry);
		while (entries.size > limit) {
			const oldest = entries.keys().next().value;
			if (oldest === undefined) break;
			entries.delete(oldest);
		}
		return entry;
	};

	return {
		read(ownerKey: string): Snapshot {
			const entry = entries.get(ownerKey);
			if (!entry) return {};
			touch(ownerKey, entry);
			const time = now();
			const result: Snapshot = {};
			if (entry.github) result.github = { value: entry.github.value, fresh: entry.github.expiresAt > time };
			if (entry.connections) result.connections = { value: entry.connections.value, fresh: entry.connections.expiresAt > time };
			return result;
		},
		setGitHub(ownerKey: string, value: ProjectGitHubSetup): void {
			writableEntry(ownerKey).github = { value, expiresAt: now() + 30_000 };
		},
		setConnections(ownerKey: string, value: RedactedProviderConnection[]): void {
			writableEntry(ownerKey).connections = { value, expiresAt: now() + 60_000 };
		},
		invalidate(ownerKey: string, resource: "github" | "connections"): void {
			const entry = entries.get(ownerKey);
			if (!entry) return;
			delete entry[resource];
			if (!entry.github && !entry.connections) entries.delete(ownerKey);
		},
		/** Account keys are [baseUrl, accountId, epoch]; project keys also have orgId before epoch. */
		invalidateAccountConnections(accountOwnerKey: string): void {
			let account: unknown;
			try { account = JSON.parse(accountOwnerKey); } catch { return; }
			if (!Array.isArray(account) || account.length !== 3 || typeof account[0] !== "string" ||
				typeof account[1] !== "string" || !account[1] || typeof account[2] !== "number" || !Number.isFinite(account[2])) return;
			for (const [key, entry] of entries) {
				let owner: unknown;
				try { owner = JSON.parse(key); } catch { continue; }
				if (!Array.isArray(owner) || owner.length !== 4 || typeof owner[0] !== "string" ||
					owner[0].replace(/\/+$/, "") !== account[0].replace(/\/+$/, "") || owner[1] !== account[1] || owner[3] !== account[2]) continue;
				delete entry.connections;
				if (!entry.github) entries.delete(key);
			}
		},
	};
}

export const cloudProjectSetupCache = createCloudProjectSetupCache();
