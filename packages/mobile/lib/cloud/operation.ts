import AsyncStorage from "@react-native-async-storage/async-storage";
import { afterDraftWrites } from "../chat/pendingSend";

export interface CloudOperation<T> {
	scopeKey: string;
	key: string;
	serialized: string;
	payload: T;
	acceptedId?: string;
}
const PREFIX = "ao.cloud.operations.v1.";
const generations = new Map<string, number>();
const storageKey = (scopeKey: string) => PREFIX + encodeURIComponent(scopeKey);
type Records = Record<string, CloudOperation<unknown>>;

export function prepareCloudOperation<T>(previous: CloudOperation<T> | undefined, scopeKey: string, payload: T, newKey: () => string): CloudOperation<T> {
	const serialized = JSON.stringify(payload);
	if (previous?.scopeKey === scopeKey && previous.serialized === serialized) return previous;
	return { scopeKey, key: newKey(), serialized, payload: JSON.parse(serialized) as T };
}
function validateSlot(slot: string) {
	if (!/^(project-create|worker-create)\/.+/.test(slot)) throw new Error("Invalid Cloud operation slot.");
}
function containsCredential(value: unknown): boolean {
	if (!value || typeof value !== "object") return false;
	return Object.entries(value).some(([key, child]) =>
		/^(secret|token|accessToken|refreshToken|password|authorization|apiKey|privateKey)$/i.test(key) || containsCredential(child));
}
async function load(scopeKey: string): Promise<Records> {
	const raw = await AsyncStorage.getItem(storageKey(scopeKey));
	if (!raw) return {};
	const parsed = JSON.parse(raw) as { version?: number; scopeKey?: string; operations?: Records };
	if (parsed.version !== 1 || parsed.scopeKey !== scopeKey || !parsed.operations || typeof parsed.operations !== "object") {
		throw new Error("The previous Cloud operation record is damaged. Check the existing project before retrying.");
	}
	for (const record of Object.values(parsed.operations)) {
		if (!record || record.scopeKey !== scopeKey || typeof record.key !== "string" || !record.key ||
			JSON.stringify(record.payload) !== record.serialized || (record.acceptedId !== undefined && typeof record.acceptedId !== "string")) {
			throw new Error("The previous Cloud operation record is damaged. Check the existing project before retrying.");
		}
	}
	return parsed.operations;
}
async function save(scopeKey: string, operations: Records): Promise<void> {
	if (Object.keys(operations).length === 0) await AsyncStorage.removeItem(storageKey(scopeKey));
	else await AsyncStorage.setItem(storageKey(scopeKey), JSON.stringify({ version: 1, scopeKey, operations }));
}
export async function readCloudOperation<T>(scopeKey: string, slot: string): Promise<CloudOperation<T> | null> {
	validateSlot(slot);
	let result: CloudOperation<T> | null = null;
	await afterDraftWrites(storageKey(scopeKey), async () => { result = (await load(scopeKey))[slot] as CloudOperation<T> ?? null; });
	return result;
}
export async function readCloudOperationsForScope<T>(scopeKey: string): Promise<Array<{ slot: string; operation: CloudOperation<T> }>> {
	let records: Records = {};
	await afterDraftWrites(storageKey(scopeKey), async () => { records = await load(scopeKey); });
	return Object.entries(records).map(([slot, operation]) => ({ slot, operation: operation as CloudOperation<T> }));
}
export function reserveCloudOperation<T>(slot: string, operation: CloudOperation<T>): Promise<void> {
	validateSlot(slot);
	if (containsCredential(operation.payload)) return Promise.reject(new Error("Credential fields cannot be stored in a Cloud operation."));
	const generation = generations.get(operation.scopeKey) ?? 0;
	// Track the scope before any async storage work. A first write may not
	// appear in getAllKeys yet when explicit account sign-out begins.
	generations.set(operation.scopeKey, generation);
	return afterDraftWrites(storageKey(operation.scopeKey), async () => {
		if (generation !== (generations.get(operation.scopeKey) ?? 0)) throw new Error("Cloud account changed.");
		const records = await load(operation.scopeKey);
		const previous = records[slot];
		if (previous && (previous.key !== operation.key || previous.serialized !== operation.serialized)) {
			throw new Error("Resolve the previous Cloud request before starting a changed request.");
		}
		if (!previous) await save(operation.scopeKey, { ...records, [slot]: operation });
		if (generation !== (generations.get(operation.scopeKey) ?? 0)) throw new Error("Cloud account changed.");
	});
}
export function acceptCloudOperation(scopeKey: string, slot: string, key: string, acceptedId: string): Promise<void> {
	validateSlot(slot);
	return afterDraftWrites(storageKey(scopeKey), async () => {
		const records = await load(scopeKey);
		if (records[slot]?.key !== key) return;
		await save(scopeKey, { ...records, [slot]: { ...records[slot], acceptedId } });
	});
}
export function clearCloudOperation(scopeKey: string, slot: string, key: string): Promise<void> {
	validateSlot(slot);
	return afterDraftWrites(storageKey(scopeKey), async () => {
		const records = await load(scopeKey);
		if (records[slot]?.key !== key) return;
		delete records[slot];
		await save(scopeKey, records);
	});
}
export function clearCloudOperationsForScope(scopeKey: string): Promise<void> {
	generations.set(scopeKey, (generations.get(scopeKey) ?? 0) + 1);
	return afterDraftWrites(storageKey(scopeKey), () => AsyncStorage.removeItem(storageKey(scopeKey)));
}

/** Explicit sign-out retires only this deployment/account's non-secret retry records. */
export async function clearCloudOperationsForAccount(baseUrl: string, userId: string): Promise<void> {
	const keys = await AsyncStorage.getAllKeys();
	const scopes = new Set(generations.keys());
	for (const key of keys) {
		if (!key.startsWith(PREFIX)) continue;
		try { scopes.add(decodeURIComponent(key.slice(PREFIX.length))); } catch {}
	}
	for (const encoded of scopes) {
		let scope: unknown;
		try { scope = JSON.parse(encoded); } catch { continue; }
		if (Array.isArray(scope) && scope.length === 3 && scope.every((value) => typeof value === "string") &&
			scope[0] === baseUrl.replace(/\/+$/, "") && scope[1] === userId) {
			await clearCloudOperationsForScope(JSON.stringify(scope));
		}
	}
}
