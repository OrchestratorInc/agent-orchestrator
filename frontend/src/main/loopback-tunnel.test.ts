// @vitest-environment node
import { afterEach, describe, expect, it } from "vitest";
import * as net from "node:net";
import { connectionOwner, createLoopbackTunnels, isLoopbackName, type RoutedSession } from "./loopback-tunnel";
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

type Host = { ports?: Record<number, number>; same?: boolean; off?: boolean };

function hosts(table: Record<string, Host>) {
	return async (hostId: string, port: number) => {
		const host = table[hostId];
		if (!host) throw new LoopbackTunnelError("unavailable", "not connected");
		if (host.off) throw new LoopbackTunnelError("disabled", "turned off");
		const target = host.same ? port : host.ports?.[port];
		if (!target) throw new LoopbackTunnelError("refused", "nothing listening");
		return connect(target);
	};
}

function fakeSession() {
	const state: { proxy?: Electron.ProxyConfig; closed: number; redirect?: (url: string) => string | undefined } = { closed: 0 };
	const session: RoutedSession = {
		setProxy: async (config) => { state.proxy = config; },
		closeAllConnections: async () => { state.closed++; },
		webRequest: {
			onBeforeRequest: (_filter, listener) => {
				state.redirect = (url) => {
					let redirected: string | undefined;
					listener({ url }, (response) => { redirected = response.redirectURL; });
					return redirected;
				};
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
		expect(state.redirect?.("http://0.0.0.0:5173/a?b#c")).toBe("http://localhost:5173/a?b#c");
		expect(state.redirect?.("http://[::]:5173/")).toBe("http://localhost:5173/");
		expect(state.redirect?.("http://localhost:5173/")).toBeUndefined();
	});

	it("tells a host on this computer, a remote host and one that does not share apart", async () => {
		const tunnels = createLoopbackTunnels(hosts({ h_same: { same: true }, h_box: { ports: {} }, h_off: { off: true } }));
		expect(await tunnels.reach("h_same")).toBe("same");
		expect(await tunnels.reach("h_box")).toBe("tunnel");
		expect(await tunnels.reach("h_off")).toBe("off");
		expect(await tunnels.reach("h_gone")).toBe("off");
	});

	it("forwards a host port to this computer until it is stopped", async () => {
		const echo = await echoServer();
		const tunnels = createLoopbackTunnels(hosts({ h_box: { ports: { [echo]: echo } }, h_same: { same: true }, h_off: { off: true } }));
		const forward = await tunnels.forward("h_box", echo);
		expect(forward.localPort).not.toBe(echo);
		expect(await tunnels.forward("h_box", echo)).toEqual(forward);
		expect(tunnels.list()).toEqual([forward]);

		const client = await connect(forward.localPort);
		client.write("ping");
		expect((await receive(client, 4)).toString()).toBe("ping");

		tunnels.stopHost("h_box");
		expect(tunnels.list()).toEqual([]);
		expect((await receive(client, 1)).length).toBe(0);
		await expect(connect(forward.localPort)).rejects.toThrow();

		const opened = await tunnels.localUrl("h_box", `http://0.0.0.0:${echo}/app?x=1#y`);
		expect(opened).toBe(`http://localhost:${tunnels.list()[0]!.localPort}/app?x=1#y`);
		expect(await tunnels.localUrl("h_box", `http://ao-preview-${"0".repeat(32)}.localhost:${echo}/`)).toBeUndefined();
		expect(await tunnels.localUrl("h_same", `http://localhost:${echo}/`)).toBeUndefined();
		expect(await tunnels.localUrl("h_box", "https://example.com/")).toBeUndefined();
		tunnels.stop("h_box", echo);

		await expect(tunnels.forward("h_same", echo)).rejects.toThrow(/runs on this computer/);
		await expect(tunnels.forward("h_off", echo)).rejects.toThrow(/does not share/);
		await expect(tunnels.forward("h_box", 0)).rejects.toThrow(/1 to 65535/);
	});

	it("moves a forward off a port another server holds on IPv6 localhost", async () => {
		const taken = net.createServer();
		const port = await new Promise<number>((resolve) => {
			taken.once("error", () => resolve(0));
			taken.listen(0, "::1", () => resolve((taken.address() as net.AddressInfo).port));
		});
		if (!port) return;
		cleanup.push(() => taken.close());
		const tunnels = createLoopbackTunnels(hosts({ h_box: { ports: { [port]: await echoServer() } } }));
		expect((await tunnels.forward("h_box", port)).localPort).not.toBe(port);
		tunnels.stopHost("h_box");
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
