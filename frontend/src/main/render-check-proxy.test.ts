// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { lookup } from "node:dns/promises";
import * as net from "node:net";
import * as os from "node:os";
import { allowRenderPage, startRenderCheckProxy } from "./render-check-proxy";

// The real resolver, which a test can override for one lookup.
vi.mock("node:dns/promises", async (importOriginal) => {
	const actual = await importOriginal<typeof import("node:dns/promises")>();
	return { ...actual, lookup: vi.fn(actual.lookup) };
});

// A public IPv4 address this machine holds, which no private range covers.
const OWN_PUBLIC_IPV4 = "198.51.100.7";
vi.mock("node:os", async (importOriginal) => {
	const actual = await importOriginal<typeof os>();
	return {
		...actual,
		networkInterfaces: () => ({
			...actual.networkInterfaces(),
			"ao-test": [
				{
					address: OWN_PUBLIC_IPV4,
					netmask: "255.255.255.0",
					family: "IPv4",
					mac: "00:00:00:00:00:00",
					internal: false,
					cidr: `${OWN_PUBLIC_IPV4}/24`,
				},
			],
		}),
	};
});

const sockets: net.Socket[] = [];
const servers: net.Server[] = [];
afterEach(async () => {
	for (const socket of sockets.splice(0)) socket.destroy();
	await Promise.all(servers.splice(0).map((server) => new Promise((resolve) => server.close(resolve))));
});

/**
 * Sends a SOCKS5 greeting and CONNECT, and resolves with the reply code and
 * the open socket. `bytewise` sends them one byte at a time.
 */
function connectThrough(proxyPort: number, target: Buffer, version = 5, bytewise = false): Promise<{ code: number; socket: net.Socket }> {
	return new Promise((resolve) => {
		const socket = net.connect(proxyPort, "127.0.0.1", async () => {
			const request = Buffer.concat([Buffer.from([5, 1, 0, version, 1, 0]), target]);
			if (!bytewise) return void socket.write(request);
			socket.setNoDelay(true);
			for (const byte of request) {
				socket.write(Buffer.from([byte]));
				await new Promise((wait) => setTimeout(wait, 2));
			}
		});
		sockets.push(socket);
		// The proxy may reset a refused connection.
		socket.on("error", () => {});
		let received = Buffer.alloc(0);
		const onData = (chunk: Buffer) => {
			received = Buffer.concat([received, chunk]);
			// Method selection (2 bytes) then the 10-byte reply.
			if (received.length >= 12) {
				socket.off("data", onData);
				resolve({ code: received[3]!, socket });
			}
		};
		socket.on("data", onData);
		socket.on("close", () => resolve({ code: received[3] ?? -1, socket }));
	});
}

async function replyCode(proxyPort: number, target: Buffer, version = 5): Promise<number> {
	return (await connectThrough(proxyPort, target, version)).code;
}

function ipv4Target(address: string, port: number): Buffer {
	const target = Buffer.alloc(7);
	target[0] = 1;
	address.split(".").forEach((octet, index) => (target[1 + index] = Number(octet)));
	target.writeUInt16BE(port, 5);
	return target;
}

function ipv6Target(address: string, port: number): Buffer {
	const target = Buffer.alloc(19);
	target[0] = 4;
	const [head = "", tail = ""] = address.split("::");
	const left = head ? head.split(":") : [];
	const right = tail ? tail.split(":") : [];
	const groups = [...left, ...Array(8 - left.length - right.length).fill("0"), ...right];
	groups.forEach((group, index) => target.writeUInt16BE(Number.parseInt(group, 16), 1 + index * 2));
	target.writeUInt16BE(port, 17);
	return target;
}

/** `a.b.c.d` as the two hex groups of an IPv4-mapped IPv6 address. */
function toHexPair(address: string): string {
	const [a = 0, b = 0, c = 0, d = 0] = address.split(".").map(Number);
	return `${((a << 8) | b).toString(16)}:${((c << 8) | d).toString(16)}`;
}

function domainTarget(host: string, port: number): Buffer {
	const name = Buffer.from(host, "latin1");
	const target = Buffer.alloc(4 + name.length);
	target[0] = 3;
	target[1] = name.length;
	name.copy(target, 2);
	target.writeUInt16BE(port, 2 + name.length);
	return target;
}

/** A loopback server that echoes what it reads, standing in for the daemon. */
async function echoServer(): Promise<number> {
	const server = net.createServer((socket) => socket.pipe(socket));
	servers.push(server);
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	return (server.address() as net.AddressInfo).port;
}

describe("render-check proxy address policy", () => {
	it("refuses loopback and private targets, by address or by name", async () => {
		const port = await startRenderCheckProxy();
		for (const target of [
			ipv4Target("127.0.0.1", 80),
			ipv4Target("10.1.2.3", 80),
			ipv4Target("169.254.169.254", 80),
			ipv4Target("198.18.0.1", 80),
			domainTarget("localhost", 80),
			// IPv6 forms that carry a local IPv4 address: NAT64, 6to4, Teredo,
			// and IPv4-compatible.
			ipv6Target("64:ff9b::7f00:1", 80),
			ipv6Target("64:ff9b::a00:1", 80),
			ipv6Target("2002:7f00:1::1", 80),
			ipv6Target("2001::1", 80),
			ipv6Target("::7f00:1", 80),
		]) {
			expect(await replyCode(port, target)).toBe(2);
		}
		// Port 0 is not a connection target.
		expect(await replyCode(port, ipv4Target("1.1.1.1", 0))).toBe(7);
	});

	it("refuses every address this machine holds, even a public one", async () => {
		const port = await startRenderCheckProxy();
		const own = Object.values(os.networkInterfaces())
			.flatMap((entries) => entries ?? [])
			.filter((entry) => !entry.internal && !entry.address.startsWith("fe80"));
		expect(own.map((entry) => entry.address)).toContain(OWN_PUBLIC_IPV4);
		// IPv4 also as IPv4-mapped IPv6, which names the same host.
		const targets = own.flatMap((entry) =>
			entry.family === "IPv6"
				? [ipv6Target(entry.address, 80)]
				: [
						ipv4Target(entry.address, 80),
						ipv6Target(`::ffff:${toHexPair(entry.address)}`, 80),
						ipv6Target(`64:ff9b::${toHexPair(entry.address)}`, 80),
					],
		);
		for (const target of targets) {
			expect(await replyCode(port, target)).toBe(2);
		}
	});

	it("refuses a request with a wrong version byte", async () => {
		const port = await startRenderCheckProxy();
		expect(await replyCode(port, ipv4Target("1.1.1.1", 443), 4)).toBe(7);
	});
});

describe("render-check proxy render-page allowance", () => {
	it("reaches only the allowed page while the check runs, and reports each refusal", async () => {
		const port = await startRenderCheckProxy();
		const pagePort = await echoServer();
		const refused: string[] = [];
		const release = allowRenderPage("127.0.0.1", pagePort, (destination) => refused.push(destination));

		const { code, socket } = await connectThrough(port, ipv4Target("127.0.0.1", pagePort));
		expect(code).toBe(0);
		const echoed = new Promise<string>((resolve) => socket.once("data", (chunk) => resolve(chunk.toString())));
		socket.write("ping");
		expect(await echoed).toBe("ping");

		// Another port on the same host, and the same port by another name.
		const otherPort = pagePort === 65_535 ? pagePort - 1 : pagePort + 1;
		expect(await replyCode(port, ipv4Target("127.0.0.1", otherPort))).toBe(2);
		expect(await replyCode(port, domainTarget("localhost", pagePort))).toBe(2);
		expect(refused).toEqual([`127.0.0.1:${otherPort}`, `localhost:${pagePort}`]);

		release();
		expect(await replyCode(port, ipv4Target("127.0.0.1", pagePort))).toBe(2);
		expect(refused).toHaveLength(2);
	});

	it("keeps the page reachable until every overlapping check releases it", async () => {
		const port = await startRenderCheckProxy();
		const pagePort = await echoServer();
		const releaseFirst = allowRenderPage("127.0.0.1", pagePort, () => {});
		const releaseSecond = allowRenderPage("127.0.0.1", pagePort, () => {});
		releaseFirst();
		expect(await replyCode(port, ipv4Target("127.0.0.1", pagePort))).toBe(0);
		releaseSecond();
		expect(await replyCode(port, ipv4Target("127.0.0.1", pagePort))).toBe(2);
	});

	it("accepts a greeting and request that arrive one byte at a time", async () => {
		const port = await startRenderCheckProxy();
		const pagePort = await echoServer();
		const release = allowRenderPage("127.0.0.1", pagePort, () => {});
		expect((await connectThrough(port, ipv4Target("127.0.0.1", pagePort), 5, true)).code).toBe(0);
		release();
	});

	it("reaches a page allowed by name, at only the addresses it checked", async () => {
		const port = await startRenderCheckProxy();
		const pagePort = await echoServer();
		const release = allowRenderPage("localhost", pagePort, () => {});
		const { code, socket } = await connectThrough(port, domainTarget("localhost", pagePort));
		expect(code).toBe(0);
		const echoed = new Promise<string>((resolve) => socket.once("data", (chunk) => resolve(chunk.toString())));
		socket.write("ping");
		expect(await echoed).toBe("ping");
		// The page listens on 127.0.0.1 only. Had the proxy resolved the name
		// again when it connected, it would have found 127.0.0.1 and reached it.
		vi.mocked(lookup).mockResolvedValueOnce([{ address: "::1", family: 6 }] as never);
		expect(await replyCode(port, domainTarget("localhost", pagePort))).toBe(-1);
		release();
	});
});

describe("render-check proxy with no network", () => {
	it("reaches the allowed page and nothing else, without resolving a name", async () => {
		const port = await startRenderCheckProxy("none");
		const pagePort = await echoServer();
		const refused: string[] = [];
		const release = allowRenderPage("127.0.0.1", pagePort, (destination) => refused.push(destination));
		const { code, socket } = await connectThrough(port, ipv4Target("127.0.0.1", pagePort));
		expect(code).toBe(0);
		socket.destroy();

		const lookups = vi.mocked(lookup).mock.calls.length;
		expect(await replyCode(port, domainTarget("example.com", 443))).toBe(2);
		expect(await replyCode(port, ipv4Target("93.184.216.34", 443))).toBe(2);
		// A name sent to a "none" proxy is never looked up, so DNS carries nothing out.
		expect(vi.mocked(lookup).mock.calls.length).toBe(lookups);
		expect(refused).toEqual(["example.com:443", "93.184.216.34:443"]);
		release();
	});
});

describe("startRenderCheckProxy", () => {
	it("runs one proxy per process for each network", async () => {
		expect(await startRenderCheckProxy()).toBe(await startRenderCheckProxy("public"));
		expect(await startRenderCheckProxy("none")).toBe(await startRenderCheckProxy("none"));
		expect(await startRenderCheckProxy("none")).not.toBe(await startRenderCheckProxy());
	});

	it("fails instead of crashing when it cannot listen, and starts on the next try", async () => {
		vi.resetModules();
		const fresh = await import("./render-check-proxy");
		const exhausted = Object.assign(new Error("too many open files"), { code: "EMFILE" });
		const listen = vi.spyOn(net.Server.prototype, "listen").mockImplementationOnce(function (this: net.Server) {
			process.nextTick(() => this.emit("error", exhausted));
			return this;
		});
		await expect(fresh.startRenderCheckProxy()).rejects.toBe(exhausted);
		listen.mockRestore();
		await expect(fresh.startRenderCheckProxy()).resolves.toBeGreaterThan(0);
	});
});
