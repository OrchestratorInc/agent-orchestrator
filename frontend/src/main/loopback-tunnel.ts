import { readFile } from "node:fs/promises";
import { createServer, type AddressInfo, type Server, type Socket } from "node:net";
import type { Duplex } from "node:stream";
import ipaddr from "ipaddr.js";
import { LoopbackTunnelError } from "./remote-proxy";
import { readRequest, refuse, reply, REPLY_HOST_UNREACHABLE, REPLY_NOT_ALLOWED, REPLY_OK, SocketReader } from "./render-check-proxy";

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

const UNUSED_PORT = 1;
const BYPASS_RULES = "*,<-loopback>,169.254.0.0/16,fe80::/10,ao-preview-*.localhost";
const UNSPECIFIED_URLS = ["0.0.0.0", "[::]"].flatMap((host) => ["*", "ws", "wss"].map((scheme) => `${scheme}://${host}/*`));
const PREVIEW_HOST = /^ao-preview-.*\.localhost$/;

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

function unspecifiedResponse(url: string): Electron.CallbackResponse {
	try {
		const parsed = new URL(url);
		if (parsed.hostname !== "0.0.0.0" && parsed.hostname !== "[::]") return {};
		if (parsed.protocol.startsWith("ws")) return { cancel: true };
		parsed.hostname = "localhost";
		return { redirectURL: parsed.href };
	} catch {
		return {};
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

export function join(a: Duplex, b: Duplex): void {
	a.on("error", () => b.destroy());
	b.on("error", () => a.destroy());
	const closeAfter = (from: Duplex, to: Duplex) => from.once("close", () => {
		if (from.readableEnded && !to.writableFinished) to.once("finish", () => to.destroy());
		else to.destroy();
	});
	closeAfter(a, b);
	closeAfter(b, a);
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

export function createLoopbackTunnels(openTunnel: OpenTunnel) {
	const socksPorts = new Map<string, Promise<number>>();
	const routed = new WeakMap<RoutedSession, Promise<void>>();

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

	const reach = (hostId: string): Promise<boolean> =>
		openTunnel(hostId, UNUSED_PORT).then((tunnel) => {
			tunnel.destroy();
			return true;
		}, (error: unknown) => error instanceof LoopbackTunnelError && error.refused);

	const route = (session: RoutedSession, hostId: string): Promise<void> => {
		let ready = routed.get(session);
		if (!ready) {
			ready = (async () => {
				const port = await socksPort(hostId);
				await session.setProxy({ mode: "fixed_servers", proxyRules: `socks5://127.0.0.1:${port}`, proxyBypassRules: BYPASS_RULES });
				session.webRequest.onBeforeRequest({ urls: UNSPECIFIED_URLS }, (details, callback) => callback(unspecifiedResponse(details.url)));
				await session.closeAllConnections();
			})();
			ready.catch(() => {
				routed.delete(session);
				session.webRequest.onBeforeRequest({ urls: ["<all_urls>"] }, (details, callback) => {
					if (!hostLocalUrl(details.url)) return callback({});
					route(session, hostId).then(() => callback(unspecifiedResponse(details.url)), () => callback({ cancel: true }));
				});
			});
			routed.set(session, ready);
		}
		return ready;
	};

	return { route, reach };
}

export type LoopbackTunnels = ReturnType<typeof createLoopbackTunnels>;
