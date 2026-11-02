// Executes `ao browser` actions in the user's Chrome. Every action is scoped
// to the session's AO tab group, gated by per-session consent and the site
// policy, and returns the same result shapes as AO's Browser panel.

import { matchInstruction, type ActCandidate } from "../main/browser-act-matcher";
import { sanitizeBrowserTitle, sanitizeBrowserURL } from "../shared/browser-sanitize";
import type { ChromeApi, Tab } from "./chrome-api";
import type { Approval, ConsentManager } from "./consent";
import type { ContentResponse } from "./content";
import { snapshotDelta, type SnapshotState } from "./delta";
import type { Annotation, Geometry } from "./dom-actions";
import type { NetworkCapture } from "./network";
import { evaluateUrl, parsePolicy, type SitePolicy, trustedInputAllowed } from "./policy";
import { BridgeError, isRecord, stringArg } from "./protocol";
import { annotatePng, base64ToBytes, bytesToBase64, MAX_SCREENSHOT_BYTES, pngSize } from "./screenshot";
import type { ActiveTab, SessionTabRegistry, SessionTabs } from "./sessions";
import type { RefInfo } from "./snapshot";
import type { TrustedInput } from "./trusted-input";
import { normalizeAgentURL, originOf } from "./urls";

const ACT_VERBS = new Set(["click", "dblclick", "focus", "hover", "fill", "type", "check", "uncheck"]);
const NAVIGATION_TIMEOUT_MS = 30_000;
const POSTCONDITION_POLL_MS = 150;
const CONTENT_TIMEOUT_MS = 15_000;
export const POLICY_KEY = "aoSitePolicy";

type FrameLink = { frameId: number; parentFrameId: number; ref: string };
// The selected frame, plus every frame between it and the page (outermost
// first): trusted input needs the sum of all their offsets.
type FrameSelection = { logicalTabId: string; frameId: number; chain: FrameLink[] };
type SessionExtras = {
	frame?: FrameSelection;
	baseline?: SnapshotState & { logicalTabId: string; interactive: boolean };
	revision: number;
};

export type ExecutorDeps = {
	chrome: ChromeApi;
	sessions: SessionTabRegistry;
	consent: ConsentManager;
	trusted: TrustedInput;
	network: NetworkCapture;
};

type Context = {
	sessionId: string;
	session: SessionTabs;
	extras: SessionExtras;
	approval: Approval;
	policy: SitePolicy;
	signal: AbortSignal;
};

export class Executor {
	private readonly extras = new Map<string, SessionExtras>();
	/** One activate-and-capture at a time per Chrome window, across sessions. */
	private readonly captureLocks = new Map<number, Promise<unknown>>();
	private readonly navigationCounts = new Map<number, number>();

	constructor(private readonly deps: ExecutorDeps) {}

	start(): void {
		const count = (tabId: number, frameId: number) => {
			if (frameId === 0) this.navigationCounts.set(tabId, (this.navigationCounts.get(tabId) ?? 0) + 1);
		};
		this.deps.chrome.webNavigation.onCommitted.addListener((details) => count(details.tabId, details.frameId));
	}

	private extrasFor(sessionId: string): SessionExtras {
		let extras = this.extras.get(sessionId);
		if (!extras) {
			extras = { revision: 0 };
			this.extras.set(sessionId, extras);
		}
		return extras;
	}

	private async policy(): Promise<SitePolicy> {
		const stored = await this.deps.chrome.storage.local.get(POLICY_KEY);
		return parsePolicy(stored[POLICY_KEY]);
	}

	async execute(sessionId: string, action: string, args: Record<string, unknown>, signal: AbortSignal): Promise<unknown> {
		if (action === "__destroy-session") {
			this.deps.network.dispose(sessionId);
			this.extras.delete(sessionId);
			await this.deps.sessions.destroy(sessionId);
			await this.deps.consent.revoke(sessionId);
			return {};
		}
		if (action.startsWith("__")) throw new BridgeError("BROWSER_ACTION_UNSUPPORTED_IN_TARGET", `${action} is not available in Chrome mode`);
		if (action === "devtools-open" || action === "devtools-close") {
			throw new BridgeError("BROWSER_ACTION_UNSUPPORTED_IN_TARGET", "Open Chrome DevTools yourself in Chrome mode (Cmd/Ctrl+Option/Alt+I)");
		}
		const approval = await this.deps.consent.ensure(sessionId);
		const session = await this.deps.sessions.ensure(sessionId);
		const context: Context = { sessionId, session, extras: this.extrasFor(sessionId), approval, policy: await this.policy(), signal };
		throwIfAborted(signal);
		return this.dispatch(context, action, args);
	}

	private async dispatch(ctx: Context, action: string, args: Record<string, unknown>): Promise<unknown> {
		switch (action) {
			case "open":
				return this.open(ctx, normalizeAgentURL(stringArg(args, "url", "URL_REQUIRED", "url is required")));
			case "snapshot":
				return this.snapshot(ctx, args.interactive === true, args.delta === true, args.full === true);
			case "act":
				return this.act(ctx, args);
			case "click":
			case "dblclick":
			case "hover":
			case "focus":
			case "highlight":
			case "scrollintoview":
			case "check":
			case "uncheck":
				return this.elementAction(ctx, action, stringArg(args, "ref", "REFERENCE_REQUIRED", "ref is required"), args);
			case "fill":
			case "type":
				return this.elementAction(ctx, action, stringArg(args, "ref", "REFERENCE_REQUIRED", "ref is required"), {
					text: stringArg(args, "text", "INVALID_ARGUMENT", "text is required", true),
				});
			case "press":
				return this.press(ctx, stringArg(args, "key", "INVALID_ARGUMENT", "key is required"));
			case "drag":
				return this.drag(
					ctx,
					stringArg(args, "ref", "REFERENCE_REQUIRED", "ref is required"),
					stringArg(args, "targetRef", "REFERENCE_REQUIRED", "target ref is required"),
					args.human === true,
				);
			case "select":
				return this.elementAction(ctx, "select", stringArg(args, "ref", "REFERENCE_REQUIRED", "ref is required"), {
					value: stringArg(args, "value", "INVALID_ARGUMENT", "value is required", true),
				});
			case "unhighlight":
				return this.withPage(ctx, (page) => this.content(page, ctx, { op: "unhighlight" }).then(() => this.result(page)));
			case "scroll": {
				const direction = stringArg(args, "direction", "INVALID_ARGUMENT", "direction is required");
				const amount = Math.min(5_000, Math.max(1, Number(args.amount) || 600));
				return this.withPage(ctx, async (page) => ({ ...(await this.content(page, ctx, { op: "scroll", direction, amount }) as object), ...this.result(page) }));
			}
			case "get":
				return this.get(ctx, stringArg(args, "property", "INVALID_ARGUMENT", "property is required"), typeof args.ref === "string" ? args.ref : undefined);
			case "wait":
				return this.wait(ctx, args);
			case "tabs":
				return this.tabsResult(ctx.session);
			case "tab-new":
				return this.tabNew(ctx, typeof args.url === "string" && args.url.trim() ? normalizeAgentURL(args.url) : undefined);
			case "tab-select": {
				const tabId = stringArg(args, "tabId", "TAB_ID_REQUIRED", "tabId is required");
				await this.deps.sessions.select(ctx.session, tabId);
				this.resetPageState(ctx);
				return this.tabResult(ctx.session, tabId);
			}
			case "tab-close": {
				const tabId = typeof args.tabId === "string" && args.tabId.trim() ? args.tabId.trim() : (ctx.session.activeTabId ?? "");
				await this.deps.sessions.close(ctx.session, tabId);
				this.resetPageState(ctx);
				return { closedTabId: tabId, ...(await this.tabsResult(ctx.session)) };
			}
			case "frame":
				return this.frame(ctx, stringArg(args, "target", "INVALID_ARGUMENT", "frame target is required"));
			case "dialog":
				return this.withPage(ctx, async (page) => {
					const operation = String(args.operation ?? "status");
					if (operation !== "status" && operation !== "accept" && operation !== "dismiss") {
						throw new BridgeError("INVALID_ARGUMENT", "operation must be status, accept, or dismiss");
					}
					return {
						...(await this.deps.trusted.dialog(page.tabId, operation, typeof args.text === "string" ? args.text : undefined, ctx.signal)),
						target: this.target(page),
					};
				}, { allowBlocked: true });
			case "screenshot":
				return this.screenshot(ctx, args.annotate === true);
			case "network-start":
				return this.withPage(ctx, async (page) =>
					this.deps.network.start(ctx.sessionId, page.logicalId, page.tabId, Math.min(300, Math.max(1, Number(args.durationSeconds) || 60))),
				);
			case "network-status":
				return this.deps.network.status(ctx.sessionId, ctx.session.activeTabId);
			case "network-list":
				return this.deps.network.list(ctx.sessionId, ctx.session.activeTabId);
			case "network-stop":
				return this.deps.network.stop(ctx.sessionId, "stopped");
			case "network-clear":
				return this.deps.network.clear(ctx.sessionId, ctx.session.activeTabId);
			case "console":
			case "errors":
				return this.withPage(ctx, async (page) => {
					const messages = await this.deps.trusted.consoleMessages(page.tabId, ctx.signal);
					return { messages: action === "console" ? messages.console : messages.errors, target: this.target(page), untrustedExternalContent: true };
				}, { allowBlocked: true });
			default:
				throw new BridgeError("INVALID_ARGUMENT", `Unsupported browser action: ${action}`);
		}
	}

	// --- pages, content scripts, and policy -------------------------------

	private async withPage<T>(ctx: Context, fn: (page: ActiveTab) => Promise<T>, options: { allowBlocked?: boolean } = {}): Promise<T> {
		const page = await this.deps.sessions.active(ctx.session);
		if (!options.allowBlocked) this.assertAllowed(ctx.policy, page.tab.url ?? page.tab.pendingUrl);
		throwIfAborted(ctx.signal);
		return fn(page);
	}

	private assertAllowed(policy: SitePolicy, url: string | undefined): void {
		const decision = evaluateUrl(policy, url);
		if (!decision.allowed) throw new BridgeError("BROWSER_SITE_BLOCKED", decision.reason);
	}

	/**
	 * Current URLs of a frame and every frame above it, up to (not including)
	 * the page itself, which withPage checks. Frames can navigate after they
	 * are selected, so these are read fresh from Chrome each time.
	 */
	private async frameChainUrls(page: ActiveTab, frameId: number): Promise<string[]> {
		if (frameId === 0) return [];
		const frames = (await this.deps.chrome.webNavigation.getAllFrames({ tabId: page.tabId })) ?? [];
		const byId = new Map(frames.map((frame) => [frame.frameId, frame]));
		const urls: string[] = [];
		for (let id = frameId; id > 0; ) {
			const frame = byId.get(id);
			if (!frame) throw new BridgeError("FRAME_NOT_FOUND", "The selected frame is gone; select it again or use `ao browser frame main`");
			urls.push(frame.url);
			id = frame.parentFrameId;
		}
		return urls;
	}

	/** Apply the site policy to a frame's own address and its ancestors'. */
	private async assertFrameAllowed(ctx: Context, page: ActiveTab, frameId: number): Promise<void> {
		for (const url of await this.frameChainUrls(page, frameId)) {
			// about:blank and srcdoc frames inherit their parent, checked in turn.
			if (inheritsParentOrigin(url)) continue;
			const decision = evaluateUrl(ctx.policy, url);
			if (!decision.allowed) throw new BridgeError("BROWSER_SITE_BLOCKED", `${decision.reason} (in a frame on this page)`);
		}
	}

	private frameFor(ctx: Context, page: ActiveTab): number {
		const frame = ctx.extras.frame;
		return frame && frame.logicalTabId === page.logicalId ? frame.frameId : 0;
	}

	private async content(page: ActiveTab, ctx: Context, request: Record<string, unknown>, frameId = this.frameFor(ctx, page)): Promise<unknown> {
		const dialog = this.deps.trusted.pendingDialog(page.tabId);
		if (dialog) {
			throw new BridgeError("BROWSER_DIALOG_OPEN", `A ${dialog.type} dialog is open; answer it with \`ao browser dialog accept|dismiss\``);
		}
		// The tab's own address is checked by withPage; a selected frame (or an
		// ancestor of it) may host a site the policy denies.
		await this.assertFrameAllowed(ctx, page, frameId);
		const message = { aoBridge: 1, ...request };
		// A page blocked by a dialog never answers; do not hang the command.
		const limit = (typeof request.timeoutMs === "number" ? request.timeoutMs : 0) + CONTENT_TIMEOUT_MS;
		const send = () =>
			Promise.race([
				this.deps.chrome.tabs.sendMessage(page.tabId, message, { frameId }) as Promise<ContentResponse | undefined>,
				sleep(limit).then(() => {
					throw new BridgeError("BROWSER_PAGE_UNRESPONSIVE", "The page did not respond (is a dialog open, or is the page hung?)");
				}),
			]);
		let response: ContentResponse | undefined;
		try {
			response = await send();
		} catch (error) {
			if (error instanceof BridgeError) throw error;
			response = undefined;
		}
		if (!response) {
			try {
				await this.deps.chrome.scripting.executeScript({ target: { tabId: page.tabId, frameIds: [frameId] }, files: ["content.js"] });
			} catch (error) {
				throw new BridgeError("BROWSER_PAGE_RESTRICTED", `AO cannot work on this page: ${error instanceof Error ? error.message : String(error)}`);
			}
			response = await send();
		}
		if (!response) throw new BridgeError("BROWSER_TARGET_UNAVAILABLE", "The page did not answer");
		if (!response.ok) throw new BridgeError(response.error.code, response.error.message);
		return response.value;
	}

	private target(page: ActiveTab): Record<string, unknown> {
		const url = page.tab.url ?? page.tab.pendingUrl ?? "";
		return { tabId: page.logicalId, url: sanitizeBrowserURL(url), origin: originOf(url) };
	}

	private result(page: ActiveTab): Record<string, unknown> {
		return { url: sanitizeBrowserURL(page.tab.url ?? ""), target: this.target(page) };
	}

	private async refreshed(page: ActiveTab): Promise<ActiveTab> {
		const tab = await this.deps.chrome.tabs.get(page.tabId).catch(() => page.tab);
		return { ...page, tab };
	}

	private resetPageState(ctx: Context): void {
		ctx.extras.frame = undefined;
		ctx.extras.baseline = undefined;
	}

	/** Trusted input only when the tab and every frame on the way allow it. */
	private async useTrustedInput(ctx: Context, page: ActiveTab): Promise<boolean> {
		await this.assertFrameAllowed(ctx, page, this.frameFor(ctx, page));
		if (!ctx.approval.trusted || !trustedInputAllowed(ctx.policy, page.tab.url)) return false;
		const urls = await this.frameChainUrls(page, this.frameFor(ctx, page));
		return urls.every((url) => inheritsParentOrigin(url) || trustedInputAllowed(ctx.policy, url));
	}

	/**
	 * Chrome drops trusted input to hidden pages, so show the agent's tab in
	 * its window (without focusing the window) and wait until it is visible.
	 */
	private async showTab(ctx: Context, page: ActiveTab): Promise<void> {
		if (!page.tab.active) await this.deps.chrome.tabs.update(page.tabId, { active: true });
		const deadline = Date.now() + 2_000;
		for (;;) {
			const state = (await this.content(page, ctx, { op: "ping" }, 0)) as { visibility?: string };
			if (state.visibility !== "hidden") return;
			if (Date.now() >= deadline) {
				throw new BridgeError(
					"BROWSER_TAB_HIDDEN",
					"Chrome is hiding the AO tab (is its window minimized?); trusted input needs the tab on screen",
				);
			}
			await sleep(50, ctx.signal);
		}
	}

	// --- navigation and tabs ----------------------------------------------

	private async waitForNavigation(tabId: number, signal: AbortSignal): Promise<boolean> {
		throwIfAborted(signal);
		const chrome = this.deps.chrome;
		return new Promise<boolean>((resolve, reject) => {
			let done = false;
			const finish = (loaded: boolean, error?: Error) => {
				if (done) return;
				done = true;
				chrome.webNavigation.onCompleted.removeListener(onCompleted);
				chrome.webNavigation.onErrorOccurred.removeListener(onError);
				clearTimeout(timer);
				signal.removeEventListener("abort", onAbort);
				if (error) reject(error);
				else resolve(loaded);
			};
			const onCompleted = (details: { tabId: number; frameId: number }) => {
				if (details.tabId === tabId && details.frameId === 0) finish(true);
			};
			const onError = (details: { tabId: number; frameId: number; error?: string }) => {
				if (details.tabId === tabId && details.frameId === 0 && details.error !== "net::ERR_ABORTED") {
					finish(false, new BridgeError("NAVIGATION_FAILED", `Navigation failed: ${details.error ?? "unknown error"}`));
				}
			};
			const onAbort = () => finish(false, new BridgeError("BROWSER_COMMAND_CANCELED", "Browser command was canceled"));
			const timer = setTimeout(() => finish(false), NAVIGATION_TIMEOUT_MS);
			chrome.webNavigation.onCompleted.addListener(onCompleted);
			chrome.webNavigation.onErrorOccurred.addListener(onError);
			signal.addEventListener("abort", onAbort, { once: true });
		});
	}

	private async open(ctx: Context, url: string): Promise<unknown> {
		this.assertAllowed(ctx.policy, url);
		const page = await this.deps.sessions.active(ctx.session);
		throwIfAborted(ctx.signal);
		this.resetPageState(ctx);
		const loaded = this.waitForNavigation(page.tabId, ctx.signal);
		await this.deps.chrome.tabs.update(page.tabId, { url });
		const complete = await loaded;
		const after = await this.refreshed(page);
		return {
			url: sanitizeBrowserURL(after.tab.url ?? url),
			title: sanitizeBrowserTitle(after.tab.title ?? ""),
			isLoading: !complete,
			target: this.target(after),
			untrustedExternalContent: true,
		};
	}

	private async tabNew(ctx: Context, url: string | undefined): Promise<unknown> {
		if (url) this.assertAllowed(ctx.policy, url);
		throwIfAborted(ctx.signal);
		const created = await this.deps.sessions.createTab(ctx.session, undefined);
		throwIfAborted(ctx.signal);
		this.resetPageState(ctx);
		if (url) {
			const loaded = this.waitForNavigation(created.tabId, ctx.signal);
			await this.deps.chrome.tabs.update(created.tabId, { url });
			await loaded;
		}
		return this.tabResult(ctx.session, created.logicalId);
	}

	private async tabResult(session: SessionTabs, logicalId: string): Promise<unknown> {
		const page = await this.deps.sessions.tab(session, logicalId);
		return tabState(page, logicalId === session.activeTabId);
	}

	private async tabsResult(session: SessionTabs): Promise<Record<string, unknown>> {
		const tabs: Record<string, unknown>[] = [];
		for (const logicalId of Object.keys(session.tabs)) {
			const page = await this.deps.sessions.tab(session, logicalId).catch(() => undefined);
			if (page) tabs.push(tabState(page, logicalId === session.activeTabId));
		}
		return { activeTabId: session.activeTabId ?? "", tabs, untrustedExternalContent: true };
	}

	private async frame(ctx: Context, target: string): Promise<unknown> {
		return this.withPage(ctx, async (page) => {
			if (target === "main") {
				ctx.extras.frame = undefined;
				ctx.extras.baseline = undefined;
				return { frame: "main", target: this.target(page) };
			}
			const parentFrameId = this.frameFor(ctx, page);
			const info = (await this.content(page, ctx, { op: "frameOffset", ref: target }, parentFrameId)) as { src: string; name: string };
			const frames = (await this.deps.chrome.webNavigation.getAllFrames({ tabId: page.tabId })) ?? [];
			const children = frames.filter((frame) => frame.parentFrameId === parentFrameId);
			const matches = children.filter((frame) => frame.url === info.src || (info.src === "" && inheritsParentOrigin(frame.url)));
			if (matches.length !== 1) {
				throw new BridgeError(
					"FRAME_NOT_FOUND",
					matches.length === 0 ? "That frame has not loaded" : "Several frames load the same address; AO cannot tell them apart",
				);
			}
			const current = ctx.extras.frame && ctx.extras.frame.logicalTabId === page.logicalId ? ctx.extras.frame.chain : [];
			const frameId = matches[0]!.frameId;
			// Never select a frame hosting a site the policy denies.
			await this.assertFrameAllowed(ctx, page, frameId);
			ctx.extras.frame = { logicalTabId: page.logicalId, frameId, chain: [...current, { frameId, parentFrameId, ref: target }] };
			ctx.extras.baseline = undefined;
			return { frame: target, name: info.name, url: sanitizeBrowserURL(matches[0]!.url), target: this.target(page) };
		});
	}

	// --- snapshot and act ---------------------------------------------------

	private async rawSnapshot(ctx: Context, page: ActiveTab, interactive: boolean): Promise<{ text: string; refs: Record<string, RefInfo> }> {
		const value = (await this.content(page, ctx, { op: "snapshot", interactive })) as { snapshot: string; refs: Record<string, RefInfo> };
		return { text: value.snapshot, refs: value.refs };
	}

	private async snapshot(ctx: Context, interactive: boolean, delta: boolean, full: boolean): Promise<unknown> {
		return this.withPage(ctx, async (page) => {
			const current = await this.rawSnapshot(ctx, page, interactive);
			if (!delta) {
				ctx.extras.baseline = undefined;
				return { text: current.text, refs: current.refs, target: this.target(page), untrustedExternalContent: true };
			}
			const base = ctx.extras.baseline;
			const usable = base && base.logicalTabId === page.logicalId && base.interactive === interactive ? base : undefined;
			const revision = ++ctx.extras.revision;
			const result = snapshotDelta(usable, current, revision, full);
			ctx.extras.baseline = { revision, text: current.text, refs: current.refs, logicalTabId: page.logicalId, interactive };
			return { ...result, untrustedExternalContent: true };
		});
	}

	private async act(ctx: Context, args: Record<string, unknown>): Promise<unknown> {
		const instruction = stringArg(args, "instruction", "INVALID_ARGUMENT", "instruction is required");
		const verb = typeof args.action === "string" && args.action.trim() ? args.action.trim() : "click";
		if (!ACT_VERBS.has(verb)) throw new BridgeError("INVALID_ARGUMENT", `Unsupported act verb: ${verb}`);
		const value = verb === "fill" || verb === "type" ? stringArg(args, "value", "INVALID_ARGUMENT", "value is required", true) : undefined;
		const nth = typeof args.nth === "number" && Number.isFinite(args.nth) ? Math.trunc(args.nth) : undefined;
		const postcondition = parsePostcondition(args.postcondition);
		return this.withPage(ctx, async (page) => {
			const resolve = async () => {
				const snapshot = await this.rawSnapshot(ctx, page, true);
				return { snapshot, match: matchInstruction(instruction, snapshot.refs, { nth }) };
			};
			const unresolved = (outcome: "ambiguous" | "no-match", candidates: unknown, snapshot: string) => {
				ctx.extras.baseline = undefined;
				return { outcome, instruction, ...(outcome === "ambiguous" ? { candidates } : {}), snapshot, untrustedExternalContent: true };
			};
			let dispatched = false;
			const perform = async (candidate: ActCandidate, retried: boolean, beforeSnapshot: string) => {
				const beforeNavigations = this.navigationCounts.get(page.tabId) ?? 0;
				const before = { tabId: page.logicalId, url: sanitizeBrowserURL(page.tab.url ?? "") };
				const alreadySatisfied = postcondition ? await this.postconditionHoldsNow(ctx, page, postcondition) : false;
				const result = (await this.elementAction(ctx, verb, candidate.ref, value !== undefined ? { text: value } : {})) as Record<string, unknown>;
				dispatched = true;
				const observed = !postcondition
					? undefined
					: postcondition.kind === "dialog" && result.dialogOpened === true
						? { status: "satisfied", kind: "dialog", dialog: result.dialog }
						: await this.observePostcondition(ctx, page, postcondition, beforeNavigations, beforeSnapshot, alreadySatisfied);
				const after = await this.refreshed(page);
				const navigated = (this.navigationCounts.get(page.tabId) ?? 0) > beforeNavigations;
				return {
					outcome: "matched",
					resolvedRef: candidate.ref,
					candidate,
					result,
					inputDispatched: true,
					before,
					after: { tabId: page.logicalId, url: sanitizeBrowserURL(after.tab.url ?? "") },
					navigation: { status: navigated ? "observed" : "not-observed" },
					...(observed ? { postcondition: observed } : {}),
					retried,
					untrustedExternalContent: true,
				};
			};
			const first = await resolve();
			if (first.match.outcome !== "matched") {
				return unresolved(first.match.outcome, "candidates" in first.match ? first.match.candidates : undefined, first.snapshot.text);
			}
			try {
				return await perform(first.match.candidate, false, first.snapshot.text);
			} catch (error) {
				if (dispatched || !(error instanceof BridgeError) || error.code !== "STALE_REFERENCE") throw error;
				const second = await resolve();
				if (second.match.outcome !== "matched") {
					return unresolved(second.match.outcome, "candidates" in second.match ? second.match.candidates : undefined, second.snapshot.text);
				}
				return perform(second.match.candidate, true, second.snapshot.text);
			}
		});
	}

	private async postconditionHoldsNow(ctx: Context, page: ActiveTab, postcondition: Postcondition): Promise<boolean> {
		if (postcondition.kind === "url") return ((await this.refreshed(page)).tab.url ?? "").includes(postcondition.value!);
		if (postcondition.kind === "text") {
			const text = (await this.content(page, ctx, { op: "get", property: "text" })) as { value: string };
			return text.value.includes(postcondition.value!);
		}
		return false;
	}

	private async observePostcondition(
		ctx: Context,
		page: ActiveTab,
		postcondition: Postcondition,
		beforeNavigations: number,
		beforeSnapshot: string,
		alreadySatisfied: boolean,
	): Promise<Record<string, unknown>> {
		const deadline = Date.now() + postcondition.timeoutMs;
		const navigated = () => (this.navigationCounts.get(page.tabId) ?? 0) > beforeNavigations;
		if ((postcondition.kind === "url" || postcondition.kind === "text") && alreadySatisfied && !navigated()) {
			return { status: "already-satisfied", kind: postcondition.kind, expected: postcondition.value, reason: "pre-existing" };
		}
		for (;;) {
			throwIfAborted(ctx.signal);
			try {
				if (postcondition.kind === "navigation" && navigated()) return { status: "satisfied", kind: "navigation" };
				if ((postcondition.kind === "url" || postcondition.kind === "text") && (await this.postconditionHoldsNow(ctx, page, postcondition))) {
					return { status: "satisfied", kind: postcondition.kind, expected: postcondition.value };
				}
				if (postcondition.kind === "dom-change") {
					if (navigated()) return { status: "satisfied", kind: "dom-change" };
					const snapshot = await this.rawSnapshot(ctx, page, true);
					if (snapshot.text !== beforeSnapshot) return { status: "satisfied", kind: "dom-change" };
				}
				if (postcondition.kind === "dialog") {
					const dialog = await this.deps.trusted.dialog(page.tabId, "status");
					if (dialog.open === true) return { status: "satisfied", kind: "dialog", dialog };
				}
			} catch (error) {
				// The page may be between documents mid-navigation; keep observing.
				if (!(error instanceof BridgeError) || !["BROWSER_TARGET_UNAVAILABLE", "BROWSER_PAGE_RESTRICTED", "STALE_REFERENCE"].includes(error.code)) throw error;
			}
			const remaining = deadline - Date.now();
			if (remaining <= 0) {
				return {
					status: "unmet",
					kind: postcondition.kind,
					...(postcondition.value !== undefined ? { expected: postcondition.value } : {}),
					reason: "timeout",
				};
			}
			await sleep(Math.min(POSTCONDITION_POLL_MS, remaining));
		}
	}

	// --- element actions ------------------------------------------------------

	private async point(ctx: Context, page: ActiveTab, ref: string): Promise<{ x: number; y: number }> {
		const geometry = (await this.content(page, ctx, { op: "geometry", ref })) as Geometry;
		if (!geometry.inViewport || geometry.width <= 0 || geometry.height <= 0) {
			throw new BridgeError("ELEMENT_NOT_VISIBLE", `Element ${ref} is not visible on screen`);
		}
		const frame = ctx.extras.frame;
		if (!frame || frame.logicalTabId !== page.logicalId) return { x: geometry.x, y: geometry.y };
		// Frame coordinates are relative to the frame; trusted input uses the
		// page's. Add each enclosing iframe's offset within its own parent.
		let x = geometry.x;
		let y = geometry.y;
		for (const link of frame.chain) {
			const offset = (await this.content(page, ctx, { op: "frameOffset", ref: link.ref }, link.parentFrameId)) as { x: number; y: number };
			x += offset.x;
			y += offset.y;
		}
		return { x, y };
	}

	private async elementAction(ctx: Context, action: string, ref: string, args: Record<string, unknown>): Promise<unknown> {
		return this.withPage(ctx, async (page) => {
			const trusted = await this.useTrustedInput(ctx, page);
			let value: unknown;
			switch (action) {
				case "click":
				case "dblclick":
					if (trusted) {
						await this.showTab(ctx, page);
						const input = await this.deps.trusted.click(page.tabId, await this.point(ctx, page, ref), {
							clicks: action === "dblclick" ? 2 : 1,
							human: args.human === true,
							signal: ctx.signal,
						});
						value = { clicked: true, ...input };
					} else {
						value = await this.content(page, ctx, { op: "click", ref, clicks: action === "dblclick" ? 2 : 1 });
					}
					break;
				case "hover":
					if (trusted) {
						await this.showTab(ctx, page);
						value = { hovered: true, ...(await this.deps.trusted.hover(page.tabId, await this.point(ctx, page, ref), args.human === true, ctx.signal)) };
					} else {
						value = await this.content(page, ctx, { op: "hover", ref });
					}
					break;
				case "type":
					if (trusted) {
						await this.showTab(ctx, page);
						await this.content(page, ctx, { op: "focus", ref });
						value = { typed: true, ...(await this.deps.trusted.insertText(page.tabId, String(args.text ?? ""), ctx.signal)) };
					} else {
						value = await this.content(page, ctx, { op: "type", ref, text: args.text });
					}
					break;
				default:
					value = await this.content(page, ctx, { op: action, ref, ...args });
			}
			const after = await this.refreshed(page);
			return { ...(isRecord(value) ? value : {}), ...this.result(after), inputTier: trusted ? "trusted" : "synthetic" };
		});
	}

	private async press(ctx: Context, key: string): Promise<unknown> {
		return this.withPage(ctx, async (page) => {
			let input = {};
			if (await this.useTrustedInput(ctx, page)) {
				await this.showTab(ctx, page);
				input = await this.deps.trusted.press(page.tabId, key, ctx.signal);
			} else await this.content(page, ctx, { op: "press", key });
			return { pressed: key, ...input, ...this.result(await this.refreshed(page)) };
		});
	}

	private async drag(ctx: Context, ref: string, targetRef: string, human: boolean): Promise<unknown> {
		return this.withPage(ctx, async (page) => {
			if (!(await this.useTrustedInput(ctx, page))) {
				throw new BridgeError("BROWSER_TRUSTED_INPUT_REQUIRED", "Dragging needs trusted input, which is off for this site");
			}
			await this.showTab(ctx, page);
			const from = await this.point(ctx, page, ref);
			const to = await this.point(ctx, page, targetRef);
			const input = await this.deps.trusted.drag(page.tabId, from, to, human, ctx.signal);
			return { dragged: true, ...input, ...this.result(await this.refreshed(page)) };
		});
	}

	private async get(ctx: Context, property: string, ref: string | undefined): Promise<unknown> {
		return this.withPage(ctx, async (page) => {
			const fresh = await this.refreshed(page);
			if (property === "url" && !ref) {
				const url = sanitizeBrowserURL(fresh.tab.url ?? "");
				return { url, value: url, target: this.target(fresh), untrustedExternalContent: true };
			}
			if (property === "title" && !ref) {
				const title = sanitizeBrowserTitle(fresh.tab.title ?? "");
				return { title, value: title, target: this.target(fresh), untrustedExternalContent: true };
			}
			if ((property === "value" || property === "checked") && !ref) {
				throw new BridgeError("REFERENCE_REQUIRED", `get ${property} needs an element ref`);
			}
			const value = (await this.content(fresh, ctx, { op: "get", property, ref })) as { value: unknown };
			return { value: value.value, target: this.target(fresh), untrustedExternalContent: true };
		});
	}

	private async wait(ctx: Context, args: Record<string, unknown>): Promise<unknown> {
		const timeoutMs = Math.min(55_000, Math.max(1, Number(args.timeoutMs) || 10_000));
		if (typeof args.ms === "number") {
			await sleep(Math.min(args.ms, 55_000), ctx.signal);
			return { waited: args.ms };
		}
		return this.withPage(ctx, async (page) => {
			const deadline = Date.now() + timeoutMs;
			const timeout = () => new BridgeError("AGENT_BROWSER_WAIT_TIMEOUT", `Timed out after ${timeoutMs}ms waiting for the condition`);
			if (typeof args.url === "string" || args.load === true) {
				for (;;) {
					throwIfAborted(ctx.signal);
					const tab = (await this.refreshed(page)).tab;
					if (typeof args.url === "string" ? (tab.url ?? "").includes(args.url) : tab.status === "complete") {
						return { satisfied: true, ...this.result({ ...page, tab }) };
					}
					if (Date.now() >= deadline) throw timeout();
					await sleep(POSTCONDITION_POLL_MS, ctx.signal);
				}
			}
			const [kind, value] =
				typeof args.text === "string"
					? ["text", args.text]
					: typeof args.textGone === "string"
						? ["text-gone", args.textGone]
						: typeof args.selector === "string"
							? ["selector", args.selector]
							: typeof args.selectorGone === "string"
								? ["selector-gone", args.selectorGone]
								: typeof args.stableMs === "number"
									? ["dom-stable", String(args.stableMs)]
									: ["", ""];
			if (!kind) throw new BridgeError("INVALID_ARGUMENT", "wait needs exactly one condition");
			// Retry through document replacement during navigation.
			for (;;) {
				throwIfAborted(ctx.signal);
				const remaining = deadline - Date.now();
				if (remaining <= 0) throw timeout();
				try {
					const result = (await this.content(page, ctx, { op: "wait", kind, value, timeoutMs: remaining })) as { satisfied: boolean };
					if (result.satisfied) return { satisfied: true, ...this.result(await this.refreshed(page)) };
					throw timeout();
				} catch (error) {
					if (error instanceof BridgeError && ["BROWSER_TARGET_UNAVAILABLE", "BROWSER_PAGE_RESTRICTED"].includes(error.code) && Date.now() < deadline) {
						await sleep(POSTCONDITION_POLL_MS, ctx.signal);
						continue;
					}
					throw error;
				}
			}
		});
	}

	/**
	 * captureVisibleTab captures whatever tab is in front of a window, not a
	 * given tab. Serialize activation and capture per window across sessions,
	 * and refuse the pixels if the window showed any other tab meanwhile, so a
	 * screenshot can never contain another session's page or the user's tab.
	 */
	private async captureOwnTab(ctx: Context, page: ActiveTab): Promise<string> {
		const chrome = this.deps.chrome;
		for (let attempt = 0; ; attempt++) {
			const fresh = await this.refreshed(page);
			const windowId = fresh.tab.windowId;
			const previous = this.captureLocks.get(windowId) ?? Promise.resolve();
			const run = previous.catch(() => undefined).then(async () => {
				throwIfAborted(ctx.signal);
				await this.showTab(ctx, fresh);
				let switched = false;
				const onActivated = (info: { windowId: number }) => {
					if (info.windowId === windowId) switched = true;
				};
				chrome.tabs.onActivated.addListener(onActivated);
				try {
					const isOurs = async () => {
						const [front] = await chrome.tabs.query({ windowId, active: true });
						return front?.id === fresh.tabId;
					};
					if (!(await isOurs())) return undefined;
					const dataUrl = await chrome.tabs.captureVisibleTab(windowId, { format: "png" });
					return !switched && (await isOurs()) ? dataUrl : undefined;
				} finally {
					chrome.tabs.onActivated.removeListener(onActivated);
				}
			});
			this.captureLocks.set(windowId, run);
			let dataUrl: string | undefined;
			try {
				dataUrl = await run;
			} finally {
				if (this.captureLocks.get(windowId) === run) this.captureLocks.delete(windowId);
			}
			if (dataUrl !== undefined) return dataUrl;
			if (attempt >= 1) {
				throw new BridgeError("BROWSER_TARGET_CHANGED", "Chrome showed another tab while capturing the session's tab; retry the screenshot");
			}
		}
	}

	private async screenshot(ctx: Context, annotate: boolean): Promise<unknown> {
		return this.withPage(ctx, async (page) => {
			let items: Annotation[] = [];
			let dpr = 1;
			if (annotate) {
				const value = (await this.content(page, ctx, { op: "annotations" }, 0)) as { annotations: Annotation[]; dpr: number };
				items = value.annotations;
				dpr = value.dpr || 1;
			}
			const dataUrl = await this.captureOwnTab(ctx, page);
			let png = base64ToBytes(dataUrl.slice(dataUrl.indexOf(",") + 1));
			if (annotate && items.length > 0) png = await annotatePng(png, items, dpr);
			if (png.length > MAX_SCREENSHOT_BYTES) throw new BridgeError("SCREENSHOT_UNAVAILABLE", "Browser screenshot exceeded AO's size limit");
			const { width, height } = pngSize(png);
			return {
				data: bytesToBase64(png),
				width,
				height,
				...(annotate ? { annotations: items.map((item, index) => ({ number: index + 1, ref: item.ref, role: item.role, name: item.name })) } : {}),
				target: this.target(page),
				untrustedExternalContent: true,
			};
		});
	}
}

type Postcondition = { kind: "url" | "text" | "dialog" | "navigation" | "dom-change"; value?: string; timeoutMs: number };

function parsePostcondition(raw: unknown): Postcondition | undefined {
	if (raw === undefined) return undefined;
	if (!isRecord(raw)) throw new BridgeError("INVALID_ARGUMENT", "postcondition must be an object");
	const kind = raw.kind;
	if (kind !== "url" && kind !== "text" && kind !== "dialog" && kind !== "navigation" && kind !== "dom-change") {
		throw new BridgeError("INVALID_ARGUMENT", "postcondition.kind must be url, text, dialog, navigation, or dom-change");
	}
	const value = typeof raw.value === "string" ? raw.value : undefined;
	if ((kind === "url" || kind === "text") && !value) throw new BridgeError("INVALID_ARGUMENT", `postcondition ${kind} requires value`);
	const timeoutMs = raw.timeoutMs === undefined ? 10_000 : Number(raw.timeoutMs);
	if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 55_000) {
		throw new BridgeError("INVALID_ARGUMENT", "postcondition.timeoutMs must be an integer between 1 and 55000");
	}
	return { kind, ...(value !== undefined ? { value } : {}), timeoutMs };
}

function inheritsParentOrigin(url: string): boolean {
	return url === "" || url === "about:blank" || url === "about:srcdoc";
}

function tabState(page: ActiveTab, active: boolean): Record<string, unknown> {
	const tab: Tab = page.tab;
	return {
		id: page.logicalId,
		url: sanitizeBrowserURL(tab.url ?? tab.pendingUrl ?? ""),
		title: sanitizeBrowserTitle(tab.title ?? ""),
		active,
		untrustedExternalContent: true,
	};
}

function throwIfAborted(signal: AbortSignal): void {
	if (signal.aborted) throw new BridgeError("BROWSER_COMMAND_CANCELED", "Browser command was canceled");
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
	return new Promise((resolve, reject) => {
		const timer = setTimeout(() => {
			signal?.removeEventListener("abort", onAbort);
			resolve();
		}, ms);
		const onAbort = () => {
			clearTimeout(timer);
			reject(new BridgeError("BROWSER_COMMAND_CANCELED", "Browser command was canceled"));
		};
		signal?.addEventListener("abort", onAbort, { once: true });
	});
}
