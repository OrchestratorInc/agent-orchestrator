import os from "node:os";
import path from "node:path";
import { IncompatibleRemoteVersionError, probeRemote, readRemoteIdentity, type RemoteHealth } from "./remote-request";
import { findRemote, removeSavedRemote, toHostViews, updateSavedRemote } from "./remotes-ipc";
import type { RemoteRegistry } from "./remote-registry";
import { addRemote, readRemotes, updateRemote, type RemoteChanges, type RemoteEntry } from "./remotes-store";

// An isolated desktop run must not read or write the real user's credentials.
// Without an override, keep the existing ~/.ao location.
export function remotesFilePath(): string {
	return path.join(process.env.AO_DATA_DIR?.trim() || path.join(os.homedir(), ".ao"), "remotes.json");
}

// The slice of Electron's ipcMain these handlers need, so tests need no Electron.
type IpcMainLike = {
	// eslint-disable-next-line @typescript-eslint/no-explicit-any -- the listener
	// args must unify Electron's IpcMain (any[]) with a test fake (unknown[]),
	// and `never[]` rejects both; `any[]` here leaks nowhere past registration.
	handle(channel: string, listener: (event: unknown, ...args: any[]) => Promise<unknown>): void;
};

export type RemotesIpcDeps = {
	file: string;
	registry: RemoteRegistry;
	requireAccount: () => Promise<void>;
	getAccountId: () => Promise<string>;
	localIdentity: () => Promise<string | null>;
	probe?: (entry: RemoteEntry) => Promise<RemoteHealth>;
	identity?: (entry: Pick<RemoteEntry, "url">) => Promise<string>;
};

class LocalIdentityUnavailableError extends Error {
	constructor() { super("Local AO identity is unavailable; remote connection is offline"); }
}

/**
 * Saved AO daemons, shared with the CLI's ~/.ao/remotes.json. Everything the
 * renderer receives back is password-free (see remotes-ipc.ts); the plaintext
 * password only travels renderer -> main when adding or editing a credential.
 */
export function registerRemotesIpc(
	ipcMain: IpcMainLike,
	{ file, registry, requireAccount, getAccountId, localIdentity, probe = probeRemote, identity = readRemoteIdentity }: RemotesIpcDeps,
): void {
	const disconnect = (url: string) => registry.disconnect(url);
	const refuseSelf = (hostId: string | undefined, localHostId: string | null) => {
		if (localHostId && hostId === localHostId) throw new Error("This AO installation is already available locally; a remote connection to itself is not allowed.");
	};
	const requireLocalIdentity = async () => {
		const hostId = await localIdentity();
		if (!hostId) throw new LocalIdentityUnavailableError();
		return hostId;
	};
	const checkConnectionContext = async (localHostId: string, account: string) => {
		const [currentLocalHostId, currentAccount] = await Promise.all([localIdentity(), accountID()]);
		if (currentAccount !== account) throw new Error("Account changed during remote connection.");
		if (!currentLocalHostId) throw new LocalIdentityUnavailableError();
		if (currentLocalHostId !== localHostId) throw new Error("Local AO identity changed; remote connection is offline");
	};
	const checkedProbe = async (entry: RemoteEntry, account: string): Promise<RemoteHealth> => {
		if (!entry.hostId) throw new Error("remote host must be paired again to record its identity");
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
		if (actual !== entry.hostId) throw new Error(`remote host identity changed for ${entry.url}; connection refused`);
		await checkConnectionContext(localHostId, account);
		const health = await probe(entry);
		await checkConnectionContext(localHostId, account);
		return health;
	};
	// Keep edits and connects ordered so an update cannot leave a stale proxy.
	// ponytail: one queue includes network probes; split by host if five-host setup is too slow.
	let pending: Promise<void> = Promise.resolve();
	const ordered = <T>(operation: () => Promise<T>): Promise<T> => {
		const result = pending.then(operation, operation);
		pending = result.then(() => undefined, () => undefined);
		return result;
	};
	const accountID = async () => {
		await requireAccount();
		const id = await getAccountId();
		if (!id) throw new Error("Sign in to AO Cloud to use remote hosts.");
		return id;
	};
	const owned = async () => {
		const account = await accountID();
		const entries = await readRemotes(file);
		if (!account) return entries;
		// Legacy rows stay local-only: show them without attributing them to the
		// signed-in account, while keeping other accounts isolated.
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
		// Probe before saving: a host that never answered is worse than no host,
		// because it looks configured.
		let hostId: string;
		try {
			hostId = await identity(input);
		} catch (error) {
			if (error instanceof IncompatibleRemoteVersionError) return "incompatible" as RemoteHealth;
			return "offline" as RemoteHealth;
		}
		refuseSelf(hostId, localHostId);
		const entry = { label: input.label, url: input.url, password: input.password, hostId, accountUserId: account };
		const health = await checkedProbe(entry, account);
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
		if (!(await owned()).some((entry) => entry.url === url)) throw new Error("Host does not belong to this account.");
		const { accountUserId: _ignored, ...editable } = changes;
		return updateSavedRemote(file, url, editable, disconnect, (entry) => checkedProbe(entry, account));
	}));
	ipcMain.handle("remotes:remove", async (_event, url: string) => ordered(async () => {
		if (!(await owned()).some((entry) => entry.url === url)) throw new Error("Host does not belong to this account.");
		return removeSavedRemote(file, url, disconnect);
	}));
	ipcMain.handle("remotes:connect", async (_event, url: string, hostId?: string) => ordered(async () => {
		const account = await accountID();
		if (!(await owned()).some((entry) => entry.url === url && (!hostId || entry.hostId === hostId))) throw new Error("Host does not belong to this account.");
		const entry = await findRemote(file, url, hostId);
		const localHostId = await localIdentity();
		refuseSelf(entry.hostId, localHostId);
		// A local restart must not retire an already verified, unchanged remote proxy.
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
		if (health !== "online") throw new Error(`host ${url} is ${health}`);
		return registry.connect(entry);
	}));
	ipcMain.handle("remotes:importAccountHost", async (_event, expectedAccountId: string, input: RemoteEntry) => ordered(async () => {
		const account = await accountID();
		if (!account || account !== expectedAccountId || !input.hostId || !/^[0-9a-f]{64}$/.test(input.password)) throw new Error("Invalid account host credential.");
		const local = (await readRemotes(file)).find((entry) => entry.hostId === input.hostId);
		// Keep the original password on the machine that performed pairing; a
		// Cloud refresh must not replace it with the narrower account token.
		if (local?.password && !/^[0-9a-f]{64}$/.test(local.password)) {
			if (local.label !== input.label || local.url !== input.url) await updateRemote(file, local.url, { label: input.label, url: input.url });
			return;
		}
		await addRemote(file, { ...input, accountUserId: account });
	}));
	ipcMain.handle("remotes:pruneAccountHosts", async (_event, expectedAccountId: string, currentHostIds: string[]) => ordered(async () => {
		if (await accountID() !== expectedAccountId) throw new Error("Account changed during host sync.");
		const current = new Set(currentHostIds);
		for (const entry of await owned()) {
			// Only account-imported credentials are directory-managed. A machine
			// paired here with its original password remains a local pairing.
			if (entry.hostId && !current.has(entry.hostId) && /^[0-9a-f]{64}$/.test(entry.password)) {
				await removeSavedRemote(file, entry.url, disconnect);
			}
		}
	}));
	ipcMain.handle("remotes:issueAccountToken", async (_event, url: string) => ordered(async () => {
		const account = await accountID();
		const entry = (await readRemotes(file)).find((saved) => saved.url === url && (!saved.accountUserId || saved.accountUserId === account));
		if (!entry?.hostId) throw new Error("Pair this host before linking it to your account.");
		const localHostId = await requireLocalIdentity();
		refuseSelf(entry.hostId, localHostId);
		const actual = await identity(entry);
		refuseSelf(actual, localHostId);
		if (actual !== entry.hostId) throw new Error("Remote host identity changed; connection refused.");
		await checkConnectionContext(localHostId, account);
		const endpoint = new URL("/api/v1/remote-host/account-token", entry.url);
		const response = await fetch(endpoint, {
			method: "POST", redirect: "error",
			headers: { Authorization: `Bearer ${entry.password}`, "X-AO-Expected-Host-ID": entry.hostId, "X-AO-Account-ID": account },
			signal: AbortSignal.timeout(8_000),
		});
		if (!response.ok) throw new Error("The host did not accept account linking. Check its password and version.");
		const result = await response.json() as { hostId?: string; token?: string };
		if (result.hostId !== entry.hostId || !/^[0-9a-f]{64}$/.test(result.token ?? "")) throw new Error("The host returned an invalid account credential.");
		return result.token;
	}));
	ipcMain.handle("remotes:disconnect", async (_event, url: string) => ordered(() => disconnect(url)));
	ipcMain.handle("remotes:previewUrl", async (_event, hostId: string, sessionId: string, sourceUrl: string) => {
		const account = await accountID();
		refuseSelf(hostId, await localIdentity());
		if (await accountID() !== account) throw new Error("Account changed during remote connection.");
		return registry.previewUrl(hostId, sessionId, sourceUrl);
	});
	ipcMain.handle("remotes:resolvePreviewUrl", async (_event, hostId: string, sessionId: string, viewedUrl: string) => {
		const account = await accountID();
		refuseSelf(hostId, await localIdentity());
		if (await accountID() !== account) throw new Error("Account changed during remote connection.");
		return registry.resolvePreviewUrl(hostId, sessionId, viewedUrl);
	});
}
