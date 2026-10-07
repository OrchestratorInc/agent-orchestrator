import AsyncStorage from "@react-native-async-storage/async-storage";
import { getAccountAccessToken, loadAccount, type Account } from "./account";
import { endpointBaseUrl, type Endpoint } from "./endpoints";
import { loadHosts, removeHost, saveHost, type Host } from "./hosts";

const CONTROL_PLANE = (process.env.EXPO_PUBLIC_AO_CLOUD_API_BASE_URL || "https://api.aoagents.dev").replace(/\/$/, "");
type AccountHost = { hostId: string; label: string; url: string; token: string };
const ignoredKey = (accountId: string) => `ao.accountHosts.ignored.${accountId}`;

async function ignoredHosts(accountId: string): Promise<Set<string>> {
	const raw = await AsyncStorage.getItem(ignoredKey(accountId));
	if (!raw) return new Set();
	try {
		const ids: unknown = JSON.parse(raw);
		return new Set(Array.isArray(ids) ? ids.filter((id): id is string => typeof id === "string") : []);
	} catch { return new Set(); }
}

function endpoint(raw: string): Endpoint | null {
	try {
		const url = new URL(raw);
		if ((url.protocol !== "https:" && url.protocol !== "http:") || !url.hostname || url.username || url.password) return null;
		return {
			kind: url.hostname.endsWith(".trycloudflare.com") ? "tunnel" : url.hostname.endsWith(".ts.net") ? "tailscale" : "lan",
			host: url.hostname,
			port: Number(url.port || (url.protocol === "https:" ? 443 : 80)),
			secure: url.protocol === "https:",
		};
	} catch { return null; }
}

// A sign-out waits for the current sync before clearing its local credentials.
let pendingSync: Promise<void> = Promise.resolve();

function enqueue<T>(operation: () => Promise<T>): Promise<T> {
	const next = pendingSync.catch(() => {}).then(operation);
	pendingSync = next.then(() => undefined, () => undefined);
	return next;
}

/** Forgetting on this phone must not re-import the host on the next account sync. */
export function ignoreAccountHost(accountId: string, hostId: string): Promise<void> {
	return enqueue(async () => {
		const ids = await ignoredHosts(accountId);
		ids.add(hostId);
		await AsyncStorage.setItem(ignoredKey(accountId), JSON.stringify([...ids]));
	});
}

/** An explicit re-pair restores a previously hidden machine on this phone. */
export function unignoreAccountHost(accountId: string, hostId: string): Promise<void> {
	return enqueue(async () => {
		const ids = await ignoredHosts(accountId);
		if (!ids.delete(hostId)) return;
		await AsyncStorage.setItem(ignoredKey(accountId), JSON.stringify([...ids]));
	});
}

/** Import only account-owned machines; never replace a manual pairing token. */
export function syncAccountHosts(account: Account): Promise<void> {
	return enqueue(async () => {
		if ((await loadAccount())?.id !== account.id) return;
		await performSync(account);
	});
}

async function performSync(account: Account): Promise<void> {
	const access = await getAccountAccessToken();
	if (!access) return;
	const response = await fetch(`${CONTROL_PLANE}/api/cloud/v1/me/hosts`, {
		headers: { Authorization: `Bearer ${access}`, Accept: "application/json" },
		signal: AbortSignal.timeout(10_000),
	});
	if (!response.ok) throw new Error(`AO Cloud host sync failed (${response.status}).`);
	const { hosts } = await response.json() as { hosts: AccountHost[] };
	const paired = await loadHosts();
	const ignored = await ignoredHosts(account.id);
	for (const manual of paired.filter((host) => !host.accountUserId && host.id && !hosts.some((item) => item.hostId === host.id))) {
		for (const address of manual.endpoints) {
			try {
				const base = endpointBaseUrl(address);
				const identity = await fetch(`${base}/api/v1/identity`, { signal: AbortSignal.timeout(5_000) });
				const found = await identity.json() as { hostId?: string };
				if (!identity.ok || found.hostId !== manual.id) continue;
				const claim = await fetch(`${base}/api/v1/remote-host/account-token`, {
					method: "POST", headers: { Authorization: `Bearer ${manual.token}`, "X-AO-Expected-Host-ID": manual.id, "X-AO-Account-ID": account.id },
					signal: AbortSignal.timeout(5_000),
				});
				if (!claim.ok) continue;
				const issued = await claim.json() as { hostId?: string; token?: string };
				if (issued.hostId !== manual.id || !/^[0-9a-f]{64}$/.test(issued.token ?? "")) continue;
				const item = { hostId: manual.id, label: manual.name, url: base, token: issued.token! };
				const saved = await fetch(`${CONTROL_PLANE}/api/cloud/v1/me/hosts/${encodeURIComponent(manual.id)}`, {
					method: "PUT", headers: { Authorization: `Bearer ${access}`, "Content-Type": "application/json" },
					body: JSON.stringify({ label: item.label, url: item.url, token: item.token }),
					signal: AbortSignal.timeout(10_000),
				});
				if (saved.ok) hosts.push(item);
				break;
			} catch { /* This endpoint may be offline; another endpoint can work. */ }
		}
	}
	const visible = hosts.filter((item) => !ignored.has(item.hostId));
	const ids = new Set(visible.map((item) => item.hostId));
	for (const old of paired) {
		if (old.accountUserId && (old.accountUserId !== account.id || !ids.has(old.id))) await removeHost(old.id);
	}
	for (const item of visible) {
		const address = endpoint(item.url);
		if (!address || !/^h_[0-9a-f-]{36}$/.test(item.hostId) || !/^[0-9a-f]{64}$/.test(item.token)) continue;
		const existing = paired.find((host) => host.id === item.hostId);
		if (existing && !existing.accountUserId) {
			// Keep the original pairing password and private-network endpoints,
			// but accept the host's newly published quick-tunnel address.
			const endpoints = [address, ...existing.endpoints.filter((candidate) =>
				address.kind === "tunnel" ? candidate.kind !== "tunnel" : JSON.stringify(candidate) !== JSON.stringify(address))];
			if (JSON.stringify(endpoints) !== JSON.stringify(existing.endpoints)) await saveHost({ ...existing, endpoints });
			continue;
		}
		if (existing && existing.accountUserId === account.id && existing.name === item.label && existing.token === item.token &&
			existing.endpoints.length === 1 && JSON.stringify(existing.endpoints[0]) === JSON.stringify(address)) continue;
		await saveHost({
			id: item.hostId, name: item.label, platform: existing?.platform ?? "unknown",
			endpoints: [address], token: item.token,
			lastConnected: existing?.lastConnected ?? 0,
			accountUserId: account.id,
		} satisfies Host);
	}
}

export async function clearAccountHosts(): Promise<void> {
	await pendingSync;
	for (const host of await loadHosts()) if (host.accountUserId) await removeHost(host.id);
}
