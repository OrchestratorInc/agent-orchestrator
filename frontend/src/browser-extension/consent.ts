// Per-session consent. The first command from an AO session opens a small
// extension window asking the user to allow it; only a person can answer
// (agents cannot reach extension pages, and their tab group never contains
// one). Nothing runs for a session until it is allowed.

import type { ChromeApi } from "./chrome-api";
import { BridgeError, isRecord } from "./protocol";

const APPROVALS_KEY = "aoApprovals";
const DENIALS_KEY = "aoDenials";
const CONSENT_TIMEOUT_MS = 120_000;
const DENIAL_COOLDOWN_MS = 10 * 60_000;

export type Approval = { approvedAt: number; trusted: boolean };
export type ConsentDecision = { type: "ao-consent-decision"; sessionId: string; allow: boolean; trusted: boolean };

type Pending = {
	promise: Promise<Approval>;
	resolve: (approval: Approval) => void;
	reject: (error: Error) => void;
	windowId?: number;
};

export class ConsentManager {
	private readonly pending = new Map<string, Pending>();

	constructor(
		private readonly chrome: ChromeApi,
		private readonly now: () => number = Date.now,
	) {}

	start(): void {
		this.chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
			if (!isRecord(message) || message.type !== "ao-consent-decision") return;
			// Decisions come only from the extension's own consent page.
			if (sender.id !== this.chrome.runtime.id || !String(sender.url ?? "").startsWith(this.chrome.runtime.getURL("consent.html"))) return;
			// Answer only once the decision is stored, so the page closes after it.
			this.decide(message as unknown as ConsentDecision).then(
				() => sendResponse({ ok: true }),
				(error) => sendResponse({ ok: false, error: String(error) }),
			);
			return true as unknown as void;
		});
		this.chrome.windows.onRemoved.addListener((windowId) => {
			for (const [sessionId, pending] of this.pending) {
				if (pending.windowId === windowId) {
					this.pending.delete(sessionId);
					pending.reject(new BridgeError("BROWSER_CHROME_CONSENT_DENIED", "The user closed the AO consent window"));
				}
			}
		});
	}

	async approvals(): Promise<Record<string, Approval>> {
		const stored = await this.chrome.storage.local.get(APPROVALS_KEY);
		return isRecord(stored[APPROVALS_KEY]) ? (stored[APPROVALS_KEY] as Record<string, Approval>) : {};
	}

	/** Resolve once the session is allowed; ask the user if it is not. */
	async ensure(sessionId: string): Promise<Approval> {
		const approval = (await this.approvals())[sessionId];
		if (approval) return approval;
		const denials = await this.denials();
		const deniedUntil = denials[sessionId];
		if (deniedUntil && deniedUntil > this.now()) {
			throw new BridgeError("BROWSER_CHROME_CONSENT_DENIED", "The user denied this AO session access to Chrome; ask them to allow it from the AO Browser Bridge menu");
		}
		const existing = this.pending.get(sessionId);
		if (existing) return existing.promise;
		let resolve!: (approval: Approval) => void;
		let reject!: (error: Error) => void;
		const promise = new Promise<Approval>((res, rej) => {
			resolve = res;
			reject = rej;
		});
		const pending: Pending = { promise, resolve, reject };
		this.pending.set(sessionId, pending);
		const timer = setTimeout(() => {
			if (this.pending.get(sessionId) !== pending) return;
			this.pending.delete(sessionId);
			if (pending.windowId !== undefined) void this.chrome.windows.remove(pending.windowId).catch(() => undefined);
			reject(new BridgeError("BROWSER_CHROME_CONSENT_TIMEOUT", "Nobody answered the AO consent prompt in Chrome"));
		}, CONSENT_TIMEOUT_MS);
		promise.then(
			() => clearTimeout(timer),
			() => clearTimeout(timer),
		);
		try {
			const window = await this.chrome.windows.create({
				url: this.chrome.runtime.getURL(`consent.html?session=${encodeURIComponent(sessionId)}`),
				type: "popup",
				width: 440,
				height: 600,
				focused: true,
			});
			pending.windowId = window.id;
		} catch (error) {
			this.pending.delete(sessionId);
			reject(new BridgeError("BROWSER_CHROME_CONSENT_UNAVAILABLE", `Could not ask for consent: ${String(error)}`));
		}
		return promise;
	}

	async decide(decision: ConsentDecision): Promise<void> {
		// Claim the prompt first: the consent window may close while the
		// decision is being stored, and that must not count as a denial.
		const pending = this.pending.get(decision.sessionId);
		this.pending.delete(decision.sessionId);
		if (decision.allow) {
			const approvals = await this.approvals();
			const approval = { approvedAt: this.now(), trusted: decision.trusted };
			approvals[decision.sessionId] = approval;
			await this.chrome.storage.local.set({ [APPROVALS_KEY]: approvals });
			const denials = await this.denials();
			delete denials[decision.sessionId];
			await this.chrome.storage.local.set({ [DENIALS_KEY]: denials });
			pending?.resolve(approval);
		} else {
			const denials = await this.denials();
			denials[decision.sessionId] = this.now() + DENIAL_COOLDOWN_MS;
			await this.chrome.storage.local.set({ [DENIALS_KEY]: denials });
			pending?.reject(new BridgeError("BROWSER_CHROME_CONSENT_DENIED", "The user denied this AO session access to Chrome"));
		}
		if (pending?.windowId !== undefined) await this.chrome.windows.remove(pending.windowId).catch(() => undefined);
	}

	/** Revoke one session, or every session (the kill switch). */
	async revoke(sessionId?: string): Promise<void> {
		const approvals = sessionId ? await this.approvals() : {};
		if (sessionId) delete approvals[sessionId];
		await this.chrome.storage.local.set({ [APPROVALS_KEY]: approvals });
		for (const [id, pending] of this.pending) {
			if (sessionId && id !== sessionId) continue;
			this.pending.delete(id);
			pending.reject(new BridgeError("BROWSER_CHROME_STOPPED", "The user stopped AO agents in Chrome"));
		}
	}

	private async denials(): Promise<Record<string, number>> {
		const stored = await this.chrome.storage.local.get(DENIALS_KEY);
		return isRecord(stored[DENIALS_KEY]) ? (stored[DENIALS_KEY] as Record<string, number>) : {};
	}
}
