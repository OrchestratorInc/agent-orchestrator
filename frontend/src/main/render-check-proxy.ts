// The render-check window's only way out: a loopback SOCKS5 listener (RFC 1928,
// CONNECT with no authentication) that opens a connection only to a public
// address or to the render page a running check allowed.
import type { LookupAddress } from "node:dns";
import { lookup } from "node:dns/promises";
import { connect, createServer, isIP, type AddressInfo, type Socket } from "node:net";
import { networkInterfaces } from "node:os";
import ipaddr from "ipaddr.js";

/**
 * The network a listener lets pages reach besides the allowed render pages:
 * the public internet, or nothing, for an agent whose own sandbox has none.
 */
export type RenderNetwork = "public" | "none";

const REPLY_OK = 0x00;
const REPLY_NOT_ALLOWED = 0x02;
const REPLY_HOST_UNREACHABLE = 0x04;
const REPLY_COMMAND_UNSUPPORTED = 0x07;
const REPLY_ADDRESS_UNSUPPORTED = 0x08;

/** Bytes a client may send before its tunnel is up; a TLS ClientHello is far smaller. */
const MAX_BUFFERED_BYTES = 64 * 1024;

/** How long this machine's interface addresses are reused; pages ask about every request. */
const INTERFACE_CACHE_MS = 5_000;

let interfaceCache: { addresses: Set<string>; expires: number } | undefined;

function interfaceAddresses(): Set<string> {
	const now = Date.now();
	if (!interfaceCache || now >= interfaceCache.expires) {
		const addresses = new Set<string>();
		for (const entries of Object.values(networkInterfaces())) {
			for (const entry of entries ?? []) {
				const normalized = normalizeAddress(entry.address);
				if (normalized) addresses.add(normalized);
			}
		}
		interfaceCache = { addresses, expires: now + INTERFACE_CACHE_MS };
	}
	return interfaceCache.addresses;
}

function normalizeAddress(address: string): string | undefined {
	const bare = address.split("%", 1)[0]!;
	return ipaddr.isValid(bare) ? ipaddr.process(bare).toNormalizedString() : undefined;
}

/** The IPv4 address carried in the low 32 bits of `parts`, starting at `index`. */
function ipv4At(parts: number[], index: number): ipaddr.IPv4 {
	const high = parts[index]!;
	const low = parts[index + 1]!;
	return new ipaddr.IPv4([high >> 8, high & 0xff, low >> 8, low & 0xff]);
}

/**
 * Whether `address` is this machine or a network a page must not reach:
 * anything outside public unicast, or a public address one of this machine's
 * interfaces holds. IPv6 forms a translator routes to an IPv4 address
 * (IPv4-mapped, NAT64, 6to4) are judged by that IPv4 address, so a public
 * NAT64 target still works on an IPv6-only network.
 */
export function isLocalAddress(address: string): boolean {
	const bare = address.split("%", 1)[0]!;
	if (!ipaddr.isValid(bare)) return true;
	const parsed = ipaddr.process(bare);
	if (interfaceAddresses().has(parsed.toNormalizedString())) return true;
	if (parsed.kind() === "ipv4") return parsed.range() !== "unicast";
	const ipv6 = parsed as ipaddr.IPv6;
	const parts = ipv6.parts;
	// IPv4-compatible (::/96), deprecated but still routed by some stacks.
	if (parts.slice(0, 6).every((part) => part === 0)) return true;
	switch (ipv6.range()) {
		case "unicast":
			return false;
		case "rfc6052":
			// Only the well-known prefix 64:ff9b::/96; the local-use prefix stays local.
			return !(parts[0] === 0x64 && parts[1] === 0xff9b && parts.slice(2, 6).every((part) => part === 0)) || isLocalAddress(ipv4At(parts, 6).toString());
		case "6to4":
			return isLocalAddress(ipv4At(parts, 1).toString());
		default:
			return true;
	}
}

function hostKey(host: string, port: number): string {
	return `${isIP(host) === 6 ? `[${host}]` : host}:${port}`;
}

/** Render pages open checks may reach, by host and port exactly as a request names them. */
const allowedPages = new Map<string, number>();
// ponytail: checks share one listener per network and Chromium's SOCKS5 sends no
// credentials, so a refusal is reported to every open check, not just its own.
const refusalListeners = new Set<(destination: string) => void>();

/**
 * Lets the listeners reach one render page until the returned function is
 * called, and reports each destination they refuse meanwhile. Overlapping
 * checks of the same page each hold it open.
 */
export function allowRenderPage(host: string, port: number, onRefused: (destination: string) => void): () => void {
	const key = hostKey(host, port);
	allowedPages.set(key, (allowedPages.get(key) ?? 0) + 1);
	const listener = (destination: string) => onRefused(destination);
	refusalListeners.add(listener);
	let released = false;
	return () => {
		if (released) return;
		released = true;
		refusalListeners.delete(listener);
		const remaining = (allowedPages.get(key) ?? 1) - 1;
		if (remaining > 0) allowedPages.set(key, remaining);
		else allowedPages.delete(key);
	};
}

type Target = { host: string; port: number };
type Decision = { kind: "refused" } | { kind: "unreachable" } | { kind: "connect"; addresses: LookupAddress[] };

/**
 * Where a CONNECT may go. The listener connects only to the addresses checked
 * here, so a name that resolves differently later (DNS rebinding) gains nothing.
 * On a "none" listener nothing but an allowed page is looked up, so a page cannot
 * send data out through DNS either.
 */
async function decide({ host, port }: Target, network: RenderNetwork): Promise<Decision> {
	const allowed = allowedPages.has(hostKey(host, port));
	if (!allowed && network === "none") return { kind: "refused" };
	const family = isIP(host);
	const addresses: LookupAddress[] = family
		? [{ address: host, family }]
		: await lookup(host, { all: true, verbatim: true }).catch(() => []);
	if (addresses.length === 0) return { kind: "unreachable" };
	if (!allowed && addresses.some(({ address }) => isLocalAddress(address))) return { kind: "refused" };
	return { kind: "connect", addresses };
}

class ClosedError extends Error {}

/**
 * Collects what a client sends and hands it out in exact lengths, so the
 * handshake reads like the RFC's field list however the bytes arrive.
 */
class SocketReader {
	private buffered = Buffer.alloc(0);
	private waiting: { length: number; resolve: (bytes: Buffer) => void; reject: (error: Error) => void } | undefined;
	private closed = false;

	constructor(private readonly socket: Socket) {
		socket.on("data", this.onData);
		socket.once("close", () => {
			this.closed = true;
			this.waiting?.reject(new ClosedError());
			this.waiting = undefined;
		});
	}

	private readonly onData = (chunk: Buffer) => {
		this.buffered = Buffer.concat([this.buffered, chunk]);
		if (this.buffered.length > MAX_BUFFERED_BYTES) {
			this.socket.destroy();
			return;
		}
		this.settle();
	};

	private settle(): void {
		if (!this.waiting || this.buffered.length < this.waiting.length) return;
		const { length, resolve } = this.waiting;
		this.waiting = undefined;
		const bytes = this.buffered.subarray(0, length);
		this.buffered = this.buffered.subarray(length);
		resolve(bytes);
	}

	read(length: number): Promise<Buffer> {
		if (this.closed) return Promise.reject(new ClosedError());
		return new Promise((resolve, reject) => {
			this.waiting = { length, resolve, reject };
			this.settle();
		});
	}

	async byte(): Promise<number> {
		return (await this.read(1))[0]!;
	}

	/** Stops reading and returns whatever arrived beyond the handshake. */
	detach(): Buffer {
		this.socket.off("data", this.onData);
		return this.buffered;
	}
}

function reply(socket: Socket, code: number): void {
	socket.write(Buffer.from([0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0]));
}

function refuse(socket: Socket, code: number): void {
	socket.end(Buffer.from([0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0]), () => socket.destroy());
}

/** Reads the greeting and request; undefined means the client was answered and closed. */
async function readRequest(client: Socket, reader: SocketReader): Promise<Target | undefined> {
	const version = await reader.byte();
	const methods = await reader.read(await reader.byte());
	if (version !== 0x05 || !methods.includes(0x00)) {
		client.end(Buffer.from([0x05, 0xff]), () => client.destroy());
		return undefined;
	}
	client.write(Buffer.from([0x05, 0x00]));

	const [requestVersion, command, reserved, addressType] = await reader.read(4);
	let host: string;
	if (addressType === 0x01) host = ipaddr.fromByteArray([...(await reader.read(4))]).toString();
	else if (addressType === 0x04) host = ipaddr.fromByteArray([...(await reader.read(16))]).toString();
	else if (addressType === 0x03) host = (await reader.read(await reader.byte())).toString("latin1");
	else {
		refuse(client, REPLY_ADDRESS_UNSUPPORTED);
		return undefined;
	}
	const port = (await reader.read(2)).readUInt16BE(0);
	if (requestVersion !== 0x05 || command !== 0x01 || reserved !== 0x00 || port === 0) {
		refuse(client, REPLY_COMMAND_UNSUPPORTED);
		return undefined;
	}
	return { host, port };
}

function guard(socket: Socket): Socket {
	socket.on("error", () => socket.destroy());
	return socket;
}

async function serve(client: Socket, network: RenderNetwork): Promise<void> {
	guard(client);
	const reader = new SocketReader(client);
	const target = await readRequest(client, reader);
	if (!target) return;
	const decision = await decide(target, network);
	if (client.destroyed) return;
	if (decision.kind === "refused") {
		const destination = hostKey(target.host, target.port);
		for (const listener of refusalListeners) listener(destination);
		return refuse(client, REPLY_NOT_ALLOWED);
	}
	if (decision.kind === "unreachable") return refuse(client, REPLY_HOST_UNREACHABLE);

	const { addresses } = decision;
	// Every checked address is tried (Happy Eyeballs), so a host stays reachable
	// where one family's route is broken; lookup hands back only those addresses.
	const upstream = guard(
		connect({
			host: target.host,
			port: target.port,
			autoSelectFamily: true,
			lookup: (_hostname, options, callback) => {
				if (options.all) callback(null, addresses);
				else callback(null, addresses[0]!.address, addresses[0]!.family);
			},
		}),
	);
	client.once("close", () => upstream.destroy());
	upstream.once("close", () => client.destroy());
	upstream.once("connect", () => {
		reply(client, REPLY_OK);
		const early = reader.detach();
		if (early.length > 0) upstream.write(early);
		client.pipe(upstream);
		upstream.pipe(client);
	});
}

const listeners = new Map<RenderNetwork, Promise<number>>();

/**
 * The loopback port of the listener for `network`, started on first use and
 * kept for the life of the process. Each network has its own listener because
 * Chromium's SOCKS5 connections carry nothing that tells checks apart. It
 * relays bytes only, so HTTP, TLS and WebSockets pass through unchanged.
 */
export function startRenderCheckProxy(network: RenderNetwork = "public"): Promise<number> {
	let port = listeners.get(network);
	if (!port) {
		port = new Promise<number>((resolve, reject) => {
			const server = createServer((client) => {
				serve(client, network).catch(() => client.destroy());
			});
			// Listening can fail (EMFILE, say); that check fails and the next one retries.
			server.once("error", reject);
			server.listen(0, "127.0.0.1", () => {
				server.off("error", reject);
				server.on("error", () => {});
				resolve((server.address() as AddressInfo).port);
			});
		});
		port.catch(() => listeners.delete(network));
		listeners.set(network, port);
	}
	return port;
}
