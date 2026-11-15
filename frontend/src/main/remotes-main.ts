import { execFile } from "node:child_process";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { IncompatibleRemoteVersionError, pairDevice, probeRemote, readAuthErrorCode, readRemoteIdentity, type PairDeviceResult, type RemoteHealth } from "./remote-request";
import { findRemote, removeSavedRemote, toHostViews, updateSavedRemote } from "./remotes-ipc";
import type { RemoteRegistry } from "./remote-registry";
import { ACCOUNT_TOKEN, DEVICE_TOKEN_PREFIX, addRemote, holdsAccountToken, holdsPairingPassword, readRemotes, updateRemote, type RemoteChanges, type RemoteEntry } from "./remotes-store";
import { ExpectedIpcError } from "./main-error-capture";

export function remotesFilePath(): string {
	return path.join(process.env.AO_DATA_DIR?.trim() || path.join(os.homedir(), ".ao"), "remotes.json");
}

type RunFile = (file: string, args: string[], options: { timeout: number }) => Promise<{ stdout: string }>;

export async function computerName(
	run: RunFile = promisify(execFile),
	platform: NodeJS.Platform = process.platform,
	hostname: () => string = os.hostname,
): Promise<string> {
	if (platform === "darwin") {
		try {
			const name = (await run("/usr/sbin/scutil", ["--get", "ComputerName"], { timeout: 2_000 })).stdout.trim();
			if (name) return name;
		} catch {
		}
	}
	return hostname().replace(/\.local$/i, "");
}

type IpcMainLike = {
	// eslint-disable-next-line @typescript-eslint/no-explicit-any -- the listener
	handle(channel: string, listener: (event: unknown, ...args: any[]) => Promise<unknown>): void;
};

export type RemotesIpcDeps = {
	file: string;
	registry: RemoteRegistry;
	requireAccount: () => Promise<void>;
	getAccountId: () => Promise<string>;
	localIdentity: () => Promise<string | null>;
	cloudRequest: (method: string, path: string, options?: { body?: unknown; signal?: AbortSignal }) => Promise<{ status: number; body: string }>;
	accountDirectoryTimeoutMs?: number;
	probe?: (entry: RemoteEntry) => Promise<RemoteHealth>;
	identity?: (entry: Pick<RemoteEntry, "url">) => Promise<string>;
	pair?: (entry: Pick<RemoteEntry, "url" | "hostId">, secret: string, device: { name: string; platform: string }) => Promise<PairDeviceResult>;
	authErrorCode?: (entry: RemoteEntry) => Promise<string | undefined>;
	deviceName?: () => Promise<string>;
};

class LocalIdentityUnavailableError extends ExpectedIpcError {
	constructor() { super("Local Orchestrator identity is unavailable; remote connection is offline"); }
}

type AccountHost = { hostId: string; label: string; url: string; token: string };

export function registerRemotesIpc(
	ipcMain: IpcMainLike,
	{
		file, registry, requireAccount, getAccountId, localIdentity, cloudRequest, accountDirectoryTimeoutMs = 10_000,
		probe = probeRemote, identity = readRemoteIdentity, pair = pairDevice, authErrorCode = readAuthErrorCode, deviceName = computerName,
	}: RemotesIpcDeps,
): void {
	const disconnect = (url: string) => registry.disconnect(url);
	const refuseSelf = (hostId: string | undefined, localHostId: string | null) => {
		if (localHostId && hostId === localHostId) throw new ExpectedIpcError("This Orchestrator installation is already available locally; a remote connection to itself is not allowed.");
	};
	const requireLocalIdentity = async () => {
		const hostId = await localIdentity();
		if (!hostId) throw new LocalIdentityUnavailableError();
		return hostId;
	};
	const checkConnectionContext = async (localHostId: string, account: string) => {
		const [currentLocalHostId, currentAccount] = await Promise.all([localIdentity(), accountID()]);
		if (currentAccount !== account) throw new ExpectedIpcError("Account changed during remote connection.");
		if (!currentLocalHostId) throw new LocalIdentityUnavailableError();
		if (currentLocalHostId !== localHostId) throw new ExpectedIpcError("Local Orchestrator identity changed; remote connection is offline");
	};
	const checkedProbe = async (entry: RemoteEntry, account: string): Promise<RemoteHealth> => {
		if (!entry.hostId) throw new ExpectedIpcError("remote host must be paired again to record its identity");
		const localHostId = await requireLocalIdentity();
		refuseSelf(entry.hostId, localHostId);
		let actual: string;
		try {
			actual = await identity(entry);
		} catch (error) {
			if (error instanceof IncompatibleRemoteVersionError) return "incompatible";
			return "offline";
		}
		refuseSelf(actual, localHostId);
		if (actual !== entry.hostId) throw new ExpectedIpcError(`remote host identity changed for ${entry.url}; connection refused`);
		await checkConnectionContext(localHostId, account);
		const health = await probe(entry);
		await checkConnectionContext(localHostId, account);
		return health;
	};
	const askAgainAt = new Map<string, number>();
	const ASK_AGAIN_MS = 60 * 60_000;
	const pairOwnToken = async (entry: RemoteEntry): Promise<{ entry: RemoteEntry; result: PairDeviceResult["kind"] }> => {
		const result = await pair(entry, entry.password, { name: await deviceName(), platform: process.platform });
		if (result.kind === "unsupported" && entry.hostId) askAgainAt.set(entry.hostId, Date.now() + ASK_AGAIN_MS);
		if (result.kind !== "paired") return { entry, result: result.kind };
		const { sealedPassword: _sealed, accountToken: _account, ...kept } = entry;
		return {
			entry: { ...kept, password: result.token, fallbackPassword: result.credential === "password" ? entry.password : undefined },
			result: "paired",
		};
	};
	const unsaved = new Map<string, { typed: string } & Pick<RemoteEntry, "password" | "fallbackPassword">>();
	const unsavedKey = (account: string, hostId: string) => `${account}\n${hostId}`;
	const pairOrReuse = async (account: string, typed: RemoteEntry) => {
		const kept = typed.hostId ? unsaved.get(unsavedKey(account, typed.hostId)) : undefined;
		if (kept?.typed !== typed.password) return pairOwnToken(typed);
		return { entry: { ...typed, password: kept.password, fallbackPassword: kept.fallbackPassword }, result: "paired" as const };
	};
	const remember = (account: string, typed: RemoteEntry, paired: Awaited<ReturnType<typeof pairOwnToken>>, health: RemoteHealth) => {
		if (!typed.hostId) return;
		if (paired.result === "paired" && health !== "online" && health !== "unauthorized") {
			unsaved.set(unsavedKey(account, typed.hostId), { typed: typed.password, password: paired.entry.password, fallbackPassword: paired.entry.fallbackPassword });
		} else {
			unsaved.delete(unsavedKey(account, typed.hostId));
		}
	};
	let pending: Promise<void> = Promise.resolve();
	const ordered = <T>(operation: () => Promise<T>): Promise<T> => {
		const result = pending.then(operation, operation);
		pending = result.then(() => undefined, () => undefined);
		return result;
	};
	const accountID = async () => {
		await requireAccount();
		const id = await getAccountId();
		if (!id) throw new ExpectedIpcError("Sign in to Orchestrator Cloud to use remote hosts.");
		return id;
	};
	const owned = async () => {
		const account = await accountID();
		const entries = await readRemotes(file);
		if (!account) return entries;
		return entries.filter((item) => !item.accountUserId || item.accountUserId === account);
	};

	ipcMain.handle("remotes:list", () => ordered(async () => {
		await accountID();
		const localHostId = await localIdentity();
		const entries = await owned();
		return toHostViews(entries.filter((entry) => !localHostId || entry.hostId !== localHostId));
	}));
	ipcMain.handle("remotes:add", async (_event, input: RemoteEntry) => ordered(async () => {
		const account = await accountID();
		const localHostId = await requireLocalIdentity();
		refuseSelf(input.hostId, localHostId);
		let hostId: string;
		try {
			hostId = await identity(input);
		} catch (error) {
			if (error instanceof IncompatibleRemoteVersionError) return "incompatible" as RemoteHealth;
			return "offline" as RemoteHealth;
		}
		refuseSelf(hostId, localHostId);
		const typed: RemoteEntry = { label: input.label, url: input.url, password: input.password, hostId, accountUserId: account };
		await checkConnectionContext(localHostId, account);
		const paired = await pairOrReuse(account, typed);
		if (paired.result === "unauthorized") return "unauthorized" as RemoteHealth;
		const entry = paired.entry;
		const health = await checkedProbe(entry, account);
		remember(account, typed, paired, health);
		if (health === "online") {
			const previous = (await owned()).find((saved) => saved.hostId === hostId);
			await addRemote(file, entry);
			if (previous && previous.url !== entry.url) await disconnect(previous.url);
			await disconnect(entry.url);
		}
		return health;
	}));
	ipcMain.handle("remotes:update", async (_event, url: string, changes: RemoteChanges) => ordered(async () => {
		const account = await accountID();
		if (!(await owned()).some((entry) => entry.url === url)) throw new ExpectedIpcError("Host does not belong to this account.");
		const editable: RemoteChanges = { label: changes.label, url: changes.url, password: changes.password };
		const current = (await readRemotes(file)).find((entry) => entry.url === url);
		if (editable.password !== undefined && current) {
			const typed: RemoteEntry = { ...current, url: editable.url ?? url, password: editable.password, fallbackPassword: undefined, sealedFallbackPassword: undefined };
			const localHostId = await requireLocalIdentity();
			refuseSelf(typed.hostId, localHostId);
			let actual: string;
			try {
				actual = await identity(typed);
			} catch (error) {
				return error instanceof IncompatibleRemoteVersionError ? "incompatible" : "offline";
			}
			refuseSelf(actual, localHostId);
			if (typed.hostId && actual !== typed.hostId) throw new ExpectedIpcError(`remote host identity changed for ${typed.url}; connection refused`);
			await checkConnectionContext(localHostId, account);
			const paired = await pairOrReuse(account, typed);
			if (paired.result === "unauthorized") return "unauthorized";
			editable.password = paired.entry.password;
			editable.fallbackPassword = paired.entry.fallbackPassword;
			const health = await updateSavedRemote(file, url, editable, disconnect, (entry) => checkedProbe(entry, account));
			remember(account, typed, paired, health);
			return health;
		}
		return updateSavedRemote(file, url, editable, disconnect, (entry) => checkedProbe(entry, account));
	}));
	ipcMain.handle("remotes:remove", async (_event, url: string) => ordered(async () => {
		if (!(await owned()).some((entry) => entry.url === url)) throw new ExpectedIpcError("Host does not belong to this account.");
		return removeSavedRemote(file, url, disconnect);
	}));
	ipcMain.handle("remotes:connect", async (_event, url: string, hostId?: string) => ordered(async () => {
		const account = await accountID();
		if (!(await owned()).some((entry) => entry.url === url && (!hostId || entry.hostId === hostId))) throw new ExpectedIpcError("Host does not belong to this account.");
		const entry = await findRemote(file, url, hostId);
		const localHostId = await localIdentity();
		refuseSelf(entry.hostId, localHostId);
		if (!localHostId) {
			const connected = registry.connected(entry);
			if (connected && await accountID() === account) return connected;
			throw new LocalIdentityUnavailableError();
		}
		let health: RemoteHealth;
		try { health = await checkedProbe(entry, account); }
		catch (error) {
			const connected = registry.connected(entry);
			if (error instanceof LocalIdentityUnavailableError && connected && await accountID() === account) return connected;
			throw error;
		}
		let current = entry;
		if (health === "unauthorized" && current.fallbackPassword && current.password.startsWith(DEVICE_TOKEN_PREFIX)
			&& await authErrorCode(current) === "BAD_PASSWORD") {
			const previous: RemoteEntry = { ...current, password: current.fallbackPassword, fallbackPassword: undefined };
			if (await checkedProbe(previous, account) === "online") {
				current = await updateRemote(file, url, { password: previous.password });
				health = "online";
			}
		}
		if (health !== "online") throw new ExpectedIpcError(`host ${url} is ${health}`);
		if (holdsPairingPassword(current) && !registry.connected(current) && Date.now() >= (askAgainAt.get(current.hostId ?? "") ?? 0)) {
			const paired = await pairOwnToken(current);
			if (paired.result === "paired") {
				current = await updateRemote(file, url, { password: paired.entry.password, fallbackPassword: paired.entry.fallbackPassword });
			}
		}
		return registry.connect(current);
	}));
	const accountHosts = async <T>(method: string, path: string, options: { body?: unknown; timeoutMs?: number } = {}): Promise<T> => {
		const signal = options.timeoutMs === undefined ? undefined : AbortSignal.timeout(options.timeoutMs);
		let response: { status: number; body: string };
		try {
			const pending = cloudRequest(method, `/api/cloud/v1/me/hosts${path}`, { body: options.body, signal });
			response = signal
				? await Promise.race([pending, new Promise<never>((_, reject) => signal.addEventListener("abort", () => reject(signal.reason), { once: true }))])
				: await pending;
		} catch (error) {
			if (error instanceof ExpectedIpcError) throw error;
			throw new ExpectedIpcError("Orchestrator Cloud did not answer the host sync.");
		}
		if (response.status >= 300) throw new ExpectedIpcError(`Orchestrator Cloud host sync failed (${response.status}).`);
		return (response.body ? JSON.parse(response.body) : {}) as T;
	};
	const importAccountHost = async (account: string, host: AccountHost) => {
		if (!host.hostId || !ACCOUNT_TOKEN.test(host.token ?? "")) throw new ExpectedIpcError("Invalid account host credential.");
		const local = (await readRemotes(file)).find((entry) => entry.hostId === host.hostId);
		if (local && (local.sealedPassword || (local.password && !holdsAccountToken(local)))) {
			if (local.label !== host.label || local.url !== host.url) await updateRemote(file, local.url, { label: host.label, url: host.url });
			return;
		}
		await addRemote(file, { hostId: host.hostId, label: host.label, url: host.url, password: host.token, accountUserId: account });
	};
	let startedSyncs = 0;
	let appliedSync = 0;
	ipcMain.handle("remotes:syncAccountHosts", async (_event, expectedAccountId: string) => {
		if (await accountID() !== expectedAccountId) throw new ExpectedIpcError("Account changed during host sync.");
		const sync = ++startedSyncs;
		const { hosts = [] } = await accountHosts<{ hosts?: AccountHost[] }>("GET", "", { timeoutMs: accountDirectoryTimeoutMs });
		await ordered(async () => {
			if (sync < appliedSync) return;
			const account = await accountID();
			if (account !== expectedAccountId) throw new ExpectedIpcError("Account changed during host sync.");
			appliedSync = sync;
			for (const host of hosts) await importAccountHost(account, host);
			const current = new Set(hosts.map((host) => host.hostId));
			for (const entry of await owned()) {
				if (entry.hostId && !current.has(entry.hostId) && holdsAccountToken(entry)) {
					await removeSavedRemote(file, entry.url, disconnect);
				}
			}
		});
	});
	ipcMain.handle("remotes:linkAccountHost", async (_event, url: string) => {
		await accountID();
		const { hostKeys } = await accountHosts<{ hostKeys?: boolean }>("GET", "", { timeoutMs: accountDirectoryTimeoutMs });
		const issued = await ordered(async () => {
			const account = await accountID();
			const entry = (await readRemotes(file)).find((saved) => saved.url === url && (!saved.accountUserId || saved.accountUserId === account));
			if (!entry?.hostId) throw new ExpectedIpcError("Pair this host before linking it to your account.");
			const localHostId = await requireLocalIdentity();
			refuseSelf(entry.hostId, localHostId);
			const actual = await identity(entry);
			refuseSelf(actual, localHostId);
			if (actual !== entry.hostId) throw new ExpectedIpcError("Remote host identity changed; connection refused.");
			await checkConnectionContext(localHostId, account);
			const endpoint = new URL("/api/v1/remote-host/account-token", entry.url);
			const response = await fetch(endpoint, {
				method: "POST", redirect: "error",
				headers: { Authorization: `Bearer ${entry.password}`, "X-AO-Expected-Host-ID": entry.hostId, "X-AO-Account-ID": account },
				signal: AbortSignal.timeout(8_000),
			});
			if (!response.ok) throw new ExpectedIpcError("The host did not accept account linking. Check its password and version.");
			const result = await response.json() as { hostId?: string; token?: string; hostKey?: string };
			if (result.hostId !== entry.hostId || !ACCOUNT_TOKEN.test(result.token ?? "")) throw new ExpectedIpcError("The host returned an invalid account credential.");
			const hostKey = hostKeys === true && /^[A-Za-z0-9_-]{43}$/.test(result.hostKey ?? "") ? result.hostKey : undefined;
			return { account, hostId: entry.hostId, registration: { label: entry.label, url: entry.url, token: result.token, hostKey } };
		});
		if (await accountID() !== issued.account) throw new ExpectedIpcError("Account changed during remote connection.");
		await accountHosts("PUT", `/${encodeURIComponent(issued.hostId)}`, { body: issued.registration });
	});
	ipcMain.handle("remotes:disconnect", async (_event, url: string) => ordered(() => disconnect(url)));
	ipcMain.handle("remotes:computerName", () => computerName());
	ipcMain.handle("remotes:previewUrl", async (_event, hostId: string, sessionId: string, sourceUrl: string) => {
		const account = await accountID();
		refuseSelf(hostId, await localIdentity());
		if (await accountID() !== account) throw new ExpectedIpcError("Account changed during remote connection.");
		return registry.previewUrl(hostId, sessionId, sourceUrl);
	});
	ipcMain.handle("remotes:agentBrowserUrl", async (_event, hostId: string, sessionId: string) => {
		await requireAccount();
		return registry.agentBrowserUrl(hostId, sessionId);
	});
	ipcMain.handle("remotes:resolvePreviewUrl", async (_event, hostId: string, sessionId: string, viewedUrl: string) => {
		const account = await accountID();
		refuseSelf(hostId, await localIdentity());
		if (await accountID() !== account) throw new ExpectedIpcError("Account changed during remote connection.");
		return registry.resolvePreviewUrl(hostId, sessionId, viewedUrl);
	});
	ipcMain.handle("remotes:externalUrl", async (_event, hostId: string, url: string) => {
		const account = await accountID();
		refuseSelf(hostId, await localIdentity());
		if (await accountID() !== account) throw new ExpectedIpcError("Account changed during remote connection.");
		return registry.externalUrl(hostId, url);
	});
}
