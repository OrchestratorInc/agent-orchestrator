import type { ServerConfig } from "./config";
import { resolveManualAddress } from "./connectionFields";
import { isPairingSecret, type PairDeviceResult } from "./deviceToken";
import { normalizeServerHost, type Endpoint } from "./endpoints";
import type { Host } from "./hosts";

export type AdoptManualDeps = {
	/** The machine's reported host id, or "" when it does not report one. */
	identity: (cfg: ServerConfig) => Promise<string>;
	saveHost: (host: Host) => Promise<void>;
	setActiveHost: (id: string) => Promise<void>;
};

export type PersistManualHostEditDeps = {
	/** Public identity probe. This must run before ping presents the saved credential. */
	probe: (endpoint: Endpoint) => Promise<{ hostId: string }>;
	ping: (cfg: ServerConfig) => Promise<unknown>;
	saveHost: (host: Host) => Promise<void>;
	renameHost: (id: string, name: string) => Promise<void>;
	pair?: (cfg: ServerConfig) => Promise<PairDeviceResult>;
};

/** Account sync rewrites this machine's route and credential from the AO
 * Cloud directory, so a hand edit would silently revert within one sync. */
export class AccountManagedHostError extends Error {
	constructor() {
		super("This machine's address and password come from your account. Scan a new code with Reconnect to connect another way.");
		this.name = "AccountManagedHostError";
	}
}

/** A typed address answered, but it was not the paired machine being edited. */
export class DifferentMachineError extends Error {
	constructor() {
		super("That address belongs to another machine. This pairing was not changed.");
		this.name = "DifferentMachineError";
	}
}

/**
 * Turns a hand-entered address into a paired machine.
 *
 * ManualConnectSheet used to write only the legacy ServerConfig. That worked
 * when there was one server; with a host list it does not, because resolution
 * reconnects the *active machine* on the next launch and migration skips once
 * any host exists. The manual connection was therefore replaced by whatever was
 * there before, seconds after the user made it.
 *
 * Storing it and selecting it is the whole fix: the address the user typed is a
 * machine like any other, and the one they just chose to talk to.
 */
export async function adoptManualConnection(
	cfg: ServerConfig,
	deps: AdoptManualDeps,
	name: string,
	fallbackToken?: string,
): Promise<string> {
	const machineName = name.trim();
	if (!machineName) throw new Error("Machine name is required");
	const hostId = await deps.identity(cfg);
	const hostname = normalizeServerHost(cfg.host);
	await deps.saveHost({
		id: hostId,
		name: machineName,
		platform: "",
		endpoints: [manualEndpoint(cfg)],
		token: cfg.password,
		...(fallbackToken ? { fallbackToken } : {}),
		lastConnected: Date.now(),
	});
	// Even without an id: the selection is by list position, and leaving it
	// unset would resolve back to the previous machine on the next launch.
	await deps.setActiveHost(hostId);
	return hostId;
}

/** Replace one saved address and its password without dropping other discovered addresses. */
export function editedManualHost(host: Host, cfg: ServerConfig, name: string, endpointIndex: number): Host {
	const endpoint = manualEndpoint(cfg);
	const previous = host.endpoints[endpointIndex];
	if (previous) endpoint.kind = previous.kind;
	const endpoints = [...host.endpoints];
	endpoints[endpointIndex] = endpoint;
	return { ...host, name: name.trim(), token: cfg.password, endpoints, ...(cfg.password !== host.token ? { fallbackToken: undefined } : {}) };
}

/**
 * Persists the edit form shared by the connection sheet and the inline
 * Environments editor.
 *
 * Name-only edits stay local. A route or credential change first performs the
 * unauthenticated identity probe, then authenticates, and only then writes the
 * new pairing. That ordering prevents a saved bearer from being presented to a
 * different machine at a mistyped address.
 */
export async function persistManualHostEdit(
	host: Host,
	cfg: ServerConfig,
	name: string,
	endpointIndex: number,
	deps: PersistManualHostEditDeps,
): Promise<Host> {
	const machineName = name.trim();
	if (!machineName) throw new Error("Machine name is required");
	if (!normalizeServerHost(cfg.host)) throw new Error("Host is required");
	const address = resolveManualAddress(cfg.host, cfg.httpPort, cfg.secure);
	if (!address.ok) throw new Error(address.incomplete ? "API port must be between 1 and 65535" : address.error);
	const target = { ...cfg, host: address.host, httpPort: address.port };

	const edited = editedManualHost(host, target, machineName, endpointIndex);
	const before = host.endpoints[endpointIndex];
	const after = edited.endpoints[endpointIndex];
	const connectionChanged = !before || before.host !== after.host || before.port !== after.port ||
		before.secure !== after.secure || host.token !== target.password;
	if (connectionChanged && host.accountUserId) throw new AccountManagedHostError();
	if (connectionChanged) {
		const { hostId } = await deps.probe(after);
		if (host.id && hostId !== host.id) throw new DifferentMachineError();
		let credential = { ...target, hostId: host.id || hostId };
		let saved = edited;
		if (deps.pair && host.token !== target.password && isPairingSecret(target.password)) {
			const paired = await deps.pair(credential);
			if (paired.kind === "paired") {
				credential = { ...credential, password: paired.token };
				saved = { ...edited, token: paired.token, fallbackToken: paired.credential === "password" ? target.password : undefined };
			}
		}
		await deps.ping(credential);
		await deps.saveHost(saved);
		return saved;
	}
	if (edited.name !== host.name) {
		await deps.renameHost(host.id, edited.name);
	}
	return edited;
}

/**
 * A typed address is one endpoint, not a list — the daemon's own list replaces
 * this on the first successful connect. TLS by hand means the tailnet path;
 * everything else was plain LAN, which mirrors how migration reads the old
 * single-server config.
 */
function manualEndpoint(cfg: ServerConfig): Endpoint {
	const secure = cfg.secure === true;
	return {
		kind: secure ? "tailscale" : "lan",
		host: normalizeServerHost(cfg.host),
		port: Number(cfg.httpPort) || 3011,
		secure,
	};
}
