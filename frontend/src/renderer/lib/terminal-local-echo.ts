import type { TerminalMux } from "./terminal-mux";
import { isMouseHoverReport } from "./terminal-mouse";

export const LOCAL_ECHO_ENABLED = true;

export const LOCAL_ECHO_PREDICTION_TIMEOUT_MS = 2_000;
const MAX_PENDING_PREDICTIONS = 4_096;
const SYNCHRONIZED_OUTPUT_START = "\x1b[?2026h";
const SYNCHRONIZED_OUTPUT_END = "\x1b[?2026l";
const MAX_SYNCHRONIZED_OUTPUT_BYTES = 1_000_000;

const DEL = "\x7f";

function suffixPrefixLength(value: string, marker: string): number {
	const limit = Math.min(value.length, marker.length - 1);
	for (let length = limit; length > 0; length -= 1) {
		if (value.endsWith(marker.slice(0, length))) return length;
	}
	return 0;
}

class SynchronizedOutputBuffer {
	private buffer = "";
	private synchronized = false;

	push(chunk: string): string[] {
		this.buffer += chunk;
		const output: string[] = [];

		while (this.buffer.length > 0) {
			if (this.synchronized) {
				const endIndex = this.buffer.indexOf(SYNCHRONIZED_OUTPUT_END);
				if (endIndex < 0) {
					if (this.buffer.length > MAX_SYNCHRONIZED_OUTPUT_BYTES) {
						output.push(this.buffer);
						this.clear();
					}
					break;
				}
				const frameEnd = endIndex + SYNCHRONIZED_OUTPUT_END.length;
				output.push(this.buffer.slice(0, frameEnd));
				this.buffer = this.buffer.slice(frameEnd);
				this.synchronized = false;
				continue;
			}

			const startIndex = this.buffer.indexOf(SYNCHRONIZED_OUTPUT_START);
			if (startIndex >= 0) {
				if (startIndex > 0) output.push(this.buffer.slice(0, startIndex));
				this.buffer = this.buffer.slice(startIndex);
				this.synchronized = true;
				continue;
			}

			const retainedLength = suffixPrefixLength(this.buffer, SYNCHRONIZED_OUTPUT_START);
			const emittedLength = this.buffer.length - retainedLength;
			if (emittedLength > 0) output.push(this.buffer.slice(0, emittedLength));
			this.buffer = this.buffer.slice(emittedLength);
			break;
		}

		return output;
	}

	clear(): void {
		this.buffer = "";
		this.synchronized = false;
	}
}

export interface TerminalLocalEchoOptions {
	now?: () => number;
	predictionTimeoutMs?: number;
}

export interface LocalEchoKeystrokeResult {
	localWrite?: string;
	sendUpstream?: string;
	submitUpstream?: string;
}

export interface LocalEchoWriteResult {
	write: string;
}

function isPredictableCharacter(data: string): boolean {
	if (data.length !== 1) return false;
	const codePoint = data.codePointAt(0) ?? 0;
	return (codePoint >= 0x20 && codePoint <= 0x7e) || (codePoint >= 0xa0 && codePoint <= 0x024f);
}

export class TerminalLocalEchoController {
	private readonly now: () => number;
	private readonly predictionTimeoutMs: number;
	private draft: Array<{ char: string; at: number }> = [];
	private pending: Array<{ char: string; at: number }> = [];
	private slashPending: Array<{ char: string; at: number }> = [];
	private slashLine: string | null = null;
	private bracketedPasteActive = false;
	private pasteMarkerCarry = "";

	constructor(options: TerminalLocalEchoOptions) {
		this.now = options.now ?? Date.now;
		this.predictionTimeoutMs = options.predictionTimeoutMs ?? LOCAL_ECHO_PREDICTION_TIMEOUT_MS;
	}

	get pendingCount(): number {
		return this.draft.length + this.pending.length + this.slashPending.length;
	}

	observeServerOutput(text: string): void {
		const scan = this.pasteMarkerCarry + text;
		this.pasteMarkerCarry = scan.slice(-7);
		const enabled = scan.lastIndexOf("\x1b[?2004h");
		const disabled = scan.lastIndexOf("\x1b[?2004l");
		if (enabled === -1 && disabled === -1) return;
		this.bracketedPasteActive = enabled > disabled;
	}

	handleKeystroke(data: string): LocalEchoKeystrokeResult {
		const now = this.now();
		if (this.slashLine !== null) {
			if (data.includes("\r") || data.includes("\n")) {
				this.slashLine = null;
				this.slashPending = [];
				return { sendUpstream: data };
			}
			if (data === "\x1b" || data === "\x03") {
				this.slashLine = null;
				const localWrite = this.rollbackSequence(this.slashPending.length);
				this.slashPending = [];
				return localWrite.length > 0
					? { localWrite, sendUpstream: data }
					: { sendUpstream: data };
			}
			if (data === "\x15") {
				this.slashLine = null;
				const localWrite = this.rollbackSequence(this.slashPending.length);
				this.slashPending = [];
				return localWrite.length > 0
					? { localWrite, sendUpstream: data }
					: { sendUpstream: data };
			}
			if (data === "\x17") {
				this.slashLine = this.slashLine.replace(/\s*\S+\s*$/, "");
				if (this.slashLine.length === 0) this.slashLine = null;
				const localWrite = this.rollbackSequence(this.slashPending.length);
				this.slashPending = [];
				return localWrite.length > 0
					? { localWrite, sendUpstream: data }
					: { sendUpstream: data };
			}
			if (data === DEL) {
				this.slashLine = this.slashLine.slice(0, -1);
				if (this.slashLine.length === 0) this.slashLine = null;
				if (this.slashPending.length > 0) {
					this.slashPending.pop();
					return { localWrite: "\b \b", sendUpstream: data };
				}
				return { sendUpstream: data };
			}
			if (this.slashPending.length < MAX_PENDING_PREDICTIONS && isPredictableCharacter(data)) {
				this.slashLine += data;
				this.slashPending.push({ char: data, at: now });
				return { localWrite: data, sendUpstream: data };
			}
			if (![...data].some((char) => char < " ")) this.slashLine += data;
			return { sendUpstream: data };
		}
		if (data === "/" && this.draft.length === 0 && this.pending.length === 0) {
			this.slashLine = "/";
			this.slashPending = [{ char: data, at: now }];
			return { localWrite: `\x1b[K${data}`, sendUpstream: data };
		}
		const canBuffer = this.pending.length === 0;
		if (data === DEL) {
			if (canBuffer && this.draft.length > 0) {
				this.draft.pop();
				return { localWrite: "\b \b" };
			}
			return { sendUpstream: data };
		}
		if (canBuffer && this.draft.length < MAX_PENDING_PREDICTIONS && isPredictableCharacter(data)) {
			const firstCharacter = this.draft.length === 0;
			this.draft.push({ char: data, at: now });
			return { localWrite: firstCharacter ? `\x1b[K${data}` : data };
		}
		if (this.draft.length > 0) {
			const draft = this.draft;
			this.draft = [];
			this.pending = draft.map(({ char }) => ({ char, at: now }));
			const text = draft.map(({ char }) => char).join("");
			if (data === "\r" || data === "\n") {
				const sendUpstream = this.bracketedPasteActive ? `\x1b[200~${text}\x1b[201~` : text;
				return { sendUpstream, submitUpstream: data };
			}
			return { sendUpstream: text + data };
		}
		return { sendUpstream: data };
	}

	handleServerData(chunk: string): LocalEchoWriteResult {
		if (this.slashLine !== null && this.slashPending.length > 0) {
			let index = 0;
			while (this.slashPending.length > 0 && index < chunk.length) {
				if (chunk[index] === this.slashPending[0].char) {
					this.slashPending.shift();
					index += 1;
					continue;
				}
				this.slashPending = [];
				return { write: chunk.slice(index) };
			}
			return { write: chunk.slice(index) };
		}
		if (this.pending.length === 0) {
			if (this.draft.length === 0) return { write: chunk };
			const draft = this.draft.map(({ char }) => char).join("");
			return { write: this.rollbackSequence(this.draft.length) + chunk + `\x1b[K${draft}` };
		}
		let index = 0;
		while (this.pending.length > 0 && index < chunk.length) {
			if (chunk[index] === this.pending[0].char) {
				this.pending.shift();
				index += 1;
				continue;
			}
			return { write: this.rollback() + chunk.slice(index) };
		}
		return { write: chunk.slice(index) };
	}

	expireStalePredictions(): LocalEchoWriteResult {
		const oldestSlash = this.slashPending[0];
		if (
			oldestSlash !== undefined &&
			this.now() - oldestSlash.at >= this.predictionTimeoutMs
		) {
			const count = this.slashPending.length;
			this.slashPending = [];
			return { write: this.rollbackSequence(count) };
		}
		const oldest = this.pending[0];
		if (oldest === undefined || this.now() - oldest.at < this.predictionTimeoutMs) {
			return { write: "" };
		}
		return { write: this.rollback() };
	}

	msUntilTimeout(): number | null {
		const oldest = this.pending[0] ?? this.slashPending[0];
		if (oldest === undefined) return null;
		return Math.max(0, oldest.at + this.predictionTimeoutMs - this.now());
	}

	discardPredictions(): LocalEchoWriteResult {
		const count = this.pending.length + this.draft.length + this.slashPending.length;
		this.pending = [];
		this.draft = [];
		this.slashPending = [];
		this.slashLine = null;
		return { write: this.rollbackSequence(count) };
	}

	private rollback(): string {
		const count = this.pending.length;
		if (count === 0) return "";
		this.pending = [];
		return this.rollbackSequence(count);
	}

	private rollbackSequence(count: number): string {
		return count > 0 ? `\x1b[${count}D\x1b[K` : "";
	}
}

export interface LineBufferedLocalInputMuxOptions {
	now?: () => number;
}

export function withLineBufferedLocalInput(
	inner: TerminalMux,
	options: LineBufferedLocalInputMuxOptions,
): TerminalMux {
	const controller = new TerminalLocalEchoController({
		now: options.now,
	});
	const encoder = new TextEncoder();
	const decoder = new TextDecoder();
	const synchronizedOutput = new SynchronizedOutputBuffer();
	const dataListeners = new Set<(bytes: Uint8Array) => void>();
	let innerDataUnsubscribe: (() => void) | null = null;
	let expiryTimer: ReturnType<typeof setTimeout> | null = null;
	let connectionOpen = false;
	let disposed = false;

	const emitLocal = (text: string): void => {
		if (text.length === 0 || disposed) return;
		const bytes = encoder.encode(text);
		dataListeners.forEach((listener) => listener(bytes));
	};

	const clearExpiryTimer = (): void => {
		if (expiryTimer !== null) {
			clearTimeout(expiryTimer);
			expiryTimer = null;
		}
	};

	const scheduleExpiry = (): void => {
		clearExpiryTimer();
		const delay = controller.msUntilTimeout();
		if (delay === null) return;
		expiryTimer = setTimeout(() => {
			expiryTimer = null;
			emitLocal(controller.expireStalePredictions().write);
			scheduleExpiry();
		}, delay);
	};

	const connectionUnsubscribe = inner.onConnectionChange((state) => {
		connectionOpen = state === "open";
		if (!connectionOpen) {
			synchronizedOutput.clear();
			emitLocal(controller.discardPredictions().write);
			scheduleExpiry();
		}
	});

	return {
		open: (id, cols, rows) => inner.open(id, cols, rows),
		resize: (id, cols, rows, force) => inner.resize(id, cols, rows, force),
		close: (id) => inner.close(id),
		onExit: (id, listener) => inner.onExit(id, listener),
		onOpened: (id, listener) => inner.onOpened(id, listener),
		onError: (id, listener) => inner.onError(id, listener),
		onConnectionChange: (listener) => inner.onConnectionChange(listener),
		sendInput: (id, input) => {
			if (!connectionOpen) {
				inner.sendInput(id, input);
				return;
			}
			if (isMouseHoverReport(input)) {
				inner.sendInput(id, input);
				return;
			}
			const { localWrite, sendUpstream, submitUpstream } = controller.handleKeystroke(input);
			if (localWrite !== undefined) {
				emitLocal(localWrite);
				scheduleExpiry();
			}
			if (sendUpstream !== undefined) {
				inner.sendInput(id, sendUpstream);
				scheduleExpiry();
			}
			if (submitUpstream !== undefined) {
				inner.sendInput(id, submitUpstream);
			}
		},
		onData: (id, listener) => {
			dataListeners.add(listener);
			if (innerDataUnsubscribe === null) {
				innerDataUnsubscribe = inner.onData(id, (bytes) => {
					const text = decoder.decode(bytes, { stream: true });
					if (text.length === 0) return;
					controller.observeServerOutput(text);
					for (const output of synchronizedOutput.push(text)) {
						const { write } = controller.handleServerData(output);
						emitLocal(write);
					}
					scheduleExpiry();
				});
			}
			return () => {
				dataListeners.delete(listener);
			};
		},
		dispose: () => {
			if (disposed) return;
			disposed = true;
			clearExpiryTimer();
			synchronizedOutput.clear();
			connectionUnsubscribe();
			innerDataUnsubscribe?.();
			innerDataUnsubscribe = null;
			dataListeners.clear();
			inner.dispose();
		},
	};
}
