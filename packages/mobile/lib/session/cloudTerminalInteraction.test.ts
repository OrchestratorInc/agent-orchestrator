import { readFileSync } from "node:fs";
import vm from "node:vm";
import { describe, expect, it } from "vitest";

type Touch = { clientX: number; clientY: number };
type TouchEvent = { touches: Touch[]; changedTouches: Touch[]; cancelable: boolean; preventDefault(): void };

function loadCloudTerminalScript(platform: "ios" | "android", alternate = false) {
	// Execute the script actually passed to the Cloud WebView, not a copy of its logic.
	const source = readFileSync(new URL("./CloudTerminalSessionScreen.tsx", import.meta.url), "utf8");
	const script = source.match(/const CLOUD_TERMINAL_JS = `([\s\S]*?)`;/)?.[1];
	if (!script) throw new Error("Cloud terminal WebView script not found");
	const listeners = new Map<string, (event: TouchEvent) => void>();
	const messages: Array<{ type: string; message: string }> = [];
	const wheelEvents: Array<{ deltaY: number; deltaMode: number }> = [];
	const styles: Array<{ textContent: string }> = [];
	const viewport = { scrollTop: 100, scrollBarWidth: 15 };
	const textarea = { disabled: false, setAttribute() {} };
	const terminalElement = { dispatchEvent(event: { deltaY: number; deltaMode: number }) { wheelEvents.push(event); } };
	const terminal = {
		buffer: { active: { type: alternate ? "alternate" : "normal" } },
		modes: { mouseTrackingMode: "none" },
		_core: { viewport },
	};
	const document = {
		createElement: () => ({ textContent: "" }),
		head: { appendChild(style: { textContent: string }) { styles.push(style); } },
		addEventListener(type: string, handler: (event: TouchEvent) => void) { listeners.set(type, handler); },
		querySelector(selector: string) {
			if (selector === ".xterm-viewport") return viewport;
			if (selector === ".xterm-helper-textarea") return textarea;
			if (selector === ".xterm") return terminalElement;
			return null;
		},
	};
	class WheelEvent {
		deltaY: number;
		deltaMode: number;
		constructor(_type: string, options: { deltaY: number; deltaMode: number }) {
			this.deltaY = options.deltaY;
			this.deltaMode = options.deltaMode;
		}
	}
	vm.runInNewContext(`var IS_ANDROID=${platform === "android"};\n${script}`, {
		document,
		window: {
			terminal,
			fitAddon: { proposeDimensions: () => ({ cols: 55, rows: 39 }) },
			ReactNativeWebView: { postMessage(value: string) { messages.push(JSON.parse(value)); } },
			addEventListener() {},
		},
		WheelEvent,
		setTimeout: () => 1,
		clearTimeout() {},
		setInterval() {},
	});
	const event = (x: number, y: number): TouchEvent => ({
		touches: [{ clientX: x, clientY: y }],
		changedTouches: [{ clientX: x, clientY: y }],
		cancelable: true,
		preventDefault() {},
	});
	return { listeners, messages, wheelEvents, styles, viewport, textarea, event };
}

describe("Cloud terminal WebView interactions", () => {
	it("scrolls normal-buffer output by the finger distance on Android", () => {
		const view = loadCloudTerminalScript("android");
		view.listeners.get("touchstart")?.(view.event(20, 100));
		view.listeners.get("touchmove")?.(view.event(20, 140));
		view.listeners.get("touchmove")?.(view.event(20, 180));
		expect(view.viewport.scrollTop).toBe(20);
	});

	it("sends wheel input to full-screen terminal apps instead of scrolling an empty viewport", () => {
		const view = loadCloudTerminalScript("ios", true);
		view.listeners.get("touchstart")?.(view.event(20, 100));
		view.listeners.get("touchmove")?.(view.event(20, 140));
		view.listeners.get("touchmove")?.(view.event(20, 180));
		expect(view.wheelEvents.length).toBeGreaterThan(0);
		expect(view.wheelEvents[0]).toMatchObject({ deltaY: -1, deltaMode: 1 });
		expect(view.viewport.scrollTop).toBe(100);
	});

	it("reports a tap, but not a drag, for keyboard dismissal", () => {
		const view = loadCloudTerminalScript("ios");
		view.listeners.get("touchstart")?.(view.event(20, 100));
		view.listeners.get("touchend")?.(view.event(20, 100));
		expect(view.messages).toContainEqual({ type: "debug", message: "AO_TERMINAL_TAP" });
		view.messages.length = 0;
		view.listeners.get("touchstart")?.(view.event(20, 100));
		view.listeners.get("touchmove")?.(view.event(20, 140));
		view.listeners.get("touchend")?.(view.event(20, 140));
		expect(view.messages).not.toContainEqual({ type: "debug", message: "AO_TERMINAL_TAP" });
	});

	it("does not dismiss the keyboard for a horizontal swipe", () => {
		const view = loadCloudTerminalScript("ios");
		view.listeners.get("touchstart")?.(view.event(20, 100));
		view.listeners.get("touchmove")?.(view.event(80, 100));
		view.listeners.get("touchend")?.(view.event(80, 100));
		expect(view.messages).not.toContainEqual({ type: "debug", message: "AO_TERMINAL_TAP" });
	});

	it("keeps the WebView's hidden input from reopening the keyboard", () => {
		const view = loadCloudTerminalScript("ios");
		expect(view.textarea.disabled).toBe(true);
		expect(view.styles.some((style) => style.textContent.includes(".xterm-screen") && style.textContent.includes("pointer-events:none"))).toBe(true);
	});
});
