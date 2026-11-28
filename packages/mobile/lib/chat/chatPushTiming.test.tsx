import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

type Listener = (event: { data: { closing: boolean } }) => void;
const nav = vi.hoisted(() => ({ listeners: new Set<Listener>() }));
vi.mock("expo-router", () => ({
	useNavigation: () => ({
		addListener: (_name: string, listener: Listener) => {
			nav.listeners.add(listener);
			return () => nav.listeners.delete(listener);
		},
	}),
}));

import { PUSH_SETTLE_FALLBACK_MS } from "../usePushSettled";
import { LARGE_CACHED_TRANSCRIPT_ROWS, useChatPushTiming } from "./chatPushTiming";
import type { ConversationSnapshot } from "./types";

type Timing = ReturnType<typeof useChatPushTiming>;
let seen: Timing[] = [];
let frames: FrameRequestCallback[] = [];
let renderer: ReactTestRenderer | undefined;

function Probe({ snapshot }: { snapshot?: ConversationSnapshot }) {
	seen.push(useChatPushTiming(snapshot));
	return null;
}

function snapshotWith(rows: number): ConversationSnapshot {
	return { items: Array.from({ length: rows }, (_, index) => ({ id: `item-${index}` })) } as unknown as ConversationSnapshot;
}

const open = (snapshot?: ConversationSnapshot) => act(() => { renderer = create(<Probe snapshot={snapshot} />); });
const nextFrame = () => act(() => { for (const frame of frames.splice(0)) frame(0); });
const slideEnds = () => act(() => { for (const listener of [...nav.listeners]) listener({ data: { closing: false } }); });

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => frames.push(callback));
	vi.stubGlobal("cancelAnimationFrame", () => {});
	seen = [];
	frames = [];
	nav.listeners.clear();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
	vi.useRealTimers();
});

it("mounts a small cached transcript a frame after the push starts", () => {
	open(snapshotWith(LARGE_CACHED_TRANSCRIPT_ROWS));
	expect(seen[0]).toEqual({ holdArrivingSnapshot: false, timelineMounted: false });
	nextFrame();
	expect(seen.at(-1)).toEqual({ holdArrivingSnapshot: false, timelineMounted: true });
});

it("holds a large cached transcript until the slide settles", () => {
	open(snapshotWith(LARGE_CACHED_TRANSCRIPT_ROWS + 1));
	nextFrame();
	expect(seen.at(-1)).toEqual({ holdArrivingSnapshot: false, timelineMounted: false });
	slideEnds();
	expect(seen.at(-1)).toEqual({ holdArrivingSnapshot: false, timelineMounted: true });
});

// A deep link or a stack's first page opens without a push.
it("mounts a large cached transcript after the fallback when no transition reports", () => {
	open(snapshotWith(LARGE_CACHED_TRANSCRIPT_ROWS + 1));
	nextFrame();
	act(() => { vi.advanceTimersByTime(PUSH_SETTLE_FALLBACK_MS - 1); });
	expect(seen.at(-1)?.timelineMounted).toBe(false);
	act(() => { vi.advanceTimersByTime(1); });
	expect(seen.at(-1)?.timelineMounted).toBe(true);
});

it("judges the size the page opened with, not what it grows to", () => {
	open(snapshotWith(10));
	act(() => renderer?.update(<Probe snapshot={snapshotWith(LARGE_CACHED_TRANSCRIPT_ROWS + 1)} />));
	nextFrame();
	expect(seen.at(-1)?.timelineMounted).toBe(true);
});

it("holds an uncached conversation that arrives mid-slide until the slide settles", () => {
	open();
	expect(seen[0]).toEqual({ holdArrivingSnapshot: false, timelineMounted: false });
	nextFrame();
	// A large page arriving is held like any other: the hold is what keeps it out of the slide.
	act(() => renderer?.update(<Probe snapshot={snapshotWith(LARGE_CACHED_TRANSCRIPT_ROWS + 1)} />));
	expect(seen.at(-1)).toEqual({ holdArrivingSnapshot: true, timelineMounted: true });
	slideEnds();
	expect(seen.at(-1)).toEqual({ holdArrivingSnapshot: false, timelineMounted: true });
});

it("draws an uncached conversation that arrives after the slide at once", () => {
	open();
	nextFrame();
	slideEnds();
	act(() => renderer?.update(<Probe snapshot={snapshotWith(5)} />));
	expect(seen.at(-1)).toEqual({ holdArrivingSnapshot: false, timelineMounted: true });
});
