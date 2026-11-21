import { randomBytes } from "node:crypto";
import { readFile } from "node:fs/promises";
import { connect, createServer, type AddressInfo, type Server, type Socket } from "node:net";
import type { Duplex } from "node:stream";
import ipaddr from "ipaddr.js";
import { LoopbackTunnelError } from "./remote-proxy";
import { readRequest, refuse, reply, REPLY_HOST_UNREACHABLE, REPLY_NOT_ALLOWED, REPLY_OK, SocketReader } from "./render-check-proxy";

export type PortForward = { hostId: string; remotePort: number; localPort: number };

export type LoopbackReach = "same" | "tunnel" | "off";

type OpenTunnel = (hostId: string, port: number) => Promise<Duplex>;

export type RoutedSession = {
	setProxy(config: Electron.ProxyConfig): Promise<void>;
	closeAllConnections(): Promise<void>;
	webRequest: {
		onBeforeRequest(
			filter: Electron.WebRequestFilter,
			listener: (details: { url: string }, callback: (response: Electron.CallbackResponse) => void) => void,
		): void;
	};
};

const MAX_FORWARD_CONNECTIONS = 64;
const PROBE_TIMEOUT_MS = 3_000;
const BYPASS_RULES = "*,<-loopback>,169.254.0.0/16,fe80::/10,ao-preview-*.localhost";
const UNSPECIFIED_URLS = ["*://0.0.0.0/*", "*://[::]/*"];
const PREVIEW_HOST = /^ao-preview-.*\.localhost\.?$/;

class SameHostError extends Error {}

export function isLoopbackName(host: string): boolean {
	const name = host.toLowerCase().replace(/^\[|\]$/g, "").replace(/\.$/, "");
	if (name === "localhost" || name.endsWith(".localhost")) return true;
	if (!ipaddr.isValid(name)) return false;
	const range = ipaddr.process(name).range();
	return range === "loopback" || range === "unspecified";
}

function hostLocalUrl(url: string): URL | undefined {
	try {
		const parsed = new URL(url);
		const host = parsed.hostname.toLowerCase();
		return /^(http|ws)s?:$/.test(parsed.protocol) && isLoopbackName(host) && !PREVIEW_HOST.test(host) ? parsed : undefined;
	} catch {
		return undefined;
	}
}

function unspecifiedToLocalhost(url: string): string | undefined {
	try {
		const parsed = new URL(url);
		if (parsed.hostname !== "0.0.0.0" && parsed.hostname !== "[::]") return undefined;
		parsed.hostname = "localhost";
		return parsed.href;
	} catch {
		return undefined;
	}
}

export function connectionOwner(table: string, peer: { address?: string; port?: number }, local: { address?: string; port?: number }): number | undefined {
	const endpoint = (word: string | undefined) => {
		const [address, port] = (word ?? "").split(":");
		if (!address || address.length !== 8 || !port) return "";
		const bytes = address.match(/../g)!.reverse().map((pair) => Number.parseInt(pair, 16));
		return `${bytes.join(".")}:${Number.parseInt(port, 16)}`;
	};
	const want = [`${peer.address}:${peer.port}`, `${local.address}:${local.port}`];
	for (const line of table.split("\n")) {
		const fields = line.trim().split(/\s+/);
		if (endpoint(fields[1]) === want[0] && endpoint(fields[2]) === want[1]) return Number(fields[7]);
	}
	return undefined;
}

async function ownedByThisUser(socket: Socket): Promise<boolean> {
	if (process.platform !== "linux") return true;
	const table = await readFile("/proc/net/tcp", "utf8").catch(() => "");
	return connectionOwner(table, { address: socket.remoteAddress, port: socket.remotePort }, { address: socket.localAddress, port: socket.localPort }) === process.getuid?.();
}

function join(a: Duplex, b: Duplex): void {
	a.on("error", () => b.destroy());
	b.on("error", () => a.destroy());
	a.once("close", () => b.destroy());
	b.once("close", () => a.destroy());
	a.pipe(b);
	b.pipe(a);
}

function listen(server: Server, port: number): Promise<number> {
	return new Promise((resolve, reject) => {
		server.once("error", reject);
		server.listen(port, "127.0.0.1", () => {
			server.off("error", reject);
			server.on("error", () => undefined);
			resolve((server.address() as AddressInfo).port);
		});
	});
}

function answers(host: string, port: number): Promise<boolean> {
	return new Promise((resolve) => {
		const probe = connect(port, host);
		probe.setTimeout(1_000, () => {
			probe.destroy();
			resolve(false);
		});
		probe.once("connect", () => {
			probe.destroy();
			resolve(true);
		});
		probe.once("error", () => resolve(false));
	});
}

export function createLoopbackTunnels(openTunnel: OpenTunnel) {
	const socksPorts = new Map<string, Promise<number>>();
	const routed = new WeakMap<RoutedSession, Promise<void>>();
	const forwards = new Map<string, { forward: PortForward; server: Server; clients: Set<Socket> }>();
	const pending = new Map<string, Promise<PortForward>>();
	const removals = new Map<string, number>();
	let generation = 0;
	const key = (hostId: string, port: number) => `${hostId}\n${port}`;

	const serveSocks = async (client: Socket, hostId: string): Promise<void> => {
		client.on("error", () => client.destroy());
		if (!(await ownedByThisUser(client))) {
			client.end(Buffer.from([0x05, 0xff]));
			return;
		}
		const reader = new SocketReader(client);
		const target = await readRequest(client, reader);
		if (!target) return;
		if (!isLoopbackName(target.host)) return refuse(client, REPLY_NOT_ALLOWED);
		const tunnel = await openTunnel(hostId, target.port).catch(() => null);
		if (!tunnel || client.destroyed) {
			tunnel?.destroy();
			return refuse(client, REPLY_HOST_UNREACHABLE);
		}
		reply(client, REPLY_OK);
		const early = reader.detach();
		if (early.length > 0) tunnel.write(early);
		join(client, tunnel);
	};

	const socksPort = (hostId: string): Promise<number> => {
		let port = socksPorts.get(hostId);
		if (!port) {
			const server = createServer((client) => void serveSocks(client, hostId).catch(() => client.destroy()));
			port = listen(server, 0);
			port.catch(() => socksPorts.delete(hostId));
			socksPorts.set(hostId, port);
		}
		return port;
	};

	const reach = async (hostId: string): Promise<LoopbackReach> => {
		const nonce = randomBytes(16);
		const probe = createServer();
		let timer: NodeJS.Timeout | undefined;
		let settle: (same: boolean) => void = () => undefined;
		const received = new Promise<boolean>((resolve) => {
			settle = resolve;
			probe.on("connection", (socket) => {
				let data = Buffer.alloc(0);
				socket.on("error", () => undefined);
				socket.on("data", (chunk: Buffer) => {
					data = Buffer.concat([data, chunk]);
					if (data.length < nonce.length) return;
					resolve(data.subarray(0, nonce.length).equals(nonce));
					socket.destroy();
				});
			});
		});
		const port = await listen(probe, 0);
		try {
			const tunnel = await openTunnel(hostId, port).catch((error: unknown) =>
				error instanceof LoopbackTunnelError && error.reason === "refused" ? null : undefined);
			if (tunnel === undefined) return "off";
			if (tunnel === null) return "tunnel";
			tunnel.on("error", () => settle(false));
			tunnel.write(nonce);
			timer = setTimeout(() => settle(false), PROBE_TIMEOUT_MS);
			const same = await received;
			tunnel.destroy();
			return same ? "same" : "tunnel";
		} finally {
			clearTimeout(timer);
			probe.close();
		}
	};

	const startForward = async (hostId: string, remotePort: number): Promise<PortForward> => {
		const removal = removals.get(hostId);
		const started = generation;
		const reached = await reach(hostId);
		if (reached === "same") throw new SameHostError("This host runs on this computer, so its ports are already on localhost here.");
		if (reached === "off") throw new Error("Can't reach this host's localhost. Check that the host is online, up to date, and has Share localhost on in Settings → Mobile.");
		const clients = new Set<Socket>();
		const server = createServer((client) => {
			client.on("error", () => client.destroy());
			if (clients.size >= MAX_FORWARD_CONNECTIONS) return void client.destroy();
			clients.add(client);
			client.once("close", () => clients.delete(client));
			void (async () => {
				const tunnel = (await ownedByThisUser(client)) ? await openTunnel(hostId, remotePort).catch(() => null) : null;
				if (!tunnel || client.destroyed) {
					tunnel?.destroy();
					client.destroy();
					return;
				}
				join(client, tunnel);
			})();
		});
		const taken = (await Promise.all([answers("127.0.0.1", remotePort), answers("::1", remotePort)])).some(Boolean);
		const localPort = taken ? await listen(server, 0) : await listen(server, remotePort).catch(() => listen(server, 0));
		if (removals.get(hostId) !== removal || generation !== started) {
			server.close();
			throw new Error("This host was removed.");
		}
		const forward = { hostId, remotePort, localPort };
		forwards.set(key(hostId, remotePort), { forward, server, clients });
		return forward;
	};

	const route = (session: RoutedSession, hostId: string): Promise<void> => {
		let ready = routed.get(session);
		if (!ready) {
			ready = (async () => {
				const port = await socksPort(hostId);
				await session.setProxy({ mode: "fixed_servers", proxyRules: `socks5://127.0.0.1:${port}`, proxyBypassRules: BYPASS_RULES });
				session.webRequest.onBeforeRequest({ urls: UNSPECIFIED_URLS }, (details, callback) => {
					const redirectURL = unspecifiedToLocalhost(details.url);
					callback(redirectURL ? { redirectURL } : {});
				});
				await session.closeAllConnections();
			})();
			ready.catch(() => {
				routed.delete(session);
				session.webRequest.onBeforeRequest({ urls: ["<all_urls>"] }, (details, callback) => {
					if (!hostLocalUrl(details.url)) return callback({});
					route(session, hostId).then(() => {
						const redirectURL = unspecifiedToLocalhost(details.url);
						callback(redirectURL ? { redirectURL } : {});
					}, () => callback({ cancel: true }));
				});
			});
			routed.set(session, ready);
		}
		return ready;
	};

	return {
		route,
		reach,
		forward(hostId: string, remotePort: number): Promise<PortForward> {
			if (!Number.isInteger(remotePort) || remotePort < 1 || remotePort > 65_535) return Promise.reject(new Error("A port is a whole number from 1 to 65535."));
			const id = key(hostId, remotePort);
			const existing = forwards.get(id);
			if (existing) return Promise.resolve(existing.forward);
			let started = pending.get(id);
			if (!started) {
				started = startForward(hostId, remotePort).finally(() => pending.delete(id));
				pending.set(id, started);
			}
			return started;
		},
		async localUrl(hostId: string, url: string): Promise<string | undefined> {
			const parsed = hostLocalUrl(url);
			if (!parsed || parsed.protocol.startsWith("ws")) return undefined;
			const host = parsed.hostname.toLowerCase();
			const forward = await this.forward(hostId, Number(parsed.port || (parsed.protocol === "https:" ? 443 : 80))).catch((error: unknown) => {
				if (error instanceof SameHostError) return undefined;
				throw error;
			});
			if (!forward) return undefined;
			if (!/\.localhost\.?$/.test(host)) parsed.hostname = "localhost";
			parsed.port = String(forward.localPort);
			return parsed.href;
		},
		stop(hostId: string, remotePort: number): void {
			const id = key(hostId, remotePort);
			const existing = forwards.get(id);
			if (!existing) return;
			forwards.delete(id);
			existing.server.close();
			for (const client of existing.clients) client.destroy();
		},
		stopHost(hostId: string): void {
			removals.set(hostId, (removals.get(hostId) ?? 0) + 1);
			for (const { forward } of [...forwards.values()]) if (forward.hostId === hostId) this.stop(hostId, forward.remotePort);
		},
		stopAll(): void {
			generation++;
			for (const { forward } of [...forwards.values()]) this.stop(forward.hostId, forward.remotePort);
		},
		list: (): PortForward[] => [...forwards.values()].map(({ forward }) => forward),
	};
}

export type LoopbackTunnels = ReturnType<typeof createLoopbackTunnels>;
