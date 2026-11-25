import { createServer, type AddressInfo, type Server, type Socket } from "node:net";
import type { Duplex } from "node:stream";
import { readRequest, refuse, reply, REPLY_HOST_UNREACHABLE, REPLY_OK, SocketReader } from "./render-check-proxy";

type OpenTunnel = (hostId: string, port: number) => Promise<Duplex>;

export type RoutedSession = {
	setProxy(config: Electron.ProxyConfig): Promise<void>;
	webRequest: {
		onBeforeRequest(
			filter: Electron.WebRequestFilter,
			listener: (details: { url: string }, callback: (response: Electron.CallbackResponse) => void) => void,
		): void;
	};
};

const BYPASS_RULES = "*,<-loopback>,169.254.0.0/16,fe80::/10,ao-preview-*.localhost";
const UNSPECIFIED_URLS = ["0.0.0.0", "[::]"].flatMap((host) => ["*", "ws", "wss"].map((scheme) => `${scheme}://${host}/*`));

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
		const reader = new SocketReader(client);
		const target = await readRequest(client, reader);
		if (!target) return;
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

	return (session: RoutedSession, hostId: string): Promise<void> => {
		let ready = routed.get(session);
		if (!ready) {
			ready = (async () => {
				const port = await socksPort(hostId);
				await session.setProxy({ mode: "fixed_servers", proxyRules: `socks5://127.0.0.1:${port}`, proxyBypassRules: BYPASS_RULES });
				session.webRequest.onBeforeRequest({ urls: UNSPECIFIED_URLS }, (details, callback) => callback(unspecifiedResponse(details.url)));
			})();
			ready.catch(() => routed.delete(session));
			routed.set(session, ready);
		}
		return ready;
	};
}
