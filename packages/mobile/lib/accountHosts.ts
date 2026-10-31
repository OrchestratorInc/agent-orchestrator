import { getAccountAccessToken, loadAccount, type Account } from "./account";
import { ignoredAccountHosts, mutateAccountHosts } from "./accountHostPreferences";
import { isTailscaleHost } from "./connectionError";
import { forgetPairedHost } from "./disconnect";
import { endpointBaseUrl, type Endpoint } from "./endpoints";
import { loadHosts, saveHost, type Host } from "./hosts";
import { isQuickTunnelHost } from "./staleTunnel";

export { ignoreAccountHost, unignoreAccountHost } from "./accountHostPreferences";

const CONTROL_PLANE = (process.env.EXPO_PUBLIC_AO_CLOUD_API_BASE_URL || "https://api.aoagents.dev").replace(/\/$/, "");
type AccountHost = { hostId: string; label: string; url: string; token: string };

// Tunnels are a quick tunnel's name or AO Cloud's fixed address,
// h-<20 hex>.<zone>, over https. Anything else, a custom domain included, is
// kept beside the pairing's tunnel rather than in its place.
function isTunnelAddress(url: URL): boolean {
	const name = url.hostname.toLowerCase();
	return url.protocol === "https:" && (isQuickTunnelHost(name) || /^h-[0-9a-f]{20}\.[^.]+\.[^.]+/.test(name));
}

function endpoint(raw: string): Endpoint | null {
	try {
		const url = new URL(raw);
		if ((url.protocol !== "https:" && url.protocol !== "http:") || !url.hostname || url.username || url.password) return null;
		return {
			kind: url.hostname.endsWith(".ts.net") || isTailscaleHost(url.hostname) ? "tailscale" : isTunnelAddress(url) ? "tunnel" : "lan",
			host: url.hostname,
			port: Number(url.port || (url.protocol === "https:" ? 443 : 80)),
			secure: url.protocol === "https:",
		};
	} catch { return null; }
}

let syncGeneration = 0;
let pendingSync: { accountId: string; controller: AbortController; promise: Promise<void> } | undefined;

export function cancelAccountHostSync(): void {
	syncGeneration++;
	pendingSync?.controller.abort();
	pendingSync = undefined;
}

/** Import only account-owned machines; never replace a manual pairing token. */
export function syncAccountHosts(account: Account): Promise<void> {
	if (pendingSync?.accountId === account.id) return pendingSync.promise;
	cancelAccountHostSync();
	const generation = syncGeneration;
	const controller = new AbortController();
	const current = async () => (await loadAccount())?.id === account.id && generation === syncGeneration;
	const promise = (async () => {
		if (await current()) await performSync(account, controller.signal, current);
	})().finally(() => {
		if (pendingSync?.promise === promise) pendingSync = undefined;
	});
	pendingSync = { accountId: account.id, controller, promise };
	return promise;
}

async function syncFetch(url: string, init: RequestInit, timeoutMs: number, signal: AbortSignal): Promise<{ ok: boolean; status: number; body: unknown }> {
	const controller = new AbortController();
	const abort = () => controller.abort();
	signal.addEventListener("abort", abort);
	if (signal.aborted) controller.abort();
	const timer = setTimeout(abort, timeoutMs);
	try {
		const response = await fetch(url, { ...init, signal: controller.signal });
		// Keep cancellation and the timeout alive until the body finishes too.
		const body: unknown = response.ok && init.method !== "PUT" ? await response.json() : undefined;
		return { ok: response.ok, status: response.status, body };
	}
	finally { clearTimeout(timer); signal.removeEventListener("abort", abort); }
}

async function performSync(account: Account, signal: AbortSignal, current: () => Promise<boolean>): Promise<void> {
	const access = await getAccountAccessToken();
	if (!access || !await current()) return;
	const response = await syncFetch(`${CONTROL_PLANE}/api/cloud/v1/me/hosts`, {
		headers: { Authorization: `Bearer ${access}`, Accept: "application/json" },
	}, 10_000, signal);
	if (!response.ok) throw new Error(`Orchestrator Cloud host sync failed (${response.status}).`);
	const { hosts, hostKeys } = response.body as { hosts: AccountHost[]; hostKeys?: boolean };
	if (!await current()) return;
	const paired = await loadHosts();
	for (const manual of paired.filter((host) => !host.accountUserId && host.id && !hosts.some((item) => item.hostId === host.id))) {
		for (const address of manual.endpoints) {
			if (!await current()) return;
			if ((await ignoredAccountHosts(account.id)).has(manual.id)) break;
			try {
				const base = endpointBaseUrl(address);
				const identity = await syncFetch(`${base}/api/v1/identity`, {}, 5_000, signal);
				if (!identity.ok) continue;
				const found = identity.body as { hostId?: string };
				if (found.hostId !== manual.id) continue;
				if (!await current() || (await ignoredAccountHosts(account.id)).has(manual.id)) break;
				const claim = await syncFetch(`${base}/api/v1/remote-host/account-token`, {
					method: "POST", headers: { Authorization: `Bearer ${manual.token}`, "X-AO-Expected-Host-ID": manual.id, "X-AO-Account-ID": account.id },
				}, 5_000, signal);
				if (!claim.ok) continue;
				const issued = claim.body as { hostId?: string; token?: string; hostKey?: string };
				if (issued.hostId !== manual.id || !/^[0-9a-f]{64}$/.test(issued.token ?? "")) continue;
				const item = { hostId: manual.id, label: manual.name, url: base, token: issued.token! };
				if (!await current() || (await ignoredAccountHosts(account.id)).has(manual.id)) break;
				const saved = await syncFetch(`${CONTROL_PLANE}/api/cloud/v1/me/hosts/${encodeURIComponent(manual.id)}`, {
					method: "PUT", headers: { Authorization: `Bearer ${access}`, "Content-Type": "application/json" },
					// The machine's host key, only where AO Cloud says it takes one: it then needs the machine's own signature.
					body: JSON.stringify({ label: item.label, url: item.url, token: item.token, hostKey: hostKeys === true ? issued.hostKey : undefined }),
				}, 10_000, signal);
				if (saved.ok) hosts.push(item);
				break;
			} catch { /* This endpoint may be offline; another endpoint can work. */ }
		}
	}
	if (!await current()) return;
	const ignored = await ignoredAccountHosts(account.id);
	const visible = hosts.filter((item) => !ignored.has(item.hostId));
	const ids = new Set(visible.map((item) => item.hostId));
	for (const old of await loadHosts()) {
		if (!await current()) return;
		if (old.accountUserId && (old.accountUserId !== account.id || !ids.has(old.id))) await forgetPairedHost(old, { ignoreAccount: false });
	}
	for (const item of visible) {
		await mutateAccountHosts(async () => {
			if (!await current() || (await ignoredAccountHosts(account.id)).has(item.hostId)) return;
			const address = endpoint(item.url);
			if (!address || !/^h_[0-9a-f-]{36}$/.test(item.hostId) || !/^[0-9a-f]{64}$/.test(item.token)) return;
			const existing = (await loadHosts()).find((host) => host.id === item.hostId);
			if (!await current()) return;
			if (existing && !existing.accountUserId) {
				// Keep the original pairing password and private-network endpoints,
				// but accept the host's newly published quick-tunnel address. A
				// fixed address goes beside the pairing's tunnel, not in its place:
				// a listing the machine no longer updates, after a new password or
				// a link to another account, keeps a fixed address nothing serves.
				const endpoints = [address, ...existing.endpoints.filter((candidate) =>
					address.kind === "tunnel" && isQuickTunnelHost(address.host) ? candidate.kind !== "tunnel" : JSON.stringify(candidate) !== JSON.stringify(address))];
				if (JSON.stringify(endpoints) !== JSON.stringify(existing.endpoints)) await saveHost({ ...existing, endpoints });
				return;
			}
			if (existing && existing.accountUserId === account.id && existing.token === item.token &&
				existing.endpoints.length === 1 && JSON.stringify(existing.endpoints[0]) === JSON.stringify(address)) return;
			await saveHost({
				// Directory labels seed new machines; a phone's chosen name and icon
				// remain local preferences when remote credentials/routes rotate.
				id: item.hostId, name: existing?.name ?? item.label, platform: existing?.platform ?? "unknown",
				appearance: existing?.appearance,
				endpoints: [address], token: item.token,
				lastConnected: existing?.lastConnected ?? 0,
				accountUserId: account.id,
			} satisfies Host);
		});
	}
}

export async function clearAccountHosts(): Promise<void> {
	cancelAccountHostSync();
	await mutateAccountHosts(async () => {});
	for (const host of await loadHosts()) if (host.accountUserId) await forgetPairedHost(host, { ignoreAccount: false });
}
