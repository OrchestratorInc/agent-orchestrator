import type { BrowserLiveCommand, BrowserLiveState } from "../browserLive";
import type { BrowserLiveFrame } from "../browserLiveProtocol";

export type PreviewFrame = BrowserLiveFrame & { crop?: { width: number; height: number } };
export type BrowserLiveHandlers = {
	onStatus(status: "connecting" | "open" | "closed" | "error", message?: string): void;
	onState(state: BrowserLiveState): void;
	onFrame(frame: PreviewFrame): void;
	onDisabled?(): void;
};
export interface BrowserLiveTransport {
	connect(): void | Promise<void>;
	send(command: BrowserLiveCommand): void;
	close(): void;
}

/** Owned by the same account/endpoint lease as the session source. */
export interface PreviewSource {
	readonly scopeKey: string;
	readonly kind?: "local" | "cloud";
	isCurrent(): boolean;
	createLive(sessionId: string, handlers: BrowserLiveHandlers): BrowserLiveTransport;
	/** Invalidates open streams immediately when an account lease is replaced. */
	dispose?(): void;
}
