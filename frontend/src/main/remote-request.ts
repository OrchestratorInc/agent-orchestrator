import { parseDaemonProbe } from "../shared/daemon-attach";
import { DEVICE_TOKEN_PREFIX, type RemoteEntry } from "./remotes-store";

export type RemoteHealth = "online" | "unauthorized" | "offline" | "not-a-daemon" | "incompatible";

export class IncompatibleRemoteVersionError extends Error {
	constructor() {
		super("Orchestrator versions are incompatible. Update Orchestrator on this computer and the remote host.");
		this.name = "IncompatibleRemoteVersionError";
	}
}

type FetchImpl = typeof fetch;

export function createLocalHostIdentity(
	attachment: () => { port: number; pid?: number; generation: number } | null,
	identity = readRemoteIdentity,
): () => Promise<string | null> {
	let cached: { key: string; hostId: string } | undefined;
	const key = (value: NonNullable<ReturnType<typeof attachment>>) => `${value.generation}:${value.port}:${value.pid ?? ""}`;
	return async () => {
		const current = attachment();
		if (!current) { cached = undefined; return null; }
		const currentKey = key(current);
		if (cached?.key === currentKey) return cached.hostId;
		cached = undefined;
		try {
			const hostId = await identity({ url: `http://127.0.0.1:${current.port}` });
			const confirmed = attachment();
			if (!confirmed || key(confirmed) !== currentKey) return null;
			cached = { key: currentKey, hostId };
			return hostId;
		} catch { return null; }
	};
}

export async function readRemoteIdentity(
	entry: Pick<RemoteEntry, "url">,
	fetchImpl: FetchImpl = fetch,
	signal: AbortSignal = AbortSignal.timeout(5_000),
): Promise<string> {
	const base = (entry.url.includes("://") ? entry.url : `http://${entry.url}`).replace(/\/+$/, "");
	const url = new URL(`${base}/api/v1/identity`);
	if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
		throw new Error("remote host must use an HTTP(S) URL without embedded credentials");
	const response = await fetchImpl(url.href, { method: "GET", redirect: "error", signal });
	if (!response.ok) throw new Error(`remote identity probe returned ${response.status}`);
	const body = (await response.json()) as { hostId?: unknown; apiVersion?: unknown };
	if (typeof body.hostId !== "string" || body.hostId === "") throw new Error("remote identity probe returned no host ID");
	if (body.hostId === "local") throw new Error("remote identity probe returned reserved local host ID");
	if (body.apiVersion !== 1) throw new IncompatibleRemoteVersionError();
	return body.hostId;
}

export async function probeRemote(
	entry: RemoteEntry,
	fetchImpl: FetchImpl = fetch,
	timeoutMs = 5_000,
): Promise<RemoteHealth> {
	try {
		const base = (entry.url.includes("://") ? entry.url : `http://${entry.url}`).replace(/\/+$/, "");
		const url = new URL(`${base}/healthz`);
		if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
			throw new Error("remote host must use an HTTP(S) URL without embedded credentials");
		const response = await fetchImpl(url.href, {
			method: "GET",
			redirect: "error",
			headers: {
				Authorization: `Bearer ${entry.password}`,
				...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}),
			},
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (response.status === 401 || response.status === 403) return "unauthorized";
		if (!response.ok) return "offline";
		const text = await response.text();
		let body: unknown;
		try { body = JSON.parse(text); }
		catch { return "not-a-daemon"; }
		return parseDaemonProbe("healthz", body) === null ? "not-a-daemon" : "online";
	} catch {
		return "offline";
	}
}

function remoteUrl(entry: Pick<RemoteEntry, "url">, path: string): URL {
	const base = (entry.url.includes("://") ? entry.url : `http://${entry.url}`).replace(/\/+$/, "");
	const url = new URL(`${base}${path}`);
	if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
		throw new Error("remote host must use an HTTP(S) URL without embedded credentials");
	return url;
}

export type PairDeviceResult =
	| { kind: "paired"; token: string; credential: "pairing-code" | "password" }
	| { kind: "unsupported" }
	| { kind: "unauthorized" }
	| { kind: "offline" };

export async function pairDevice(
	entry: Pick<RemoteEntry, "url" | "hostId">,
	secret: string,
	device: { name: string; platform: string },
	fetchImpl: FetchImpl = fetch,
	timeoutMs = 8_000,
): Promise<PairDeviceResult> {
	try {
		const response = await fetchImpl(remoteUrl(entry, "/api/v1/remote-host/devices").href, {
			method: "POST",
			redirect: "error",
			headers: {
				Authorization: `Bearer ${secret}`,
				"Content-Type": "application/json",
				...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}),
			},
			body: JSON.stringify(device),
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (response.status === 401) return { kind: "unauthorized" };
		if ([403, 404, 405, 409, 501].includes(response.status)) return { kind: "unsupported" };
		if (!response.ok) return { kind: "offline" };
		const body = (await response.json()) as { hostId?: unknown; token?: unknown; credential?: unknown };
		if (typeof body.token !== "string" || !body.token.startsWith(DEVICE_TOKEN_PREFIX) || (entry.hostId && body.hostId !== entry.hostId)) return { kind: "offline" };
		return { kind: "paired", token: body.token, credential: body.credential === "pairing-code" ? "pairing-code" : "password" };
	} catch {
		return { kind: "offline" };
	}
}

export async function readAuthErrorCode(entry: RemoteEntry, fetchImpl: FetchImpl = fetch, timeoutMs = 5_000): Promise<string | undefined> {
	try {
		const response = await fetchImpl(remoteUrl(entry, "/healthz").href, {
			method: "GET",
			redirect: "error",
			headers: { Authorization: `Bearer ${entry.password}`, ...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}) },
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (response.status !== 401) return undefined;
		const body = (await response.json()) as { code?: unknown };
		return typeof body.code === "string" ? body.code : undefined;
	} catch {
		return undefined;
	}
}
