// AO Browser Bridge service worker. Connects to AO through Chrome native
// messaging (`ao browser-host`), runs commands for consenting AO sessions in
// their own tab groups, and serves the popup and options pages.

import { chromeApi, type ChromeApi, type Port } from "./chrome-api";
import { ConsentManager } from "./consent";
import { Executor, POLICY_KEY } from "./executor";
import { NetworkCapture } from "./network";
import { DEFAULT_SITE_POLICY, parsePolicy } from "./policy";
import { BridgeError, errorFrame, HOST_NAME, HOST_PROTOCOL, isRecord, type ResultFrame } from "./protocol";
import { SessionTabRegistry } from "./sessions";
import { TrustedInput } from "./trusted-input";

const MAX_BACKOFF_MS = 60_000;

export type BridgeStatus = {
	state: "connecting" | "connected" | "disconnected";
	error?: { code: string; message: string };
	version: string;
	activeCommands: number;
};

export class Bridge {
	private port?: Port;
	private ready = false;
	private backoff = 1_000;
	private reconnectTimer?: ReturnType<typeof setTimeout>;
	private lastError?: { code: string; message: string };
	private readonly inFlight = new Map<string, { controller: AbortController; sessionId: string }>();
	readonly sessions: SessionTabRegistry;
	readonly consent: ConsentManager;
	private readonly executor: Executor;

	constructor(private readonly chrome: ChromeApi) {
		this.sessions = new SessionTabRegistry(chrome);
		this.consent = new ConsentManager(chrome);
		this.executor = new Executor({
			chrome,
			sessions: this.sessions,
			consent: this.consent,
			trusted: new TrustedInput(chrome),
			network: new NetworkCapture(chrome, this.sessions),
		});
	}

	start(): void {
		this.sessions.start();
		this.consent.start();
		this.executor.start();
		this.chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
			if (!isRecord(message) || typeof message.type !== "string" || !message.type.startsWith("ao-ui-")) return;
			// Popup and options pages only; content scripts and pages cannot ask.
			if (sender.id !== this.chrome.runtime.id || sender.tab) return;
			this.handleUi(message).then(sendResponse, (error) => sendResponse({ error: String(error) }));
			return true as unknown as void;
		});
		void this.updateBadge();
		this.connect();
	}

	status(): BridgeStatus {
		return {
			state: this.ready ? "connected" : this.port ? "connecting" : "disconnected",
			...(this.lastError ? { error: this.lastError } : {}),
			version: this.chrome.runtime.getManifest().version,
			activeCommands: this.inFlight.size,
		};
	}

	connect(): void {
		if (this.port) return;
		clearTimeout(this.reconnectTimer);
		let port: Port;
		try {
			port = this.chrome.runtime.connectNative(HOST_NAME);
		} catch (error) {
			this.lastError = { code: "HOST_NOT_INSTALLED", message: String(error) };
			this.scheduleReconnect();
			return;
		}
		this.port = port;
		this.ready = false;
		port.onMessage.addListener((message) => this.onHostMessage(message));
		port.onDisconnect.addListener(() => {
			const reason = this.chrome.runtime.lastError?.message;
			if (reason && !this.lastError) {
				this.lastError = /not found|specified native messaging host/i.test(reason)
					? { code: "HOST_NOT_INSTALLED", message: "AO is not set up for Chrome. Run `ao browser chrome setup`." }
					: { code: "HOST_DISCONNECTED", message: reason };
			}
			this.port = undefined;
			this.ready = false;
			for (const { controller } of this.inFlight.values()) controller.abort();
			this.inFlight.clear();
			void this.updateBadge();
			this.scheduleReconnect();
		});
		port.postMessage({ type: "hello", protocol: HOST_PROTOCOL, extensionVersion: this.chrome.runtime.getManifest().version });
	}

	private scheduleReconnect(): void {
		clearTimeout(this.reconnectTimer);
		this.reconnectTimer = setTimeout(() => this.connect(), this.backoff);
		this.backoff = Math.min(MAX_BACKOFF_MS, this.backoff * 2);
	}

	private onHostMessage(message: unknown): void {
		if (!isRecord(message)) return;
		switch (message.type) {
			case "ping":
				this.port?.postMessage({ type: "pong" });
				return;
			case "ready":
				this.ready = true;
				this.lastError = undefined;
				this.backoff = 1_000;
				void this.updateBadge();
				return;
			case "error":
				this.lastError = { code: String(message.code ?? "HOST_ERROR"), message: String(message.message ?? "") };
				return;
			case "command":
				void this.runCommand(message);
				return;
			case "cancel": {
				const entry = this.inFlight.get(String(message.requestId ?? ""));
				entry?.controller.abort();
				return;
			}
			default:
		}
	}

	private async runCommand(message: Record<string, unknown>): Promise<void> {
		const requestId = String(message.requestId ?? "");
		const sessionId = String(message.sessionId ?? "");
		const action = String(message.action ?? "");
		const args = isRecord(message.args) ? message.args : {};
		const controller = new AbortController();
		this.inFlight.set(requestId, { controller, sessionId });
		void this.updateBadge();
		let frame: ResultFrame;
		try {
			if (!requestId || !sessionId || !action) throw new BridgeError("INVALID_ARGUMENT", "Malformed browser command");
			const result = await this.executor.execute(sessionId, action, args, controller.signal);
			frame = { type: "result", requestId, ok: true, result };
		} catch (error) {
			frame = errorFrame(requestId, controller.signal.aborted && !(error instanceof BridgeError) ? new BridgeError("BROWSER_COMMAND_CANCELED", "Browser command was canceled") : error);
		} finally {
			this.inFlight.delete(requestId);
			void this.updateBadge();
		}
		this.port?.postMessage(frame);
	}

	/** The kill switch: stop every agent and forget every approval. */
	async stopAll(): Promise<void> {
		for (const { controller } of this.inFlight.values()) controller.abort();
		await this.consent.revoke();
	}

	private async handleUi(message: Record<string, unknown>): Promise<unknown> {
		switch (message.type) {
			case "ao-ui-status": {
				const sessions = await this.sessions.all();
				const approvals = await this.consent.approvals();
				return {
					...this.status(),
					sessions: sessions.map((session) => ({
						sessionId: session.sessionId,
						tabs: Object.keys(session.tabs).length,
						approved: Boolean(approvals[session.sessionId]),
						trusted: approvals[session.sessionId]?.trusted ?? false,
					})),
				};
			}
			case "ao-ui-stop-all":
				await this.stopAll();
				return { stopped: true };
			case "ao-ui-revoke": {
				const sessionId = String(message.sessionId ?? "");
				for (const entry of this.inFlight.values()) {
					if (entry.sessionId === sessionId) entry.controller.abort();
				}
				await this.consent.revoke(sessionId);
				return { revoked: true };
			}
			case "ao-ui-reconnect":
				this.backoff = 1_000;
				this.port?.disconnect();
				this.port = undefined;
				this.connect();
				return this.status();
			case "ao-ui-get-policy": {
				const stored = await this.chrome.storage.local.get(POLICY_KEY);
				return { policy: parsePolicy(stored[POLICY_KEY]), defaults: DEFAULT_SITE_POLICY };
			}
			case "ao-ui-set-policy":
				await this.chrome.storage.local.set({ [POLICY_KEY]: parsePolicy(message.policy) });
				return { saved: true };
			default:
				throw new Error(`Unknown request ${String(message.type)}`);
		}
	}

	private async updateBadge(): Promise<void> {
		const busy = this.inFlight.size > 0;
		const text = busy ? "AO" : this.ready ? "" : "!";
		await this.chrome.action.setBadgeText({ text }).catch(() => undefined);
		await this.chrome.action.setBadgeBackgroundColor({ color: busy ? "#f97316" : "#6b7280" }).catch(() => undefined);
		const sessions = [...new Set([...this.inFlight.values()].map((entry) => entry.sessionId))];
		const title = busy
			? `AO agent working: ${sessions.join(", ")}`
			: this.ready
				? "AO Browser Bridge: connected"
				: "AO Browser Bridge: not connected to AO";
		await this.chrome.action.setTitle({ title }).catch(() => undefined);
	}
}

const chrome = (globalThis as { chrome?: ChromeApi }).chrome;
if (chrome?.runtime?.connectNative) {
	new Bridge(chromeApi()).start();
}
