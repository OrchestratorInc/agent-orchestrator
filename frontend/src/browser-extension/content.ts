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
import { BridgeError, isRecord } from "./protocol";
import { buildSnapshot, RefRegistry } from "./snapshot";

export type ContentRequest = { aoBridge: 1; op: string; [key: string]: unknown };
export type ContentResponse = { ok: true; value: unknown } | { ok: false; error: { code: string; message: string } };

export function handleContentRequest(document: Document, registry: RefRegistry, request: ContentRequest): unknown | Promise<unknown> {
	const element = () => registry.resolve(String(request.ref ?? ""));
	switch (request.op) {
		case "ping":
			return { url: document.location?.href ?? "", title: document.title, visibility: document.visibilityState };
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
