import AsyncStorage from "@react-native-async-storage/async-storage";

const ignoredKey = (accountId: string) => `ao.accountHosts.ignored.${accountId}`;

// Serialize local read/write transactions, never remote requests. Sync commits
// use the same queue so a completed Forget cannot be undone by a late response.
let mutationTail: Promise<void> = Promise.resolve();
export function mutateAccountHosts<T>(operation: () => Promise<T>): Promise<T> {
	const next = mutationTail.then(operation, operation);
	mutationTail = next.then(() => undefined, () => undefined);
	return next;
}

export async function ignoredAccountHosts(accountId: string): Promise<Set<string>> {
	const raw = await AsyncStorage.getItem(ignoredKey(accountId));
	if (!raw) return new Set();
	try {
		const ids: unknown = JSON.parse(raw);
		return new Set(Array.isArray(ids) ? ids.filter((id): id is string => typeof id === "string") : []);
	} catch { return new Set(); }
}

/** Forget is durable on this phone without waiting for any network request. */
export function ignoreAccountHost(accountId: string, hostId: string): Promise<void> {
	return mutateAccountHosts(async () => {
		const ids = await ignoredAccountHosts(accountId);
		ids.add(hostId);
		await AsyncStorage.setItem(ignoredKey(accountId), JSON.stringify([...ids]));
	});
}

/** An explicit re-pair restores a previously hidden machine on this phone. */
export function unignoreAccountHost(accountId: string, hostId: string): Promise<void> {
	return mutateAccountHosts(async () => {
		const ids = await ignoredAccountHosts(accountId);
		if (!ids.delete(hostId)) return;
		await AsyncStorage.setItem(ignoredKey(accountId), JSON.stringify([...ids]));
	});
}
