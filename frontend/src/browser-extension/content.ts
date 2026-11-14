// AO Browser Bridge content script. Injected on demand into the session's
// tabs (isolated world) and answers the service worker's DOM requests. Page
// JavaScript cannot see this world, its listeners, or its element refs.

import { chromeApi } from "./chrome-api";
import {
	annotations,
	clickSynthetic,
	elementChecked,
	elementValue,
	fill,
	frameOffset,
	geometry,
	highlight,
	hoverSynthetic,
	pageText,
	pressSynthetic,
	scrollPage,
	selectOption,
	setChecked,
	typeText,
	unhighlight,
	waitFor,
	type WaitKind,
} from "./dom-actions";
import { describeApprovalTarget } from "../shared/browser-approval-describe";
import { BridgeError, isRecord } from "./protocol";
import { buildSnapshot, RefRegistry } from "./snapshot";

export type ContentRequest = { aoBridge: 1; op: string; [key: string]: unknown };
export type ContentResponse = { ok: true; value: unknown } | { ok: false; error: { code: string; message: string } };

// One id per document: a navigation (or a replaced document) gets a new one,
// so an approval bound to the old document cannot act on the new one.
const documentIds = new WeakMap<Document, string>();
function documentId(document: Document): string {
	let id = documentIds.get(document);
	if (!id) {
		id = typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
		documentIds.set(document, id);
	}
	return id;
}

// Focus ids the approval gate checks: Enter goes wherever focus is, so an
// approved Enter must still find the element that was focused when described.
const focusIds = new WeakMap<Element, string>();
function focusIdOf(document: Document, mark: boolean): string {
	const el = document.activeElement;
	if (!el || el === document.body) return "";
	let id = focusIds.get(el);
	if (!id && mark) {
		id = typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
		focusIds.set(el, id);
	}
	return id ?? "";
}

export function handleContentRequest(document: Document, registry: RefRegistry, request: ContentRequest): unknown | Promise<unknown> {
	const element = () => registry.resolve(String(request.ref ?? ""));
	switch (request.op) {
		case "ping":
			return {
				url: document.location?.href ?? "",
				title: document.title,
				visibility: document.visibilityState,
				documentId: documentId(document),
				focusId: focusIdOf(document, false),
				viewport: { width: document.defaultView?.innerWidth ?? 0, height: document.defaultView?.innerHeight ?? 0 },
				dpr: document.defaultView?.devicePixelRatio ?? 1,
			};
		case "snapshot": {
			const result = buildSnapshot(document, registry, { interactive: request.interactive === true });
			return { snapshot: result.snapshot, refs: result.refs, truncated: result.truncated };
		}
		case "geometry":
			return geometry(element());
		case "focus":
			(element() as HTMLElement).focus?.();
			return { focused: true };
		case "scrollintoview":
			(element() as HTMLElement).scrollIntoView?.({ block: "center", inline: "center" });
			return { scrolled: true };
		case "fill":
			fill(element(), String(request.text ?? ""));
			return { filled: true };
		case "type":
			typeText(element(), String(request.text ?? ""));
			return { typed: true };
		case "select":
			return { value: selectOption(element(), String(request.value ?? "")) };
		case "check":
		case "uncheck":
			return { checked: setChecked(element(), request.op === "check") };
		case "click":
			clickSynthetic(element(), request.clicks === 2 ? 2 : 1);
			return { clicked: true };
		case "hover":
			hoverSynthetic(element());
			return { hovered: true };
		case "press":
			pressSynthetic(document, String(request.key ?? ""));
			return { pressed: true };
		case "scroll":
			return scrollPage(document, String(request.direction ?? ""), Number(request.amount) || 600, request.ref ? element() : undefined);
		case "get": {
			const property = String(request.property ?? "");
			if (property === "url") return { value: document.location?.href ?? "" };
			if (property === "title") return { value: document.title };
			if (property === "text") return { value: pageText(document, request.ref ? element() : undefined) };
			if (property === "value") return { value: elementValue(element()) };
			if (property === "checked") return { value: elementChecked(element()) };
			throw new BridgeError("INVALID_ARGUMENT", "property must be url, title, text, value, or checked");
		}
		case "wait":
			return waitFor(document, String(request.kind) as WaitKind, String(request.value ?? ""), Math.max(1, Number(request.timeoutMs) || 10_000)).then(
				(satisfied) => ({ satisfied }),
			);
		case "highlight":
			highlight(element());
			return { highlighted: true };
		case "unhighlight":
			unhighlight(document);
			return { highlighted: false };
		case "annotations":
			return { annotations: annotations(document, registry), dpr: document.defaultView?.devicePixelRatio ?? 1 };
		case "frameOffset":
			return frameOffset(element());
		case "describe":
			// The approval gate's read of what an action would act on.
			return {
				facts: request.ref
					? describeApprovalTarget(document, { mode: "element" }, element())
					: describeApprovalTarget(document, { mode: "focused" }),
				viewport: { width: document.defaultView?.innerWidth ?? 0, height: document.defaultView?.innerHeight ?? 0 },
				dpr: document.defaultView?.devicePixelRatio ?? 1,
				documentId: documentId(document),
				// The described document's own address: in a selected frame this is
				// the frame's site, which the approval is scoped to.
				url: document.location?.href ?? "",
				title: document.title,
				...(request.ref ? {} : { focusId: focusIdOf(document, true) }),
			};
		default:
			throw new BridgeError("INVALID_ARGUMENT", `Unknown content operation: ${request.op}`);
	}
}

function toResponse(value: unknown): ContentResponse {
	return { ok: true, value };
}

function toError(error: unknown): ContentResponse {
	if (error instanceof BridgeError) return { ok: false, error: { code: error.code, message: error.message } };
	return { ok: false, error: { code: "BROWSER_COMMAND_FAILED", message: error instanceof Error ? error.message : String(error) } };
}

function install(): void {
	const scope = globalThis as unknown as { __aoBridgeContent?: boolean };
	if (scope.__aoBridgeContent) return;
	scope.__aoBridgeContent = true;
	const registry = new RefRegistry();
	chromeApi().runtime.onMessage.addListener((message, sender, sendResponse) => {
		if (!isRecord(message) || message.aoBridge !== 1) return;
		// Only the extension's own service worker may drive this script.
		if (sender.id !== chromeApi().runtime.id || sender.tab) return;
		try {
			const result = handleContentRequest(document, registry, message as ContentRequest);
			if (result instanceof Promise) {
				result.then((value) => sendResponse(toResponse(value)), (error) => sendResponse(toError(error)));
				return true as unknown as void;
			}
			sendResponse(toResponse(result));
		} catch (error) {
			sendResponse(toError(error));
		}
	});
}

if (typeof document !== "undefined" && (globalThis as { chrome?: { runtime?: unknown } }).chrome?.runtime) install();
