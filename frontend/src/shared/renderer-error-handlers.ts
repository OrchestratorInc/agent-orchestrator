import type { RendererTelemetryCaptureInput } from "./telemetry-policy";

type Handler = (error: unknown, source: "window-error" | "unhandledrejection") => void;
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
	const report = (error: unknown, source: "window-error" | "unhandledrejection", event?: ErrorEvent) => {
		try {
			if (state.capture) { state.capture(error, source); return; }
			if (!root.ao?.telemetry) return;
			const object = error !== null && typeof error === "object" ? error : undefined;
			if (object && state.early.has(object)) return;
			if ((error instanceof Error || error instanceof DOMException) && error.name === "AbortError") return;
			const types = new Set(["Error", "EvalError", "RangeError", "ReferenceError", "SyntaxError", "TypeError", "URIError", "AggregateError", "DOMException"]);
			const name = error instanceof Error && types.has(error.name) ? error.name : "Error";
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
			void root.ao.telemetry.capture({
				kind: "exception", message: "Renderer startup error", level: "error",
				exception: { type: name, frames: filename || lineno ? [{ function: "renderer_frame", ...(filename ? { filename } : {}), ...(lineno ? { lineno } : {}), ...(colno ? { colno } : {}) }] : [] },
			}).catch(() => undefined);
		} catch { /* reporting must not become another uncaught error */ }
	};
	window.addEventListener("error", (event: Event) => {
		if (event instanceof ErrorEvent) report(event.error ?? new Error("Renderer startup error"), "window-error", event);
		else if (event.target instanceof HTMLScriptElement) report(new Error("Renderer script load failed"), "window-error");
	}, true);
	window.addEventListener("unhandledrejection", (event) => report(event.reason, "unhandledrejection"));
}

export function wasCapturedBeforeRenderer(error: object): boolean {
	return typeof window !== "undefined" && !!(window as ErrorWindow).__aoRendererErrors?.early.has(error);
}
