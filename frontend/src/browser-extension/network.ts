// Network capture for Chrome mode: request metadata from chrome.webRequest
// (observational, no bodies), in the shapes `ao browser network` prints for
// AO's Browser panel.

import { sanitizeBrowserURL } from "../shared/browser-sanitize";
import type { ChromeApi, WebRequestDetails } from "./chrome-api";
import type { SessionTabRegistry } from "./sessions";

export const MAX_NETWORK_REQUESTS = 200;

type Request = {
	sequence: number;
	method: string;
	url: string;
	resourceType: string;
	status?: number;
	failed?: boolean;
	errorText?: string;
	fromCache?: boolean;
	durationMs?: number;
};

type Capture = {
	active: boolean;
	tabId: string;
	chromeTabId: number;
	startedAt: string;
	expiresAt: string;
	stoppedAt?: string;
	stopReason?: string;
	nextSequence: number;
	requests: Request[];
	byId: Map<string, { request: Request; started: number }>;
	timer?: ReturnType<typeof setTimeout>;
};

const RESOURCE_TYPES: Record<string, string> = { main_frame: "document", sub_frame: "document", xmlhttprequest: "xhr" };

export class NetworkCapture {
	private readonly captures = new Map<string, Capture>();
	private listening = false;

	constructor(private readonly chrome: ChromeApi, private readonly sessions: SessionTabRegistry) {
		sessions.onOwnershipChanged(() => {
			for (const [sessionId, capture] of this.captures) {
				if (sessions.ownerOf(capture.chromeTabId)?.session.sessionId !== sessionId) this.stop(sessionId, "ownership-lost");
			}
		});
	}

	private listen(): void {
		if (this.listening) return;
		this.listening = true;
		const filter = { urls: ["<all_urls>"] };
		this.chrome.webRequest.onBeforeRequest.addListener((details) => this.started(details), filter);
		this.chrome.webRequest.onCompleted.addListener((details) => this.finished(details, false), filter);
		this.chrome.webRequest.onErrorOccurred.addListener((details) => this.finished(details, true), filter);
	}

	private captureFor(chromeTabId: number): Capture | undefined {
		for (const [sessionId, capture] of this.captures) {
			if (!capture.active || capture.chromeTabId !== chromeTabId) continue;
			if (this.sessions.ownerOf(chromeTabId)?.session.sessionId !== sessionId) {
				this.stop(sessionId, "ownership-lost");
				continue;
			}
			return capture;
		}
		return undefined;
	}

	private started(details: WebRequestDetails): void {
		const capture = this.captureFor(details.tabId);
		if (!capture || capture.requests.length >= MAX_NETWORK_REQUESTS) return;
		const request: Request = {
			sequence: capture.nextSequence++,
			method: details.method,
			url: sanitizeBrowserURL(details.url),
			resourceType: RESOURCE_TYPES[details.type] ?? details.type,
		};
		capture.requests.push(request);
		capture.byId.set(details.requestId, { request, started: details.timeStamp });
	}

	private finished(details: WebRequestDetails, failed: boolean): void {
		const capture = this.captureFor(details.tabId);
		const entry = capture?.byId.get(details.requestId);
		if (!entry) return;
		if (failed) {
			entry.request.failed = true;
			entry.request.errorText = details.error;
		} else {
			entry.request.status = details.statusCode;
			entry.request.fromCache = details.fromCache;
		}
		entry.request.durationMs = Math.max(0, Math.round(details.timeStamp - entry.started));
		capture!.byId.delete(details.requestId);
	}

	start(sessionId: string, tabId: string, chromeTabId: number, durationSeconds: number): Record<string, unknown> {
		const existing = this.captures.get(sessionId);
		if (existing?.active) return { ...this.status(sessionId), alreadyActive: true };
		this.listen();
		const now = Date.now();
		const capture: Capture = {
			active: true,
			tabId,
			chromeTabId,
			startedAt: new Date(now).toISOString(),
			expiresAt: new Date(now + durationSeconds * 1_000).toISOString(),
			nextSequence: 1,
			requests: [],
			byId: new Map(),
		};
		capture.timer = setTimeout(() => this.stop(sessionId, "expired"), durationSeconds * 1_000);
		this.captures.set(sessionId, capture);
		return this.status(sessionId);
	}

	status(sessionId: string, fallbackTabId = ""): Record<string, unknown> {
		const capture = this.captures.get(sessionId);
		if (!capture) {
			return { active: false, metadataOnly: true, tabId: fallbackTabId, requestCount: 0, maxEntries: MAX_NETWORK_REQUESTS, untrustedExternalContent: true };
		}
		return {
			active: capture.active,
			metadataOnly: true,
			tabId: capture.tabId,
			requestCount: capture.requests.length,
			maxEntries: MAX_NETWORK_REQUESTS,
			startedAt: capture.startedAt,
			expiresAt: capture.expiresAt,
			...(capture.stoppedAt ? { stoppedAt: capture.stoppedAt } : {}),
			...(capture.stopReason ? { stopReason: capture.stopReason } : {}),
			untrustedExternalContent: true,
		};
	}

	list(sessionId: string, fallbackTabId = ""): Record<string, unknown> {
		return { ...this.status(sessionId, fallbackTabId), requests: [...(this.captures.get(sessionId)?.requests ?? [])], untrustedExternalContent: true };
	}

	stop(sessionId: string, reason: string): Record<string, unknown> {
		const capture = this.captures.get(sessionId);
		if (capture?.active) {
			if (capture.timer) clearTimeout(capture.timer);
			capture.active = false;
			capture.stoppedAt = new Date().toISOString();
			capture.stopReason = reason;
		}
		return this.list(sessionId);
	}

	clear(sessionId: string, fallbackTabId = ""): Record<string, unknown> {
		const capture = this.captures.get(sessionId);
		if (capture) {
			capture.requests = [];
			capture.byId.clear();
		}
		return this.status(sessionId, fallbackTabId);
	}

	dispose(sessionId: string): void {
		this.stop(sessionId, "session-ended");
		this.captures.delete(sessionId);
	}
}
