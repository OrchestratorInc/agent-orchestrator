import type { RendererTelemetryCaptureInput } from "./telemetry-policy";

type Source = "window-error" | "unhandledrejection" | "script-load";
type Handler = (error: unknown, source: Source, event?: ErrorEvent) => void;
type State = { capture?: Handler; early: WeakSet<object> };
type ErrorWindow = Window & {
	__aoRendererErrors?: State;
	ao?: { telemetry?: { capture: (input: RendererTelemetryCaptureInput) => Promise<boolean> } };
};

// This function is serialized by contextBridge.executeInMainWorld. Keep every
// runtime dependency inside its body, so it runs before renderer imports.
export function installRendererErrorHandlers(capture?: Handler): void {
	const root = window as ErrorWindow;
	if (root.__aoRendererErrors) {
		if (capture) root.__aoRendererErrors.capture = capture;
		return;
	}
	const state: State = { capture, early: new WeakSet() };
	root.__aoRendererErrors = state;
	const budgets = new Map<string, { minute: number; day: number; minuteCount: number; dayCount: number }>();
	const report = (error: unknown, source: Source, event?: ErrorEvent) => {
		try {
			if (state.capture) { state.capture(error, source, event); return; }
			if (!root.ao?.telemetry) return;
			const object = error !== null && typeof error === "object" ? error : undefined;
			if (object && state.early.has(object)) return;
			if ((error instanceof Error || error instanceof DOMException) && error.name === "AbortError") return;
			const types = new Set(["Error", "EvalError", "RangeError", "ReferenceError", "SyntaxError", "TypeError", "URIError", "AggregateError", "DOMException"]);
			const message = (error instanceof Error || error instanceof DOMException ? error.message : typeof error === "string" ? error : event?.message) ?? "";
			const type = error instanceof Error || error instanceof DOMException ? error.name : /^(?:Uncaught )?(\w+): /.exec(message)?.[1];
			const name = type && types.has(type) ? type : "Error";
			const location = event?.filename?.replace(/^app:\/\/renderer\//, "");
			const filename = location && /^assets\/[A-Za-z0-9_][A-Za-z0-9_.-]{0,80}-[A-Za-z0-9_-]{8,16}\.(?:js|mjs)$/.test(location) ? location : undefined;
			const line = event?.lineno, column = event?.colno;
			const lineno = Number.isSafeInteger(line) && line! > 0 && line! <= 1_000_000 ? line : undefined;
			const colno = Number.isSafeInteger(column) && column! > 0 && column! <= 10_000_000 ? column : undefined;
			let key = `${name}:${filename ?? "unknown"}:${lineno ?? 0}:${colno ?? 0}`;
			if (!budgets.has(key) && budgets.size >= 128) key = "overflow";
			const minute = Math.floor(Date.now() / 60_000), day = Math.floor(Date.now() / 86_400_000);
			let budget = budgets.get(key);
			if (!budget || budget.day !== day) budget = { minute, day, minuteCount: 0, dayCount: 0 };
			if (budget.minute !== minute) { budget.minute = minute; budget.minuteCount = 0; }
			if (budget.minuteCount >= 5 || budget.dayCount >= 200) return;
			budget.minuteCount++; budget.dayCount++; budgets.set(key, budget);
			if (object) state.early.add(object);
			// This runs before the renderer module can parse stacks. Extract only
			// packaged asset coordinates and look up their injected debug IDs.
			const ids = (root as Window & { _sentryDebugIds?: Record<string, string> })._sentryDebugIds;
			const assets = new Map<string, string>();
			for (const [stack, id] of Object.entries(ids ?? {})) {
				const asset = /app:\/\/renderer\/(assets\/[^\s:)?#]+)/.exec(stack)?.[1];
				if (asset) assets.set(asset, id);
			}
			const frames = [];
			const stack = error instanceof Error && typeof error.stack === "string" ? error.stack : "";
			for (const frame of stack.split("\n")) {
				const match = /^\s*at\s+(?:.*?\()?app:\/\/renderer\/(assets\/[A-Za-z0-9_][A-Za-z0-9_.-]{0,80}-[A-Za-z0-9_-]{8,16}\.(?:js|mjs))(?:[?#][^\s)]*)?:(\d+):(\d+)\)?$/.exec(frame);
				if (!match) continue;
				const [, filename, line, column] = match;
				const lineno = Number(line), colno = Number(column);
				if (lineno < 1 || lineno > 1_000_000 || colno < 1 || colno > 10_000_000) continue;
				const debugId = assets.get(filename);
				frames.push({ function: "renderer_frame", filename, lineno, colno, ...(debugId ? { debug_id: debugId } : {}) });
				if (frames.length === 8) break;
			}
			if (!frames.length && (filename || lineno)) {
				const debugId = filename && assets.get(filename);
				frames.push({ function: "renderer_frame", ...(filename ? { filename } : {}), ...(lineno ? { lineno } : {}), ...(colno ? { colno } : {}), ...(debugId ? { debug_id: debugId } : {}) });
			}
			void root.ao.telemetry.capture({
				kind: "exception", message: message.length <= 4096 ? message : "", level: "error",
				tags: { category: source === "unhandledrejection" ? "renderer_rejection" : "renderer_exception",
					code: source === "unhandledrejection" ? "UNHANDLED_REJECTION" : source === "script-load" ? "RENDERER_SCRIPT_LOAD_FAILED" : "WINDOW_ERROR" },
				exception: { type: name, frames: frames.reverse() },
			}).catch(() => undefined);
		} catch { /* reporting must not become another uncaught error */ }
	};
	window.addEventListener("error", (event: Event) => {
		if (event instanceof ErrorEvent) report(event.error ?? event.message, "window-error", event);
		else if (event.target instanceof HTMLScriptElement) report(new Error("Renderer script load failed"), "script-load");
	}, true);
	window.addEventListener("unhandledrejection", (event) => report(event.reason, "unhandledrejection"));
}

export function wasCapturedBeforeRenderer(error: object): boolean {
	return typeof window !== "undefined" && !!(window as ErrorWindow).__aoRendererErrors?.early.has(error);
}
