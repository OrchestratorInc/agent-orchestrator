// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { lookup } from "node:dns/promises";
import * as net from "node:net";
import ipaddr from "ipaddr.js";
import { allowRenderPage, isLocalAddress, startRenderCheckProxy } from "./render-check-proxy";

vi.mock("node:dns/promises", async (importOriginal) => {
	const actual = await importOriginal<typeof import("node:dns/promises")>();
	return { ...actual, lookup: vi.fn(actual.lookup) };
});

// A public address this machine's (fake) interface holds.
const MACHINE_PUBLIC = "1.2.3.4";
vi.mock("node:os", async (importOriginal) => {
	const actual = await importOriginal<typeof import("node:os")>();
	return {
		...actual,
		networkInterfaces: () => ({
			...actual.networkInterfaces(),
			test0: [
				{ address: MACHINE_PUBLIC, netmask: "255.255.255.0", family: "IPv4", mac: "02:00:00:00:00:01", internal: false, cidr: `${MACHINE_PUBLIC}/24` },
			],
		}),
	};
});

const cleanup: Array<() => void> = [];
afterEach(() => {
	for (const done of cleanup.splice(0)) done();
});

function target(host: string, port: number): Buffer {
	const portBytes = Buffer.alloc(2);
	portBytes.writeUInt16BE(port);
	if (ipaddr.isValid(host)) {
		const bytes = ipaddr.parse(host).toByteArray();
		return Buffer.concat([Buffer.from([bytes.length === 4 ? 0x01 : 0x04, ...bytes]), portBytes]);
	}
	return Buffer.concat([Buffer.from([0x03, host.length]), Buffer.from(host, "latin1"), portBytes]);
}

interface Attempt {
	version?: number;
	command?: number;
	methods?: number[];
	payload?: string;
	trickle?: boolean;
}

/**
 * Greets the proxy and asks it to CONNECT, resolving with the reply code
 * (0xff for a refused greeting, -1 for a close with no reply) and the socket.
 */
function attempt(proxyPort: number, host: string, port: number, options: Attempt = {}): Promise<{ code: number; socket: net.Socket }> {
	const { version = 5, command = 1, methods = [0], payload = "", trickle = false } = options;
	const bytes = Buffer.concat([
		Buffer.from([5, methods.length, ...methods]),
		Buffer.from([version, command, 0]),
		target(host, port),
		Buffer.from(payload),
	]);
	return new Promise((resolve) => {
		const socket = net.connect(proxyPort, "127.0.0.1");
		cleanup.push(() => socket.destroy());
		socket.on("error", () => {});
		socket.once("connect", async () => {
			if (!trickle) return void socket.write(bytes);
			socket.setNoDelay(true);
			for (const byte of bytes) {
				if (socket.destroyed) return;
				socket.write(Buffer.from([byte]));
				await new Promise((wait) => setTimeout(wait, 1));
			}
		});
		let received = Buffer.alloc(0);
		const onData = (chunk: Buffer) => {
			received = Buffer.concat([received, chunk]);
			if (received.length >= 12) {
				socket.off("data", onData);
				const extra = received.subarray(12);
				if (extra.length > 0) socket.unshift(extra);
				resolve({ code: received[3]!, socket });
			}
		};
		socket.on("data", onData);
		socket.once("close", () => {
			const code = received.length >= 12 ? received[3]! : received[1] === 0xff ? 0xff : -1;
			resolve({ code, socket });
		});
	});
}

const code = async (proxyPort: number, host: string, port: number, options?: Attempt) => (await attempt(proxyPort, host, port, options)).code;

function nextData(socket: net.Socket): Promise<string> {
	return new Promise((resolve) => socket.once("data", (chunk) => resolve(chunk.toString())));
}

/** A loopback server standing in for the daemon's render page: it echoes what it reads. */
async function pageServer(): Promise<number> {
	const server = net.createServer((socket) => socket.pipe(socket));
	cleanup.push(() => server.close());
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	return (server.address() as net.AddressInfo).port;
}

function allow(host: string, port: number, refused: string[] = []): () => void {
	const release = allowRenderPage(host, port, (destination) => refused.push(destination));
	cleanup.push(release);
	return release;
}

describe("isLocalAddress", () => {
	it.each([
		"127.0.0.1",
		"0.0.0.0",
		"10.20.30.40",
		"172.16.5.4",
		"192.168.1.1",
		"100.64.0.9",
		"169.254.169.254",
		"198.18.7.7",
		"224.0.0.251",
		"255.255.255.255",
		"::",
		"::1",
		"::a00:1",
		"::ffff:192.168.0.1",
		"fd12:3456::1",
		"fe80::1%en0",
		"ff02::fb",
		"64:ff9b::c0a8:101",
		"64:ff9b:1::808:808",
		"2002:a00:1::1",
		"2001:0:4136:e378::1",
		MACHINE_PUBLIC,
		`::ffff:${MACHINE_PUBLIC}`,
		"64:ff9b::102:304",
		"not an address",
	])("treats %s as local", (address) => {
		expect(isLocalAddress(address)).toBe(true);
	});

	it.each(["8.8.8.8", "93.184.216.34", "::ffff:8.8.4.4", "2606:4700:4700::1111", "64:ff9b::808:808", "2002:808:808::1"])(
		"treats %s as public",
		(address) => {
			expect(isLocalAddress(address)).toBe(false);
		},
	);
});

describe("render-check proxy", () => {
	it("refuses local targets by address and by name", async () => {
		const proxy = await startRenderCheckProxy();
		for (const host of ["127.0.0.1", "192.168.0.10", "169.254.169.254", "::1", "64:ff9b::7f00:1", "2002:7f00:1::1", "localhost", MACHINE_PUBLIC]) {
			expect(await code(proxy, host, 80)).toBe(2);
		}
	});

	it("answers malformed and unsupported requests", async () => {
		const proxy = await startRenderCheckProxy();
		expect(await code(proxy, "8.8.8.8", 443, { version: 4 })).toBe(7);
		expect(await code(proxy, "8.8.8.8", 443, { command: 2 })).toBe(7);
		expect(await code(proxy, "8.8.8.8", 0)).toBe(7);
		expect(await code(proxy, "8.8.8.8", 443, { methods: [2] })).toBe(0xff);
		vi.mocked(lookup).mockResolvedValueOnce([] as never);
		expect(await code(proxy, "nowhere.invalid", 443)).toBe(4);
	});

	it("reaches an allowed page, refuses its neighbours, and reports them", async () => {
		const proxy = await startRenderCheckProxy();
		const page = await pageServer();
		const refused: string[] = [];
		const release = allow("127.0.0.1", page, refused);

		const { code: ok, socket } = await attempt(proxy, "127.0.0.1", page);
		expect(ok).toBe(0);
		const echoed = nextData(socket);
		socket.write("hello");
		expect(await echoed).toBe("hello");

		const neighbour = page === 65_535 ? page - 1 : page + 1;
		expect(await code(proxy, "127.0.0.1", neighbour)).toBe(2);
		expect(await code(proxy, "localhost", page)).toBe(2);
		expect(refused).toEqual([`127.0.0.1:${neighbour}`, `localhost:${page}`]);

		release();
		expect(await code(proxy, "127.0.0.1", page)).toBe(2);
		expect(refused).toHaveLength(2);
	});

	it("keeps a page reachable until every check that allowed it lets go", async () => {
		const proxy = await startRenderCheckProxy();
		const page = await pageServer();
		const first = allow("127.0.0.1", page);
		const second = allow("127.0.0.1", page);
		first();
		first();
		expect(await code(proxy, "127.0.0.1", page)).toBe(0);
		second();
		expect(await code(proxy, "127.0.0.1", page)).toBe(2);
	});

	it("handles a handshake that arrives a byte at a time", async () => {
		const proxy = await startRenderCheckProxy();
		const page = await pageServer();
		allow("127.0.0.1", page);
		expect(await code(proxy, "127.0.0.1", page, { trickle: true })).toBe(0);
	});

	it("forwards what a client sends before its tunnel is up", async () => {
		const proxy = await startRenderCheckProxy();
		const page = await pageServer();
		allow("127.0.0.1", page);
		const { code: ok, socket } = await attempt(proxy, "127.0.0.1", page, { payload: "early" });
		expect(ok).toBe(0);
		expect(await nextData(socket)).toBe("early");
	});

	it("connects to the addresses it checked, not to a fresh lookup", async () => {
		const proxy = await startRenderCheckProxy();
		const page = await pageServer();
		allow("page.invalid", page);
		// Nothing else can resolve this name, so the tunnel works only through the checked address.
		vi.mocked(lookup).mockResolvedValueOnce([{ address: "127.0.0.1", family: 4 }] as never);
		const { code: ok, socket } = await attempt(proxy, "page.invalid", page);
		expect(ok).toBe(0);
		const echoed = nextData(socket);
		socket.write("pinned");
		expect(await echoed).toBe("pinned");
	});

	it("refuses a name whose answers include a local address", async () => {
		const proxy = await startRenderCheckProxy();
		vi.mocked(lookup).mockResolvedValueOnce([
			{ address: "93.184.216.34", family: 4 },
			{ address: "10.0.0.1", family: 4 },
		] as never);
		expect(await code(proxy, "rebind.invalid", 80)).toBe(2);
	});
});

describe("render-check proxy with no network", () => {
	it("reaches only the allowed page and never looks a name up", async () => {
		const proxy = await startRenderCheckProxy("none");
		const page = await pageServer();
		const refused: string[] = [];
		allow("127.0.0.1", page, refused);
		expect(await code(proxy, "127.0.0.1", page)).toBe(0);

		const lookups = vi.mocked(lookup).mock.calls.length;
		expect(await code(proxy, "example.com", 443)).toBe(2);
		expect(await code(proxy, "8.8.8.8", 53)).toBe(2);
		expect(vi.mocked(lookup).mock.calls.length).toBe(lookups);
		expect(refused).toEqual(["example.com:443", "8.8.8.8:53"]);
	});
});

describe("startRenderCheckProxy", () => {
	it("runs one listener per network for the life of the process", async () => {
		const [publicA, publicB, noneA, noneB] = await Promise.all([
			startRenderCheckProxy(),
			startRenderCheckProxy("public"),
			startRenderCheckProxy("none"),
			startRenderCheckProxy("none"),
		]);
		expect(publicA).toBe(publicB);
		expect(noneA).toBe(noneB);
		expect(noneA).not.toBe(publicA);
	});

	it("rejects when it cannot listen and starts on the next call", async () => {
		vi.resetModules();
		const fresh = await import("./render-check-proxy");
		const failure = Object.assign(new Error("no file descriptors left"), { code: "EMFILE" });
		const listen = vi.spyOn(net.Server.prototype, "listen").mockImplementationOnce(function (this: net.Server) {
			queueMicrotask(() => this.emit("error", failure));
			return this;
		});
		await expect(fresh.startRenderCheckProxy()).rejects.toBe(failure);
		listen.mockRestore();
		await expect(fresh.startRenderCheckProxy()).resolves.toBeGreaterThan(0);
	});
});
