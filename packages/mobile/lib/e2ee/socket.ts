import { sha1 } from "@noble/hashes/legacy.js";
import { E2EEError } from "./handshake";
import { openWire, readHead, requestHead, type SecureTarget, type Wire } from "./http";
import { loadRandomBytes } from "./random";

const ACCEPT_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";
const MAX_MESSAGE = 64 * 1024 * 1024;

const base64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes));

/** A client frame: masked, as RFC 6455 requires of clients. */
function frame(opcode: number, payload: Uint8Array, mask: Uint8Array): Uint8Array {
	const ext = payload.length < 126 ? 0 : payload.length < 65536 ? 2 : 8;
	const out = new Uint8Array(2 + ext + 4 + payload.length);
	const view = new DataView(out.buffer);
	out[0] = 0x80 | opcode;
	out[1] = 0x80 | (ext === 0 ? payload.length : ext === 2 ? 126 : 127);
	if (ext === 2) view.setUint16(2, payload.length);
	if (ext === 8) {
		view.setUint32(2, Math.floor(payload.length / 2 ** 32));
		view.setUint32(6, payload.length >>> 0);
	}
	out.set(mask, 2 + ext);
	for (let i = 0; i < payload.length; i++) out[2 + ext + 4 + i] = payload[i] ^ mask[i & 3];
	return out;
}

/** The parts of WebSocket that mux.ts uses, over an encrypted channel. */
export class SecureWebSocket {
	static readonly CONNECTING = 0;
	static readonly OPEN = 1;
	static readonly CLOSING = 2;
	static readonly CLOSED = 3;

	readyState = SecureWebSocket.CONNECTING;
	onopen: ((event: object) => void) | null = null;
	onmessage: ((event: { data: string | ArrayBuffer }) => void) | null = null;
	onerror: ((event: object) => void) | null = null;
	onclose: ((event: { code: number; reason: string }) => void) | null = null;
	private wire: Wire | null = null;
	private randomBytes: (count: number) => Uint8Array = () => {
		throw new Error("WebSocket is not open");
	};

	constructor(
		target: SecureTarget,
		path: string,
		headers: Record<string, string>,
		connect: (target: SecureTarget) => Promise<Wire> = openWire,
	) {
		void this.run(target, path, headers, connect);
	}

	send(data: string | Uint8Array | ArrayBuffer): void {
		if (this.readyState !== SecureWebSocket.OPEN || !this.wire) throw new Error("WebSocket is not open");
		this.wire.write(typeof data === "string" ? frame(1, new TextEncoder().encode(data), this.randomBytes(4)) : frame(2, data instanceof ArrayBuffer ? new Uint8Array(data) : data, this.randomBytes(4)));
	}

	close(code = 1000, reason = ""): void {
		if (this.readyState >= SecureWebSocket.CLOSING) return;
		const wire = this.wire;
		if (wire && this.readyState === SecureWebSocket.OPEN) {
			const payload = new Uint8Array(2 + new TextEncoder().encode(reason).length);
			new DataView(payload.buffer).setUint16(0, code);
			payload.set(new TextEncoder().encode(reason), 2);
			wire.write(frame(8, payload, this.randomBytes(4)));
		}
		this.finish(code, reason);
	}

	private finish(code: number, reason: string) {
		if (this.readyState === SecureWebSocket.CLOSED) return;
		this.readyState = SecureWebSocket.CLOSED;
		this.wire?.close();
		this.onclose?.({ code, reason });
	}

	private async run(target: SecureTarget, path: string, headers: Record<string, string>, connect: (target: SecureTarget) => Promise<Wire>) {
		try {
			this.randomBytes = await loadRandomBytes();
			const wire = await connect(target);
			if (this.readyState !== SecureWebSocket.CONNECTING) return wire.close();
			this.wire = wire;
			const key = base64(this.randomBytes(16));
			wire.write(requestHead(target.authority, "GET", path, headers, {
				Upgrade: "websocket",
				Connection: "Upgrade",
				"Sec-WebSocket-Key": key,
				"Sec-WebSocket-Version": "13",
			}));
			const head = await readHead(wire);
			if (head.status !== 101) throw new E2EEError(`The machine refused the WebSocket with ${head.status}.`);
			if (head.headers.get("sec-websocket-accept") !== base64(sha1(new TextEncoder().encode(key + ACCEPT_GUID)))) {
				throw new E2EEError("The machine sent a wrong WebSocket accept key.");
			}
			if (this.readyState !== SecureWebSocket.CONNECTING) return;
			this.readyState = SecureWebSocket.OPEN;
			this.onopen?.({});
			await this.readFrames(wire);
		} catch {
			if (this.readyState !== SecureWebSocket.CLOSED) this.onerror?.({});
			this.finish(1006, "");
		}
	}

	private async readFrames(wire: Wire) {
		let fragments: Uint8Array[] = [];
		let fragmentOpcode = 0;
		let fragmentLength = 0;
		while (this.readyState === SecureWebSocket.OPEN) {
			const head = await wire.readExactly(2);
			const fin = (head[0] & 0x80) !== 0;
			const opcode = head[0] & 0x0f;
			let length = head[1] & 0x7f;
			if (length === 126) length = new DataView((await wire.readExactly(2)).buffer).getUint16(0);
			else if (length === 127) {
				const view = new DataView((await wire.readExactly(8)).buffer);
				length = view.getUint32(0) * 2 ** 32 + view.getUint32(4);
			}
			if (length > MAX_MESSAGE) throw new E2EEError("The machine sent a message that is too large.");
			const mask = head[1] & 0x80 ? await wire.readExactly(4) : null;
			const payload = await wire.readExactly(length);
			if (mask) for (let i = 0; i < payload.length; i++) payload[i] ^= mask[i & 3];

			if (opcode === 8) {
				const code = payload.length >= 2 ? new DataView(payload.buffer).getUint16(0) : 1005;
				const reason = new TextDecoder().decode(payload.subarray(2));
				wire.write(frame(8, payload.subarray(0, 2), this.randomBytes(4)));
				return this.finish(code, reason);
			}
			if (opcode === 9) {
				wire.write(frame(10, payload, this.randomBytes(4)));
				continue;
			}
			if (opcode === 10) continue;
			if (opcode !== 0) {
				fragmentOpcode = opcode;
				fragments = [];
				fragmentLength = 0;
			}
			fragments.push(payload);
			fragmentLength += payload.length;
			if (fragmentLength > MAX_MESSAGE) throw new E2EEError("The machine sent a message that is too large.");
			if (!fin) continue;
			const message = new Uint8Array(fragmentLength);
			let at = 0;
			for (const piece of fragments) {
				message.set(piece, at);
				at += piece.length;
			}
			fragments = [];
			fragmentLength = 0;
			this.onmessage?.({ data: fragmentOpcode === 1 ? new TextDecoder().decode(message) : message.buffer });
		}
	}
}
