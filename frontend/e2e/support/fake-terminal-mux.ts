import type { Page } from "@playwright/test";

export type FakeTerminalMuxStats = {
	closes: Record<string, number>;
	inputs: Record<string, string[]>;
	openFrames: Record<
		string,
		{ cols?: number; phase: string | null; rows?: number }[]
	>;
	opens: Record<string, number>;
	resizePhases: Record<string, (string | null)[]>;
	resizes: Record<string, { cols: number; rows: number }[]>;
	sockets: number;
	writers: number;
};

export type FakeTerminalMuxController = {
	disconnect: (handleId: string) => void;
	emit: (handleId: string, text: string) => void;
	stats: () => FakeTerminalMuxStats;
};

declare global {
	interface Window {
		__aoFakeTerminalMux?: FakeTerminalMuxController;
	}
}

export async function installFakeTerminalMux(
	page: Page,
	initialReplay: Record<string, string>,
): Promise<void> {
	await page.addInitScript((replayByHandle) => {
		type Listener = (event: Event) => void;
		type ClientFrame = {
			ch: string;
			cols?: number;
			data?: string;
			id?: string;
			rows?: number;
			type: string;
		};

		const sockets: FakeWebSocket[] = [];
		const opens: Record<string, number> = {};
		const closes: Record<string, number> = {};
		const inputs: Record<string, string[]> = {};
		const openFrames: Record<
			string,
			{ cols?: number; phase: string | null; rows?: number }[]
		> = {};
		const resizes: Record<string, { cols: number; rows: number }[]> = {};
		const resizePhases: Record<string, (string | null)[]> = {};

		const terminalPhase = (handleId: string): string | null =>
			Array.from(
				document.querySelectorAll<HTMLElement>("[data-terminal-cache-key]"),
			).find((element) =>
				element.dataset.terminalCacheKey?.includes(`handle:${handleId}`),
			)?.dataset.terminalActivationPhase ?? null;

		const encode = (text: string): string => {
			const bytes = new TextEncoder().encode(text);
			let binary = "";
			for (const byte of bytes) binary += String.fromCharCode(byte);
			return btoa(binary);
		};

		const decode = (data: string): string => {
			const binary = atob(data);
			const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
			return new TextDecoder().decode(bytes);
		};

		class FakeWebSocket {
			static readonly CLOSED = 3;
			static readonly CLOSING = 2;
			static readonly CONNECTING = 0;
			static readonly OPEN = 1;

			readonly url: string;
			readyState = FakeWebSocket.CONNECTING;
			private readonly handles = new Set<string>();
			private readonly listeners = new Map<string, Set<Listener>>();

			get attachmentCount(): number {
				return this.handles.size;
			}

			get isTerminalMux(): boolean {
				return new URL(this.url, window.location.href).pathname === "/mux";
			}

			constructor(url: string | URL) {
				this.url = String(url);
				sockets.push(this);
				queueMicrotask(() => {
					if (this.readyState !== FakeWebSocket.CONNECTING) return;
					this.readyState = FakeWebSocket.OPEN;
					this.dispatch("open", new Event("open"));
				});
			}

			addEventListener(type: string, listener: EventListenerOrEventListenerObject): void {
				const callback: Listener =
					typeof listener === "function" ? listener : (event) => listener.handleEvent(event);
				const set = this.listeners.get(type) ?? new Set<Listener>();
				set.add(callback);
				this.listeners.set(type, set);
			}

			removeEventListener(type: string, listener: EventListenerOrEventListenerObject): void {
				if (typeof listener !== "function") return;
				this.listeners.get(type)?.delete(listener);
			}

			send(raw: string): void {
				const frame = JSON.parse(raw) as ClientFrame;
				if (frame.ch !== "terminal" || !frame.id) return;
				const handleId = frame.id;
				if (frame.type === "open") {
					this.handles.add(handleId);
					opens[handleId] = (opens[handleId] ?? 0) + 1;
					(openFrames[handleId] ??= []).push({
						cols: frame.cols,
						phase: terminalPhase(handleId),
						rows: frame.rows,
					});
					queueMicrotask(() => {
						if (this.readyState !== FakeWebSocket.OPEN) return;
						this.message({ ch: "terminal", id: handleId, type: "opened" });
						const replay = replayByHandle[handleId];
						if (replay) this.message({ ch: "terminal", id: handleId, type: "data", data: encode(replay) });
					});
					return;
				}
				if (frame.type === "close") {
					this.handles.delete(handleId);
					closes[handleId] = (closes[handleId] ?? 0) + 1;
					return;
				}
				if (frame.type === "data" && frame.data) {
					(inputs[handleId] ??= []).push(decode(frame.data));
					return;
				}
				if (frame.type === "resize" && frame.cols && frame.rows) {
					(resizes[handleId] ??= []).push({ cols: frame.cols, rows: frame.rows });
					(resizePhases[handleId] ??= []).push(terminalPhase(handleId));
				}
			}

			close(): void {
				if (this.readyState >= FakeWebSocket.CLOSING) return;
				this.readyState = FakeWebSocket.CLOSED;
				for (const handleId of this.handles) {
					closes[handleId] = (closes[handleId] ?? 0) + 1;
				}
				this.handles.clear();
				this.dispatch("close", new CloseEvent("close"));
			}

			emit(handleId: string, text: string): void {
				if (this.readyState !== FakeWebSocket.OPEN || !this.handles.has(handleId)) return;
				this.message({ ch: "terminal", id: handleId, type: "data", data: encode(text) });
			}

			disconnect(handleId: string): void {
				if (!this.handles.has(handleId)) return;
				this.close();
			}

			private message(frame: Record<string, unknown>): void {
				this.dispatch("message", new MessageEvent("message", { data: JSON.stringify(frame) }));
			}

			private dispatch(type: string, event: Event): void {
				for (const listener of this.listeners.get(type) ?? []) listener(event);
			}
		}

		(window as unknown as { WebSocket: typeof WebSocket }).WebSocket =
			FakeWebSocket as unknown as typeof WebSocket;
		window.__aoFakeTerminalMux = {
			disconnect: (handleId) => {
				for (const socket of sockets) socket.disconnect(handleId);
			},
			emit: (handleId, text) => {
				for (const socket of sockets) socket.emit(handleId, text);
			},
			stats: () => ({
				closes: structuredClone(closes),
				inputs: structuredClone(inputs),
				openFrames: structuredClone(openFrames),
				opens: structuredClone(opens),
				resizePhases: structuredClone(resizePhases),
				resizes: structuredClone(resizes),
				sockets: sockets.filter(
					(socket) => socket.isTerminalMux && socket.readyState < FakeWebSocket.CLOSED,
				).length,
				writers: sockets.reduce(
					(total, socket) =>
						total +
						(socket.isTerminalMux && socket.readyState < FakeWebSocket.CLOSED
							? socket.attachmentCount
							: 0),
					0,
				),
			}),
		};
	}, initialReplay);
}
