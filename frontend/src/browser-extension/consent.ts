import type { ChromeApi } from "./chrome-api";
import { BridgeError, isRecord } from "./protocol";

const APPROVALS_KEY = "aoApprovals";
const DENIALS_KEY = "aoDenials";
const ALL_SESSIONS = "*";
const CONSENT_TIMEOUT_MS = 120_000;
const DENIAL_COOLDOWN_MS = 10 * 60_000;

export type Approval = { approvedAt: number; trusted: boolean };
export type ConsentDecision = { type: "ao-consent-decision"; sessionId: string; allow: boolean; trusted: boolean; allSessions?: boolean };

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
			if (sender.id !== this.chrome.runtime.id || !String(sender.url ?? "").startsWith(this.chrome.runtime.getURL("consent.html"))) return;
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
					pending.reject(new BridgeError("BROWSER_CHROME_CONSENT_DENIED", "The user closed the Orchestrator consent window"));
				}
			}
		});
	}

	async approvals(): Promise<Record<string, Approval>> {
		const stored = await this.chrome.storage.local.get(APPROVALS_KEY);
		return isRecord(stored[APPROVALS_KEY]) ? (stored[APPROVALS_KEY] as Record<string, Approval>) : {};
	}

	async ensure(sessionId: string): Promise<Approval> {
		const approvals = await this.approvals();
		const approval = approvals[sessionId] ?? approvals[ALL_SESSIONS];
		if (approval) return approval;
		const denials = await this.denials();
		const deniedUntil = denials[sessionId];
		if (deniedUntil && deniedUntil > this.now()) {
			throw new BridgeError("BROWSER_CHROME_CONSENT_DENIED", "The user denied this Orchestrator-managed session access to Chrome; ask them to allow it from the Orchestrator Browser Bridge menu");
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
			reject(new BridgeError("BROWSER_CHROME_CONSENT_TIMEOUT", "Nobody answered the Orchestrator consent prompt in Chrome"));
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
		const pending = this.pending.get(decision.sessionId);
		this.pending.delete(decision.sessionId);
		if (decision.allow) {
			const approvals = await this.approvals();
			const approval = { approvedAt: this.now(), trusted: decision.trusted };
			approvals[decision.allSessions === true ? ALL_SESSIONS : decision.sessionId] = approval;
			await this.chrome.storage.local.set({ [APPROVALS_KEY]: approvals });
			const denials = await this.denials();
			delete denials[decision.sessionId];
			await this.chrome.storage.local.set({ [DENIALS_KEY]: denials });
			pending?.resolve(approval);
		} else {
			const denials = await this.denials();
			denials[decision.sessionId] = this.now() + DENIAL_COOLDOWN_MS;
			await this.chrome.storage.local.set({ [DENIALS_KEY]: denials });
			pending?.reject(new BridgeError("BROWSER_CHROME_CONSENT_DENIED", "The user denied this Orchestrator-managed session access to Chrome"));
		}
		if (pending?.windowId !== undefined) await this.chrome.windows.remove(pending.windowId).catch(() => undefined);
	}

	approvalFor(approvals: Record<string, Approval>, sessionId: string): Approval | undefined {
		return approvals[sessionId] ?? approvals[ALL_SESSIONS];
	}

	async revoke(sessionId?: string, includeAllSessions = false): Promise<void> {
		const approvals = sessionId ? await this.approvals() : {};
		if (sessionId) delete approvals[sessionId];
		if (includeAllSessions) delete approvals[ALL_SESSIONS];
		await this.chrome.storage.local.set({ [APPROVALS_KEY]: approvals });
		for (const [id, pending] of this.pending) {
			if (sessionId && id !== sessionId) continue;
			this.pending.delete(id);
			pending.reject(new BridgeError("BROWSER_CHROME_STOPPED", "The user stopped Orchestrator agents in Chrome"));
		}
	}

	private async denials(): Promise<Record<string, number>> {
		const stored = await this.chrome.storage.local.get(DENIALS_KEY);
		return isRecord(stored[DENIALS_KEY]) ? (stored[DENIALS_KEY] as Record<string, number>) : {};
	}
}
