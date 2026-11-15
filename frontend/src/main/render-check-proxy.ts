import { lookup } from "node:dns/promises";
import { BlockList, connect, createServer, isIP, isIPv6, type AddressInfo, type Socket } from "node:net";
import { networkInterfaces } from "node:os";

const LOCAL_ADDRESSES = new BlockList();
for (const [network, prefix] of [
	["0.0.0.0", 8],
	["10.0.0.0", 8],
	["100.64.0.0", 10],
	["127.0.0.0", 8],
	["169.254.0.0", 16],
	["172.16.0.0", 12],
	["192.168.0.0", 16],
	["198.18.0.0", 15],
	["224.0.0.0", 3],
] as const) {
	LOCAL_ADDRESSES.addSubnet(network, prefix, "ipv4");
}
for (const [network, prefix] of [
	["::", 96],
	["64:ff9b:1::", 48],
	["2001::", 32],
	["fc00::", 7],
	["fe80::", 10],
	["ff00::", 8],
] as const) {
	LOCAL_ADDRESSES.addSubnet(network, prefix, "ipv6");
}

const NAT64 = new BlockList();
NAT64.addSubnet("64:ff9b::", 96, "ipv6");
const SIX_TO_FOUR = new BlockList();
SIX_TO_FOUR.addSubnet("2002::", 16, "ipv6");

function ipv6Groups(address: string): number[] {
	const bare = address.split("%", 1)[0]!;
	const dotted = /(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(bare);
	const text = dotted
		? `${bare.slice(0, dotted.index)}${((+dotted[1]! << 8) | +dotted[2]!).toString(16)}:${((+dotted[3]! << 8) | +dotted[4]!).toString(16)}`
		: bare;
	const [head = "", tail] = text.split("::");
	const left = head ? head.split(":") : [];
	const right = tail ? tail.split(":") : [];
	const fill = tail === undefined ? 0 : 8 - left.length - right.length;
	return [...left, ...Array<string>(fill).fill("0"), ...right].map((group) => Number.parseInt(group, 16));
}

function embeddedIPv4(address: string): string | undefined {
	const at = NAT64.check(address, "ipv6") ? 6 : SIX_TO_FOUR.check(address, "ipv6") ? 1 : -1;
	if (at === -1) return undefined;
	const groups = ipv6Groups(address);
	const high = groups[at] ?? 0;
	const low = groups[at + 1] ?? 0;
	return [high >> 8, high & 0xff, low >> 8, low & 0xff].join(".");
}

const OWN_ADDRESSES_TTL_MS = 5_000;
let ownAddressesCache: { list: BlockList; at: number } | undefined;

function ownAddresses(): BlockList {
	const now = Date.now();
	if (ownAddressesCache && now - ownAddressesCache.at < OWN_ADDRESSES_TTL_MS) return ownAddressesCache.list;
	const list = new BlockList();
	for (const entry of Object.values(networkInterfaces()).flat()) {
		if (entry) list.addAddress(entry.address, entry.family === "IPv6" ? "ipv6" : "ipv4");
	}
	ownAddressesCache = { list, at: now };
	return list;
}

export function isLocal(address: string, family: number): boolean {
	const type = family === 6 ? "ipv6" : "ipv4";
	if (LOCAL_ADDRESSES.check(address, type) || ownAddresses().check(address, type)) return true;
	const embedded = family === 6 ? embeddedIPv4(address) : undefined;
	return embedded !== undefined && isLocal(embedded, 4);
}

const destination = (host: string, port: number) => (isIPv6(host) ? `[${host}]:${port}` : `${host}:${port}`);

const allowed = new Map<string, number>();
const refusalListeners = new Set<(destination: string) => void>();

export function allowRenderPage(host: string, port: number, onRefused: (destination: string) => void): () => void {
	const key = destination(host, port);
	allowed.set(key, (allowed.get(key) ?? 0) + 1);
	refusalListeners.add(onRefused);
	return () => {
		const count = (allowed.get(key) ?? 1) - 1;
		if (count > 0) allowed.set(key, count);
		else allowed.delete(key);
		refusalListeners.delete(onRefused);
	};
}

export type RenderNetwork = "public" | "none";

async function addressesFor(host: string, port: number, network: RenderNetwork) {
	const isAllowed = allowed.has(destination(host, port));
	if (!isAllowed && network === "none") return "refused";
	const family = isIP(host);
	const addresses = family
		? [{ address: host, family }]
		: await lookup(host, { all: true, verbatim: true }).catch(() => []);
	if (addresses.length === 0 || isAllowed) return addresses;
	return addresses.some(({ address, family }) => isLocal(address, family)) ? "refused" : addresses;
}

const reply = (code: number) => Buffer.from([5, code, 0, 1, 0, 0, 0, 0, 0, 0]);

const MAX_EARLY_BYTES = 64 * 1024;

function refuse(client: Socket, code: number): void {
	client.end(reply(code), () => client.destroy());
}

function readRequest(data: Buffer) {
	if (data.length < 5) return "short" as const;
	if (data[0] !== 5 || data[2] !== 0) return undefined;
	const type = data[3];
	const end = type === 1 ? 10 : type === 3 ? 7 + data[4]! : type === 4 ? 22 : -1;
	if (end === -1) return undefined;
	if (data.length < end) return "short" as const;
	const host =
		type === 1
			? [...data.subarray(4, 8)].join(".")
			: type === 3
				? data.subarray(5, 5 + data[4]!).toString("latin1")
				: Array.from({ length: 8 }, (_, index) => data.readUInt16BE(4 + index * 2).toString(16)).join(":");
	return { command: data[1], host, port: data.readUInt16BE(end - 2), rest: data.subarray(end) };
}

function track(socket: Socket): Socket {
	socket.on("error", () => socket.destroy());
	return socket;
}

function serve(client: Socket, network: RenderNetwork): void {
	track(client);
	let data = Buffer.alloc(0);
	let greeted = false;
	const onData = (chunk: Buffer) => {
		data = Buffer.concat([data, chunk]);
		if (!greeted) {
			if (data.length < 2 || data.length < 2 + data[1]!) return;
			if (data[0] !== 5 || !data.subarray(2, 2 + data[1]!).includes(0)) {
				return void client.end(Buffer.from([5, 0xff]));
			}
			data = data.subarray(2 + data[1]!);
			greeted = true;
			client.write(Buffer.from([5, 0]));
		}
		const request = readRequest(data);
		if (request === "short") return;
		client.off("data", onData);
		if (request === undefined || request.command !== 1 || request.port === 0) {
			return refuse(client, 7);
		}
		const early: Buffer[] = [request.rest];
		let earlyBytes = request.rest.length;
		const holdEarly = (chunk: Buffer) => {
			earlyBytes += chunk.length;
			if (earlyBytes > MAX_EARLY_BYTES) return void client.destroy();
			early.push(chunk);
		};
		client.on("data", holdEarly);
		void addressesFor(request.host, request.port, network).then((addresses) => {
			if (client.destroyed) return;
			client.off("data", holdEarly);
			if (addresses === "refused") {
				const refused = destination(request.host, request.port);
				for (const listener of refusalListeners) listener(refused);
				return refuse(client, 2);
			}
			if (addresses.length === 0) return refuse(client, 4);
			client.pause();
			const upstream = track(
				connect({
					host: request.host,
					port: request.port,
					autoSelectFamily: true,
					lookup: (_host, options, callback) =>
						options.all
							? callback(null, addresses)
							: callback(null, addresses[0]!.address, addresses[0]!.family),
				}),
			);
			upstream.once("connect", () => {
				client.write(reply(0));
				for (const chunk of early) upstream.write(chunk);
				upstream.pipe(client);
				client.pipe(upstream);
			});
			upstream.on("close", () => client.destroy());
			client.on("close", () => upstream.destroy());
		}).catch(() => client.destroy());
	};
	client.on("data", onData);
}

const started = new Map<RenderNetwork, Promise<number>>();

export function startRenderCheckProxy(network: RenderNetwork = "public"): Promise<number> {
	let listening = started.get(network);
	if (!listening) {
		listening = new Promise<number>((resolve, reject) => {
			const server = createServer((client) => serve(client, network));
			server.on("error", reject);
			server.listen(0, "127.0.0.1", () => resolve((server.address() as AddressInfo).port));
		}).catch((error: unknown) => {
			started.delete(network);
			throw error;
		});
		started.set(network, listening);
	}
	return listening;
}
