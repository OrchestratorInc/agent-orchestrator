import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useBufferedStreamingText } from "./useBufferedStreamingText";

const appState = vi.hoisted(() => ({ currentState: "active" }));
vi.mock("react-native", () => ({ AppState: appState }));

let root: ReactTestRenderer | undefined;
let rendered: string;

function Reply({ text, streaming, reducedMotion }: { text: string; streaming: boolean; reducedMotion: boolean }) {
	rendered = useBufferedStreamingText(text, streaming, reducedMotion);
	return null;
}

async function show(text: string, streaming = true, reducedMotion = false) {
	await act(async () => {
		const element = <Reply text={text} streaming={streaming} reducedMotion={reducedMotion} />;
		if (root) root.update(element);
		else root = create(element);
	});
}

async function advance(ms: number) {
	await act(async () => { await vi.advanceTimersByTimeAsync(ms); });
}

beforeEach(() => {
	vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("requestAnimationFrame", (callback: (time: number) => void) => setTimeout(() => callback(performance.now()), 16));
	vi.stubGlobal("cancelAnimationFrame", clearTimeout);
	appState.currentState = "active";
});

afterEach(async () => {
	await act(async () => { root?.unmount(); });
	root = undefined;
	vi.unstubAllGlobals();
	vi.useRealTimers();
});

it("shows the first snapshot immediately and reveals the next burst in whole graphemes", async () => {
	const first = "First ";
	const grapheme = "👩🏽‍💻";
	const latest = first + grapheme.repeat(40);
	await show(first);
	expect(rendered).toBe(first);
	await show(latest);
	expect(rendered).toBe(first);
	await advance(16);
	expect(rendered.length).toBeGreaterThan(first.length);
	expect(rendered).toBe(first + grapheme);
	expect(rendered).toBe(first + grapheme.repeat((rendered.length - first.length) / grapheme.length));
	await advance(1200);
	expect(rendered).toBe(latest);
});

it("shows corrections immediately and drains a short final burst", async () => {
	await show("First");
	await show("First draft with a buffered burst");
	await show("First correction");
	expect(rendered).toBe("First correction");
	await show("First correction with a last burst");
	await show("First correction with a last burst", false);
	expect(rendered).toBe("First correction");
	await advance(500);
	expect(rendered).toBe("First correction with a last burst");
});

it("shows long snapshots at the lower update rate used by desktop", async () => {
	const first = "a".repeat(4001);
	await show(first);
	await show(first + "b");
	await show(first + "bc");
	await advance(99);
	expect(rendered).toBe(first);
	await advance(1);
	expect(rendered).toBe(first + "bc");
});

it("flushes the buffer for reduced motion or when the app goes into the background", async () => {
	await show("First");
	await show("First burst");
	await show("First burst", true, true);
	expect(rendered).toBe("First burst");
	await show("First burst followed by another one");
	appState.currentState = "background";
	await advance(16);
	expect(rendered).toBe("First burst followed by another one");
});
