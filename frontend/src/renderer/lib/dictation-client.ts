import { aoBridge } from "./bridge";
import { apiClient, getApiBaseUrl } from "./api-client";
import type { DictationEvent } from "../../shared/dictation";
import workletURL from "./dictation-worklet.js?url&no-inline";

const listeners = new Set<(event: DictationEvent) => void>();
let active: { native?: boolean; stop(): Promise<void>; cancel(): void } | undefined;
let generation = 0;
let finishRequested = false;
const emit = (event: DictationEvent) => {
	for (const listener of listeners) listener(event);
};

export const dictationClient = {
	/**
	 * Asks macOS for the speech and microphone access native dictation needs,
	 * showing its prompts now. Always granted off macOS and for local models.
	 */
	authorize() {
		return aoBridge.dictation.authorize();
	},
	onEvent(listener: (event: DictationEvent) => void) {
		listeners.add(listener);
		const unsubscribe = aoBridge.dictation.onEvent((event) => {
			if (active?.native) listener(event);
		});
		return () => {
			listeners.delete(listener);
			unsubscribe();
		};
	},
	async start() {
		active?.cancel();
		active = undefined;
		const started = ++generation;
		finishRequested = false;
		const base = new URL(getApiBaseUrl() || window.location.origin);
		if (!["127.0.0.1", "localhost", "[::1]"].includes(base.hostname))
			throw new Error("Dictation requires the local desktop daemon.");
		const { data, error } = await apiClient.GET("/api/v1/dictation");
		if (started !== generation) return;
		if (error || !data) throw new Error("Could not load dictation settings.");
		if (data.selected === "native-macos") {
			active = {
				native: true,
				stop: () => aoBridge.dictation.stop(),
				cancel: () => {
					void aoBridge.dictation.cancel();
				},
			};
			await aoBridge.dictation.start();
			if (started !== generation) return;
			if (finishRequested) await active?.stop();
			return;
		}
		if (!data.models.some((model) => model.id === data.selected && model.installed))
			throw new Error("Install a model in Settings → Dictation first.");
		base.pathname = "/api/v1/dictation/stream";
		base.protocol = base.protocol === "https:" ? "wss:" : "ws:";
		base.search = "";
		base.hash = "";
		const socket = new WebSocket(base);
		let stream: MediaStream | undefined;
		let context: AudioContext | undefined;
		let node: AudioWorkletNode | undefined;
		let finished = false;
		let stopping = false;
		let engineReady = false;
		let stopAfterFlush = false;
		let stopSent = false;
		let queued: ArrayBuffer[] = [];
		let queuedBytes = 0;
		let capturedBytes = 0;
		let pumpTimer: number | undefined;
		let flush: (() => void) | undefined;
		const timer = window.setTimeout(() => fail("Dictation timed out. Try again."), 7 * 60_000);
		let recordTimer: number | undefined;
		const releaseMicrophone = () => {
			stream?.getTracks().forEach((track) => track.stop());
			stream = undefined;
			node?.disconnect();
			node = undefined;
			if (context && context.state !== "closed") void context.close().catch(() => {});
			context = undefined;
			window.clearTimeout(recordTimer);
		};
		const cleanup = () => {
			finished = true;
			flush?.();
			window.clearTimeout(timer);
			window.clearTimeout(pumpTimer);
			queued = [];
			queuedBytes = 0;
			releaseMicrophone();
			socket.close();
		};
		const fail = (message: string) => {
			if (finished || started !== generation) return;
			cleanup();
			emit({ type: "error", message });
		};
		// Capture can begin before inference is ready. Keep audio ordered and bounded.
		const pump = () => {
			window.clearTimeout(pumpTimer);
			if (finished || !engineReady || socket.readyState !== WebSocket.OPEN) return;
			try {
				while (queued.length && socket.bufferedAmount < 512 * 1024) {
					const block = queued.shift()!;
					queuedBytes -= block.byteLength;
					socket.send(block);
				}
				if (queued.length) pumpTimer = window.setTimeout(pump, 10);
				else if (stopAfterFlush && !stopSent) {
					stopSent = true;
					socket.send(JSON.stringify({ type: "stop" }));
				}
			} catch {
				fail("Could not send microphone audio.");
			}
		};
		const session = {
			async stop() {
				if (stopping || finished) return;
				stopping = true;
				emit({ type: "finishing" });
				// The worklet acknowledges after emitting its final partial audio block.
				if (node)
					await new Promise<void>((resolve, reject) => {
						const timeout = window.setTimeout(
							() => reject(new Error("The microphone did not finish. Try recording again.")),
							1000,
						);
						flush = () => {
							window.clearTimeout(timeout);
							resolve();
						};
						node!.port.postMessage("flush");
					});
				releaseMicrophone();
				stopAfterFlush = true;
				pump();
			},
			cancel: cleanup,
		};
		active = session;
		socket.onerror = () => fail("Could not connect to the local speech engine.");
		socket.onclose = () => {
			if (!finished) fail("Local dictation disconnected. Try again.");
		};
		socket.onmessage = ({ data: message }: MessageEvent<string>) => {
			if (finished || started !== generation) return;
			try {
				const event: DictationEvent = JSON.parse(message);
				if (event.type === "listening") {
					engineReady = true;
					pump();
					return;
				}
				if (event.type === "end" || event.type === "error") cleanup();
				emit(event);
			} catch {
				fail("Invalid response from the local speech engine.");
			}
		};
		const capture = async () => {
			stream = await navigator.mediaDevices.getUserMedia({
				audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true },
				video: false,
			});
			if (finished || stopping || started !== generation) {
				releaseMicrophone();
				return;
			}
			context = new AudioContext({ sampleRate: 16000 });
			if (context.sampleRate !== 16000) throw new Error("This microphone does not support 16 kHz recording.");
			await context.audioWorklet.addModule(workletURL);
			if (finished || stopping || started !== generation) {
				releaseMicrophone();
				return;
			}
			node = new AudioWorkletNode(context, "ao-dictation");
			node.port.onmessage = ({ data: block }: MessageEvent<Float32Array | "flushed">) => {
				if (block === "flushed") {
					flush?.();
					flush = undefined;
					return;
				}
				if (finished || started !== generation) return;
				capturedBytes += block.byteLength;
				if (capturedBytes > 16000 * 4 * 300 || queuedBytes + block.byteLength > 16000 * 4 * 300) {
					fail("Dictation is limited to five minutes.");
					return;
				}
				const copy = new Float32Array(block).buffer;
				queued.push(copy);
				queuedBytes += copy.byteLength;
				pump();
				const rms = Math.sqrt(block.reduce((sum, sample) => sum + sample * sample, 0) / block.length);
				emit({ type: "levels", values: [Math.min(1, Math.max(0, (20 * Math.log10(Math.max(rms, 1e-6)) + 60) / 50))] });
			};
			context.createMediaStreamSource(stream).connect(node);
			node.connect(context.destination);
			await context.resume();
			if (finished || stopping || started !== generation) return;
			if (finishRequested) {
				await session.stop();
				return;
			}
			recordTimer = window.setTimeout(() => {
				void session.stop().catch((error) => fail(error.message));
			}, 299_000);
			emit({ type: "listening" });
		};
		if (finishRequested) void session.stop().catch((error) => fail(error.message));
		else
			void capture().catch((error: unknown) =>
				fail(error instanceof Error ? error.message : "Could not start the microphone."),
			);
	},
	async stop() {
		finishRequested = true;
		await active?.stop();
	},
	async cancel() {
		generation += 1;
		active?.cancel();
		active = undefined;
	},
};
