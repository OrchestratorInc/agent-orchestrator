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

import { PUSH_SETTLE_FALLBACK_MS, usePushSettled } from "./usePushSettled";

let seen: boolean[] = [];
let renderer: ReactTestRenderer | undefined;
function Probe() {
	seen.push(usePushSettled());
	return null;
}
const emit = (closing: boolean) => act(() => { for (const listener of [...nav.listeners]) listener({ data: { closing } }); });

beforeEach(() => {
	vi.useFakeTimers();
	seen = [];
	nav.listeners.clear();
	act(() => { renderer = create(<Probe />); });
});

afterEach(() => {
	act(() => renderer?.unmount());
	vi.useRealTimers();
});

it("settles when the push finishes sliding in", () => {
	expect(seen.at(-1)).toBe(false);
	emit(false);
	expect(seen.at(-1)).toBe(true);
	expect(nav.listeners.size).toBe(0);
});

it("ignores the page's own closing transition", () => {
	emit(true);
	expect(seen.at(-1)).toBe(false);
});

// A deep link or a stack's first page opens without a push.
it("settles anyway when no transition reports", () => {
	act(() => { vi.advanceTimersByTime(PUSH_SETTLE_FALLBACK_MS - 1); });
	expect(seen.at(-1)).toBe(false);
	act(() => { vi.advanceTimersByTime(1); });
	expect(seen.at(-1)).toBe(true);
});
