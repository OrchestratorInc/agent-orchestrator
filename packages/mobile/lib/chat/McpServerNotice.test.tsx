import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const storage = vi.hoisted(() => {
	const values = new Map<string, string>();
	return {
		values,
		getItem: vi.fn(async (key: string): Promise<string | null> => values.get(key) ?? null),
		setItem: vi.fn(async (key: string, value: string) => { values.set(key, value); }),
	};
});

const host = vi.hoisted(() => async (name: string) => {
	const React = await import("react");
	return ({ children, ...props }: { children?: import("react").ReactNode }) => React.createElement(name, props, children);
});

const announce = vi.hoisted(() => vi.fn());

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: storage.getItem, setItem: storage.setItem } }));
vi.mock("react-native", async () => {
	class Value { constructor(public value: number) {} setValue(next: number) { this.value = next; } }
	return {
		AccessibilityInfo: { announceForAccessibility: announce },
		Animated: {
			Value,
			View: await host("AnimatedView"),
			// Runs for its real duration, so tests can see the note while it fades.
			timing: (value: Value, config: { toValue: number; duration: number }) => ({
				start: (done?: () => void) => setTimeout(() => { value.setValue(config.toValue); done?.(); }, config.duration),
			}),
		},
		AppState: { currentState: "active", addEventListener: () => ({ remove: () => {} }) },
		StyleSheet: { create: (styles: object) => styles },
		View: await host("View"),
		Text: await host("Text"),
	};
});
vi.mock("../AppSymbol", async () => ({ AppSymbol: await host("AppSymbol") }));
vi.mock("../tokens", () => ({ space: { xxs: 4, xs: 6, sm: 8 }, type: { caption1: { fontSize: 12, lineHeight: 16 } } }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_target, key) => `#${String(key)}` });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { McpServerNotice } from "./McpServerNotice";
import { mcpNoticeKey } from "./conversationBanners";
import type { McpServer } from "./types";

const failed: McpServer[] = [{ name: "notion", status: "failed" }];
const STORED = (key: string) => `ao:mcp-notice-shown:${key}`;
// The "shown this launch" set lives for the module's lifetime, so every test
// uses keys of its own.
let keySeq = 0;
const freshKey = () => mcpNoticeKey("h_test", `ao-${++keySeq}`, "2026-11-09T10:00:00Z");

let renderer: ReactTestRenderer | undefined;

async function render(noticeKey: string, { visible = true, servers = failed }: { visible?: boolean; servers?: McpServer[] } = {}) {
	const element = <McpServerNotice noticeKey={noticeKey} servers={servers} visible={visible} />;
	await act(async () => {
		if (renderer) renderer.update(element);
		else renderer = create(element);
	});
}

/** A remount, as when the chat screen is left and opened again. */
async function remount(noticeKey: string, options?: { visible?: boolean; servers?: McpServer[] }) {
	act(() => renderer?.unmount());
	renderer = undefined;
	await render(noticeKey, options);
}

async function advance(ms: number) {
	await act(async () => { vi.advanceTimersByTime(ms); });
}

/** Shows the note and lets it run its full course. */
async function readThrough(noticeKey: string) {
	await render(noticeKey);
	await advance(3_000 + 200);
}

const noteText = () => renderer!.root.findAllByType("Text" as never).map((node) => String(node.props.children)).join("");
const noteShown = () => noteText().includes("didn’t start");
const opacity = () => (renderer!.root.findByType("AnimatedView" as never).props.style as Array<{ opacity?: { value: number } }>)[1].opacity!.value;

beforeEach(() => {
	vi.useFakeTimers();
	storage.values.clear();
	storage.getItem.mockClear();
	storage.setItem.mockClear();
	announce.mockClear();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
});

describe("McpServerNotice", () => {
	it("says which servers did not start, for 3 s, then fades out", async () => {
		const key = freshKey();
		await render(key, { servers: [{ name: "canva", status: "failed" }, { name: "posthog", status: "failed" }] });
		expect(noteText()).toBe("Canva, Posthog didn’t start. Continuing without them.");
		expect(announce).toHaveBeenCalledTimes(1);

		await advance(2_999);
		expect(noteShown()).toBe(true);
		expect(opacity()).toBe(1);
		expect(storage.setItem).not.toHaveBeenCalled();

		await advance(1);
		// Fading: still drawn while the 200 ms fade runs.
		expect(noteShown()).toBe(true);
		expect(storage.setItem).toHaveBeenCalledWith(STORED(key), "1");

		await advance(200);
		expect(noteShown()).toBe(false);
	});

	it("waits while no server has failed, and shows when one does", async () => {
		const key = freshKey();
		await render(key, { servers: [] });
		await advance(10_000);
		expect(renderer!.toJSON()).toBeNull();
		expect(storage.getItem).not.toHaveBeenCalled();

		await render(key, { servers: failed });
		expect(noteText()).toBe("Notion didn’t start. Continuing without it.");
	});

	it("shows once per run across remounts", async () => {
		const key = freshKey();
		await readThrough(key);
		// Storage is wiped to prove the in-memory record alone keeps it quiet.
		storage.values.clear();
		announce.mockClear();
		await remount(key);
		expect(noteShown()).toBe(false);
		await advance(5_000);
		expect(announce).not.toHaveBeenCalled();
	});

	it("does not replay after a relaunch", async () => {
		const key = freshKey();
		storage.values.set(STORED(key), "1");
		await render(key);
		expect(noteShown()).toBe(false);
		await advance(5_000);
		expect(announce).not.toHaveBeenCalled();
		expect(storage.getItem).toHaveBeenCalledWith(STORED(key));
	});

	it("shows again for a restarted session (new createdAt)", async () => {
		const first = mcpNoticeKey("h_test", "ao-restart", "2026-11-09T10:00:00Z");
		await readThrough(first);
		await remount(mcpNoticeKey("h_test", "ao-restart", "2026-11-09T11:00:00Z"));
		expect(noteShown()).toBe(true);
	});

	it("still shows when the storage read fails", async () => {
		storage.getItem.mockRejectedValueOnce(new Error("disk"));
		await render(freshKey());
		expect(noteShown()).toBe(true);
	});

	it("waits while the chat is covered and shows once it is on screen", async () => {
		const key = freshKey();
		await render(key, { visible: false });
		await advance(5_000);
		expect(noteShown()).toBe(false);
		expect(storage.setItem).not.toHaveBeenCalled();

		await render(key, { visible: true });
		expect(noteShown()).toBe(true);
	});

	it("is not used up when the chat is covered before the read ends", async () => {
		const key = freshKey();
		await render(key);
		await advance(1_500);
		await render(key, { visible: false });
		await advance(5_000);
		expect(noteShown()).toBe(false);
		expect(storage.setItem).not.toHaveBeenCalled();

		await render(key, { visible: true });
		expect(noteShown()).toBe(true);
	});
});
