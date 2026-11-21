import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useBufferedStreamingText } from "./useBufferedStreamingText";

const appState = vi.hoisted(() => ({ currentState: "active" }));
vi.mock("react-native", () => ({ AppState: appState }));

let root: ReactTestRenderer | undefined;
let rendered: string;

function Reply({ text, streaming, reducedMotion, playFromStart }: {
	text: string; streaming: boolean; reducedMotion: boolean; playFromStart: boolean;
}) {
	rendered = useBufferedStreamingText(text, streaming, reducedMotion, playFromStart);
	return null;
}

async function show(text: string, streaming = true, reducedMotion = false, playFromStart = false) {
	await act(async () => {
		const element = <Reply text={text} streaming={streaming} reducedMotion={reducedMotion} playFromStart={playFromStart} />;
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
	const revealed = (rendered.length - first.length) / grapheme.length;
	expect(Number.isInteger(revealed)).toBe(true);
	expect(rendered).toBe(first + grapheme.repeat(revealed));
	await advance(1200);
	expect(rendered).toBe(latest);
});

it("shows corrections immediately and drains a short final burst", async () => {
	await show("First");
	await show("First draft with a buffered burst");
	await show("Provider correction");
	expect(rendered).toBe("Provider correction");
	await show("Provider correction with a last burst");
	await show("Provider correction with a last burst", false);
	expect(rendered).toBe("Provider correction");
	await advance(800);
	expect(rendered).toBe("Provider correction with a last burst");
});

it("spreads the next batch across the observed arrival gap", async () => {
	const prefix = "Start.";
	const first = prefix + "a".repeat(80);
	const next = first + "b".repeat(80);
	await show(prefix);
	await show(first);
	await advance(640);
	expect(rendered).toBe(first);
	await show(next);

	await advance(320);
	expect(rendered.length - first.length).toBeGreaterThanOrEqual(38);
	expect(rendered.length - first.length).toBeLessThanOrEqual(44);
	await advance(336);
	expect(rendered).toBe(next);
});

it("paces the first snapshot of a reply that arrived after the chat opened", async () => {
	const first = "first word ".repeat(32);
	await show(first, true, false, true);
	expect(rendered).toBe("");
	await advance(16);
	expect(rendered.length).toBeGreaterThan(0);
	expect(rendered.length).toBeLessThan(8);
	await advance(100);
	expect(rendered.length).toBeLessThan(24);
	await advance(2800);
	expect(rendered).toBe(first);
});

it("paces a large first snapshot even if it arrives completed", async () => {
	const text = "finished ".repeat(64);
	await show(text, false, false, true);
	expect(rendered).toBe("");
	await advance(16);
	expect(rendered.length).toBeGreaterThan(0);
	expect(rendered.length).toBeLessThan(9);
	await advance(3200);
	expect(rendered).toBe(text);
});

it("paces a large final append on a reply already in view", async () => {
	const first = "First.";
	const final = first + " Another sentence.".repeat(36);
	await show(first);
	await show(final, false);
	expect(rendered).toBe(first);
	await advance(16);
	expect(rendered.length - first.length).toBeGreaterThan(0);
	expect(rendered.length - first.length).toBeLessThan(9);
	await advance(3200);
	expect(rendered).toBe(final);
});

it("keeps draining in small batches across the long-reply threshold", async () => {
	const first = "a".repeat(3998);
	const latest = first + "b".repeat(100);
	await show(first);
	await show(latest);
	await advance(16);
	expect(rendered.length).toBeGreaterThan(4000);
	expect(rendered.length - first.length).toBeLessThan(8);
	await advance(16);
	expect(rendered.length - first.length).toBeLessThan(8);
	await advance(1600);
	expect(rendered).toBe(latest);
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
