import { AoError } from "../../../../packages/shared/telemetry/ao-error";

// Canvas players for the Device tab, the same pipeline the phone uses natively:
// Android's H.264 decoded by WebCodecs, iOS's MJPEG parsed and drawn newest
// frame first. An <img> showing MJPEG paints a part only once the next starts,
// so every frame showed a frame late; screenshots refreshed twice a second.

export type Nal = { type: number; start: number; end: number };

/** Splits an Annex-B access unit into its NAL units ([start, end) excludes the start code). */
export function findNals(data: Uint8Array): Nal[] {
	const starts: number[] = [];
	for (let i = 0; i + 2 < data.length; i++) {
		if (data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 1) {
			starts.push(i + 3);
			i += 2;
		}
	}
	return starts.map((start, index) => {
		let end = index + 1 < starts.length ? starts[index + 1] - 3 : data.length;
		while (end > start && data[end - 1] === 0) end--;
		return { type: data[start] & 0x1f, start, end };
	}).filter((nal) => nal.end > nal.start);
}

const SEMU = 0x53454d55;

/** Strips the hub's SEMU header (v1: 16 bytes, v2: 24) from a video message; raw Annex-B passes through. */
export function framePayload(bytes: Uint8Array): Uint8Array {
	if (bytes.length > 24) {
		const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
		if (view.getUint32(0) === SEMU) return bytes.subarray(view.getUint8(4) === 1 ? 16 : 24);
	}
	return bytes;
}

/** The WebCodecs codec string for an SPS NAL: avc1.PPCCLL (profile, constraints, level). */
export function avcCodecString(data: Uint8Array, sps: Nal): string {
	const hex = (n: number) => n.toString(16).padStart(2, "0");
	return `avc1.${hex(data[sps.start + 1])}${hex(data[sps.start + 2])}${hex(data[sps.start + 3])}`;
}

/** Splits a multipart/x-mixed-replace byte stream into its parts (by Content-Length). */
export class MultipartParser {
	private buffer = new Uint8Array(0);

	feed(chunk: Uint8Array): Uint8Array[] {
		const merged = new Uint8Array(this.buffer.length + chunk.length);
		merged.set(this.buffer);
		merged.set(chunk, this.buffer.length);
		this.buffer = merged;
		const parts: Uint8Array[] = [];
		for (;;) {
			const headerEnd = indexOfBlankLine(this.buffer);
			if (headerEnd < 0) break;
			const header = new TextDecoder().decode(this.buffer.subarray(0, headerEnd));
			const length = Number(/content-length:\s*(\d+)/i.exec(header)?.[1] ?? -1);
			if (length < 0) {
				this.buffer = this.buffer.subarray(headerEnd + 4);
				continue;
			}
			const end = headerEnd + 4 + length;
			if (this.buffer.length < end) break;
			parts.push(this.buffer.slice(headerEnd + 4, end));
			this.buffer = this.buffer.subarray(end);
		}
		return parts;
	}
}

function indexOfBlankLine(data: Uint8Array): number {
	for (let i = 0; i + 3 < data.length; i++) {
		if (data[i] === 13 && data[i + 1] === 10 && data[i + 2] === 13 && data[i + 3] === 10) return i;
	}
	return -1;
}

export type StreamPlayer = { stop(): void };

function drawTo(canvas: HTMLCanvasElement, image: CanvasImageSource, width: number, height: number) {
	if (canvas.width !== width || canvas.height !== height) {
		canvas.width = width;
		canvas.height = height;
	}
	canvas.getContext("2d")?.drawImage(image, 0, 0, width, height);
}

/**
 * Plays the emulator's H.264 WebSocket stream onto a canvas with WebCodecs, or
 * returns null where VideoDecoder is missing. After a decode error or a backlog
 * it waits for the next key frame (and asks for one) rather than smear.
 */
export function playH264(url: string, canvas: HTMLCanvasElement, onFrame: () => void): StreamPlayer | null {
	if (typeof VideoDecoder === "undefined") return null;
	let stopped = false;
	let decoder: VideoDecoder | null = null;
	let codec = "";
	let waitingForKey = true;
	let timestamp = 0;
	let lastKeyRequest = 0;
	let socket: WebSocket | null = null;
	let retry: ReturnType<typeof setTimeout> | undefined;
	const requestKey = () => {
		const now = Date.now();
		if (now - lastKeyRequest < 1000 || socket?.readyState !== WebSocket.OPEN) return;
		lastKeyRequest = now;
		socket.send(JSON.stringify({ type: "reset-video" }));
	};
	const output = (frame: VideoFrame) => {
		if (!stopped) {
			drawTo(canvas, frame, frame.displayWidth, frame.displayHeight);
			onFrame();
		}
		frame.close();
	};
	const connect = () => {
		const ws = new WebSocket(url);
		socket = ws;
		ws.binaryType = "arraybuffer";
		ws.onmessage = (event) => {
			if (!(event.data instanceof ArrayBuffer)) return;
			const data = framePayload(new Uint8Array(event.data));
			const nals = findNals(data);
			const sps = nals.find((nal) => nal.type === 7);
			const isKey = nals.some((nal) => nal.type === 5);
			if (sps) {
				const next = avcCodecString(data, sps);
				if (next !== codec || !decoder || decoder.state === "closed") {
					if (decoder && decoder.state !== "closed") decoder.close();
					decoder = new VideoDecoder({ output, error: () => { decoder = null; waitingForKey = true; requestKey(); } });
					decoder.configure({ codec: next, optimizeForLatency: true });
					codec = next;
					waitingForKey = true;
				}
			}
			if (!decoder || decoder.state !== "configured" || (waitingForKey && !isKey)) {
				requestKey();
				return;
			}
			// Behind: drop to the next key frame instead of playing a backlog late.
			if (!isKey && decoder.decodeQueueSize > 3) {
				waitingForKey = true;
				requestKey();
				return;
			}
			timestamp += 16_667;
			decoder.decode(new EncodedVideoChunk({ type: isKey ? "key" : "delta", timestamp, data }));
			if (isKey) waitingForKey = false;
		};
		ws.onclose = () => {
			if (stopped) return;
			waitingForKey = true;
			retry = setTimeout(connect, 1_500);
		};
		ws.onerror = () => ws.close();
	};
	connect();
	return {
		stop() {
			stopped = true;
			if (retry) clearTimeout(retry);
			socket?.close();
			if (decoder && decoder.state !== "closed") decoder.close();
		},
	};
}

/**
 * Plays the simulator's MJPEG stream onto a canvas: parts are parsed as they
 * arrive and only the newest is decoded and drawn, so a slow decode skips a
 * frame instead of falling behind.
 */
export function playMjpeg(url: string, canvas: HTMLCanvasElement, onFrame: () => void): StreamPlayer {
	let stopped = false;
	let controller: AbortController | null = null;
	let retry: ReturnType<typeof setTimeout> | undefined;
	let latest: Uint8Array | null = null;
	let drawing = false;
	const drawLatest = async () => {
		if (drawing) return;
		drawing = true;
		while (latest && !stopped) {
			const part = latest;
			latest = null;
			try {
				const bitmap = await createImageBitmap(new Blob([part as BlobPart], { type: "image/jpeg" }));
				if (!stopped) {
					drawTo(canvas, bitmap, bitmap.width, bitmap.height);
					onFrame();
				}
				bitmap.close();
			} catch {
				// A broken part: the next one replaces it.
			}
		}
		drawing = false;
	};
	const connect = async () => {
		controller = new AbortController();
		try {
			const res = await fetch(url, { cache: "no-store", signal: controller.signal });
			if (!res.ok || !res.body) throw new AoError("DEVICE_STREAM_FAILED", `stream ${res.status}`);
			const reader = res.body.getReader();
			const parser = new MultipartParser();
			for (;;) {
				const { done, value } = await reader.read();
				if (done || stopped) break;
				const parts = parser.feed(value);
				if (parts.length) {
					latest = parts[parts.length - 1];
					void drawLatest();
				}
			}
		} catch {
			// Closed by stop() or lost.
		}
		if (!stopped) retry = setTimeout(() => void connect(), 1_500);
	};
	void connect();
	return {
		stop() {
			stopped = true;
			if (retry) clearTimeout(retry);
			controller?.abort();
		},
	};
}
