import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const storage = vi.hoisted(() => {
	const values = new Map<string, string>();
	return {
		values,
		getItem: vi.fn(async (key: string) => values.get(key) ?? null),
		setItem: vi.fn(async (key: string, value: string) => { values.set(key, value); }),
	};
});

const host = vi.hoisted(() => async (name: string) => {
	const React = await import("react");
	return ({ children, ...props }: { children?: import("react").ReactNode }) => React.createElement(name, props, children);
});

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: storage.getItem, setItem: storage.setItem } }));
vi.mock("react-native", async () => {
	class Value { constructor(public value: number) {} setValue(next: number) { this.value = next; } }
	return {
		AccessibilityInfo: { announceForAccessibility: vi.fn() },
		Animated: {
			Value,
			View: await host("AnimatedView"),
			timing: (value: Value, config: { toValue: number }) => ({ start: (done?: () => void) => { value.setValue(config.toValue); done?.(); } }),
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

const servers = [{ name: "notion", status: "failed" }];
const STORED = (key: string) => `ao:mcp-notice-shown:${key}`;

let renderer: ReactTestRenderer | undefined;

async function render(noticeKey: string, visible: boolean) {
	await act(async () => {
		if (renderer) renderer.update(<McpServerNotice noticeKey={noticeKey} servers={servers} visible={visible} />);
		else renderer = create(<McpServerNotice noticeKey={noticeKey} servers={servers} visible={visible} />);
	});
}

const noteShown = () => renderer!.root.findAllByType("Text" as never).some((node) => String(node.props.children).includes("Notion didn’t start"));

beforeEach(() => {
	vi.useFakeTimers();
	storage.values.clear();
	storage.setItem.mockClear();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
});

describe("McpServerNotice", () => {
	it("waits while the chat is covered and shows once it is on screen", async () => {
		await render("hidden", false);
		await act(async () => { vi.advanceTimersByTime(5_000); });
		expect(noteShown()).toBe(false);
		expect(storage.setItem).not.toHaveBeenCalled();

		await render("hidden", true);
		expect(noteShown()).toBe(true);
	});

	it("is not used up when the chat is covered before the read ends", async () => {
		await render("covered", true);
		expect(noteShown()).toBe(true);
		await act(async () => { vi.advanceTimersByTime(1_500); });
		await render("covered", false);
		await act(async () => { vi.advanceTimersByTime(5_000); });
		expect(noteShown()).toBe(false);
		expect(storage.setItem).not.toHaveBeenCalled();

		await render("covered", true);
		expect(noteShown()).toBe(true);
	});

	it("is remembered after a full read and fades away", async () => {
		await render("read", true);
		await act(async () => { vi.advanceTimersByTime(3_000); });
		expect(storage.setItem).toHaveBeenCalledWith(STORED("read"), "1");
		expect(noteShown()).toBe(false);
	});

	it("stays quiet for a run whose note was already read", async () => {
		storage.values.set(STORED("earlier"), "1");
		await render("earlier", true);
		expect(noteShown()).toBe(false);
	});
});
