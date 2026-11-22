import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({ appState: "active", reduceMotion: false, now: 0, frames: new Map<number, (time: number) => void>(), nextFrame: 1 }));
vi.mock("react-native", () => ({ AppState: { get currentState() { return fixture.appState; } } }));
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => fixture.reduceMotion }));
import { ReplyPacingContext, usePacedReply, type ReplyPacing } from "./usePacedReply";

let renderer: ReactTestRenderer | undefined;
let shown = "";
function Reply({ id = "reply", text, streaming }: { id?: string; text: string; streaming: boolean }) {
	shown = usePacedReply(id, text, streaming);
	return null;
}
function tree(pacing: ReplyPacing, props: { id?: string; text: string; streaming: boolean }) {
	return <ReplyPacingContext.Provider value={pacing}><Reply {...props} /></ReplyPacingContext.Provider>;
}
/** Runs animation frames for `ms` of display time at 60 Hz. */
async function play(ms: number) {
	for (let elapsed = 0; elapsed < ms; elapsed += 1000 / 60) {
		fixture.now += 1000 / 60;
		const frames = [...fixture.frames.values()];
		fixture.frames.clear();
		await act(async () => { frames.forEach((frame) => frame(fixture.now)); });
	}
}
function livePacing(live = true): ReplyPacing {
	return { progress: new Map(), live: { current: live } };
}

beforeEach(() => {
	fixture.appState = "active";
	fixture.reduceMotion = false;
	fixture.now = 0;
	fixture.frames.clear();
	vi.stubGlobal("performance", { now: () => fixture.now });
	vi.stubGlobal("requestAnimationFrame", (callback: (time: number) => void) => {
		const id = fixture.nextFrame++;
		fixture.frames.set(id, callback);
		return id;
	});
	vi.stubGlobal("cancelAnimationFrame", (id: number) => { fixture.frames.delete(id); });
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

const REPLY = "The change is in. I updated the parser and its tests, and the build passes.";

it("shows a reply that was already there when the conversation opened at once", async () => {
	await act(async () => { renderer = create(tree(livePacing(false), { text: REPLY.slice(0, 30), streaming: true })); });
	expect(shown).toBe(REPLY.slice(0, 30));
});

it("plays a reply that starts streaming in front of the reader and ends on its exact text", async () => {
	const pacing = livePacing();
	await act(async () => { renderer = create(tree(pacing, { text: REPLY.slice(0, 24), streaming: true })); });
	expect(shown).toBe("");
	await play(50);
	expect(shown.length).toBeGreaterThan(0);
	expect(shown.length).toBeLessThan(24);
	await act(async () => renderer!.update(tree(pacing, { text: REPLY, streaming: false })));
	await play(800);
	expect(shown).toBe(REPLY);
	expect(pacing.progress.has("reply")).toBe(false);
});

it("never shows a word cut in half while text remains", async () => {
	const pacing = livePacing();
	await act(async () => { renderer = create(tree(pacing, { text: REPLY, streaming: true })); });
	const seen = new Set<string>();
	for (let index = 0; index < 30; index++) {
		await play(34);
		seen.add(shown);
	}
	const cuts = [...seen].filter((text) => text.length < REPLY.length && /\w/.test(REPLY[text.length - 1] ?? "") && /\w/.test(REPLY[text.length] ?? ""));
	expect(cuts.length).toBeLessThanOrEqual(2);
});

it("shows the text as it is with Reduce Motion on", async () => {
	fixture.reduceMotion = true;
	await act(async () => { renderer = create(tree(livePacing(), { text: REPLY.slice(0, 20), streaming: true })); });
	await act(async () => renderer!.update(tree(livePacing(), { text: REPLY.slice(0, 60), streaming: true })));
	expect(shown).toBe(REPLY.slice(0, 60));
});

it("shows what arrived while the app was away instead of replaying it", async () => {
	const pacing = livePacing();
	await act(async () => { renderer = create(tree(pacing, { text: REPLY.slice(0, 10), streaming: true })); });
	fixture.appState = "background";
	await act(async () => renderer!.update(tree(pacing, { text: REPLY.slice(0, 70), streaming: true })));
	expect(shown).toBe(REPLY.slice(0, 70));
});

it("carries on where it was when the list draws its row again", async () => {
	const pacing = livePacing();
	await act(async () => { renderer = create(tree(pacing, { text: REPLY, streaming: true })); });
	await play(120);
	const before = shown;
	expect(before.length).toBeGreaterThan(0);
	act(() => renderer!.unmount());
	await act(async () => { renderer = create(tree(pacing, { text: REPLY, streaming: true })); });
	expect(shown).toBe(before);
});

it("keeps history immediate: a settled reply's revision is not played", async () => {
	await act(async () => { renderer = create(tree(livePacing(), { text: "Short.", streaming: false })); });
	await act(async () => renderer!.update(tree(livePacing(), { text: "Short. And a revision.", streaming: false })));
	expect(shown).toBe("Short. And a revision.");
});
