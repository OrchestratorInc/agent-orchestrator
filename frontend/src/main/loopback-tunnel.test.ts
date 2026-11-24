// @vitest-environment node
import { afterEach, describe, expect, it } from "vitest";
import * as net from "node:net";
import { Duplex } from "node:stream";
import { connectionOwner, createLoopbackTunnels, isLoopbackName, join, type RoutedSession } from "./loopback-tunnel";
import { LoopbackTunnelError } from "./remote-proxy";

const cleanup: Array<() => void> = [];
afterEach(() => {
	for (const done of cleanup.splice(0)) done();
});

async function echoServer(): Promise<number> {
	const server = net.createServer((socket) => {
		socket.on("error", () => undefined);
		socket.pipe(socket);
	});
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	cleanup.push(() => server.close());
	return (server.address() as net.AddressInfo).port;
}

function connect(port: number): Promise<net.Socket> {
	return new Promise((resolve, reject) => {
		const socket = net.connect(port, "127.0.0.1", () => resolve(socket));
		socket.once("error", reject);
		cleanup.push(() => socket.destroy());
	});
}

function receive(socket: net.Socket, length: number): Promise<Buffer> {
	return new Promise((resolve, reject) => {
		let data = Buffer.alloc(0);
		const timer = setTimeout(() => reject(new Error(`received ${data.length} of ${length} bytes`)), 3_000);
		socket.on("data", (chunk: Buffer) => {
			data = Buffer.concat([data, chunk]);
			if (data.length < length) return;
			clearTimeout(timer);
			resolve(data);
		});
		socket.once("close", () => { clearTimeout(timer); resolve(data); });
	});
}

async function socksConnect(proxyPort: number, host: string, port: number, payload = ""): Promise<{ code: number; echoed: string }> {
	const socket = await connect(proxyPort);
	const portBytes = Buffer.alloc(2);
	portBytes.writeUInt16BE(port);
	socket.write(Buffer.concat([Buffer.from([5, 1, 0, 5, 1, 0, 3, host.length]), Buffer.from(host), portBytes, Buffer.from(payload)]));
	const data = await receive(socket, 12 + payload.length);
	return { code: data[3] ?? -1, echoed: data.subarray(12).toString() };
}

type Host = { ports?: Record<number, number>; off?: boolean };

function hosts(table: Record<string, Host>) {
	return async (hostId: string, port: number) => {
		const host = table[hostId];
		if (!host) throw new LoopbackTunnelError(false, "not connected");
		if (host.off) throw new LoopbackTunnelError(false, "turned off");
		const target = host.ports?.[port];
		if (!target) throw new LoopbackTunnelError(true, "nothing listening");
		return connect(target);
	};
}

function fakeSession() {
	const state: { proxy?: Electron.ProxyConfig; closed: number; failProxy?: boolean; respond?: (url: string) => Promise<Electron.CallbackResponse> } = { closed: 0 };
	const session: RoutedSession = {
		setProxy: async (config) => {
			if (state.failProxy) throw new Error("proxy refused");
			state.proxy = config;
		},
		closeAllConnections: async () => { state.closed++; },
		webRequest: {
			onBeforeRequest: (_filter, listener) => {
				state.respond = (url) => new Promise((resolve) => listener({ url }, resolve));
			},
		},
	};
	return { session, state };
}

describe("loopback tunnels", () => {
	it("routes a remote session's loopback names through the host and nothing else", async () => {
		const echo = await echoServer();
		const tunnels = createLoopbackTunnels(hosts({ h_box: { ports: { 5173: echo } } }));
		const { session, state } = fakeSession();
		await tunnels.route(session, "h_box");
		await tunnels.route(session, "h_box");
		expect(state.closed).toBe(1);
		expect(state.proxy?.proxyBypassRules).toBe("*,<-loopback>,169.254.0.0/16,fe80::/10,ao-preview-*.localhost");
		const socksPort = Number(/^socks5:\/\/127\.0\.0\.1:(\d+)$/.exec(state.proxy?.proxyRules ?? "")?.[1]);

		expect(await socksConnect(socksPort, "localhost", 5173, "hello")).toEqual({ code: 0, echoed: "hello" });
		expect((await socksConnect(socksPort, "localhost", 5174)).code).toBe(0x04);
		expect((await socksConnect(socksPort, "example.com", 5173)).code).toBe(0x02);
		expect(await state.respond?.("http://0.0.0.0:5173/a?b#c")).toEqual({ redirectURL: "http://localhost:5173/a?b#c" });
		expect(await state.respond?.("http://[::]:5173/")).toEqual({ redirectURL: "http://localhost:5173/" });
		expect(await state.respond?.("http://localhost:5173/")).toEqual({});
	});

	it("keeps a partition it could not route away from this computer's localhost and retries on the next request", async () => {
		const tunnels = createLoopbackTunnels(hosts({ h_box: { ports: {} } }));
		const { session, state } = fakeSession();
		state.failProxy = true;
		await expect(tunnels.route(session, "h_box")).rejects.toThrow("proxy refused");
		for (const url of ["http://localhost:5173/", "http://app.localhost:5173/", "ws://127.0.0.1:5173/hmr", "http://0.0.0.0:3000/", "http://[::1]:8080/"]) expect(await state.respond?.(url)).toEqual({ cancel: true });
		for (const url of ["https://example.com/", "http://ao-preview-artifact.abc.localhost:3011/"]) expect(await state.respond?.(url)).toEqual({});

		state.failProxy = false;
		expect(await state.respond?.("http://0.0.0.0:5173/")).toEqual({ redirectURL: "http://localhost:5173/" });
		expect(state.proxy?.proxyRules).toMatch(/^socks5:\/\/127\.0\.0\.1:\d+$/);
	});

	it("tells a host that shares its localhost from one that does not", async () => {
		const tunnels = createLoopbackTunnels(hosts({ h_box: { ports: {} }, h_listening: { ports: { 1: await echoServer() } }, h_off: { off: true } }));
		expect(await tunnels.reach("h_box")).toBe(true);
		expect(await tunnels.reach("h_listening")).toBe(true);
		expect(await tunnels.reach("h_off")).toBe(false);
		expect(await tunnels.reach("h_gone")).toBe(false);
	});
});

describe("join", () => {
	it("lets a slow client finish the reply when the host's server closes the connection", async () => {
		const delivered: Buffer[] = [];
		const client = new Duplex({ read() {}, write(chunk: Buffer, _encoding, done) { setTimeout(() => { delivered.push(chunk); done(); }, 1); } });
		const tunnel = new Duplex({ read() {}, write(_chunk, _encoding, done) { done(); } });
		tunnel.once("end", () => tunnel.destroy());
		const closed = new Promise((resolve) => client.once("close", resolve));
		join(client, tunnel);
		for (let i = 0; i < 8; i++) tunnel.push(Buffer.alloc(1024, i));
		tunnel.push(null);
		await closed;
		expect(Buffer.concat(delivered).length).toBe(8 * 1024);
	});

	it("still drops the other side at once when one side fails", async () => {
		const client = new Duplex({ read() {}, write(_chunk, _encoding, done) { setTimeout(done, 1_000); } });
		const tunnel = new Duplex({ read() {}, write(_chunk, _encoding, done) { done(); } });
		join(client, tunnel);
		tunnel.push(Buffer.alloc(1024));
		const closed = new Promise((resolve) => client.once("close", resolve));
		tunnel.destroy(new Error("tunnel lost"));
		await closed;
		expect(client.destroyed).toBe(true);
	});
});

describe("isLoopbackName", () => {
	it.each(["localhost", "LOCALHOST.", "app.localhost", "127.0.0.2", "[::1]", "::1", "0.0.0.0", "::"])("accepts %s", (host) => {
		expect(isLoopbackName(host)).toBe(true);
	});
	it.each(["example.com", "192.168.1.2", "10.0.0.1", "localhost.example.com"])("refuses %s", (host) => {
		expect(isLoopbackName(host)).toBe(false);
	});
});

describe("connectionOwner", () => {
	const table = [
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode",
		"   0: 0100007F:1F90 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1001        0 11 1 0 20 4 30 10 -1",
		"   1: 0100007F:C350 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 12 1 0 20 4 30 10 -1",
	].join("\n");

	it("reads the uid of the process on the other end", () => {
		expect(connectionOwner(table, { address: "127.0.0.1", port: 50000 }, { address: "127.0.0.1", port: 8080 })).toBe(1000);
		expect(connectionOwner(table, { address: "127.0.0.1", port: 50001 }, { address: "127.0.0.1", port: 8080 })).toBeUndefined();
	});
});
