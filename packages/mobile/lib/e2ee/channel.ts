import { E2EEError, MAX_PLAIN, startHandshake } from "./handshake";
import { loadRandomBytes } from "./random";

/** An encrypted byte stream to one machine. */
export type Channel = {
	write(bytes: Uint8Array): void;
	close(): void;
	readonly closed: boolean;
	/** Set once; bytes that arrive before it is set are held for it. */
	onData: ((bytes: Uint8Array) => void) | null;
	onClose: ((error?: Error) => void) | null;
};

export type ChannelDeps = {
	WebSocket: new (url: string) => WebSocket;
	randomBytes: (count: number) => Uint8Array;
};

export const HANDSHAKE_TIMEOUT_MS = 10_000;

/** Opens `url` (the host's /api/v1/e2ee) and checks the host holds `hostKey`. */
export async function openChannel(url: string, hostKey: Uint8Array, deps: Partial<ChannelDeps> = {}): Promise<Channel> {
	const randomBytes = deps.randomBytes ?? (await loadRandomBytes());
	const Socket = deps.WebSocket ?? WebSocket;
	return new Promise((resolve, reject) => {
		const handshake = startHandshake(randomBytes(32));
		const ws = new Socket(url);
		ws.binaryType = "arraybuffer";
		let records: ReturnType<typeof handshake.finish> | null = null;
		let pending: Uint8Array[] = [];
		let closed = false;
		const timer = setTimeout(() => fail(new E2EEError("The machine did not answer the encrypted connection.")), HANDSHAKE_TIMEOUT_MS);

		const channel: Channel = {
			write(bytes) {
				if (closed || !records) return;
				for (let at = 0; at < bytes.length; at += MAX_PLAIN) {
					ws.send(records.toHost.seal(bytes.subarray(at, at + MAX_PLAIN)));
				}
			},
			close() {
				fail();
			},
			get closed() {
				return closed;
			},
			onClose: null,
			get onData() {
				return deliver;
			},
			set onData(handler) {
				deliver = handler;
				if (!handler) return;
				const held = pending;
				pending = [];
				for (const bytes of held) handler(bytes);
			},
		};
		let deliver: Channel["onData"] = null;

		function fail(error?: Error) {
			clearTimeout(timer);
			if (closed) return;
			closed = true;
			try {
				ws.close();
			} catch {
				// Already closing.
			}
			if (records) channel.onClose?.(error);
			else reject(error ?? new E2EEError("The encrypted connection closed."));
		}

		ws.onopen = () => ws.send(handshake.hello);
		ws.onerror = () => fail(new E2EEError("The encrypted connection failed."));
		ws.onclose = () => fail();
		ws.onmessage = (event: MessageEvent) => {
			if (closed) return;
			if (!(event.data instanceof ArrayBuffer)) return fail(new E2EEError("The machine sent text on the encrypted connection."));
			const message = new Uint8Array(event.data);
			try {
				if (!records) {
					records = handshake.finish(message, hostKey);
					clearTimeout(timer);
					resolve(channel);
					return;
				}
				const plain = records.toPhone.open(message);
				if (deliver) deliver(plain);
				else pending.push(plain);
			} catch (error) {
				fail(error instanceof E2EEError ? error : new E2EEError("A record from the machine did not decrypt."));
			}
		};
	});
}
